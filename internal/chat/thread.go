package chat

import (
	"cmp"
	"encoding/json"
	"log"
	"strings"
	"sync"

	"github.com/jbuffin/admiral/internal/chatlog"
)

// threadHeadLen is how much of a pasted turn identifies its user line in the transcript, which may reshape the rest.
const threadHeadLen = 200

// threadState decides which log entries belong to the report turn Admiral last typed. It is memory only: a restart
// mid-turn leaves the rest of that turn's entries unmarked, and the marks already in the log stay.
type threadState struct {
	mu   sync.Mutex
	id   string // the ship card whose report turn this is; "" when none is open
	head string // the start of the pasted text
	seen bool   // the turn's own user line has been logged
}

func threadHead(s string) string {
	s = strings.Join(strings.Fields(s), " ")
	if len(s) > threadHeadLen {
		s = s[:threadHeadLen]
	}
	return s
}

func (t *threadState) open(id, text string) { t.id, t.head, t.seen = id, threadHead(text), false }

// classify returns the thread a not yet logged message carries, and the change it makes to the state once it is logged.
// The thread stays open after the turn ends, so a late reply is still threaded, until the engineer or a new turn speaks.
func (t *threadState) classify(m Message) (string, func()) {
	noop := func() {}
	if t.id == "" || m.Kind != chatlog.KindMessage {
		return "", noop
	}
	id := t.id
	switch {
	case m.Role == "user" && m.notice:
		return "", noop
	case m.Role == "user" && !t.seen:
		h := threadHead(m.Text)
		if h != "" && (strings.HasPrefix(t.head, h) || strings.HasPrefix(h, t.head)) {
			return id, func() { t.seen = true }
		}
		return "", noop
	case m.Role == "user":
		return "", func() { t.id = "" }
	case m.Role == "assistant" && t.seen:
		return id, noop
	}
	return "", noop
}

// replyOf is the first assistant entry the log marks for the ship card.
func (s *fleetChat) replyOf(card string) (chatlog.Entry, bool) {
	es, err := s.history().Recent(s.key, HistoryWindow)
	s.noteLogErr(err)
	for _, e := range es {
		if e.Thread == card && e.Kind == chatlog.KindMessage && e.Role == "assistant" {
			return e, true
		}
	}
	return chatlog.Entry{}, false
}

// endTurn is called once for each way trackTurn ends the turn that was pasted under id. A normal end reads the
// transcript so the reply is in the log, and hands the reply over once; any other end hands over nothing.
func (s *fleetChat) endTurn(id string, how TurnEnd) {
	if how != TurnEndedNormally || id == "" {
		return
	}
	_, _ = s.refresh(true)
	e, ok := s.replyOf(id)
	if !ok {
		return
	}
	first := false
	s.turns(func(t *turnState) {
		for _, r := range t.reported {
			if r == id {
				return
			}
		}
		first = true
		t.reported = append(t.reported, id)
		if n := len(t.reported) - deliveredKeep; n > 0 {
			t.reported = t.reported[n:]
		}
		if err := s.saveTurnsLocked(t); err != nil {
			log.Printf("admiral: chat %s: could not save the report queue: %v", s.key, err)
		}
	})
	if first && s.OnReply != nil {
		s.OnReply(s.key, ReportReply{CardID: id, MessageID: e.ID, Text: e.Text})
	}
}

// ShipCardOf is the ship's display name and result of the ship card the log holds under id.
func (s *Service) ShipCardOf(fleet, id string) (name, result string, ok bool) {
	es, err := s.history().Recent(fleet, launchLineLookback)
	if err != nil {
		return "", "", false
	}
	for i := len(es) - 1; i >= 0; i-- {
		if es[i].ID != id || es[i].Kind != chatlog.KindShipCard {
			continue
		}
		var c struct{ Name, Ship, Result string }
		if json.Unmarshal(es[i].Card, &c) != nil {
			return "", "", false
		}
		return cmp.Or(c.Name, c.Ship), c.Result, true
	}
	return "", "", false
}
