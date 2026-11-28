package ships

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/jbuffin/admiral/internal/inbox"
	"github.com/jbuffin/admiral/internal/outcomes"
	"github.com/jbuffin/admiral/internal/sessionhost"
)

// StopAnswered is who answered a stop: Via is "admiralbot" for the AdmiralBot actor token and empty for the engineer.
type StopAnswered struct {
	StopID string `json:"stop_id"`
	Via    string `json:"via,omitempty"`
}

// StopAnswer answers one stop of a ship: a permission prompt by Choice ("allow" or "deny"), a finished turn by Text.
// Reason is the reason the card was posted for; the answer holds only while the pane's live state, Reason and StopID still match.
type StopAnswer struct {
	StopID string
	Reason string
	Choice string
	Text   string
}

// ErrStopMoved is AnswerStop's refusal when the pane no longer waits on that stop; nothing was sent.
var ErrStopMoved = errors.New("the ship is no longer at that stop")

// StopRefused is AnswerStop's refusal of an answer the stop cannot take: a menu with no recorded keys, an unknown choice, text that cannot be typed.
type StopRefused struct{ Msg string }

func (e *StopRefused) Error() string { return e.Msg }

// AnswerStop takes the pane's answer lock, re-reads the live state, reason and stop id, and only then sends the keys or types the text.
func (s *Service) AnswerStop(ctx context.Context, session string, a StopAnswer) error {
	sess, ok := s.find(session)
	if !ok {
		return ErrNotFound
	}
	pane := sess.PaneID
	if pane == "" {
		pane = sessionhost.ShipTarget(session)
	}
	// The same mutex serveAnswer holds: a second answer must see what the first did.
	mu, _ := s.answering.LoadOrStore(pane, new(sync.Mutex))
	mu.(*sync.Mutex).Lock()
	defer mu.(*sync.Mutex).Unlock()
	// Not the request's context: a client that hangs up must not leave a half-sent answer.
	ctx2, cancel := context.WithTimeout(context.WithoutCancel(ctx), time.Minute)
	defer cancel()
	tm := s.tmux()
	live := map[string]string{}
	for _, name := range []string{"state", "reason", sessionhost.OptStopID, sessionhost.OptStopAnswered} {
		v, err := tm.ReadPaneOption(ctx2, pane, name)
		if err != nil {
			return fmt.Errorf("ship %s could not be read: %w", session, err)
		}
		live[name] = v
	}
	if status(sess, time.Now()) == StatusAnchored || live["state"] != "needs_you" || live["reason"] != a.Reason || !stopStillOpen(a.StopID, live[sessionhost.OptStopID], live[sessionhost.OptStopAnswered]) {
		return ErrStopMoved
	}
	via := viaOfCtx(ctx)
	mark := a.StopID
	if via != "" {
		mark += " " + via
	}
	if a.Reason == "stop" {
		text := a.Text
		switch {
		case a.Choice != "":
			return &StopRefused{"a finished turn takes text, not a choice"}
		case !inbox.Typable(text):
			return &StopRefused{"the text is empty or holds a control character, so it can't be typed at a prompt"}
		case via != "" && (strings.HasPrefix(strings.TrimSpace(text), "/") || strings.HasPrefix(strings.TrimSpace(text), "!")):
			return &StopRefused{"AdmiralBot may give a ship a prompt, never a harness command: the text can't start with / or !"}
		case len(text) > sessionhost.MaxPaste:
			return &StopRefused{fmt.Sprintf("the text is %d bytes; at most %d", len(text), sessionhost.MaxPaste)}
		}
		if err := s.Host.MarkStopAnswered(ctx2, pane, mark); err != nil {
			return err
		}
		typed, err := s.typeAtPrompt(ctx2, pane, sess.Options["harness"], text)
		if !typed {
			_ = s.Host.MarkStopAnswered(ctx2, pane, "")
		} else {
			s.record(outcomes.Record{Ship: session, Event: "resumed", Fields: withVia(map[string]any{"live": true}, via)}, nil)
		}
		return err
	}
	if a.Text != "" {
		return &StopRefused{"a permission prompt takes a choice, not text"}
	}
	if a.Choice != "allow" && a.Choice != "deny" {
		return &StopRefused{"the choice is allow or deny"}
	}
	t := target{pane: pane, what: "ship " + session, harness: sess.Options["harness"], needsYou: true, reason: a.Reason,
		clearSeen: func() error { _, err := s.InputSeen(session); return err }}
	keys, _, why := s.answerKeys(ctx2, t, session, answerRequest{Card: "permission", Reason: a.Reason, Choice: a.Choice})
	if why != "" {
		return &StopRefused{why}
	}
	if err := s.Host.MarkStopAnswered(ctx2, pane, mark); err != nil {
		return err
	}
	if err := tm.SendLiteral(ctx2, pane, keys); err != nil {
		_ = s.Host.MarkStopAnswered(ctx2, pane, "")
		return fmt.Errorf("could not send the answer: %w", err)
	}
	return s.finishPermission(ctx2, t, session, a.Choice)
}
