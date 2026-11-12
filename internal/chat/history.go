package chat

import (
	"encoding/json"
	"fmt"
	"log"
	"time"

	"github.com/jbuffin/admiral/internal/chatlog"
	"github.com/jbuffin/admiral/internal/inbox"
)

// record appends what the chat just showed: its transcript's messages, then its questions as
// cards. The log skips what it holds, so a transcript read again adds nothing.
func (s *fleetChat) record(snap Snapshot) {
	l := s.history()
	var err error
	for _, m := range snap.Messages {
		at, _ := time.Parse(time.RFC3339Nano, m.At)
		if _, e := l.Append(s.key, chatlog.Entry{ID: m.ID, Kind: chatlog.KindMessage, Role: m.Role, Text: m.Text, At: at}); e != nil {
			err = e
		}
	}
	for _, q := range snap.Questions {
		card, e := json.Marshal(q.Item)
		if e != nil {
			continue
		}
		if _, e := l.Append(s.key, chatlog.Entry{ID: "q-" + q.Item.ID, Kind: chatlog.KindQuestion, Text: title(q.Item), At: q.Item.Created, Card: card}); e != nil {
			err = e
		}
	}
	s.noteLogErr(err)
}

// markReset appends the New chat marker. Everything the chat said before it is logged first,
// since New chat reads the chat before it acts.
func (s *fleetChat) markReset() {
	_, err := s.history().Append(s.key, chatlog.Entry{ID: fmt.Sprintf("reset-%d", time.Now().UnixNano()), Kind: chatlog.KindReset, Text: "New chat"})
	s.noteLogErr(err)
}

// recent is the log's newest window as the session serves it.
func (s *fleetChat) recent() []Message {
	es, err := s.history().Recent(s.key, HistoryWindow)
	s.noteLogErr(err)
	out := make([]Message, 0, len(es))
	for _, e := range es {
		at := e.At
		if at.IsZero() {
			at = e.Logged
		}
		out = append(out, Message{ID: e.ID, Kind: e.Kind, Role: e.Role, Text: e.Text, At: at.UTC().Format(time.RFC3339Nano), Card: e.Card})
	}
	return out
}

// noteLogErr reports a failing log once until it works again.
func (s *fleetChat) noteLogErr(err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	switch {
	case err == nil:
		s.logErr = ""
	case s.logErr != err.Error():
		s.logErr = err.Error()
		log.Printf("admiral: chat %s: history log: %v", s.key, err)
	}
}

func title(it inbox.Item) string {
	var t string
	_ = json.Unmarshal(it.Field("title"), &t)
	return t
}
