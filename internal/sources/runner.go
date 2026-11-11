package sources

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"sync/atomic"
	"time"

	"github.com/jbuffin/admiral/internal/config"
)

const (
	StateStarting     = "starting"
	StateOK           = "ok"
	StateError        = "error"
	StateAuthRequired = "auth_required"
)

const (
	protocolVersion = 1
	firstBackoff    = time.Second
	maxBackoff      = time.Minute
	// stableAfter is how long a plugin must run for its backoff to start over.
	stableAfter = 60 * time.Second
	// maxQueued bounds the orders.launched calls kept for a plugin that is down.
	maxQueued = 1000
)

var knownCapabilities = []string{"add", "edit", "remove", "refresh"}

type runner struct {
	home string
	key  string
	cfg  func() *config.Config
	ctx  context.Context
	done chan struct{}

	notify    func() // the state changed; must not block
	restartCh chan struct{}
	wake      chan struct{}
	gen       atomic.Uint64 // restart requests so far

	mu       sync.Mutex
	def      config.Source
	sid      int // the current process; a notification from an older one is ignored
	p        conn
	ready    bool // initialized and listed: requests may be sent
	state    string
	readOnly bool
	errMsg   string
	auth     *Auth
	caps     []string
	schema   json.RawMessage
	panels   []Panel
	warnings []string // the router's, for a built-in GitHub source
	queue    []string // orders.launched not yet delivered
	pulls    pulls
}

type pulls struct {
	panels bool
	all    bool
	one    map[string]bool
}

func newRunner(ctx context.Context, home string, cfg func() *config.Config, def config.Source, notify func(), prev *runner) *runner {
	r := &runner{home: home, key: def.Key, cfg: cfg, ctx: ctx, def: def, notify: notify, done: make(chan struct{}),
		restartCh: make(chan struct{}, 1), wake: make(chan struct{}, 1), state: StateStarting, caps: []string{}}
	go func() {
		if prev != nil {
			<-prev.done // never two processes on one data directory
		}
		r.loop()
	}()
	return r
}

func (r *runner) logf(format string, a ...any) {
	log.Printf("source %s: %s", r.key, fmt.Sprintf(format, a...))
}

func (r *runner) restart() {
	r.gen.Add(1)
	select {
	case r.restartCh <- struct{}{}:
	default:
	}
}

func (r *runner) loop() {
	defer close(r.done)
	defer func() {
		r.mu.Lock()
		defer r.mu.Unlock()
		if len(r.queue) > 0 {
			r.logf("%d orders.launched calls were never delivered", len(r.queue))
		}
	}()
	backoff := firstBackoff
	for r.ctx.Err() == nil {
		ranFor, restarted, err := r.session()
		if r.ctx.Err() != nil {
			return
		}
		if restarted {
			backoff = firstBackoff
			continue
		}
		if ranFor >= stableAfter {
			backoff = firstBackoff
		}
		r.fail(err)
		var stay *incompatibleError
		if errors.As(err, &stay) {
			// Starting it again would change nothing: wait for a settings
			// save or a Restart, which may come with a fixed plugin.
			r.logf("%v; not restarting until its settings are saved or Restart is pressed", err)
			select {
			case <-r.restartCh:
				backoff = firstBackoff
				continue
			case <-r.ctx.Done():
				return
			}
		}
		r.logf("%v; restarting in %s", err, backoff)
		timer := time.NewTimer(backoff)
		select {
		case <-timer.C:
			backoff = min(backoff*2, maxBackoff)
		case <-r.restartCh:
			backoff = firstBackoff
		case <-r.ctx.Done():
			timer.Stop()
			return
		}
		timer.Stop()
	}
}

// The source stays read-only with its last orders until it runs again.
func (r *runner) fail(err error) {
	r.mu.Lock()
	r.state, r.readOnly, r.ready = StateError, true, false
	r.errMsg = cleanMsg(err.Error())
	r.auth = nil
	r.mu.Unlock()
	r.notify()
}

func (r *runner) session() (ranFor time.Duration, restarted bool, err error) {
	ctx, cancel := context.WithCancel(r.ctx)
	defer cancel()
	var byRequest atomic.Bool
	go func() {
		select {
		case <-r.restartCh:
			byRequest.Store(true)
			cancel()
		case <-ctx.Done():
		}
	}()
	defer func() { restarted = byRequest.Load() && r.ctx.Err() == nil }()

	r.mu.Lock()
	r.state, r.errMsg, r.auth, r.ready = StateStarting, "", nil, false
	r.sid++
	sid := r.sid
	def := r.def
	r.mu.Unlock()
	r.notify()

	p, err := r.open(sid, def, func(method string, params json.RawMessage) { r.note(sid, method, params) })
	if err != nil {
		return 0, false, err
	}
	dataDir := filepath.Join(r.home, "data", r.key)
	if err := os.MkdirAll(dataDir, 0o700); err != nil {
		p.kill()
		return 0, false, err
	}
	r.mu.Lock()
	r.p = p
	r.mu.Unlock()
	defer func() {
		r.mu.Lock()
		r.p, r.ready = nil, false
		r.mu.Unlock()
		p.stop()
	}()
	fatal := func(err error) (time.Duration, bool, error) {
		p.kill()
		return 0, false, err
	}

	settings := def.Settings
	if settings == nil {
		settings = json.RawMessage("{}")
	}
	res, err := p.call(ctx, "initialize", map[string]any{"protocolVersion": protocolVersion, "settings": settings, "dataDir": dataDir}, callTimeout)
	if err != nil {
		return fatal(fmt.Errorf("initialize: %w", err))
	}
	caps, schema, err := parseInitialize(res)
	if err != nil {
		return fatal(err)
	}
	res, err = p.call(ctx, "panels.list", map[string]any{}, callTimeout)
	if err != nil {
		return fatal(fmt.Errorf("panels.list: %w", err))
	}
	panels, err := parsePanels(res)
	if err != nil {
		return fatal(err)
	}
	r.mu.Lock()
	r.caps, r.schema = caps, schema
	r.mergePanels(panels)
	r.mu.Unlock()
	for _, pn := range panels {
		r.pullOrders(ctx, p, pn.ID)
	}
	select {
	case <-p.exited():
		return fatal(errors.New(p.exitReason()))
	default:
	}

	r.mu.Lock()
	r.ready, r.readOnly, r.errMsg = true, false, ""
	r.state = StateOK
	if r.auth != nil {
		r.state = StateAuthRequired
	}
	queued := r.queue
	r.queue = nil
	r.mu.Unlock()
	r.notify()
	for _, id := range queued {
		go r.deliverLaunched(p, id)
	}
	readyAt := time.Now()

	for {
		select {
		case <-p.exited():
			return time.Since(readyAt), false, errors.New(p.exitReason())
		case <-ctx.Done():
			return time.Since(readyAt), false, nil
		case <-r.wake:
			r.processPulls(ctx, p)
		}
	}
}

func (r *runner) open(sid int, def config.Source, onNote func(string, json.RawMessage)) (conn, error) {
	if def.Plugin == builtinGitHub {
		return openGitHub(openArgs{key: r.key, def: def, cfg: r.cfg, onNote: onNote, logf: r.logf,
			setWarnings: func(w []string) { r.setWarnings(sid, w) }}), nil
	}
	m, dir, err := LoadManifest(r.home, def.Plugin)
	if err != nil {
		return nil, err
	}
	env := config.Fleet{}.Environ()
	if f, ok := r.cfg().Fleet(def.Fleet); ok {
		env = f.Environ()
	}
	return startProc(r.key, m, dir, env, onNote)
}

// A stopped session's late cycle must not overwrite the current one's warnings.
func (r *runner) setWarnings(sid int, w []string) {
	r.mu.Lock()
	if sid != r.sid {
		r.mu.Unlock()
		return
	}
	changed := !slices.Equal(r.warnings, w)
	r.warnings = slices.Clone(w)
	r.mu.Unlock()
	if changed {
		r.notify()
	}
}

func parseInitialize(raw json.RawMessage) ([]string, json.RawMessage, error) {
	var res struct {
		ProtocolVersion *int            `json:"protocolVersion"`
		Capabilities    []string        `json:"capabilities"`
		SettingsSchema  json.RawMessage `json:"settingsSchema"`
	}
	if err := decodeInto(raw, &res); err != nil {
		return nil, nil, fmt.Errorf("initialize: %w", err)
	}
	if res.ProtocolVersion == nil || *res.ProtocolVersion != protocolVersion {
		got := "none"
		if res.ProtocolVersion != nil {
			got = fmt.Sprint(*res.ProtocolVersion)
		}
		return nil, nil, &incompatibleError{fmt.Sprintf("the order source speaks protocol version %s, but Admiral speaks version %d", got, protocolVersion)}
	}
	caps := []string{}
	for _, c := range res.Capabilities {
		if slices.Contains(knownCapabilities, c) && !slices.Contains(caps, c) {
			caps = append(caps, c)
		}
	}
	// The schema becomes a form in the UI: only an object of sane size goes on.
	schema := res.SettingsSchema
	var obj map[string]json.RawMessage
	if len(schema) > maxSchema || json.Unmarshal(schema, &obj) != nil || obj == nil {
		schema = json.RawMessage("{}")
	}
	return caps, schema, nil
}

const maxSchema = 256 << 10

type incompatibleError struct{ msg string }

func (e *incompatibleError) Error() string { return e.msg }

// r.mu must be held.
func (r *runner) mergePanels(fresh []Panel) {
	for i, pn := range fresh {
		for _, old := range r.panels {
			if old.ID == pn.ID {
				fresh[i].Orders = old.Orders
				fresh[i].Error = old.Error
			}
		}
	}
	r.panels = fresh
}

func (r *runner) pullOrders(ctx context.Context, p conn, panel string) {
	res, err := p.call(ctx, "orders.list", map[string]any{"panel": panel}, callTimeout)
	if err != nil {
		r.logf("orders.list for panel %q failed: %v", panel, err)
		var rpc *rpcError
		if errors.As(err, &rpc) {
			msg := rpc.Error()
			if msg == "" {
				msg = "This view's orders couldn't be listed."
			}
			r.setPanelError(panel, msg)
		}
		return
	}
	orders, err := parseOrders(res)
	if err != nil {
		r.logf("protocol error: %v", err)
		return
	}
	r.mu.Lock()
	for i := range r.panels {
		if r.panels[i].ID == panel {
			r.panels[i].Orders = orders
			r.panels[i].Error = ""
		}
	}
	r.mu.Unlock()
	r.notify()
}

func (r *runner) setPanelError(panel, msg string) {
	r.mu.Lock()
	for i := range r.panels {
		if r.panels[i].ID == panel {
			r.panels[i].Error = msg
		}
	}
	r.mu.Unlock()
	r.notify()
}

func (r *runner) pullPanels(ctx context.Context, p conn) {
	res, err := p.call(ctx, "panels.list", map[string]any{}, callTimeout)
	if err != nil {
		r.logf("panels.list failed: %v", err)
		if errors.Is(err, errTimeout) {
			p.kill() // a hung panels.list is a crash; the session sees the exit and backs off
		}
		return
	}
	panels, err := parsePanels(res)
	if err != nil {
		r.logf("protocol error: %v", err)
		return
	}
	r.mu.Lock()
	r.mergePanels(panels)
	r.mu.Unlock()
	r.notify()
	for _, pn := range panels {
		r.pullOrders(ctx, p, pn.ID)
	}
}

// panel "" means every panel.
func (r *runner) wantPull(panelList bool, panel string) {
	r.mu.Lock()
	switch {
	case panelList:
		r.pulls.panels = true
	case panel == "":
		r.pulls.all = true
	default:
		if r.pulls.one == nil {
			r.pulls.one = map[string]bool{}
		}
		r.pulls.one[panel] = true
	}
	r.mu.Unlock()
	select {
	case r.wake <- struct{}{}:
	default:
	}
}

func (r *runner) processPulls(ctx context.Context, p conn) {
	r.mu.Lock()
	todo := r.pulls
	r.pulls = pulls{}
	var ids []string
	for _, pn := range r.panels {
		ids = append(ids, pn.ID)
	}
	r.mu.Unlock()
	switch {
	case todo.panels:
		r.pullPanels(ctx, p)
	case todo.all:
		for _, id := range ids {
			r.pullOrders(ctx, p, id)
		}
	default:
		for id := range todo.one {
			r.pullOrders(ctx, p, id)
		}
	}
}

func (r *runner) note(sid int, method string, params json.RawMessage) {
	r.mu.Lock()
	current := sid == r.sid
	r.mu.Unlock()
	if !current {
		return
	}
	switch method {
	case "orders.changed":
		var p struct {
			Panel *string `json:"panel"`
		}
		_ = json.Unmarshal(params, &p) // no params, or odd ones, mean every panel
		if p.Panel != nil {
			r.wantPull(false, *p.Panel)
		} else {
			r.wantPull(false, "")
		}
	case "panels.changed":
		r.wantPull(true, "")
	case "auth.required":
		a, err := parseAuth(params)
		if err != nil {
			r.logf("protocol error: %v", err)
			return
		}
		r.mu.Lock()
		r.auth = a
		if r.ready {
			r.state = StateAuthRequired
		}
		r.mu.Unlock()
		r.notify()
	case "auth.ok":
		r.mu.Lock()
		r.auth = nil
		if r.state == StateAuthRequired {
			r.state = StateOK
		}
		r.mu.Unlock()
		r.notify()
	default:
		r.logf("protocol error: the notification %q is not one Admiral knows", method)
	}
}

func (r *runner) live() (conn, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.p, r.ready && r.p != nil
}

// Delivered now if the plugin runs, else after its next start.
func (r *runner) launched(id string) {
	r.mu.Lock()
	if r.ready && r.p != nil {
		p := r.p
		r.mu.Unlock()
		go r.deliverLaunched(p, id)
		return
	}
	if len(r.queue) < maxQueued && !slices.Contains(r.queue, id) {
		r.queue = append(r.queue, id)
	}
	r.mu.Unlock()
}

func (r *runner) deliverLaunched(p conn, id string) {
	if _, err := p.call(r.ctx, "orders.launched", map[string]any{"id": id}, callTimeout); err != nil && r.ctx.Err() == nil {
		r.logf("orders.launched for %q failed: %v", id, err)
	}
}

func (r *runner) refreshNow() {
	r.mu.Lock()
	builtin := r.def.Plugin == builtinGitHub
	r.mu.Unlock()
	if !builtin {
		return
	}
	if p, ok := r.live(); ok {
		_, _ = p.call(r.ctx, "orders.refresh", map[string]any{}, callTimeout)
	}
}
