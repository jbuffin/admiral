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
	"time"

	"github.com/jbuffin/admiral/internal/home"
	"github.com/jbuffin/admiral/internal/launcher"
	"github.com/jbuffin/admiral/internal/server"
	"github.com/jbuffin/admiral/internal/sessionhost"
	"github.com/jbuffin/admiral/internal/state"
)

// Ship is one ship as sent to the UI. ExitCode and ExitSignal are set only on
// an anchored ship.
type Ship struct {
	Session    string          `json:"session"`
	Name       string          `json:"name"`
	Fleet      string          `json:"fleet"`
	FleetName  string          `json:"fleet_name"`
	FleetColor string          `json:"fleet_color"`
	Harness    string          `json:"harness"`
	Dir        string          `json:"dir"`
	Repo       string          `json:"repo"`
	Skill      *string         `json:"skill"`
	Source     string          `json:"source"`
	Title      string          `json:"title"`
	URL        string          `json:"url"`
	OrderID    string          `json:"order_id"`
	Launched   string          `json:"launched"`
	Status     string          `json:"status"`
	ExitCode   *int            `json:"exit_code"`
	ExitSignal *string         `json:"exit_signal"`
	Position   *state.Position `json:"position"`
}

// Orders is where a launch finds its order: the order sources.
type Orders interface {
	// Order is the text to launch with, the order's title, its repo and its web
	// URL. It refuses with a launcher.Error when the source or the order is
	// unknown.
	Order(source, id string) (text, title, repo, url string, err *launcher.Error)
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
}

func (s *Service) ship(sess sessionhost.Session) Ship {
	sh := FromSession(sess, time.Now())
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
		Source: o["source"], Title: o["title"], URL: o["url"], OrderID: o["order"], Launched: o["launched"], Status: status(sess, now)}
	sh.ExitCode, sh.ExitSignal = exit(sess)
	if o["dir"] == "" {
		sh.Repo = ""
	}
	if sk := o["skill"]; sk != "" {
		sh.Skill = &sk
	}
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
		// An exit-0 ship from before a restart is not shown sailing off again.
		for _, sess := range s.Host.Sessions() {
			if sess.IsShip() && exitedCleanly(sess) {
				_ = s.Host.KillSession(ctx, sess.Name)
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
		gone, changed := false, false
		select {
		case ev := <-events:
			for more := true; more; { // one push for a burst
				gone = gone || prunes(ev)
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
		b.WriteString(sh.Session + "=" + sh.Status + ";")
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
	if err := s.Host.SetPaneState(ctx, session, "needs_you", "bell"); err == nil {
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
			killed = s.Host.KillSession(ctx, sess.Name) == nil || killed
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
	// An order from a source is launched with the source's text, whatever the
	// client sent. Without a source the client's own text is used.
	if req.Source != "" {
		if HiddenOrders(s.List())[OrderKey{req.Source, req.OrderID}] {
			writeError(w, http.StatusConflict, "this order already has a ship")
			return
		}
		var lerr *launcher.Error
		if req.Text, req.Title, _, req.URL, lerr = s.Orders.Order(req.Source, req.OrderID); lerr != nil {
			writeError(w, lerr.Status, lerr.Msg)
			return
		}
	}
	got, err := s.Launcher.Launch(req)
	if err != nil {
		if le, ok := err.(*launcher.Error); ok {
			writeError(w, le.Status, le.Msg)
		} else {
			writeError(w, http.StatusInternalServerError, err.Error())
		}
		return
	}
	s.State.SetLastDir(req.Fleet, req.Harness, got.Options["dir"]) // the cleaned path the launch used
	if req.Source != "" {
		s.Orders.Launched(req.Source, req.OrderID)
	}
	// Not the request's context: a client that hangs up must not abandon the read.
	syncCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	_ = s.Host.Sync(syncCtx)
	cancel()
	s.broadcast()
	sess := sessionhost.Session{Name: got.Session, Options: got.Options}
	if now, ok := s.find(got.Session); ok {
		sess = now
	}
	writeJSON(w, http.StatusCreated, map[string]any{"ship": s.ship(sess)})
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
		if err := s.Host.SetPaneState(ctx, session, "working", ""); err != nil {
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
	if err := s.Host.KillSession(ctx, session); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	_ = s.Host.Sync(ctx)
	s.keep()
	s.broadcast()
	writeJSON(w, http.StatusOK, map[string]any{"dismissed": session})
}
