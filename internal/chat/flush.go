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
	// Every rule waits out the short window, so the pane is read only after it.
	if now.Sub(idleSince) < short {
		return
	}
	key := s.PromptKey(s.key)
	in := ResetInput{
		Now: now, Running: true, State: snap.State, QuestionWaiting: unanswered(snap.Questions), IdleSince: idleSince,
		Threshold: fleet.Chat.ContextThreshold, LastReset: lastReset, ShortIdle: short, LongIdle: long,
		TurnSinceReset: key != s.turnKeyAtReset(),
	}
	if kept, err := s.readPaneOption(sessionhost.StatuslineOption[len(sessionhost.Prefix):]); err == nil {
		in.Context, in.ContextSeen, in.HaveContext = ContextTokens([]byte(kept))
	}
	rule := DueReset(in)
	if rule == ResetNone || !s.claimReset() {
		return
	}
	go func() {
		defer s.releaseReset()
		s.scheduledReset(rule, key, lastReset)
	}()
}

// claimReset marks a reset as under way before it takes op, so a send is refused from the decision on and no
// second reset starts for the fleet.
func (s *fleetChat) claimReset() bool {
	s.rst.mu.Lock()
	defer s.rst.mu.Unlock()
	if s.rst.running || !s.resetting.CompareAndSwap(false, true) {
		return false
	}
	s.rst.running = true
	return true
}

func (s *fleetChat) releaseReset() {
	s.rst.mu.Lock()
	defer s.rst.mu.Unlock()
	s.rst.running = false
	s.resetting.Store(false)
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

// scheduledReset flushes, clears and marks one chat that a rule found due, unless the chat is no longer idle or
// something happened since the tick decided: key is the prompt stamp it saw, seen the last reset it saw.
func (s *fleetChat) scheduledReset(rule ResetRule, key string, seen time.Time) {
	s.op.Lock()
	defer s.op.Unlock()
	snap, err := s.refresh(true)
	if err != nil || !snap.Running || snap.State != "idle" || !s.paneIdle() {
		return
	}
	s.rst.mu.Lock()
	moved := s.rst.lastReset != seen
	s.rst.mu.Unlock()
	if moved || s.PromptKey(s.key) != key {
		s.restartIdleClock()
		return
	}
	pane, path, dir, raw, started := s.clearTarget()
	if !started {
		return
	}
	// New chat clears the flag when it finishes, which may have been while this waited for op.
	s.resetting.Store(true)
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

func (s *fleetChat) restartIdleClock() {
	s.rst.mu.Lock()
	s.rst.idleSince = time.Now()
	s.rst.mu.Unlock()
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
	s.startFlush()
	if err := s.pasteMessage(pane, FlushPrompt); err != nil {
		log.Printf("admiral: chat %s: flush: %v", s.key, err)
		s.dropFlush()
		return false
	}
	for end := time.Now().Add(FlushWait()); time.Now().Before(end); time.Sleep(100 * time.Millisecond) {
		if s.PromptKey(s.key) != before && s.paneIdle() {
			s.endFlush()
			return true
		}
		if unanswered(s.questions(true)) {
			s.endFlush()
			return false
		}
	}
	// A turn still running may write its reply after the clear; the anchor stays open and hides it.
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

// flushAnchor is the flush turn's place in the chat: the message that was last when the prompt was pasted, then
// the prompt's own ID once it shows up, and the last message of the turn once the flush loop saw it end. Only
// that stretch is hidden, so a reply after an engineer answers a question card shows, and the turn stays hidden
// across a clear and once the window slides past its prompt.
type flushAnchor struct {
	active bool
	pre    string // the last message before the paste, "" when there was none
	from   string // the prompt's message ID, "" until it is seen
	until  string // the turn's last message, once closed
	closed bool   // false: the turn may still be writing, so it ends at the next user message
}

func isUser(m Message) bool { return m.Kind == "message" && m.Role == "user" }

func indexOfMessage(ms []Message, id string) int {
	for i, m := range ms {
		if m.ID == id {
			return i
		}
	}
	return -1
}

// hide returns ms without the flush turn and moves the anchor along: it learns the prompt's ID and closes once a
// user message follows an open turn. The caller holds the chat's lock.
func (a *flushAnchor) hide(ms []Message) []Message {
	if !a.active {
		return ms
	}
	if a.from == "" {
		i := 0
		if p := indexOfMessage(ms, a.pre); p >= 0 {
			i = p + 1
		}
		for ; i < len(ms) && !(isUser(ms[i]) && strings.HasSuffix(strings.TrimSpace(ms[i].Text), FlushPrompt)); i++ {
		}
		if i == len(ms) {
			return ms
		}
		a.from = ms[i].ID
	}
	lo := indexOfMessage(ms, a.from) // -1 once a clear or the window's slide dropped the prompt
	hi := 0
	switch {
	case a.closed:
		hi = indexOfMessage(ms, a.until) + 1
	default:
		hi = len(ms)
		for i := max(lo+1, 0); i < len(ms); i++ {
			if isUser(ms[i]) {
				hi = i
				a.closed = true
				if i > 0 {
					a.until = ms[i-1].ID
				}
				break
			}
		}
	}
	lo = max(lo, 0)
	if hi <= lo {
		if a.closed && lo == 0 {
			a.active = false
		}
		return ms
	}
	out := make([]Message, 0, len(ms)-(hi-lo))
	return append(append(out, ms[:lo]...), ms[hi:]...)
}

func (s *fleetChat) startFlush() {
	s.mu.Lock()
	defer s.mu.Unlock()
	pre := ""
	if ms := s.st.served(); len(ms) > 0 {
		pre = ms[len(ms)-1].ID
	}
	s.st.flush = flushAnchor{active: true, pre: pre}
}

func (s *fleetChat) dropFlush() {
	s.mu.Lock()
	s.st.flush = flushAnchor{}
	s.mu.Unlock()
}

// endFlush closes the anchor at the turn's last message as of now.
func (s *fleetChat) endFlush() {
	_, _ = s.refresh(true)
	s.mu.Lock()
	defer s.mu.Unlock()
	a := &s.st.flush
	ms := s.st.served()
	if !a.active || a.from == "" || len(ms) == 0 {
		return
	}
	a.until, a.closed = ms[len(ms)-1].ID, true
}
