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

// A source's state on the wire.
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

// runner keeps one source's process running and holds what it last showed.
type runner struct {
	home string
	key  string
	ctx  context.Context
	done chan struct{}

	notify    func() // the state changed; must not block
	restartCh chan struct{}
	wake      chan struct{}
	gen       atomic.Uint64 // restart requests so far

	mu       sync.Mutex
	def      config.Source
	sid      int // the current process; a notification from an older one is ignored
	p        *proc
	ready    bool // initialized and listed: requests may be sent
	state    string
	readOnly bool
	errMsg   string
	auth     *Auth
	caps     []string
	schema   json.RawMessage
	panels   []Panel
	queue    []string // orders.launched not yet delivered
	pulls    pulls
}

// pulls is the lists Admiral still has to ask the plugin for.
type pulls struct {
	panels bool
	all    bool
	one    map[string]bool
}

func newRunner(ctx context.Context, home string, def config.Source, notify func(), prev *runner) *runner {
	r := &runner{home: home, key: def.Key, ctx: ctx, def: def, notify: notify, done: make(chan struct{}),
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

// restart asks for a restart now, cutting any backoff short.
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

// fail records a crash: the source is read-only with its last orders until it
// runs again.
func (r *runner) fail(err error) {
	r.mu.Lock()
	r.state, r.readOnly, r.ready = StateError, true, false
	r.errMsg = cleanMsg(err.Error())
	r.auth = nil
	r.mu.Unlock()
	r.notify()
}

// session runs the plugin once, from start to the end of the process. It
// reports how long the plugin ran after it was ready and whether a restart
// request, not a failure, ended it.
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

	m, dir, err := LoadManifest(r.home, def.Plugin)
	if err != nil {
		return 0, false, err
	}
	dataDir := filepath.Join(r.home, "data", r.key)
	if err := os.MkdirAll(dataDir, 0o700); err != nil {
		return 0, false, err
	}
	p, err := startProc(r.key, m, dir, func(method string, params json.RawMessage) { r.note(sid, method, params) })
	if err != nil {
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
	case <-p.exited:
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
		case <-p.exited:
			return time.Since(readyAt), false, errors.New(p.exitReason())
		case <-ctx.Done():
			return time.Since(readyAt), false, nil
		case <-r.wake:
			r.processPulls(ctx, p)
		}
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

// maxSchema is the largest settingsSchema Admiral passes on, in bytes.
const maxSchema = 256 << 10

// incompatibleError is a plugin that speaks another protocol version.
type incompatibleError struct{ msg string }

func (e *incompatibleError) Error() string { return e.msg }

// mergePanels takes a new panel list and keeps the orders and the error of
// panels that stay. r.mu must be held.
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

// pullOrders asks for one panel's orders. An error answer from the plugin is
// logged and shown on the panel until its next good list. Any other bad or
// missing answer is logged and the panel keeps what it had.
func (r *runner) pullOrders(ctx context.Context, p *proc, panel string) {
	res, err := p.call(ctx, "orders.list", map[string]any{"panel": panel}, callTimeout)
	if err != nil {
		r.logf("orders.list for panel %q failed: %v", panel, err)
		var rpc *rpcError
		if errors.As(err, &rpc) {
			msg := rpc.Error()
			if msg == "" {
				msg = "This panel's orders couldn't be listed."
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

// setPanelError shows a message on one panel and tells the watchers.
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

// pullPanels asks for the panels again and then for each one's orders.
func (r *runner) pullPanels(ctx context.Context, p *proc) {
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

// wantPull records lists to pull and wakes the session. panel "" means all.
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

func (r *runner) processPulls(ctx context.Context, p *proc) {
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

// note handles a notification from the plugin's process number sid.
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

// live is the plugin process when it can take requests.
func (r *runner) live() (*proc, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.p, r.ready && r.p != nil
}

// launched tells the plugin an order was launched, now if it runs and after its
// next start if not.
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

func (r *runner) deliverLaunched(p *proc, id string) {
	if _, err := p.call(r.ctx, "orders.launched", map[string]any{"id": id}, callTimeout); err != nil && r.ctx.Err() == nil {
		r.logf("orders.launched for %q failed: %v", id, err)
	}
}
