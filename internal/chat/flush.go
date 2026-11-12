package chat

import (
	"context"
	"log"
	"strings"
	"sync"
	"time"

	"github.com/jbuffin/admiral/internal/sessionhost"
)

// resetState is what the scheduler remembers of one fleet's chat between ticks, all in memory: after a restart the
// clocks start from the first tick, which only delays a reset.
type resetState struct {
	mu        sync.Mutex
	pane      string // the session these figures are for
	idleSince time.Time
	lastReset time.Time // or when Admiral first saw the session
	turnKey   string    // PromptKey at the last reset
	holdUntil time.Time // after an abandoned flush
	running   bool      // a scheduled reset is in flight
}

// begin starts the clocks over for a session; the caller holds mu.
func (rs *resetState) begin(pane string, now time.Time) {
	// A second back, so a status line kept in the same second as the first sight still counts as this session's.
	rs.pane, rs.lastReset = pane, now.Add(-time.Second)
	rs.idleSince, rs.holdUntil, rs.turnKey, rs.running = time.Time{}, time.Time{}, "", false
}

func (rs *resetState) forget(pane string) {
	rs.mu.Lock()
	defer rs.mu.Unlock()
	rs.begin(pane, time.Time{})
}

// ResetTick starts the reset each running chat is due; Run calls it on its tick. A reset runs on its own goroutine,
// one at a time per fleet, so a slow flush never holds up the tick.
func (s *Service) ResetTick() {
	if s.Host == nil {
		return
	}
	now := time.Now()
	for _, fc := range s.onFleets() {
		fc.resetTick(now)
	}
}

func (s *fleetChat) resetTick(now time.Time) {
	sess, running := s.session()
	rs := &s.rst
	if !running {
		rs.forget("")
		return
	}
	snap := s.current()
	rs.mu.Lock()
	fresh := rs.pane != sess.PaneID
	if fresh {
		rs.begin(sess.PaneID, now)
	}
	if snap.State == "idle" {
		if rs.idleSince.IsZero() {
			rs.idleSince = now
		}
	} else {
		rs.idleSince = time.Time{}
	}
	busy := rs.running || now.Before(rs.holdUntil) || s.resetting.Load()
	idleSince, lastReset := rs.idleSince, rs.lastReset
	rs.mu.Unlock()
	if fresh {
		key := s.PromptKey(s.key)
		rs.mu.Lock()
		rs.turnKey = key
		rs.mu.Unlock()
	}
	fleet, off, ok := s.Config().ChatFleet(s.key)
	if !ok || off != "" || busy || idleSince.IsZero() {
		return
	}
	short, long := ResetIdleWindows()
	in := ResetInput{
		Now: now, Running: true, State: snap.State, QuestionWaiting: unanswered(snap.Questions), IdleSince: idleSince,
		Threshold: fleet.Chat.ContextThreshold, LastReset: lastReset, ShortIdle: short, LongIdle: long, TurnSinceReset: true,
	}
	// The status line is read only once a context rule could fire on it.
	if now.Sub(idleSince) >= short {
		if kept, err := s.readPaneOption(sessionhost.StatuslineOption[len(sessionhost.Prefix):]); err == nil {
			in.Context, in.ContextSeen, in.HaveContext = ContextTokens([]byte(kept))
		}
	}
	rule := DueReset(in)
	if rule == ResetDaily && s.PromptKey(s.key) == s.turnKeyAtReset() {
		return
	}
	if rule == ResetNone {
		return
	}
	rs.mu.Lock()
	if rs.running {
		rs.mu.Unlock()
		return
	}
	rs.running = true
	rs.mu.Unlock()
	go func() {
		defer func() {
			rs.mu.Lock()
			rs.running = false
			rs.mu.Unlock()
		}()
		s.scheduledReset(rule)
	}()
}

func (s *fleetChat) turnKeyAtReset() string {
	s.rst.mu.Lock()
	defer s.rst.mu.Unlock()
	return s.rst.turnKey
}

func (s *fleetChat) readPaneOption(name string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	return s.server().ReadPaneOption(ctx, "="+s.name()+":", name)
}

// scheduledReset flushes, clears and marks one chat that a rule found due, unless the chat is no longer idle.
func (s *fleetChat) scheduledReset(rule ResetRule) {
	s.op.Lock()
	defer s.op.Unlock()
	snap, err := s.refresh(true)
	if err != nil || !snap.Running || snap.State != "idle" {
		return
	}
	pane, path, dir, raw, started := s.clearTarget()
	if !started {
		return
	}
	s.resetting.Store(true)
	defer s.resetting.Store(false)
	if !s.flush(pane) {
		log.Printf("admiral: chat %s: %s reset abandoned: the flush turn did not end", s.key, rule)
		s.holdReset()
		return
	}
	if err := s.clearChat(pane, path, dir, raw, MarkerRefreshed); err != nil {
		log.Printf("admiral: chat %s: %s reset: %v", s.key, rule, err)
		s.holdReset()
	}
}

// holdReset makes the next try a full idle window away, for any rule.
func (s *fleetChat) holdReset() {
	short, _ := ResetIdleWindows()
	now := time.Now()
	s.rst.mu.Lock()
	s.rst.idleSince, s.rst.holdUntil = now, now.Add(short)
	s.rst.mu.Unlock()
}

// clearTarget is where a clear goes and what the usage record reads; started is false until the first SessionStart.
func (s *fleetChat) clearTarget() (pane, path, dir, raw string, started bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.st.pane, s.st.path, s.configDir(), s.st.optRaw, s.st.optRaw != ""
}

// flush pastes the flush prompt and waits for its turn to end. It is false when the turn did not end in time,
// a question opened, or the prompt could not be pasted.
func (s *fleetChat) flush(pane string) bool {
	before := s.PromptKey(s.key)
	if err := s.pasteMessage(pane, FlushPrompt); err != nil {
		log.Printf("admiral: chat %s: flush: %v", s.key, err)
		return false
	}
	for end := time.Now().Add(FlushWait()); time.Now().Before(end); time.Sleep(100 * time.Millisecond) {
		if s.PromptKey(s.key) != before && s.paneIdle() {
			return true
		}
		if unanswered(s.questions(true)) {
			return false
		}
	}
	return false
}

// paneIdle reads the hooks' state straight from the pane: the host's copy can lag the prompt stamp.
func (s *fleetChat) paneIdle() bool {
	state, err := s.readPaneOption("state")
	if err != nil {
		return false
	}
	reason, _ := s.readPaneOption("reason")
	return mapState(state, reason) == "idle"
}

// clearChat sends /clear to the pane and leaves the marker; the caller holds op.
func (s *fleetChat) clearChat(pane, path, dir, raw, marker string) error {
	// What was said since the last read is logged before the reader skips to the end.
	_, _ = s.refresh(true)
	s.withdrawAndRender()
	s.mu.Lock()
	s.clearLocked()
	s.mu.Unlock()
	s.broadcast()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	// Typed, then Enter: the way an engineer runs a command.
	if err := s.server().SendLiteral(ctx, pane, resetCommand(s.harness())); err != nil {
		return err
	}
	if err := s.server().SendEnter(ctx, pane); err != nil {
		return err
	}
	s.recordReset(s.harness(), path, dir, s.key, raw)
	s.markReset(marker)
	s.noteReset()
	s.broadcast()
	return nil
}

// noteReset starts the scheduler's clocks over after a clear.
func (s *fleetChat) noteReset() {
	key := s.PromptKey(s.key)
	now := time.Now()
	s.rst.mu.Lock()
	s.rst.lastReset, s.rst.turnKey, s.rst.holdUntil = now, key, time.Time{}
	s.rst.mu.Unlock()
}

// hideFlush drops the flush turn from what the chat shows: its prompt and what Claude says until the next
// message that isn't it.
func hideFlush(ms []Message) []Message {
	out := make([]Message, 0, len(ms))
	hidden := false
	for _, m := range ms {
		if m.Kind == "message" && m.Role == "user" {
			hidden = strings.TrimSpace(m.Text) == FlushPrompt
		}
		if !hidden {
			out = append(out, m)
		}
	}
	return out
}
