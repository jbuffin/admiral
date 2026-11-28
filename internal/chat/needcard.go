package chat

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/jbuffin/admiral/internal/chatlog"
	"github.com/jbuffin/admiral/internal/inbox"
	"github.com/jbuffin/admiral/internal/outcomes"
	"github.com/jbuffin/admiral/internal/sessionhost"
)

// The states and places a need card reads.
const (
	NeedOpen      = "open"
	NeedAnswered  = "answered"
	NeedRemoved   = "removed"
	AnsweredChat  = "chat"
	AnsweredInbox = "inbox"
)

// needRec is what the chat keeps about a need card beside its log entry: which item it is for and, once it settles, how. The Inbox deletes a kept ship's files on answer, so the state cannot be read back from them.
type needRec struct {
	Fleet   string `json:"fleet"`
	Session string `json:"session"`
	Item    string `json:"item"`
	Created string `json:"created"`
	State   string `json:"state"`
	In      string `json:"in,omitempty"`
}

type needStore struct {
	mu      sync.Mutex
	path    string
	recs    map[string]*needRec
	pending map[string]bool // items the card route is answering, by session and item
	poke    chan struct{}
}

func (s *Service) needs() *needStore {
	s.needOnce.Do(func() {
		n := &needStore{path: filepath.Join(s.Home, "need-cards.json"), recs: map[string]*needRec{}, pending: map[string]bool{}, poke: make(chan struct{}, 1)}
		if b, err := os.ReadFile(n.path); err == nil {
			_ = json.Unmarshal(b, &n.recs)
		}
		s.need = n
	})
	return s.need
}

func (n *needStore) save() {
	b, err := json.Marshal(n.recs)
	if err == nil {
		tmp := n.path + ".tmp"
		if err = os.WriteFile(tmp, b, 0o600); err == nil {
			err = os.Rename(tmp, n.path)
		}
	}
	if err != nil {
		log.Printf("admiral: need cards: %v", err)
	}
}

func (n *needStore) get(id string) (needRec, bool) {
	n.mu.Lock()
	defer n.mu.Unlock()
	r, ok := n.recs[id]
	if !ok {
		return needRec{}, false
	}
	return *r, true
}

func (n *needStore) add(id string, r needRec) {
	n.mu.Lock()
	defer n.mu.Unlock()
	if _, ok := n.recs[id]; !ok {
		n.recs[id] = &r
		n.save()
	}
}

// settle records how an open card ended; a settled card never changes.
func (n *needStore) settle(id, state, in string) {
	n.mu.Lock()
	defer n.mu.Unlock()
	if r := n.recs[id]; r != nil && r.State == NeedOpen {
		r.State, r.In = state, in
		n.save()
	}
}

func (n *needStore) answeringItem(session, item string) bool {
	n.mu.Lock()
	defer n.mu.Unlock()
	return n.pending[session+"\x00"+item]
}

func (n *needStore) setAnswering(session, item string, on bool) {
	n.mu.Lock()
	defer n.mu.Unlock()
	if on {
		n.pending[session+"\x00"+item] = true
	} else {
		delete(n.pending, session+"\x00"+item)
	}
}

func (n *needStore) openIDs(session, item string) []string {
	n.mu.Lock()
	defer n.mu.Unlock()
	var ids []string
	for id, r := range n.recs {
		if r.State == NeedOpen && (session == "" || r.Session == session && r.Item == item) {
			ids = append(ids, id)
		}
	}
	return ids
}

func needCreated(t time.Time) string { return t.UTC().Format(time.RFC3339Nano) }

// needCardID folds the item's identity in, so a reused session name or item id gets its own card; hashed, since a session name can hold any character.
func needCardID(session, item, created string) string {
	h := sha256.Sum256([]byte(session + "\x00" + item + "\x00" + created))
	return "need-" + hex.EncodeToString(h[:12])
}

// InboxChanged tells the need card poster the Inbox's list changed; it never blocks.
func (s *Service) InboxChanged() {
	select {
	case s.needs().poke <- struct{}{}:
	default:
	}
}

// InboxSettled records, before the Inbox releases a kept ship's files, that an item was answered or removed through the Inbox.
func (s *Service) InboxSettled(session, id string, removed bool) {
	n := s.needs()
	for _, cid := range n.openIDs(session, id) {
		switch {
		case removed:
			n.settle(cid, NeedRemoved, "")
		case n.answeringItem(session, id):
			n.settle(cid, NeedAnswered, AnsweredChat)
		default:
			n.settle(cid, NeedAnswered, AnsweredInbox)
		}
	}
}

// RunNeedCards posts a need card for every open question of a ship that has a fleet, until ctx ends.
func (s *Service) RunNeedCards(ctx context.Context) {
	if s.Inbox == nil {
		<-ctx.Done()
		return
	}
	poke := s.needs().poke
	for {
		s.needPass()
		select {
		case <-poke:
		case <-ctx.Done():
			return
		}
	}
}

func (s *Service) needPass() {
	n := s.needs()
	items := s.Inbox.List()
	cfg := s.Config()
	for _, it := range items {
		if it.Kind != inbox.KindQuestion || it.Fleet == "" || sessionhost.IsChatSession(it.Session) || it.Session == inbox.AdmiralSession {
			continue
		}
		if _, _, ok := cfg.ChatFleet(it.Fleet); !ok {
			continue
		}
		created := needCreated(it.Created)
		id := needCardID(it.Session, it.ID, created)
		if _, ok := n.get(id); ok {
			continue
		}
		var q struct {
			Questions json.RawMessage `json:"questions"`
		}
		if json.Unmarshal(it.Field(inbox.KindQuestion), &q) != nil || len(q.Questions) == 0 {
			continue
		}
		card, err := json.Marshal(map[string]any{"ship": it.Session, "item": it.ID, "item_kind": it.Kind, "questions": q.Questions, "state": NeedOpen})
		if err != nil {
			continue
		}
		if _, err := s.history().Append(it.Fleet, chatlog.Entry{ID: id, Kind: chatlog.KindNeedCard, Text: title(it), At: it.Created, Card: card}); err != nil {
			log.Printf("admiral: need card for %s/%s: %v", it.Session, it.ID, err)
			continue
		}
		n.add(id, needRec{Fleet: it.Fleet, Session: it.Session, Item: it.ID, Created: created, State: NeedOpen})
	}
	open := s.openItems(items)
	for _, id := range n.openIDs("", "") {
		s.needState(id, open)
	}
}

// openItems is the questions the Inbox lists, keyed by session, item and created stamp.
func (s *Service) openItems(items []inbox.Item) map[string]bool {
	open := map[string]bool{}
	for _, it := range items {
		if it.Kind == inbox.KindQuestion {
			open[it.Session+"\x00"+it.ID+"\x00"+needCreated(it.Created)] = true
		}
	}
	return open
}

// needState is the card's record, settled first when its item has left the Inbox's list: answered in the Inbox when an answer file stands, removed when none does.
func (s *Service) needState(id string, open map[string]bool) (needRec, bool) {
	n := s.needs()
	r, ok := n.get(id)
	if !ok || r.State != NeedOpen || s.Inbox == nil || open[r.Session+"\x00"+r.Item+"\x00"+r.Created] {
		return r, ok
	}
	present, mark := s.Inbox.AnswerFile(r.Session, r.Item)
	switch {
	case !present || mark:
		n.settle(id, NeedRemoved, "")
	case n.answeringItem(r.Session, r.Item):
		n.settle(id, NeedAnswered, AnsweredChat)
	default:
		n.settle(id, NeedAnswered, AnsweredInbox)
	}
	return n.get(id)
}

// withNeedState serves a need card with its state as it is now; the log keeps the card as posted.
func (s *Service) withNeedState(id string, card json.RawMessage, open func() map[string]bool) json.RawMessage {
	r, ok := s.needState(id, open())
	if !ok {
		return card
	}
	var c map[string]json.RawMessage
	if json.Unmarshal(card, &c) != nil {
		return card
	}
	c["state"], _ = json.Marshal(r.State)
	delete(c, "answered_in")
	if r.In != "" {
		c["answered_in"], _ = json.Marshal(r.In)
	}
	out, err := json.Marshal(c)
	if err != nil {
		return card
	}
	return out
}

func (s *Service) serveNeedCardAnswer(w http.ResponseWriter, r *http.Request) {
	fleet, cardID := r.PathValue("fleet"), r.PathValue("card")
	if _, _, ok := s.Config().ChatFleet(fleet); !ok {
		writeError(w, http.StatusNotFound, "no fleet "+fleet)
		return
	}
	n := s.needs()
	rec, ok := n.get(cardID)
	if e, found, err := s.history().Get(fleet, cardID); !ok || rec.Fleet != fleet || err != nil || !found || e.Kind != chatlog.KindNeedCard || s.Inbox == nil {
		writeError(w, http.StatusNotFound, "no need card "+cardID+" in the "+fleet+" chat")
		return
	}
	var req inbox.QuestionRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 256<<10)).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "the request is not a valid JSON object: "+err.Error())
		return
	}
	if rec.State != NeedOpen {
		writeError(w, http.StatusConflict, "item "+rec.Item+" is already answered")
		return
	}
	defer n.setAnswering(rec.Session, rec.Item, false)
	err := s.Inbox.Answer(rec.Session, rec.Item, inbox.KindQuestion, func(it inbox.Item) (any, []outcomes.Record, error) {
		if needCreated(it.Created) != rec.Created {
			return nil, nil, inbox.ErrAnswered
		}
		file, err := inbox.BuildQuestionAnswer(it, req)
		if err != nil {
			return nil, nil, err
		}
		n.setAnswering(rec.Session, rec.Item, true)
		return file, []outcomes.Record{{Event: "answered", Fields: map[string]any{"item": rec.Item, "kind": inbox.KindQuestion}}}, nil
	})
	var bad *inbox.InvalidError
	switch {
	case err == nil:
		writeJSON(w, http.StatusOK, map[string]string{"answered": rec.Item})
	case errors.Is(err, inbox.ErrAnswered):
		writeError(w, http.StatusConflict, "item "+rec.Item+" is already answered")
	case errors.Is(err, inbox.ErrNotFound):
		writeError(w, http.StatusNotFound, "no item "+rec.Item+" in the inbox of "+rec.Session)
	case errors.As(err, &bad):
		writeError(w, http.StatusBadRequest, bad.Msg)
	default:
		writeError(w, http.StatusInternalServerError, err.Error())
	}
}
