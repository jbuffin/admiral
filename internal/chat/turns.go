package chat

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/jbuffin/admiral/internal/sessionhost"
)

// A queued turn is one message Admiral types into a fleet's chat by itself, and it can't act: while it runs the actor
// guard refuses AdmiralBot's writes. Items wait for an idle chat, one at a time, and are never merged with the
// engineer's own messages.

const (
	turnsFile     = "turns.json"
	deliveredKeep = 300
	// turnStartWait is how long a pasted turn may go without its prompt hook before the queue moves on.
	turnStartWait = 2 * time.Minute
	turnLoopEvery = 500 * time.Millisecond
)

type queuedTurn struct {
	ID   string `json:"id"`
	Text string `json:"text"`
}

// turnState is one fleet's queue. Its gate state (active, started) is in memory only, so a restarted Admiral starts
// with the gate open; the queue and the delivered ids are in the file.
type turnState struct {
	mu        sync.Mutex
	loaded    bool
	queue     []queuedTurn
	delivered []string
	reported  []string // ship cards whose reply was handed over, so one reply pushes once

	activeID string // the id of the turn that is active
	active   bool   // a turn was pasted and has not ended
	started  bool   // its prompt hook has fired
	pasteKey string // PromptKey just before the paste
	runKey   string // PromptKey once the turn started
	pastedAt time.Time

	// A message pasted by send whose turn may not have started: its prompt hook fires some time after the paste.
	sendHeld bool
	sendKey  string // PromptKey just before that paste; "" when it couldn't be read
	sendAt   time.Time

	clearPending bool   // New chat is waiting for the queue to drain
	clearPane    string // the chat session it was asked for
	clearing     bool   // the deferred clear is running on its own goroutine
	lastErr      string
}

func (s *fleetChat) turnsPath() string { return filepath.Join(s.Home, "chat", s.key, turnsFile) }

// turns runs f on the fleet's queue with its file read.
func (s *fleetChat) turns(f func(t *turnState)) {
	t := &s.tq
	t.mu.Lock()
	defer t.mu.Unlock()
	if !t.loaded {
		t.loaded = true
		var file struct {
			Queue     []queuedTurn `json:"queue"`
			Delivered []string     `json:"delivered"`
			Reported  []string     `json:"reported"`
		}
		b, err := os.ReadFile(s.turnsPath())
		switch {
		case errors.Is(err, os.ErrNotExist):
		case err != nil:
			log.Printf("admiral: chat %s: report queue: %v", s.key, err)
		case json.Unmarshal(b, &file) != nil:
			log.Printf("admiral: chat %s: %s is not valid JSON, kept as %s.bad; starting with an empty queue", s.key, turnsFile, turnsFile)
			_ = os.Rename(s.turnsPath(), s.turnsPath()+".bad")
			file.Queue, file.Delivered, file.Reported = nil, nil, nil
		}
		t.queue, t.delivered, t.reported = file.Queue, file.Delivered, file.Reported
	}
	f(t)
}

// saveTurnsLocked writes the queue by temp file and rename; the caller holds t.mu.
func (s *fleetChat) saveTurnsLocked(t *turnState) error {
	b, err := json.MarshalIndent(map[string]any{"queue": t.queue, "delivered": t.delivered, "reported": t.reported}, "", "  ")
	if err != nil {
		return err
	}
	path := s.turnsPath()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), turnsFile+".*.tmp")
	if err != nil {
		return err
	}
	_, werr := tmp.Write(b)
	if werr == nil {
		werr = tmp.Sync()
	}
	if cerr := tmp.Close(); werr == nil {
		werr = cerr
	}
	if werr != nil {
		_ = os.Remove(tmp.Name())
		return werr
	}
	if err := os.Rename(tmp.Name(), path); err != nil {
		_ = os.Remove(tmp.Name())
		return err
	}
	return nil
}

// EnqueueTurn queues a turn that can't act for the fleet's chat under a stable id; it only records, TurnTick delivers.
// An id already queued or delivered queues nothing, so a re-posted event never types twice.
func (s *Service) EnqueueTurn(fleet, id, text string) error {
	if _, _, ok := s.Config().ChatFleet(fleet); !ok {
		return fmt.Errorf("no fleet %s", fleet)
	}
	text = sessionhost.CleanMessage(text)
	switch {
	case id == "":
		return errors.New("a queued turn needs an id")
	case text == "":
		return errors.New("a queued turn needs its text")
	case len(text) > sessionhost.MaxPaste:
		return fmt.Errorf("the turn is %d bytes; at most %d", len(text), sessionhost.MaxPaste)
	}
	var err error
	s.chat(fleet).turns(func(t *turnState) {
		for _, q := range t.queue {
			if q.ID == id {
				return
			}
		}
		for _, d := range t.delivered {
			if d == id {
				return
			}
		}
		t.queue = append(t.queue, queuedTurn{ID: id, Text: text})
		if err = s.chat(fleet).saveTurnsLocked(t); err != nil {
			t.queue = t.queue[:len(t.queue)-1]
		}
	})
	return err
}

// QueuedTurns are the ids of the fleet's undelivered turns, head first.
func (s *Service) QueuedTurns(fleet string) []string {
	var ids []string
	s.chat(fleet).turns(func(t *turnState) {
		for _, q := range t.queue {
			ids = append(ids, q.ID)
		}
	})
	return ids
}

// TurnCantAct is whether a queued turn is pasted and not over, so AdmiralBot's writes are refused. It counts from the
// paste, not from the prompt hook: a stalled loop or a hook that never fires over-gates until the turn-start wait.
func (s *Service) TurnCantAct(fleet string) bool {
	running := false
	s.chat(fleet).turns(func(t *turnState) { running = t.active })
	return running
}

// turnBusy is whether anything is queued or running, so a reset waits.
func (s *fleetChat) turnBusy() bool {
	busy := false
	s.turns(func(t *turnState) { busy = len(t.queue) > 0 || t.active })
	return busy
}

// RunTurns is the queue's loop. Nothing in a tick blocks for long: a deferred New chat's clear runs on its own goroutine.
func (s *Service) RunTurns(ctx context.Context) {
	t := time.NewTicker(turnLoopEvery)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			s.TurnTick()
		}
	}
}

// TurnTick is one pass of the queue's loop for every fleet: it tracks the running turn, types the head item when the
// chat is idle, and carries out a New chat clear that was deferred.
func (s *Service) TurnTick() {
	if s.Host == nil {
		return
	}
	for _, fc := range s.onFleets() {
		fc.turnTick()
	}
}

func (s *fleetChat) turnTick() {
	sess, running := s.session()
	active := false
	s.turns(func(t *turnState) {
		active = t.active
		// A deferred clear belongs to the chat session it was asked in.
		if t.clearPending && (!running || sess.PaneID != t.clearPane) {
			t.clearPending = false
		}
	})
	if active {
		s.trackTurn()
	}
	var head *queuedTurn
	var clearNow bool
	s.turns(func(t *turnState) {
		if t.active {
			return
		}
		if len(t.queue) > 0 {
			q := t.queue[0]
			head = &q
		}
		clearNow = head == nil && t.clearPending && !t.clearing
	})
	switch {
	case head != nil:
		s.deliver(*head)
	case clearNow && s.idleNow():
		claimed := false
		s.turns(func(t *turnState) {
			if t.clearPending && !t.clearing {
				t.clearing, claimed = true, true
			}
		})
		if claimed {
			go func() {
				defer s.turns(func(t *turnState) { t.clearing = false })
				s.clearDeferred()
			}()
		}
	}
}

// trackTurn ends the running turn when the chat idles again after it started, or when its prompt stamp moves past the
// turn's own (the engineer's prompt has started), or when the session is gone or never took the message. A failed read
// of the stamp changes nothing.
func (s *fleetChat) trackTurn() {
	_, running := s.session()
	key, ok, idle := "", false, false
	if running {
		key, ok = s.promptKey(s.key)
		idle = s.paneIdle()
	}
	var endedID string
	var how TurnEnd
	s.turns(func(t *turnState) {
		end := func(h TurnEnd) { t.active, endedID, how = false, t.activeID, h }
		switch {
		case !t.active:
		case !running:
			end(TurnDropped)
		case !ok:
		case !t.started:
			if key != t.pasteKey {
				t.started, t.runKey = true, key
			} else if time.Since(t.pastedAt) > turnStartWait {
				end(TurnDropped)
			}
		case key != t.runKey || idle:
			end(TurnEndedNormally)
		}
	})
	if endedID != "" {
		s.endTurn(endedID, how)
	}
}

// afterSend records a paste that isn't a report: its turn may not have started when the queue next looks, and a
// report typed then would land inside it. A send also drops a deferred New chat, since the engineer has moved on;
// that policy is this one place.
func (s *fleetChat) afterSend(key string) {
	s.turns(func(t *turnState) {
		t.sendHeld, t.sendKey, t.sendAt = true, key, time.Now()
		t.clearPending = false
	})
}

// sendHolds is whether a message pasted by send is still starting its turn. It lets go once the stamp has moved past
// the one the paste went over and the pane idles again, or after the turn-start wait; an unreadable stamp holds.
func (s *fleetChat) sendHolds() bool {
	var held bool
	var from string
	var at time.Time
	s.turns(func(t *turnState) { held, from, at = t.sendHeld, t.sendKey, t.sendAt })
	if !held {
		return false
	}
	key, ok := s.promptKey(s.key)
	if !ok {
		return true
	}
	if time.Since(at) <= turnStartWait && (from == "" || key == from || !s.paneIdle()) {
		return true
	}
	s.turns(func(t *turnState) { t.sendHeld = false })
	return false
}

// idleNow is whether the chat can take a turn: running, no turn and no unanswered question. The snapshot's state reads
// "no hook yet" as idle and lags the host, so the pane's own state is read too.
func (s *fleetChat) idleNow() bool {
	snap := s.current()
	return snap.Running && snap.State == "idle" && !s.resetting.Load() && s.paneIdle()
}

// deliver pastes the head item when the chat is idle. The chat's op lock is held for the paste only, never across the
// turn, and a busy lock means the engineer or a reset has the chat: try again next tick.
func (s *fleetChat) deliver(q queuedTurn) {
	if !s.idleNow() || !s.op.TryLock() {
		return
	}
	defer s.op.Unlock()
	if s.sendHolds() {
		return
	}
	pane, _, _ := s.ready()
	if pane == "" || !s.idleNow() {
		return
	}
	key, ok := s.promptKey(s.key)
	if !ok {
		return
	}
	s.th.mu.Lock()
	prev := threadState{id: s.th.id, since: s.th.since, seen: s.th.seen, ended: s.th.ended}
	s.th.open(q.ID)
	s.th.mu.Unlock()
	if err := s.pasteMessage(pane, q.Text); err != nil {
		s.th.mu.Lock()
		s.th.id, s.th.since, s.th.seen, s.th.ended = prev.id, prev.since, prev.seen, prev.ended
		s.th.mu.Unlock()
		s.turns(func(t *turnState) {
			if msg := err.Error(); msg != t.lastErr {
				t.lastErr = msg
				log.Printf("admiral: chat %s: queued turn %s: %v", s.key, q.ID, err)
			}
		})
		return
	}
	s.turns(func(t *turnState) {
		t.lastErr = ""
		if len(t.queue) > 0 && t.queue[0].ID == q.ID {
			t.queue = t.queue[1:]
		}
		t.delivered = append(t.delivered, q.ID)
		if n := len(t.delivered) - deliveredKeep; n > 0 {
			t.delivered = t.delivered[n:]
		}
		t.active, t.activeID, t.started, t.pasteKey, t.runKey, t.pastedAt = true, q.ID, false, key, "", time.Now()
		if err := s.saveTurnsLocked(t); err != nil {
			log.Printf("admiral: chat %s: could not save the report queue: %v", s.key, err)
		}
	})
}

// clearDeferred runs the New chat that waited for the queue, once the chat is idle.
func (s *fleetChat) clearDeferred() {
	if !s.idleNow() || !s.op.TryLock() {
		return
	}
	defer s.op.Unlock()
	defer s.resetting.Store(false)
	still := false
	s.turns(func(t *turnState) { still, t.clearPending = t.clearPending, false })
	if !still {
		return
	}
	if _, lerr := s.newChatLocked(false); lerr != nil {
		log.Printf("admiral: chat %s: the deferred New chat: %v", s.key, lerr.Msg)
	}
}

// ReportReply is the reply AdmiralBot gave to a report turn: the ship card it belongs to and its first message.
type ReportReply struct {
	CardID    string
	MessageID string
	Text      string
}

// DigestReply is the reply AdmiralBot gave to a fleet digest turn: the turn's id and its first message.
type DigestReply struct {
	TurnID    string
	MessageID string
	Text      string
}

// TurnEnd is how a queued turn ended.
type TurnEnd int

const (
	TurnEndedNormally TurnEnd = iota
	// TurnDropped is a turn whose paste never took or whose session went away.
	TurnDropped
)
