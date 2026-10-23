// Package ships is the ships of every fleet as the UI sees them (ADR 0012). Admiral
// keeps no ship table: every ship is read from tmux's session options each
// time, plus the position stored in state.json. This package serves them over
// HTTP and the WebSocket and launches new ones.
package ships

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/jbuffin/admiral/internal/home"
	"github.com/jbuffin/admiral/internal/inbox"
	"github.com/jbuffin/admiral/internal/launcher"
	"github.com/jbuffin/admiral/internal/outcomes"
	"github.com/jbuffin/admiral/internal/server"
	"github.com/jbuffin/admiral/internal/sessionhost"
	"github.com/jbuffin/admiral/internal/state"
)

// Ship is one ship as sent to the UI. ExitCode and ExitSignal are set only on
// an anchored ship.
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
	// Resources are the locked resources recorded at launch (#118); a ship
	// holds them while it is underway or needs you.
	Resources []string `json:"-"`
	// OriginSource and OriginPanel are the autopilot panel the ship counts
	// against, when it is not the one it was launched in (#120); empty for a
	// ship autopilot did not launch.
	OriginSource string          `json:"-"`
	OriginPanel  string          `json:"-"`
	Title        string          `json:"title"`
	URL          string          `json:"url"`
	OrderID      string          `json:"order_id"`
	Launched     string          `json:"launched"`
	Status       string          `json:"status"`
	ExitCode     *int            `json:"exit_code"`
	ExitSignal   *string         `json:"exit_signal"`
	Position     *state.Position `json:"position"`
	// Reason and StopSince are set only while the ship needs you: the validated
	// reason code (`idle` for a ship no hook has spoken for) and when the stop began.
	Reason    string `json:"reason,omitempty"`
	StopSince string `json:"stop_since,omitempty"`
}

// Origin is where the ship ran, for a resume order of its report: its checkout
// and the autopilot panel it counts against.
func (sh Ship) Origin() inbox.Origin {
	return inbox.Origin{Dir: sh.Dir, Source: sh.OriginSource, Panel: sh.OriginPanel}
}

// Orders is where a launch finds its order: the order sources.
type Orders interface {
	// Order is the text to launch with, the order's title, its repo, its web
	// URL and the id of the panel it is in. It refuses with a launcher.Error
	// when the source or the order is unknown.
	Order(source, id string) (text, title, repo, url, panel string, err *launcher.Error)
	// Launched tells the source its order was launched.
	Launched(source, id string)
}

// Service ties the session host, the launcher and the state together.
type Service struct {
	Host     *sessionhost.Host
	Launcher *launcher.Launcher
	State    *state.Store
	Hub      *server.Hub
	Orders   Orders
	// OnChange runs after every push of the ships. It must not block.
	OnChange func()
	// Outcomes is the outcome log; nil keeps none.
	Outcomes *outcomes.Writer
	// BeforeRemoveFiles runs with a ship's session just before its report and
	// inbox are deleted, to keep what Admiral still needs from them. It must not block.
	// The ship's tmux options go along, for a session that may be gone by then.
	BeforeRemoveFiles func(session string, from inbox.Origin)

	pass   sync.Mutex        // one observe at a time
	mu     sync.Mutex        // guards tracks and what they point to
	tracks map[string]*track // the ships the log follows, by session
	// launching counts the launches under way: a name is not known before
	// Launch returns, so removeShipFiles skips the delete, and keeps the files,
	// until none is.
	launching int
	// launchingOrders holds the orders with a launch under way, so a second
	// launch of one is refused.
	launchingOrders map[OrderKey]bool
	// listing holds the orders of launched ships the host has not listed yet,
	// so the hiding that ends with the launch does not end before the ship is
	// in the list.
	listing map[OrderKey]unlisted
}

// unlisted is a launched ship the host has not listed yet.
type unlisted struct {
	session                   string
	panel                     string
	at                        time.Time
	resources                 []string
	originSource, originPanel string
}

// listedWithin is how long a launched ship may stay unlisted before its order
// stops being held; a ship killed before the host saw it must not hold one for good.
const listedWithin = 30 * time.Second

// NotKnownMsg is the 503 a launch from a source gets while the ship list says
// nothing about which orders hold a ship.
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
	if p, ok := s.State.Position(sess.Name); ok {
		sh.Position = &p
	}
	return sh
}

// FromSession is a ship as read from its tmux session, without a position. The
// CLI uses it to list ships when the server is not running.
func FromSession(sess sessionhost.Session, now time.Time) Ship {
	o, session := sess.Options, sess.Name
	sh := Ship{Session: session, Name: o["name"], Fleet: o["fleet"], FleetName: o["fleet-name"],
		FleetColor: o["fleet-color"], Harness: o["harness"], Dir: o["dir"], Repo: filepath.Base(o["dir"]),
		Source: o["source"], Panel: o["panel"], Title: o["title"], URL: o["url"], OrderID: o["order"], Launched: o["launched"], Status: status(sess, now)}
	sh.ExitCode, sh.ExitSignal = exit(sess)
	if sh.Status == StatusNeedsYou {
		sh.Reason = reasonOf(sess)
	}
	if o["dir"] == "" {
		sh.Repo = ""
	}
	if sk := o["skill"]; sk != "" {
		sh.Skill = &sk
	}
	if r := o["resources"]; r != "" {
		sh.Resources = strings.Split(r, ",")
	}
	sh.OriginSource, sh.OriginPanel = o["origin-source"], o["origin-panel"]
	return sh
}

// List is every ship in tmux right now.
func (s *Service) List() []Ship {
	out := []Ship{}
	for _, sess := range s.Host.Sessions() {
		if sess.IsShip() {
			out = append(out, s.ship(sess))
		}
	}
	return out
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

// Connected sends a new client the ships.
func (s *Service) Connected(c *server.Client) { _ = c.SendOnce("ships", s.message) }

// Routes registers the ships API.
func (s *Service) Routes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/ships", s.serveList)
	mux.HandleFunc("POST /api/ships", s.serveLaunch)
	mux.HandleFunc("PUT /api/ships/{session}/position", s.servePosition)
	mux.HandleFunc("POST /api/ships/{session}/input-seen", s.serveInputSeen)
	mux.HandleFunc("POST /api/ships/{session}/dismiss", s.serveDismiss)
}

// tick is how often Run looks for changes that no tmux event announces: the
// 10 s without a hook and the end of the sail-off.
const tick = 500 * time.Millisecond

// Run pushes the ships to every client whenever tmux or the state changes, and
// drops stored positions of ships that are gone. It also turns a Codex bell
// into needs you and kills the session of a ship that has sailed off.
func (s *Service) Run(ctx context.Context) {
	events, cancel := s.Host.Subscribe()
	defer cancel()
	select {
	case <-s.Host.Ready():
		// A ship that died while Admiral was stopped gets its `ended` first.
		s.baselineAll()
		s.observe(ctx)
		// An exit-0 ship from before a restart is not shown sailing off again.
		for _, sess := range s.Host.Sessions() {
			if sess.IsShip() && exitedCleanly(sess) {
				_ = s.killShip(ctx, sess.Name, true)
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

// prunes says whether an event can leave a stored position without a ship.
func prunes(ev sessionhost.Event) bool {
	return ev.Kind == sessionhost.SessionRemoved || ev.Kind == sessionhost.Resumed
}

// signature is the statuses of the ships, to tell when a clock moved one.
func (s *Service) signature() string {
	var b strings.Builder
	for _, sh := range s.List() {
		b.WriteString(sh.Session + "=" + sh.Status + ":" + sh.Reason + ";")
	}
	return b.String()
}

// bell makes a Codex ship that rang its bell need you. Codex has no waiting
// hook; other harnesses' bells are ignored. The state and its reason, bell, go
// into the pane so they have one source of truth and survive a restart.
func (s *Service) bell(ctx context.Context, session string) {
	sess, ok := s.find(session)
	if !ok || sess.Options["harness"] != "codex" || sess.PaneDead || sess.State == "needs_you" {
		return
	}
	if err := s.Host.SetNeedsYou(ctx, session, "bell"); err == nil {
		_ = s.Host.Sync(ctx)
	}
}

// sailOff kills the session of every ship that exited 0 once it has been shown
// sailing_off for SailOffDelay.
func (s *Service) sailOff(ctx context.Context, since map[string]time.Time) {
	now, killed := time.Now(), false
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
			killed = s.killShip(ctx, sess.Name, true) == nil || killed
		}
	}
	for name := range since {
		if !live[name] {
			delete(since, name)
		}
	}
	if killed {
		_ = s.Host.Sync(ctx)
	}
}

// find is the ship's session as last read.
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
	writeJSON(w, http.StatusOK, map[string]any{"ships": s.List(), "home": home.ID(s.Launcher.Home)})
}

func (s *Service) serveLaunch(w http.ResponseWriter, r *http.Request) {
	var req launcher.Request
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4<<20)).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "request is not valid JSON: "+err.Error())
		return
	}
	got, lerr := s.LaunchOrder(req)
	if lerr != nil {
		writeError(w, lerr.Status, lerr.Msg)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"ship": got})
}

// LaunchOrder launches a ship and does the bookkeeping, for a hand launch and
// for autopilot alike so the refusal rules cannot drift. An order from a source
// is launched with the source's text, whatever the request holds; one that
// already has a ship, or whose launch is still running, is refused with 409;
// while the ships are not known, with 503.
func (s *Service) LaunchOrder(req launcher.Request) (Ship, *launcher.Error) {
	var panel string
	key := OrderKey{req.Source, req.OrderID}
	if req.Source != "" {
		if !s.Host.Known() {
			return Ship{}, &launcher.Error{Status: http.StatusServiceUnavailable, Msg: NotKnownMsg}
		}
		if !s.lockOrder(key) {
			return Ship{}, &launcher.Error{Status: http.StatusConflict, Msg: "this order already has a ship"}
		}
		defer s.unlockOrder(key)
		if HiddenOrders(s.List())[key] {
			return Ship{}, &launcher.Error{Status: http.StatusConflict, Msg: "this order already has a ship"}
		}
		var lerr *launcher.Error
		if req.Text, req.Title, _, req.URL, panel, lerr = s.Orders.Order(req.Source, req.OrderID); lerr != nil {
			return Ship{}, lerr
		}
		if req.Panel == "" {
			req.Panel = panel
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
	if req.Source != "" {
		s.Orders.Launched(req.Source, req.OrderID)
	}
	// Not the request's context: a client that hangs up must not abandon the read.
	syncCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	_ = s.Host.Sync(syncCtx)
	cancel()
	sess := sessionhost.Session{Name: got.Session, Options: got.Options}
	if now, ok := s.find(got.Session); ok {
		sess = now
	} else if req.Source != "" {
		s.holdUnlisted(key, got.Session, req.Panel, req.Resources, req.OriginSource, req.OriginPanel)
	}
	s.broadcast()
	return s.ship(sess), nil
}

// holdUnlisted keeps the order's key refused until the host lists the ship, for
// when the sync after a launch failed.
func (s *Service) holdUnlisted(key OrderKey, session, panel string, resources []string, originSource, originPanel string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.listing == nil {
		s.listing = map[OrderKey]unlisted{}
	}
	s.listing[key] = unlisted{session, panel, time.Now(), resources, originSource, originPanel}
}

// Pending is the ships launched but not yet in the host's list, as underway
// ships, so a count of running ships includes them. They are held for at most
// listedWithin.
func (s *Service) Pending() []Ship {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []Ship
	for key, u := range s.listing {
		if _, listed := s.find(u.session); listed || time.Since(u.at) >= listedWithin {
			continue
		}
		out = append(out, Ship{Session: u.session, Source: key.Source, OrderID: key.OrderID, Panel: u.panel, Resources: u.resources,
			OriginSource: u.originSource, OriginPanel: u.originPanel, Status: StatusUnderway})
	}
	return out
}

// lockOrder takes the order's key for the length of a launch; false when
// another launch of it holds the key.
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

// Get is one ship as the UI sees it.
func (s *Service) Get(session string) (Ship, bool) {
	sess, ok := s.find(session)
	if !ok {
		return Ship{}, false
	}
	return s.ship(sess), true
}

// ErrExited is InputSeen's answer for a ship that has exited.
var ErrExited = errors.New("ship has exited")

// InputSeen records that a browser sent input to a ship: it clears needs you,
// in Admiral's view, until the next waiting hook. It returns the ship as it is
// now, and ErrExited for an anchored or sailing-off ship.
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

// ErrNotFound is InputSeen's answer for a session that is not a ship.
var ErrNotFound = errors.New("ship not found")

// serveInputSeen is called when a browser sends input to a ship.
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

// serveDismiss ends an anchored ship. A live ship cannot be ended from the UI.
func (s *Service) serveDismiss(w http.ResponseWriter, r *http.Request) {
	session := r.PathValue("session")
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
	s.record(outcomes.Record{Ship: session, Event: "dismissed"}, nil)
	if err := s.killShip(ctx, session, false); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	_ = s.Host.Sync(ctx)
	s.keep()
	s.broadcast()
	writeJSON(w, http.StatusOK, map[string]any{"dismissed": session})
}

// launched writes the `launched` record of a ship just created and starts
// following it: no other record of a ship comes before this one.
func (s *Service) launched(req launcher.Request, got *launcher.Launched, panel string) {
	o := got.Options
	s.record(outcomes.Record{Ship: got.Session, Event: "launched", Fields: map[string]any{
		"fleet": o["fleet"], "harness": req.Harness, "skill": map[string]string{"kind": req.Skill.Kind, "name": o["skill"]},
		"source": req.Source, "panel": panel, "order_id": req.OrderID, "order_url": o["url"], "title": o["title"],
		"dir": o["dir"], "autopilot": req.Autopilot,
	}}, nil)
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.tracks == nil {
		s.tracks = map[string]*track{}
	}
	s.tracks[got.Session] = &track{tally: tally{Status: StatusUnderway}, saved: map[string]string{}}
}

// launchDone ends the launch serveLaunch counted, once the launch has failed or
// its ship is in the host's list.
func (s *Service) launchDone() {
	s.mu.Lock()
	s.launching--
	s.mu.Unlock()
}
