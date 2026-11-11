package ships

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/jbuffin/admiral/internal/outcomes"
	"github.com/jbuffin/admiral/internal/sessionhost"
)

// target is one pane a message or an interrupt goes to: the ship's own, or a crew boat's.
type target struct {
	pane      string
	exited    bool
	clearSeen func() error
}

// resolve finds the ship, or its crew boat when name is set. It writes the error response itself.
func (s *Service) resolve(w http.ResponseWriter, session, name string) (target, bool) {
	sess, ok := s.find(session)
	if !ok {
		writeError(w, http.StatusNotFound, "ship "+session+" not found")
		return target{}, false
	}
	if name == "" {
		pane := sess.PaneID
		if pane == "" {
			pane = sessionhost.ShipTarget(session)
		}
		st := status(sess, time.Now())
		return target{pane: pane, exited: st == StatusAnchored || st == StatusSailingOff,
			clearSeen: func() error { _, err := s.InputSeen(session); return err }}, true
	}
	for _, c := range sess.Crew {
		if c.Name == name {
			return target{pane: c.PaneID, exited: c.State == sessionhost.CrewAnchored,
				clearSeen: func() error { _, err := s.CrewInputSeen(session, c.WindowID); return err }}, true
		}
	}
	writeError(w, http.StatusNotFound, "crew boat "+name+" not found")
	return target{}, false
}

func (s *Service) tmux() sessionhost.Server {
	return sessionhost.NewServer(s.Launcher.Tmux, s.Launcher.Socket).WithoutUTF8().StderrErrors()
}

func (s *Service) serveSendInput(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Text string `json:"text"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "request is not valid JSON: "+err.Error())
		return
	}
	text := sessionhost.CleanMessage(req.Text)
	switch {
	case text == "":
		writeError(w, http.StatusBadRequest, "the message is empty")
		return
	case len(text) > sessionhost.MaxPaste:
		writeError(w, http.StatusBadRequest, fmt.Sprintf("the message is %d bytes; at most %d", len(text), sessionhost.MaxPaste))
		return
	}
	session, name := r.PathValue("session"), r.PathValue("name")
	t, ok := s.resolve(w, session, name)
	if !ok {
		return
	}
	if t.exited {
		writeError(w, http.StatusConflict, "ship "+session+" has exited")
		return
	}
	// Not the request's context: a client that hangs up must not leave a half-sent message.
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := s.tmux().PasteMessage(ctx, t.pane, text); err != nil {
		writeError(w, http.StatusBadGateway, "could not send the message: "+err.Error())
		return
	}
	if err := t.clearSeen(); err != nil && !errors.Is(err, ErrExited) && !errors.Is(err, ErrNotFound) {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"sent": true})
}

func (s *Service) serveInterrupt(w http.ResponseWriter, r *http.Request) {
	session := r.PathValue("session")
	t, ok := s.resolve(w, session, r.PathValue("name"))
	if !ok {
		return
	}
	if t.exited {
		writeError(w, http.StatusConflict, "ship "+session+" has exited")
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := s.tmux().SendEscape(ctx, t.pane); err != nil {
		writeError(w, http.StatusBadGateway, "could not interrupt: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"interrupted": true})
}

// serveStop ends a ship, live or anchored. A live ship is killed, so its `ended` is
// written here with signal KILL, as a dead pane's would be, before `stopped`.
func (s *Service) serveStop(w http.ResponseWriter, r *http.Request) {
	session := r.PathValue("session")
	// Not the request's context: a client that hangs up must not abandon the write.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	sess, ok := s.find(session)
	if !ok {
		writeError(w, http.StatusNotFound, "ship "+session+" not found")
		return
	}
	switch status(sess, time.Now()) {
	case StatusAnchored, StatusSailingOff:
		s.observe(ctx) // the ship's `ended` goes into the log before `stopped`
		s.retryEnded(sess)
	default:
		s.recordKilled(sess)
	}
	s.record(outcomes.Record{Ship: session, Event: "stopped"}, nil)
	if err := s.killShip(ctx, session, false); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	_ = s.Host.Sync(ctx)
	s.keep()
	s.broadcast()
	writeJSON(w, http.StatusOK, map[string]any{"stopped": session})
}

// recordKilled queues the `ended` of a live ship that is about to be killed.
func (s *Service) recordKilled(sess sessionhost.Session) {
	sess.PaneDead, sess.Signal, sess.DeadTime = true, "KILL", time.Now()
	s.mu.Lock()
	tr := s.tracks[sess.Name]
	if tr == nil || tr.end != nil {
		s.mu.Unlock()
		return
	}
	tr.end = &endState{}
	e := ending{sess, tr.tally, tr.end, tr}
	s.mu.Unlock()
	s.queueEnded(e)
}
