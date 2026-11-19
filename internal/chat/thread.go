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

// digestTurnRe is the turn id line a fleet digest turn carries. Its fact lines all start with "- ", so one can't forge it.
var digestTurnRe = regexp.MustCompile(`(?m)^turn: (digest:\S+)[ \t\r]*$`)

// digestTurnPrefix starts the id of every fleet digest turn; a ship card's id never does.
const digestTurnPrefix = "digest:"

// DigestTurnID is the id of the digest turn a routine's run queues.
func DigestTurnID(routine, run string) string { return digestTurnPrefix + routine + ":" + run }

// DigestTurnHeader is the first two lines of a digest turn: the marker and the turn id line turnIDOf matches.
func DigestTurnHeader(name, turn string) string {
	return "[Digest: " + name + "]\nturn: " + turn + "\n"
}

// turnIDOf is the turn id a pasted report turn names (its ship card) or a pasted digest turn carries, "" when the text is neither.
func turnIDOf(text string) string {
	re := reportCardRe
	switch {
	case strings.HasPrefix(strings.TrimSpace(text), "[Report: "):
	case strings.HasPrefix(strings.TrimSpace(text), "[Digest: "):
		re = digestTurnRe
	default:
		return ""
	}
	if m := re.FindStringSubmatch(text); m != nil {
		return m[1]
	}
	return ""
}

// threadState decides which log entries belong to the report or digest turn Admiral last typed. It is memory only: a restart
// mid-turn leaves the rest of that turn's entries unmarked, and the marks already in the log stay.
type threadState struct {
	mu    sync.Mutex
	turn  string    // the id of the open report or digest turn; "" when none is open
	since time.Time // when the turn was pasted
	seen  bool      // the turn's own user line has been logged
	ended bool      // the turn has ended: late replies are threaded until the next user line of any kind
}

func (t *threadState) open(id string) {
	t.turn, t.since, t.seen, t.ended = id, time.Now(), false, false
}

// after is whether a transcript timestamp is not before the paste; one that can't be read counts as after.
func (t *threadState) after(at string) bool {
	when, err := time.Parse(time.RFC3339Nano, at)
	return err != nil || !when.Before(t.since.Add(-threadSkew))
}

// end is called when the turn ends; the next user line closes the thread.
func (t *threadState) end(id string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.turn == id {
		t.ended = true
	}
}

// classify returns the thread a not yet logged message carries, and the change it makes to the state once it is logged.
// A user line is the pasted turn when it names the open turn's id after the paste (or a delivered turn the log has no
// line for, which a restart left behind). Any other user line closes the thread once the turn has ended; before that
// the engineer's does, and a notice does not.
func (t *threadState) classify(m Message, restored func(turn string) bool) (string, func()) {
	noop := func() {}
	if m.Kind != chatlog.KindMessage {
		return "", noop
	}
	switch m.Role {
	case "assistant":
		if t.turn != "" && t.seen {
			return t.turn, noop
		}
	case "user":
		closeIt := func() { t.turn = "" }
		turn := ""
		if !m.notice {
			turn = turnIDOf(m.Text)
		}
		switch {
		case turn != "" && turn == t.turn && !t.seen && t.after(m.At):
			return turn, func() { t.seen = true }
		case turn != "" && turn != t.turn && restored(turn):
			return turn, func() { t.turn, t.seen, t.ended = turn, true, true }
		case t.turn == "":
		case t.ended:
			return "", closeIt
		case m.notice || !t.seen:
		default:
			return "", closeIt
		}
	}
	return "", noop
}

// replyOf is the first assistant entry the log marks for the turn id.
func (s *fleetChat) replyOf(turn string) (chatlog.Entry, bool) {
	es, err := s.history().Recent(s.key, HistoryWindow)
	s.noteLogErr(err)
	for _, e := range es {
		if e.Thread == turn && e.Kind == chatlog.KindMessage && e.Role == "assistant" {
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
		for _, r := range t.replied {
			if r == id {
				return
			}
		}
		first = true
		t.replied = append(t.replied, id)
		if n := len(t.replied) - deliveredKeep; n > 0 {
			t.replied = t.replied[n:]
		}
		if err := s.saveTurnsLocked(t); err != nil {
			log.Printf("admiral: chat %s: could not save the report queue: %v", s.key, err)
		}
	})
	if first && strings.HasPrefix(id, digestTurnPrefix) {
		if s.OnDigestReply != nil {
			s.OnDigestReply(s.key, DigestReply{TurnID: id, MessageID: e.ID, Text: e.Text})
		}
		return
	}
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

// restoredCheck returns restored: whether a pasted turn naming a turn id was delivered, yet the log holds no line for it,
// as a restart between the paste and the record leaves it. The log is read once, and only if a delivered turn is asked about.
func (s *fleetChat) restoredCheck() func(turn string) bool {
	var threaded map[string]bool
	return func(turn string) bool {
		delivered := false
		s.turns(func(t *turnState) {
			for _, d := range t.delivered {
				delivered = delivered || d == turn
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
		return !threaded[turn]
	}
}
