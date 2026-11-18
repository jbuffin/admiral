package chat

import (
	"cmp"
	"encoding/json"
	"log"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/jbuffin/admiral/internal/chatlog"
)

// threadSkew is how far a transcript's timestamp for the pasted line may precede the moment Admiral pasted it.
const threadSkew = 2 * time.Second

// reportCardRe is the card line a report turn carries, which names the ship card the turn is about.
var reportCardRe = regexp.MustCompile(`(?m)^card: (\S+)[ \t\r]*$`)

// reportCardOf is the ship card id a pasted report turn names, "" when the text is not one.
func reportCardOf(text string) string {
	if !strings.HasPrefix(strings.TrimSpace(text), "[Report: ") {
		return ""
	}
	if m := reportCardRe.FindStringSubmatch(text); m != nil {
		return m[1]
	}
	return ""
}

// threadState decides which log entries belong to the report turn Admiral last typed. It is memory only: a restart
// mid-turn leaves the rest of that turn's entries unmarked, and the marks already in the log stay.
type threadState struct {
	mu    sync.Mutex
	id    string    // the ship card whose report turn this is; "" when none is open
	since time.Time // when the turn was pasted
	seen  bool      // the turn's own user line has been logged
	ended bool      // the turn has ended: late replies are threaded until the next user line of any kind
}

func (t *threadState) open(id string) { t.id, t.since, t.seen, t.ended = id, time.Now(), false, false }

// after is whether a transcript timestamp is not before the paste; one that can't be read counts as after.
func (t *threadState) after(at string) bool {
	when, err := time.Parse(time.RFC3339Nano, at)
	return err != nil || !when.Before(t.since.Add(-threadSkew))
}

// end is called when the turn ends; the next user line closes the thread.
func (t *threadState) end(id string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.id == id {
		t.ended = true
	}
}

// classify returns the thread a not yet logged message carries, and the change it makes to the state once it is logged.
// A user line is the pasted turn when it names the open turn's card after the paste (or a delivered turn the log has no
// line for, which a restart left behind). Any other user line closes the thread once the turn has ended; before that
// the engineer's does, and a notice does not.
func (t *threadState) classify(m Message, restored func(card string) bool) (string, func()) {
	noop := func() {}
	if m.Kind != chatlog.KindMessage {
		return "", noop
	}
	switch m.Role {
	case "assistant":
		if t.id != "" && t.seen {
			return t.id, noop
		}
	case "user":
		closeIt := func() { t.id = "" }
		card := ""
		if !m.notice {
			card = reportCardOf(m.Text)
		}
		switch {
		case card != "" && card == t.id && !t.seen && t.after(m.At):
			return card, func() { t.seen = true }
		case card != "" && card != t.id && restored(card):
			return card, func() { t.id, t.seen, t.ended = card, true, true }
		case t.id == "":
		case t.ended:
			return "", closeIt
		case m.notice || !t.seen:
		default:
			return "", closeIt
		}
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
	if id == "" {
		return
	}
	// Closed before the read: the notice that started the next turn may already be in the transcript.
	s.th.end(id)
	if how != TurnEndedNormally {
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

// restoredCheck returns restored: whether a pasted turn naming card was delivered, yet the log holds no line for it,
// as a restart between the paste and the record leaves it. The log is read once, and only if a delivered card is asked about.
func (s *fleetChat) restoredCheck() func(card string) bool {
	var threaded map[string]bool
	return func(card string) bool {
		delivered := false
		s.turns(func(t *turnState) {
			for _, d := range t.delivered {
				delivered = delivered || d == card
			}
		})
		if !delivered {
			return false
		}
		if threaded == nil {
			threaded = map[string]bool{}
			es, err := s.history().Recent(s.key, HistoryWindow)
			s.noteLogErr(err)
			for _, e := range es {
				if e.Role == "user" && e.Thread != "" {
					threaded[e.Thread] = true
				}
			}
		}
		return !threaded[card]
	}
}
