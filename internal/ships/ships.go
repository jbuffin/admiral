// Package ships is the ships of every fleet as the UI sees them (ADR 0012). Admiral
// keeps no ship table: every ship is read from tmux's session options each
// time, plus the position stored in state.json. This package serves them over
// HTTP and the WebSocket and launches new ones.
package ships

import (
	"context"
	"encoding/json"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/jbuffin/admiral/internal/launcher"
	"github.com/jbuffin/admiral/internal/server"
	"github.com/jbuffin/admiral/internal/sessionhost"
	"github.com/jbuffin/admiral/internal/state"
)

// StatusUnderway is every ship's status until acting on its state arrives.
const StatusUnderway = "underway"

// Ship is one ship as sent to the UI.
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
	OrderID    string          `json:"order_id"`
	Launched   string          `json:"launched"`
	Status     string          `json:"status"`
	Position   *state.Position `json:"position"`
}

// Service ties the session host, the launcher and the state together.
type Service struct {
	Host     *sessionhost.Host
	Launcher *launcher.Launcher
	State    *state.Store
	Hub      *server.Hub
}

func (s *Service) ship(session string, o map[string]string) Ship {
	sh := Ship{Session: session, Name: o["name"], Fleet: o["fleet"], FleetName: o["fleet-name"],
		FleetColor: o["fleet-color"], Harness: o["harness"], Dir: o["dir"], Repo: filepath.Base(o["dir"]),
		Source: o["source"], OrderID: o["order"], Launched: o["launched"], Status: StatusUnderway}
	if o["dir"] == "" {
		sh.Repo = ""
	}
	if sk := o["skill"]; sk != "" {
		sh.Skill = &sk
	}
	if p, ok := s.State.Position(session); ok {
		sh.Position = &p
	}
	return sh
}

// List is every ship in tmux right now.
func (s *Service) List() []Ship {
	out := []Ship{}
	for _, sess := range s.Host.Sessions() {
		if sess.IsShip() {
			out = append(out, s.ship(sess.Name, sess.Options))
		}
	}
	return out
}

func (s *Service) message() any {
	return map[string]any{"type": "ships", "ships": s.List()}
}

func (s *Service) broadcast() { s.Hub.BroadcastOnce("ships", s.message) }

// Connected sends a new client the ships.
func (s *Service) Connected(c *server.Client) { _ = c.SendOnce("ships", s.message) }

// Routes registers the ships API.
func (s *Service) Routes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/ships", s.serveList)
	mux.HandleFunc("POST /api/ships", s.serveLaunch)
	mux.HandleFunc("PUT /api/ships/{session}/position", s.servePosition)
}

// Run pushes the ships to every client whenever tmux or the state changes, and
// drops stored positions of ships that are gone.
func (s *Service) Run(ctx context.Context) {
	events, cancel := s.Host.Subscribe()
	defer cancel()
	select {
	case <-s.Host.Ready():
		s.keep()
		s.broadcast()
	case <-ctx.Done():
		return
	}
	for {
		select {
		case ev := <-events:
			gone := ev.Kind == sessionhost.SessionRemoved || ev.Kind == sessionhost.Resumed
			for more := true; more; { // one push for a burst
				select {
				case ev = <-events:
					gone = gone || ev.Kind == sessionhost.SessionRemoved || ev.Kind == sessionhost.Resumed
				default:
					more = false
				}
			}
			if gone {
				s.keep()
			}
			s.broadcast()
		case <-ctx.Done():
			return
		}
	}
}

func (s *Service) keep() {
	live := map[string]bool{}
	for _, sess := range s.Host.Sessions() {
		live[sess.Name] = true
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
	writeJSON(w, http.StatusOK, map[string]any{"ships": s.List()})
}

func (s *Service) serveLaunch(w http.ResponseWriter, r *http.Request) {
	var req launcher.Request
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4<<20)).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "request is not valid JSON: "+err.Error())
		return
	}
	// The spec's request carries no text: from the order sources unit the
	// server takes it from the order and ignores this field.
	got, err := s.Launcher.Launch(req)
	if err != nil {
		if le, ok := err.(*launcher.Error); ok {
			writeError(w, le.Status, le.Msg)
		} else {
			writeError(w, http.StatusInternalServerError, err.Error())
		}
		return
	}
	s.State.SetLastDir(req.Fleet, req.Harness, req.Dir)
	// Not the request's context: a client that hangs up must not abandon the read.
	syncCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	_ = s.Host.Sync(syncCtx)
	cancel()
	s.broadcast()
	writeJSON(w, http.StatusCreated, map[string]any{"ship": s.ship(got.Session, got.Options)})
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
