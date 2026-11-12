package ships

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/jbuffin/admiral/internal/conversation"
	"github.com/jbuffin/admiral/internal/keymap"
	"github.com/jbuffin/admiral/internal/outcomes"
	"github.com/jbuffin/admiral/internal/sessionhost"
)

// target is one pane a message or an interrupt goes to: the ship's own, or a crew boat's.
type target struct {
	pane      string
	what      string // "ship <name>" or "crew boat <name>", for messages
	exited    bool
	harness   string
	needsYou  bool
	reason    string // the pane's own hook reason, "" unless it needs you
	crew      string
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
		return target{pane: pane, what: "ship " + session, exited: st == StatusAnchored || st == StatusSailingOff,
			harness: sess.Options["harness"], needsYou: st == StatusNeedsYou, reason: reasonOf(sess),
			clearSeen: func() error { _, err := s.InputSeen(session); return err }}, true
	}
	for _, c := range sess.Crew {
		if c.Name == name {
			return target{pane: c.PaneID, what: "crew boat " + name, exited: c.State == sessionhost.CrewAnchored,
				harness: c.Harness, needsYou: c.State == sessionhost.CrewNeedsYou, reason: c.Reason, crew: name,
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
		writeError(w, http.StatusConflict, t.what+" has exited")
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
		writeError(w, http.StatusConflict, t.what+" has exited")
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
	s.mu.Lock()
	tr := s.tracks[session]
	s.mu.Unlock()
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
	// killShip ran while the host still listed the ship as live, so it left the files for now.
	s.mu.Lock()
	written := tr != nil && tr.end != nil && tr.end.written
	s.mu.Unlock()
	if written {
		s.removeShipFiles(session, tr)
	}
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

type answerRequest struct {
	Card   string `json:"card"`
	Reason string `json:"reason"`
	Choice string `json:"choice"`
	Item   string `json:"item"`
	Option int    `json:"option"`
}

// valid says whether the body is a whole answer; the pane's state is checked after it.
func (a answerRequest) valid() bool {
	if a.Reason == "" {
		return false
	}
	switch a.Card {
	case "permission":
		return a.Choice == "allow" || a.Choice == "deny"
	case "question":
		return a.Item != "" && a.Option >= 1
	}
	return false
}

// serveAnswer picks an item of the pane's permission or question menu, but only while the pane is on that very card.
func (s *Service) serveAnswer(w http.ResponseWriter, r *http.Request) {
	var req answerRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<16)).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "request is not valid JSON: "+err.Error())
		return
	}
	if !req.valid() {
		writeError(w, http.StatusBadRequest, "the answer needs a card, its reason, and a choice or an item and option")
		return
	}
	session := r.PathValue("session")
	t, ok := s.resolve(w, session, r.PathValue("name"))
	if !ok {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	// One answer at a time per pane, from the gate to the state change: a second tap must see what the first did.
	mu, _ := s.answering.LoadOrStore(t.pane, new(sync.Mutex))
	mu.(*sync.Mutex).Lock()
	defer mu.(*sync.Mutex).Unlock()
	keys, why := s.answerKeys(ctx, t, session, req)
	if why != "" {
		writeError(w, http.StatusConflict, why)
		return
	}
	if err := s.tmux().SendLiteral(ctx, t.pane, keys); err != nil {
		writeError(w, http.StatusBadGateway, "could not send the answer: "+err.Error())
		return
	}
	if req.Choice == "deny" {
		// The pane sits at its prompt after a deny; Claude's hooks don't say so, and Codex's Interrupt hook says it only after a flush, so write it now.
		if err := s.Host.SetPaneNeedsYou(ctx, t.pane, "stop"); err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		_ = s.Host.Sync(ctx)
		s.broadcast()
	} else if err := t.clearSeen(); err != nil && !errors.Is(err, ErrExited) && !errors.Is(err, ErrNotFound) {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"answered": true})
}

// answerKeys gives the keys that answer, or why the pane cannot take the answer now.
func (s *Service) answerKeys(ctx context.Context, t target, session string, req answerRequest) (keys, why string) {
	m, ok := keymap.For(t.harness)
	switch {
	case t.exited:
		return "", t.what + " has exited"
	case !ok:
		return "", t.what + " has no answer keys for its harness: answer in its terminal"
	}
	// The poll cache can trail a hook by half a second, so the pane's own options decide.
	state, err := s.tmux().ReadPaneOption(ctx, t.pane, "state")
	if err != nil {
		return "", t.what + " could not be read: " + err.Error()
	}
	reason, err := s.tmux().ReadPaneOption(ctx, t.pane, "reason")
	if err != nil {
		return "", t.what + " could not be read: " + err.Error()
	}
	if state != "needs_you" || reason != req.Reason {
		return "", t.what + " is no longer asking that"
	}
	if req.Card == "permission" {
		switch reason {
		case "permission:AskUserQuestion", "permission:ExitPlanMode":
			return "", "that menu is not a yes or no: answer in the terminal"
		}
		if !strings.HasPrefix(reason, "permission:") && !(t.harness == "copilot" && reason == "notification:permission_prompt") {
			return "", t.what + " is no longer asking that"
		}
		if !m.CardTool(strings.TrimPrefix(reason, "permission:")) {
			return "", "that menu has no recorded keys: answer in the terminal"
		}
		keys, _ = m.Permission(req.Choice)
		return keys, ""
	}
	// PermissionRequest fires for AskUserQuestion too and overwrites the question's reason. Codex rings a bell for a
	// question, which proves nothing alone: the observer's pending question below is what it asks.
	asking := reason == "question" || reason == "permission:AskUserQuestion"
	if t.harness == "codex" {
		asking = reason == "bell"
	}
	if !asking {
		return "", t.what + " is no longer asking that"
	}
	if t.harness == "codex" && s.Approving != nil && s.Approving(session, t.crew) {
		return "", "an approval is waiting too: answer in the terminal"
	}
	var qs []conversation.Item
	if s.Questions != nil {
		qs = s.Questions(session, t.crew)
	}
	if len(qs) == 0 {
		return "", "that question is not the one the pane is asking"
	}
	q := qs[len(qs)-1]
	switch {
	case q.ID != req.Item:
		return "", "that question is not the one the pane is asking"
	case len(qs) > 1 && callOf(qs[len(qs)-2].ID) == callOf(q.ID):
		return "", "the call holds several questions: answer in the terminal"
	case !q.Question.Pending:
		return "", "that question is not the one the pane is asking"
	case q.Question.MultiSelect:
		return "", "a multi-select question is answered in the terminal"
	case req.Option > len(q.Question.Options):
		return "", "the question has no option " + strconv.Itoa(req.Option)
	}
	keys, ok = m.Question(req.Option)
	if !ok {
		return "", "the question has no key for option " + strconv.Itoa(req.Option)
	}
	return keys, ""
}

// callOf is the id of the transcript line an item came from: items of one line share it before the `#`.
func callOf(id string) string { return strings.SplitN(id, "#", 2)[0] }
