// Package ships keeps no ship table: every ship is read from tmux's session
// options each time, plus the position stored in state.json.
package ships

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/jbuffin/admiral/internal/actor"
	"github.com/jbuffin/admiral/internal/autopilotgate"
	"github.com/jbuffin/admiral/internal/conversation"
	"github.com/jbuffin/admiral/internal/github"
	"github.com/jbuffin/admiral/internal/home"
	"github.com/jbuffin/admiral/internal/launcher"
	"github.com/jbuffin/admiral/internal/moor"
	"github.com/jbuffin/admiral/internal/outcomes"
	"github.com/jbuffin/admiral/internal/server"
	"github.com/jbuffin/admiral/internal/sessionhost"
	"github.com/jbuffin/admiral/internal/state"
	"github.com/jbuffin/admiral/internal/usage"
)

// Ship's ExitCode and ExitSignal are set only on an anchored ship.
type Ship struct {
	Session    string  `json:"session"`
	Name       string  `json:"name"`
	Fleet      string  `json:"fleet"`
	FleetName  string  `json:"fleet_name"`
	FleetColor string  `json:"fleet_color"`
	Harness    string  `json:"harness"`
	Dir        string  `json:"dir"`
	Repo       string  `json:"repo"`
	Skill      *string `json:"skill"`
	Source     string  `json:"source"`
	Panel      string  `json:"panel"`
	// A ship holds its Resources while it is underway or needs you.
	Resources []string `json:"-"`
	Title     string   `json:"title"`
	URL       string   `json:"url"`
	Issues    []string `json:"issues,omitempty"`
	OrderID   string   `json:"order_id"`
	Launched  string   `json:"launched"`
	// Via is "admiralbot" for a ship AdmiralBot launched; absent for the engineer's.
	Via        string          `json:"via,omitempty"`
	Chat       string          `json:"-"`
	Status     string          `json:"status"`
	ExitCode   *int            `json:"exit_code"`
	ExitSignal *string         `json:"exit_signal"`
	Position   *state.Position `json:"position"`
	// PRs and MooredAt are set only on a moored ship.
	PRs      []moor.PR `json:"prs,omitempty"`
	MooredAt string    `json:"moored_at,omitempty"`
	// PolledAt is the last poll that answered; Stale says the latest one failed.
	PolledAt string `json:"polled_at,omitempty"`
	Stale    bool   `json:"stale,omitempty"`
	// StackReady, ReadyUpTo and MergeMethod are set on a moored ship: whether every PR is ready to merge, the
	// URL of the highest layer up to which every layer is, and the repository's default merge method.
	StackReady  bool   `json:"stack_ready,omitempty"`
	ReadyUpTo   string `json:"ready_up_to,omitempty"`
	MergeMethod string `json:"merge_method,omitempty"`
	// NoResume is true on a moored ship whose record has no valid resume text, which cannot be woken.
	NoResume bool `json:"no_resume,omitempty"`
	// Waiting is why a moored ship's feedback round has not started, while one is due.
	Waiting string `json:"waiting,omitempty"`
	// Reason and StopSince are set only while the ship needs you; Reason is `idle`
	// for a ship no hook has spoken for.
	Reason    string `json:"reason,omitempty"`
	StopSince string `json:"stop_since,omitempty"`
	// Crew and Subagents are always present, empty lists as [].
	Crew      []CrewBoat             `json:"crew"`
	Subagents []sessionhost.Subagent `json:"subagents"`
	CrewLive  int                    `json:"crew_live"`
	Tail      string                 `json:"tail"`
}

type Orders interface {
	Order(source, id string) (text, title, repo, url, panel string, err *launcher.Error)
	Launched(source, id string)
}

type Service struct {
	// answering holds one mutex per pane id, taken for the whole of an answer.
	answering sync.Map
	Host      *sessionhost.Host
	Launcher  *launcher.Launcher
	State     *state.Store
	Hub       *server.Hub
	// MergeGuard wraps the Merge route with its token check; with none the route refuses everything.
	MergeGuard func(http.Handler) http.Handler
	Orders     Orders
	// OnChange must not block.
	OnChange func()
	// Tail gives a ship's conversation tail from a cache; it must not block.
	Tail func(session string) string
	// Questions gives the pending questions of a ship's conversation, or of a crew boat's when crew is set, oldest first.
	Questions func(session, crew string) []conversation.Item
	// Approving is whether a Codex ship's app-server is waiting on an approval: a bell then may be for it, not for a question.
	Approving func(session, crew string) bool
	Outcomes  *outcomes.Writer
	// ChatPrompt names the prompt a fleet's chat is answering; an actor's freeform launch is keyed on it.
	ChatPrompt func(fleet string) string
	// UsageRead replaces the transcript reading at a ship's exit, for tests.
	UsageRead func(ctx context.Context, sess sessionhost.Session) ExitUsage
	// LastLive gives a ship's last live figures when its transcript has none; nil when nothing keeps them.
	LastLive func(session string) (usage.Figures, bool)
	// ForgetLive drops a ship's live figures once its usage record has read them, so a later round or a ship of the same name starts from nothing.
	ForgetLive func(session string)
	// Reserve takes a unit of each named resource, or none: it returns the
	// release, or why the first is not ready (busy, missing or pending). Nil
	// when there are no resources to hold.
	Reserve func(names []string) (release func(), name, why string)
	// GitHub is the client of the fleet's built-in GitHub source; nil when the
	// fleet has none, and the ship then sails off.
	GitHub func(fleet string) moor.GitHub
	// CaptureResume keeps the answered items' resume text before a report is deleted.
	CaptureResume func(session string)

	// admitMu is held from an autopilot launch's cap check until its ship is launched,
	// so two cannot both see the last free slot. Interactive launches never take it.
	admitMu sync.Mutex
	pass    sync.Mutex        // one observe at a time
	mu      sync.Mutex        // guards tracks and what they point to
	tracks  map[string]*track // the ships the log follows, by session
	// launching: a name is not known before Launch returns, so removeShipFiles
	// keeps the files while any launch is under way.
	launching int
	// launchingOrders holds the orders with a launch under way, so a second
	// launch of one is refused.
	launchingOrders map[OrderKey]bool
	// listing keeps an order hidden from the end of its launch until the host
	// lists its ship.
	listing map[OrderKey]unlisted
	// settling holds the exit-0 ships whose PRs are being checked, by session.
	settling map[string]bool
	settleWG sync.WaitGroup
	// waking holds the moored ships with a wake under way, by session.
	waking map[string]bool
	// roundLogged is each session's last round whose end is written, by launch and round, so a retried end writes none.
	roundLogged map[string]string

	mooredMu     sync.Mutex
	moored       []moor.Record // read from $ADMIRAL_HOME/moored on first use
	mooredLoaded bool
	mooredPolls  map[string]polled // by session
	committed    map[string]bool   // by session: a wake is making the session, so no ending takes the record; under mooredMu
	moorKick     chan struct{}
	mergeMethods map[string]string // by fleet and repository: its default merge method; under mooredMu
	merging      map[string]bool   // by session: a merge is under way; under mooredMu
	pollMu       sync.Mutex        // one poll at a time
	wakeMu       sync.Mutex        // every read-modify-write of a ship's wake state; taken after mooredMu is released, never inside it
	// issueLocks holds the GitHub issues (by ClaimKey) with a launch under way, each with who holds it.
	issueLocks map[string]string
	chatLocks  map[string]string
}

type unlisted struct {
	session   string
	panel     string
	at        time.Time
	resources []string
	url       string
	issues    []string
	chat      string
	fleet     string
}

// listedWithin: a ship killed before the host saw it must not hold its order for good.
const listedWithin = 30 * time.Second

const NotKnownMsg = "ships are not known yet"

func (s *Service) ship(sess sessionhost.Session) Ship {
	sh := FromSession(sess, time.Now())
	if sh.Status == StatusNeedsYou {
		// The track's Since is set in the same pass as its status; before that
		// pass the stop is just beginning.
		since := time.Now()
		s.mu.Lock()
		if tr := s.tracks[sess.Name]; tr != nil && !tr.Since.IsZero() {
			since = tr.Since
		}
		s.mu.Unlock()
		sh.StopSince = since.UTC().Format(time.RFC3339Nano)
	}
	// Only the wire counts the crew: the ship's own status stays what FromSession
	// gives, for the CLI, the outcome log and every decision about the ship.
	if _, reason, since, ok := wireStatus(sh.Status, sh.Crew); ok {
		sh.Status, sh.Reason, sh.StopSince = StatusNeedsYou, reason, since
	}
	if p, ok := s.State.Position(sess.Name); ok {
		sh.Position = &p
	}
	if s.Tail != nil {
		sh.Tail = s.Tail(sess.Name)
	}
	return sh
}

// FromSession has no position; the CLI uses it when the server is not running.
func FromSession(sess sessionhost.Session, now time.Time) Ship {
	o, session := sess.Options, sess.Name
	sh := Ship{Session: session, Name: o["name"], Fleet: o["fleet"], FleetName: o["fleet-name"],
		FleetColor: o["fleet-color"], Harness: o["harness"], Dir: o["dir"], Repo: filepath.Base(o["dir"]),
		Source: o["source"], Panel: o["panel"], Title: o["title"], URL: o["url"], OrderID: o["order"], Launched: o["launched"], Via: o["via"], Chat: o["chat"], Status: status(sess, now)}
	sh.ExitCode, sh.ExitSignal = exit(sess)
	sh.Crew, sh.Subagents = crewBoats(sess.Crew), sess.Subagents
	if sh.Subagents == nil {
		sh.Subagents = []sessionhost.Subagent{}
	}
	sh.CrewLive = crewLive(sh.Crew)
	if sh.Status == StatusNeedsYou {
		sh.Reason = reasonOf(sess)
	}
	if o["dir"] == "" {
		sh.Repo = ""
	}
	if sk := o["skill"]; sk != "" {
		sh.Skill = &sk
	}
	if is := o["issues"]; is != "" {
		sh.Issues = strings.Split(is, ",")
	}
	if r := o["resources"]; r != "" {
		sh.Resources = strings.Split(r, ",")
	}
	return sh
}

func (s *Service) List() []Ship {
	out := []Ship{}
	live := map[string]bool{}
	for _, sess := range s.Host.Sessions() {
		live[sess.Name] = true
		if sess.IsShip() {
			out = append(out, s.ship(sess))
		}
	}
	return append(out, s.mooredShips(live)...)
}

func (s *Service) message() any {
	return map[string]any{"type": "ships", "ships": s.List()}
}

func (s *Service) broadcast() {
	s.Hub.BroadcastOnce("ships", s.message)
	if s.OnChange != nil {
		s.OnChange()
	}
}

func (s *Service) Connected(c *server.Client) { _ = c.SendOnce("ships", s.message) }

func (s *Service) Routes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/ships", s.serveList)
	mux.HandleFunc("POST /api/ships", s.serveLaunch)
	mux.HandleFunc("PUT /api/ships/{session}/position", s.servePosition)
	mux.HandleFunc("POST /api/ships/{session}/input-seen", s.serveInputSeen)
	mux.HandleFunc("POST /api/ships/{session}/dismiss", s.serveDismiss)
	mux.HandleFunc("POST /api/ships/{session}/unfollow", s.serveUnfollow)
	mux.HandleFunc("POST /api/ships/{session}/wake", s.serveWake)
	mux.HandleFunc("POST /api/ships/{session}/next", s.serveNext)
	mux.HandleFunc("GET /api/outcomes", s.serveOutcomes)
	mux.Handle("POST /api/ships/{session}/merge", s.mergeGuarded(http.HandlerFunc(s.serveMerge)))
	mux.HandleFunc("POST /api/moored/refresh", s.serveMooredRefresh)
	mux.HandleFunc("POST /api/ships/{session}/crew/{name}/close", s.serveCrewClose)
	mux.HandleFunc("POST /api/ships/{session}/messages", s.serveSendInput)
	mux.HandleFunc("POST /api/ships/{session}/interrupt", s.serveInterrupt)
	mux.HandleFunc("POST /api/ships/{session}/answer", s.serveAnswer)
	mux.HandleFunc("POST /api/ships/{session}/crew/{name}/answer", s.serveAnswer)
	mux.HandleFunc("POST /api/ships/{session}/stop", s.serveStop)
	mux.HandleFunc("POST /api/ships/{session}/crew/{name}/messages", s.serveSendInput)
	mux.HandleFunc("POST /api/ships/{session}/crew/{name}/interrupt", s.serveInterrupt)
}

// tick catches changes no tmux event announces: NoHookWait and the end of the sail-off.
const tick = 500 * time.Millisecond

func (s *Service) Run(ctx context.Context) {
	events, cancel := s.Host.Subscribe()
	defer cancel()
	defer s.settleWG.Wait()
	select {
	case <-s.Host.Ready():
		// A ship that died while Admiral was stopped gets its `ended` first.
		s.baselineAll()
		s.observe(ctx)
		// An exit-0 ship from before a restart is checked at once, not after another sail-off delay.
		for _, sess := range s.Host.Sessions() {
			if sess.IsShip() && exitedCleanly(sess) {
				s.settle(ctx, sess)
			}
		}
		_ = s.Host.Sync(ctx)
		s.keep()
		s.broadcast()
	case <-ctx.Done():
		return
	}
	sailing := map[string]time.Time{} // exit-0 ships by when they were first seen dead
	seen := s.signature()
	timer := time.NewTicker(tick)
	defer timer.Stop()
	for {
		var bells []string
		resumed := false
		gone, changed := false, false
		select {
		case ev := <-events:
			for more := true; more; { // one push for a burst
				gone = gone || prunes(ev)
				resumed = resumed || ev.Kind == sessionhost.Resumed
				if ev.Kind == sessionhost.Bell {
					bells = append(bells, ev.Session)
				}
				select {
				case ev = <-events:
				default:
					more = false
				}
			}
			changed = true
		case <-timer.C:
			changed = s.signature() != seen
		case <-ctx.Done():
			return
		}
		for _, name := range bells {
			s.bell(ctx, name)
		}
		if resumed {
			s.baselineAll()
		}
		s.observe(ctx)
		s.sailOff(ctx, sailing)
		if gone {
			s.keep()
		}
		if changed {
			s.broadcast()
			seen = s.signature()
		}
	}
}

func prunes(ev sessionhost.Event) bool {
	return ev.Kind == sessionhost.SessionRemoved || ev.Kind == sessionhost.Resumed
}

func (s *Service) signature() string {
	var b strings.Builder
	for _, sh := range s.List() {
		b.WriteString(sh.Session + "=" + sh.Status + ":" + sh.Reason + crewSignature(sh) + "|" + sh.Tail + ";")
	}
	return b.String()
}

// bell exists because Codex has no waiting hook. The state goes into the pane so
// it has one source of truth and survives a restart.
func (s *Service) bell(ctx context.Context, session string) {
	sess, ok := s.find(session)
	if !ok || sess.Options["harness"] != "codex" || sess.PaneDead || sess.State == "needs_you" {
		return
	}
	if err := s.Host.SetNeedsYou(ctx, session, "bell"); err == nil {
		_ = s.Host.Sync(ctx)
	}
}

// Asking puts a Codex ship at needs_you with the bell's reason while its server waits on a question, and back to
// working once it does not. Only a pane the hooks have not put at needs_you for something else is written, and only
// the observer's own mark is cleared.
func (s *Service) Asking(session string, asking bool) {
	sess, ok := s.find(session)
	if !ok || sess.Options["harness"] != "codex" || sess.PaneDead {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	switch {
	case asking && sess.State != "needs_you":
		if s.Host.SetNeedsYou(ctx, session, "bell") == nil {
			_ = s.Host.Sync(ctx)
		}
	case !asking && sess.State == "needs_you" && sess.Reason == "bell":
		if s.Host.SetWorking(ctx, session) == nil {
			_ = s.Host.Sync(ctx)
		}
	}
}

func (s *Service) sailOff(ctx context.Context, since map[string]time.Time) {
	now := time.Now()
	live := map[string]bool{}
	for _, sess := range s.Host.Sessions() {
		if !sess.IsShip() || !exitedCleanly(sess) {
			continue
		}
		live[sess.Name] = true
		if _, ok := since[sess.Name]; !ok {
			since[sess.Name] = now
		}
		if now.Sub(since[sess.Name]) >= SailOffDelay {
			s.settle(ctx, sess)
		}
	}
	for name := range since {
		if !live[name] {
			delete(since, name)
		}
	}
}

func (s *Service) find(session string) (sessionhost.Session, bool) {
	for _, sess := range s.Host.Sessions() {
		if sess.Name == session && sess.IsShip() {
			return sess, true
		}
	}
	return sessionhost.Session{}, false
}

func (s *Service) keep() {
	live := map[string]bool{}
	for _, sess := range s.Host.Sessions() {
		live[sess.Name] = true
	}
	for _, name := range s.Host.Unreadable() { // alive, only unreadable
		live[name] = true
	}
	s.State.Keep(live)
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}

func (s *Service) serveList(w http.ResponseWriter, r *http.Request) {
	// home says whose ships these are, for the commands that find a server by port.
	list := s.List()
	if fleet, ok := actor.FleetOf(r.Context()); ok {
		list = slices.DeleteFunc(list, func(sh Ship) bool { return sh.Fleet != fleet })
	}
	writeJSON(w, http.StatusOK, map[string]any{"ships": list, "home": home.ID(s.Launcher.Home)})
}

func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}

// releaseWhenListed keeps the reservation until the host lists the ship, which
// holds the resources from then on, so a failed sync after the launch doesn't free them early.
func (s *Service) releaseWhenListed(session string, release func()) {
	if _, ok := s.find(session); ok {
		release()
		return
	}
	go func() {
		defer release()
		for i := 0; i < 100; i++ {
			time.Sleep(100 * time.Millisecond)
			if _, ok := s.find(session); ok {
				return
			}
		}
	}()
}

// admit checks what a launch over HTTP may claim and takes its resources: autopilot
// mode needs a guarded harness and room under max_ships. The caller holds admitMu
// for an autopilot launch until LaunchOrder returns.
func (s *Service) admit(req launcher.Request) (release func(), lerr *launcher.Error) {
	release = func() {}
	cfg := s.Launcher.Config()
	if req.Autopilot {
		fleet, ok := cfg.Fleet(req.Fleet)
		if !ok {
			return release, &launcher.Error{Status: http.StatusNotFound, Msg: "fleet " + strconv.Quote(req.Fleet) + " not found"}
		}
		if _, ok := fleet.Harness(req.Harness); !ok {
			return release, &launcher.Error{Status: http.StatusNotFound, Msg: "fleet " + fleet.Name + " has no " + req.Harness + " dock"}
		}
		if err := autopilotgate.Check(s.Launcher.Home, fleet, req.Harness); err != nil {
			return release, &launcher.Error{Status: http.StatusBadRequest, Msg: err.Error()}
		}
	}
	for _, name := range req.Resources {
		if _, ok := cfg.Resources[name]; !ok {
			return release, &launcher.Error{Status: http.StatusBadRequest, Msg: "no resource named " + strconv.Quote(name)}
		}
	}
	// A proposal launch is the engineer's own act: like a hand wake it counts toward the cap and is never blocked by it.
	if req.Autopilot && req.By != "proposal" {
		// Ships the host lists, ships launched but not listed yet, and launches
		// still running (a hand launch is not counted twice by much: it errs
		// towards refusing).
		n := 0
		for _, sh := range append(s.List(), s.Pending()...) {
			if sh.Status == StatusUnderway || sh.Status == StatusNeedsYou {
				n++
			}
		}
		s.mu.Lock()
		n += s.launching
		s.mu.Unlock()
		if n >= cfg.MaxShips {
			return release, &launcher.Error{Status: http.StatusConflict, Msg: launcher.AutopilotCapText + " of " +
				strconv.Itoa(cfg.MaxShips) + plural(cfg.MaxShips, " ship", " ships"), Wait: WaitCap}
		}
	}
	if len(req.Resources) > 0 && s.Reserve != nil {
		rel, name, why := s.Reserve(req.Resources)
		if rel == nil {
			return release, &launcher.Error{Status: http.StatusConflict, Msg: "resource " + strconv.Quote(name) + " is " + why,
				Wait: "Waiting for " + name + " (" + why + ")"}
		}
		release = rel
	}
	return release, nil
}

// LaunchOrder serves hand launches and autopilot alike, so the refusal rules
// cannot drift. A source's order launches with the source's text, whatever the request holds.
func (s *Service) LaunchOrder(req launcher.Request) (Ship, *launcher.Error) {
	var panel string
	key := OrderKey{req.Source, req.OrderID}
	if req.Source == "" && req.Round == 0 { // only a source's order sets a title and URL, so a client's never stand
		req.Title, req.URL = "", ""
	}
	if len(req.Issues) > 0 {
		if req.Source == "" && req.Round == 0 {
			hosts := []string{github.DefaultHost}
			if fleet, ok := s.Launcher.Config().Fleet(req.Fleet); ok {
				hosts = launcher.GitHubHosts(s.Launcher.Config(), fleet)
			}
			var lerr *launcher.Error
			if req.Issues, lerr = checkIssueURLs(req.Issues, hosts); lerr != nil {
				return Ship{}, lerr
			}
		} else {
			for _, u := range req.Issues {
				if _, ok := ClaimKey(u); !ok {
					return Ship{}, &launcher.Error{Status: http.StatusBadRequest, Msg: "issues must be GitHub issue URLs, not " + strconv.Quote(u)}
				}
			}
		}
		req.Issues = distinctIssues(req.Issues)
		req.URL = req.Issues[0]
	}
	chat := req.ChatOption()
	self := "" // a wake's own moored record holds its key and claims
	if req.Round > 0 {
		self = cmpName(req.Name, req.Session)
	}
	if req.Round == 0 && (req.Source != "" || len(req.Issues) > 0 || chat != "") && !s.Host.Known() {
		return Ship{}, &launcher.Error{Status: http.StatusServiceUnavailable, Msg: NotKnownMsg}
	}
	if req.Source != "" && req.Round == 0 {
		if !s.lockOrder(key) {
			return Ship{}, s.orderRefused(req, key)
		}
		defer s.unlockOrder(key)
		if HiddenOrders(s.List())[key] {
			return Ship{}, s.orderRefused(req, key)
		}
		var lerr *launcher.Error
		if req.Text, req.Title, _, req.URL, panel, lerr = s.Orders.Order(req.Source, req.OrderID); lerr != nil {
			return Ship{}, lerr
		}
		if req.Panel == "" {
			req.Panel = panel
		}
		if len(req.Issues) > 0 {
			req.URL = req.Issues[0]
		}
	}
	if issues := claimed(req); len(issues) > 0 {
		if holder, ok := s.lockIssues(issues, "a launch in flight"); !ok {
			return Ship{}, issueHeld(holder)
		}
		defer s.unlockIssues(issues)
		claims := claimingShips(append(s.List(), s.Pending()...))
		for _, u := range issues {
			if sh, held := claims[claimKeyOf(u)]; held && cmpName(sh.Name, sh.Session) != self {
				return Ship{}, issueHeld(holderOf(req, sh))
			}
		}
	}
	if chat != "" && req.Round == 0 {
		if holder, ok := s.lockChat(chat, "a launch in flight"); !ok {
			return Ship{}, chatHeld(holder)
		}
		defer s.unlockChat(chat)
		for _, sh := range append(s.List(), s.Pending()...) {
			if sh.Chat == chat {
				return Ship{}, chatHeld(holderOf(req, sh))
			}
		}
	}
	s.mu.Lock()
	s.launching++
	s.mu.Unlock()
	defer s.launchDone()
	got, err := s.Launcher.Launch(req)
	if err != nil {
		if le, ok := err.(*launcher.Error); ok {
			return Ship{}, le
		}
		return Ship{}, &launcher.Error{Status: http.StatusInternalServerError, Msg: err.Error()}
	}
	s.State.SetLastDir(req.Fleet, req.Harness, got.Options["dir"]) // the cleaned path the launch used
	s.launched(req, got, req.Panel)
	if req.Source != "" && req.Round == 0 {
		s.Orders.Launched(req.Source, req.OrderID)
	}
	// Not the request's context: a client that hangs up must not abandon the read.
	syncCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	_ = s.Host.Sync(syncCtx)
	cancel()
	sess := sessionhost.Session{Name: got.Session, Options: got.Options}
	if now, ok := s.find(got.Session); ok {
		sess = now
	} else if req.Source != "" || len(req.Issues) > 0 || chat != "" {
		if req.Source == "" { // the pending listing is keyed by the order, so a sourceless launch is keyed by its session
			key = OrderKey{OrderID: got.Session}
		}
		s.holdUnlisted(key, got.Session, req.Panel, req.Resources)
		s.setUnlistedIssues(key, req.URL, req.Issues, chat, req.Fleet)
	}
	s.broadcast()
	return s.ship(sess), nil
}

// claimed is the issues a launch holds: its slice, else its one issue URL when it came from a source's order.
func claimed(req launcher.Request) []string {
	if len(req.Issues) > 0 {
		return req.Issues
	}
	if req.Source != "" && req.URL != "" {
		return []string{req.URL}
	}
	return nil
}

func chatHeld(holder string) *launcher.Error {
	return &launcher.Error{Status: http.StatusConflict, Msg: "this chat message already has a ship (" + holder + ")"}
}

// orderRefused is the 409 for an order that already has a ship; a slice's refusal names the ship holding its issues.
func (s *Service) orderRefused(req launcher.Request, key OrderKey) *launcher.Error {
	all := append(s.List(), s.Pending()...)
	if len(req.Issues) > 0 {
		claims := claimingShips(all)
		for _, u := range req.Issues {
			if sh, held := claims[claimKeyOf(u)]; held {
				return issueHeld(holderOf(req, sh))
			}
		}
		return issueHeld("a launch in flight")
	}
	for _, sh := range all {
		if sh.Source == key.Source && sh.OrderID == key.OrderID {
			return &launcher.Error{Status: http.StatusConflict, Msg: "this order already has a ship (" + holderOf(req, sh) + ")"}
		}
	}
	return &launcher.Error{Status: http.StatusConflict, Msg: "this order already has a ship (a launch in flight)"}
}

// lockChat takes the in-flight hold on a chat key; when another launch has it, ok is false and holder names it.
func (s *Service) lockChat(key, holder string) (heldBy string, ok bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if h, taken := s.chatLocks[key]; taken {
		return h, false
	}
	if s.chatLocks == nil {
		s.chatLocks = map[string]string{}
	}
	s.chatLocks[key] = holder
	return "", true
}

func (s *Service) unlockChat(key string) {
	s.mu.Lock()
	delete(s.chatLocks, key)
	s.mu.Unlock()
}

func (s *Service) holdUnlisted(key OrderKey, session, panel string, resources []string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.listing == nil {
		s.listing = map[OrderKey]unlisted{}
	}
	s.listing[key] = unlisted{session: session, panel: panel, at: time.Now(), resources: resources}
}

// Pending reports unlisted launches as underway, so a count of running ships
// includes them.
func (s *Service) Pending() []Ship {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []Ship
	for key, u := range s.listing {
		if _, listed := s.find(u.session); listed || time.Since(u.at) >= listedWithin {
			if key.Source == "" { // only lockOrder ever drops an order's entry; a session key would stay for good
				delete(s.listing, key)
			}
			continue
		}
		orderID := key.OrderID
		if key.Source == "" {
			orderID = ""
		}
		out = append(out, Ship{Session: u.session, Source: key.Source, OrderID: orderID, Chat: u.chat, Fleet: u.fleet, Panel: u.panel, Resources: u.resources, URL: u.url, Issues: u.issues, Status: StatusUnderway})
	}
	return out
}

func (s *Service) lockOrder(key OrderKey) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.launchingOrders[key] {
		return false
	}
	if u, ok := s.listing[key]; ok {
		if _, listed := s.find(u.session); !listed && time.Since(u.at) < listedWithin {
			return false
		}
		delete(s.listing, key)
	}
	if s.launchingOrders == nil {
		s.launchingOrders = map[OrderKey]bool{}
	}
	s.launchingOrders[key] = true
	return true
}

func (s *Service) unlockOrder(key OrderKey) {
	s.mu.Lock()
	delete(s.launchingOrders, key)
	s.mu.Unlock()
}

func (s *Service) servePosition(w http.ResponseWriter, r *http.Request) {
	session := r.PathValue("session")
	var p state.Position
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&p); err != nil {
		writeError(w, http.StatusBadRequest, "position is not valid JSON: "+err.Error())
		return
	}
	if !(p.X >= 0 && p.X <= 1 && p.Y >= 0 && p.Y <= 1) {
		writeError(w, http.StatusBadRequest, "x and y must be between 0 and 1, got "+
			strconv.FormatFloat(p.X, 'g', -1, 64)+" and "+strconv.FormatFloat(p.Y, 'g', -1, 64))
		return
	}
	found := false
	for _, sess := range s.Host.Sessions() {
		found = found || (sess.Name == session && sess.IsShip())
	}
	if !found {
		writeError(w, http.StatusNotFound, "ship "+strings.TrimSpace(session)+" not found")
		return
	}
	s.State.SetPosition(session, p)
	s.broadcast()
	writeJSON(w, http.StatusOK, map[string]any{"position": p})
}

func (s *Service) Get(session string) (Ship, bool) {
	sess, ok := s.find(session)
	if !ok {
		return Ship{}, false
	}
	return s.ship(sess), true
}

// FleetOf is the fleet of a live or moored ship, and false when it is neither or has none.
func (s *Service) FleetOf(session string) (string, bool) {
	if sess, ok := s.find(session); ok {
		return sess.Options["fleet"], sess.Options["fleet"] != ""
	}
	if r, ok := s.mooredRecord(session); ok {
		return r.Fleet, r.Fleet != ""
	}
	return "", false
}

var ErrExited = errors.New("ship has exited")

// InputSeen clears needs you in Admiral's view only, until the next waiting hook.
func (s *Service) InputSeen(session string) (Ship, error) {
	// Not a request's context: a client that hangs up must not abandon the write.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	sess, ok := s.find(session)
	if !ok {
		return Ship{}, ErrNotFound
	}
	switch status(sess, time.Now()) {
	case StatusAnchored, StatusSailingOff:
		return s.ship(sess), ErrExited
	case StatusNeedsYou:
		if err := s.Host.SetWorking(ctx, session); err != nil {
			return Ship{}, err
		}
		_ = s.Host.Sync(ctx)
		s.broadcast()
	}
	sess, _ = s.find(session)
	return s.ship(sess), nil
}

var ErrNotFound = errors.New("ship not found")

func (s *Service) serveInputSeen(w http.ResponseWriter, r *http.Request) {
	session := r.PathValue("session")
	sh, err := s.InputSeen(session)
	switch {
	case errors.Is(err, ErrNotFound):
		writeError(w, http.StatusNotFound, "ship "+session+" not found")
	case errors.Is(err, ErrExited):
		writeError(w, http.StatusConflict, "ship "+session+" has exited")
	case err != nil:
		writeError(w, http.StatusInternalServerError, err.Error())
	default:
		writeJSON(w, http.StatusOK, map[string]any{"ship": sh})
	}
}

// Dismiss ends an anchored or moored ship; a live one is ended by Stop.
func (s *Service) serveDismiss(w http.ResponseWriter, r *http.Request) {
	session := r.PathValue("session")
	if _, ok := s.mooredRecord(session); ok {
		s.endByRequest(w, r, session, "dismissed", "dismissed")
		return
	}
	// Not the request's context: a client that hangs up must not abandon the write.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	sess, ok := s.find(session)
	if !ok {
		writeError(w, http.StatusNotFound, "ship "+session+" not found")
		return
	}
	if st := status(sess, time.Now()); st != StatusAnchored {
		writeError(w, http.StatusConflict, "ship "+session+" is "+st+", and only an anchored ship can be dismissed")
		return
	}
	s.observe(ctx) // the ship's `ended` goes into the log before `dismissed`
	s.retryEnded(sess)
	s.record(outcomes.Record{Ship: session, Event: "dismissed", Fields: withVia(nil, viaOf(r))}, nil)
	if err := s.killShip(ctx, session, false); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if name := sess.Options["name"]; name != "" { // an anchored woken ship's mooring will not come back
		s.removeWake(name)
	}
	_ = s.Host.Sync(ctx)
	s.keep()
	s.broadcast()
	writeJSON(w, http.StatusOK, map[string]any{"dismissed": session})
}

// No other record of a ship comes before `launched`.
func (s *Service) launched(req launcher.Request, got *launcher.Launched, panel string) {
	o := got.Options
	if req.Round > 0 {
		fields := map[string]any{"round": req.Round, "by": req.By}
		if req.PR != "" {
			fields["pr"] = req.PR
		}
		s.record(outcomes.Record{Ship: got.Session, Event: "woken", Fields: withVia(fields, req.WokenVia)}, nil)
		s.track(got.Session)
		return
	}
	fields := map[string]any{
		"fleet": o["fleet"], "harness": req.Harness, "skill": map[string]string{"kind": req.Skill.Kind, "name": o["skill"]},
		"source": req.Source, "panel": panel, "order_id": req.OrderID, "order_url": o["url"], "title": o["title"],
		"dir": o["dir"], "autopilot": req.Autopilot,
	}
	if len(req.Issues) > 0 {
		fields["order_urls"] = req.Issues
	}
	s.record(outcomes.Record{Ship: got.Session, Event: "launched", Fields: withVia(fields, req.Via)}, nil)
	s.track(got.Session)
}

func (s *Service) track(session string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.tracks == nil {
		s.tracks = map[string]*track{}
	}
	s.tracks[session] = &track{tally: tally{Status: StatusUnderway}, saved: map[string]string{}}
}

func (s *Service) launchDone() {
	s.mu.Lock()
	s.launching--
	s.mu.Unlock()
}
