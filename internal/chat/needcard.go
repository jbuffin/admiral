package chat

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"sync"

	"github.com/jbuffin/admiral/internal/chatlog"
	"github.com/jbuffin/admiral/internal/inbox"
	"github.com/jbuffin/admiral/internal/needcard"
	"github.com/jbuffin/admiral/internal/outcomes"
)

// The states and places a need card reads.
const (
	NeedOpen     = needcard.StateOpen
	NeedAnswered = "answered"
	NeedRemoved  = "removed"
	// NeedSettledTerminal is a stop that ended with no Admiral route answering it; NeedMovedOn is a routine proposal that closed without the engineer, or a ship that stopped needing you by leaving: moored, ended, gone.
	NeedSettledTerminal = "settled_terminal"
	NeedMovedOn         = "moved_on"
	AnsweredChat        = "chat"
	AnsweredInbox       = "inbox"
	AnsweredAdmiralBot  = "admiralbot"
)

// needRec is what the chat keeps about a need card beside its log entry: which item it is for and, once it settles, how. The Inbox deletes a kept ship's files on answer, so the state cannot be read back from them.
type needRec struct {
	Fleet   string `json:"fleet"`
	Session string `json:"session"`
	Item    string `json:"item"`
	// Kind is the Inbox item's kind, empty on a card posted before other kinds had one (a question), or a stop card's kind (stopKinds).
	Kind    string `json:"kind,omitempty"`
	Created string `json:"created"`
	Sig     string `json:"sig,omitempty"`
	// Reason is the ship's reason a stop card was posted for.
	Reason string `json:"reason,omitempty"`
	// Answerable is false for a stop answered in the terminal.
	Answerable bool   `json:"answerable,omitempty"`
	State      string `json:"state"`
	In         string `json:"in,omitempty"`
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
		if b, err := os.ReadFile(n.path); err == nil && json.Unmarshal(b, &n.recs) != nil {
			// Kept aside so the next save cannot overwrite what the engineer may repair.
			n.recs = map[string]*needRec{}
			if err := os.Rename(n.path, n.path+".bad"); err != nil {
				log.Printf("admiral: need cards: %v", err)
			}
			log.Printf("admiral: need cards: %s did not parse; moved to %s.bad", n.path, n.path)
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

// reserve holds the record in memory only and says whether the card was new.
func (n *needStore) reserve(id string, r needRec) bool {
	n.mu.Lock()
	defer n.mu.Unlock()
	if _, ok := n.recs[id]; ok {
		return false
	}
	n.recs[id] = &r
	return true
}

func (n *needStore) release(id string) {
	n.mu.Lock()
	defer n.mu.Unlock()
	delete(n.recs, id)
}

func (n *needStore) commit() {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.save()
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

func (n *needStore) tapping(session, item string) bool {
	n.mu.Lock()
	defer n.mu.Unlock()
	return n.pending[session+"\x00"+item]
}

func (n *needStore) setTapping(session, item string, on bool) {
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
		if r.State == NeedOpen && (session == "" || !needcard.StopKinds[r.Kind] && r.Session == session && r.Item == item) {
			ids = append(ids, id)
		}
	}
	return ids
}

// needSig is what the engineer sees on the card; the route refuses an item whose questions, review or findings changed since.
func needSig(field json.RawMessage) string {
	h := sha256.Sum256(field)
	return hex.EncodeToString(h[:12])
}

// InboxChanged tells the need card poster the Inbox's list changed; it never blocks.
func (s *Service) InboxChanged() {
	select {
	case s.needs().poke <- struct{}{}:
	default:
	}
}

// ShipsChanged tells the need card poster the ships' list changed; it never blocks.
func (s *Service) ShipsChanged() { s.InboxChanged() }

// InboxSettled records, before the Inbox releases a kept ship's files, that an item was answered or removed through the Inbox.
func (s *Service) InboxSettled(session, id string, removed bool, via string) {
	n := s.needs()
	for _, cid := range n.openIDs(session, id) {
		switch {
		case removed:
			n.settle(cid, NeedRemoved, "")
		case via == AnsweredChat:
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
	s.stopPass()
	if !s.Inbox.Scanned() { // before the first scan the list is empty, not settled
		return
	}
	n := s.needs()
	items := s.Inbox.List()
	res := s.cardResolver()
	for _, it := range items {
		id, _ := res.ForItem(it)
		if id == "" {
			continue
		}
		fleet := needcard.Fleet(it)
		created := needcard.Created(it.Created)
		body, ok := needcard.Body(it)
		if !ok {
			continue
		}
		card, err := json.Marshal(body)
		if err != nil {
			continue
		}
		sig := needSig(it.Field(it.Kind))
		if it.Kind == inbox.KindAllowRules {
			sig = "" // the item grows in place under its id and created; its card follows
		}
		// Known to the route before it is in the log, so a client that sees the card and taps at once finds its record; saved only once the entry is in, so a crash between posts it again.
		if !n.reserve(id, needRec{Fleet: fleet, Session: it.Session, Item: it.ID, Kind: it.Kind, Created: created, Sig: sig, State: NeedOpen}) {
			continue
		}
		if _, err := s.history().Append(fleet, chatlog.Entry{ID: id, Kind: chatlog.KindNeedCard, Text: title(it), At: it.Created, Card: card}); err != nil {
			log.Printf("admiral: need card for %s/%s: %v", it.Session, it.ID, err)
			n.release(id)
			continue
		}
		n.commit()
	}
	open := s.openItems(items)
	for _, id := range n.openIDs("", "") {
		s.needState(id, open)
	}
}

// openItems is the items the Inbox lists that post a card, keyed by session, item and created stamp.
func (s *Service) openItems(items []inbox.Item) map[string]bool {
	open := map[string]bool{}
	for _, it := range items {
		if needcard.Kinds[it.Kind] {
			open[it.Session+"\x00"+it.ID+"\x00"+needcard.Created(it.Created)] = true
		}
	}
	return open
}

// needState is the card's record, settled first when its item has left the Inbox's list: moved on when a routine proposal closed without the engineer, answered in the Inbox when an answer file stands, removed when none does.
func (s *Service) needState(id string, open map[string]bool) (needRec, bool) {
	n := s.needs()
	r, ok := n.get(id)
	if !ok || needcard.StopKinds[r.Kind] || r.State != NeedOpen || s.Inbox == nil || !s.Inbox.Scanned() || open[r.Session+"\x00"+r.Item+"\x00"+r.Created] {
		return r, ok
	}
	if r.Kind == inbox.KindRoutineProposal {
		// An engineer's launch wins over expiry: a launch in flight, from the chat or the Inbox, leaves the card open until its answer file is written.
		if n.tapping(r.Session, r.Item) {
			return r, ok
		}
		moved, launching := s.Inbox.MovedOn(r.Session, r.Item)
		if launching {
			return r, ok
		}
		if moved {
			n.settle(id, NeedMovedOn, "")
			return n.get(id)
		}
	}
	present, mark := s.Inbox.AnswerFile(r.Session, r.Item)
	switch {
	case !present || mark:
		n.settle(id, NeedRemoved, "")
	case n.tapping(r.Session, r.Item):
		// A tap in flight settles the card itself through OnSettled; guessing here could read an answer from the Inbox as the chat's.
		return r, ok
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
	if r.State == NeedOpen && r.Kind == inbox.KindAllowRules {
		s.refreshRules(r, c)
	}
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

// refreshRules sets an open allow rule card's rules to the item's now: the item grows in place while rounds add rules, and its one card follows.
func (s *Service) refreshRules(r needRec, c map[string]json.RawMessage) {
	for _, it := range s.Inbox.List() {
		if it.Session == r.Session && it.ID == r.Item && needcard.Created(it.Created) == r.Created {
			if o, ok := it.AllowRulesObject(); ok {
				c["cards"], _ = json.Marshal(o.Cards)
			}
			return
		}
	}
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
	if needcard.StopKinds[rec.Kind] {
		s.serveStopCardAnswer(w, r, n, cardID, rec)
		return
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 1<<20))
	if err != nil {
		writeError(w, http.StatusBadRequest, "the request is not a valid JSON object: "+err.Error())
		return
	}
	if rec.State != NeedOpen {
		if !json.Valid(body) {
			writeError(w, http.StatusBadRequest, "the request is not a valid JSON object")
			return
		}
		writeError(w, http.StatusConflict, "item "+rec.Item+" is already answered")
		return
	}
	var top map[string]json.RawMessage
	if json.Unmarshal(body, &top) == nil && string(top["choice"]) != "null" && top["choice"] != nil {
		if body = s.choiceBody(w, rec, top["choice"]); body == nil {
			return
		}
	}
	n.setTapping(rec.Session, rec.Item, true)
	defer n.setTapping(rec.Session, rec.Item, false)
	// The item must still be the one the card showed; via marks the answer as given in the chat once it is written.
	g := &inbox.Guard{Via: AnsweredChat, Check: func(it inbox.Item) error {
		if !rec.sameItem(it) {
			return inbox.ErrAnswered
		}
		return nil
	}}
	out := map[string]any{"answered": rec.Item}
	switch rec.Kind {
	case inbox.KindReview:
		var req struct {
			inbox.SubmitRequest
			Discard bool `json:"discard"`
		}
		if err = json.Unmarshal(body, &req); err == nil {
			var res inbox.ReviewResult
			if req.Discard {
				res, err = s.Inbox.DiscardReviewGuarded(rec.Session, rec.Item, g)
			} else {
				res, err = s.Inbox.SubmitReviewGuarded(rec.Session, rec.Item, req.SubmitRequest, g)
			}
			out["action"] = res.Action
			if res.InvalidTarget {
				out["invalid_target"] = true
			}
		}
	case inbox.KindFindings:
		var req inbox.FindingsRequest
		if err = json.Unmarshal(body, &req); err == nil {
			err = s.Inbox.AnswerFindingsGuarded(rec.Session, rec.Item, req, g)
		}
	case inbox.KindRoutineProposal:
		var req struct {
			Launch  bool `json:"launch"`
			Dismiss bool `json:"dismiss"`
		}
		if err = json.Unmarshal(body, &req); err == nil {
			switch {
			case req.Launch == req.Dismiss:
				err = &inbox.InvalidError{Msg: "say launch or dismiss, one of them"}
			case req.Launch:
				var ship any
				if ship, err = s.Inbox.LaunchRoutineItemGuarded(r.Context(), rec.Item, g); err == nil {
					out = map[string]any{"ship": ship}
				}
			default:
				err = s.Inbox.DismissRoutineProposalGuarded(rec.Item, g)
			}
		}
	case inbox.KindAllowRules:
		var req inbox.AllowRulesRequest
		if err = json.Unmarshal(body, &req); err == nil {
			err = s.Inbox.AnswerAllowRulesGuarded(rec.Item, req, g)
		}
	case inbox.KindCampaignDraft:
		var req struct {
			inbox.CampaignDraftRequest
			Dismiss bool `json:"dismiss"`
		}
		if err = json.Unmarshal(body, &req); err == nil {
			if req.Dismiss {
				err = s.Inbox.DismissCampaignDraftGuarded(rec.Item, g)
			} else {
				var created inbox.CreatedCampaign
				if created, err = s.Inbox.CreateCampaignItemGuarded(r.Context(), rec.Item, req.CampaignDraftRequest, g); err == nil {
					out = map[string]any{"campaign": map[string]string{"id": created.ID, "url": created.URL}}
				}
			}
		}
	case inbox.KindQuestion, "":
		var req inbox.QuestionRequest
		if err = json.Unmarshal(body, &req); err == nil {
			err = s.Inbox.AnswerGuarded(rec.Session, rec.Item, inbox.KindQuestion, g, func(it inbox.Item) (any, []outcomes.Record, error) {
				file, err := inbox.BuildQuestionAnswer(it, req)
				if err != nil {
					return nil, nil, err
				}
				return file, []outcomes.Record{{Event: "answered", Fields: map[string]any{"item": rec.Item, "kind": inbox.KindQuestion}}}, nil
			})
		}
	default:
		err = fmt.Errorf("need card %s is for a %s item, which has no answer path", cardID, rec.Kind)
	}
	if status, refusal, ok := inbox.Refusal(rec.Item, err); ok {
		writeJSON(w, status, refusal)
		return
	}
	var bad *inbox.InvalidError
	var gh *inbox.GhError
	var syn *json.SyntaxError
	var typ *json.UnmarshalTypeError
	switch {
	case err == nil:
		writeJSON(w, http.StatusOK, out)
	case errors.As(err, &syn), errors.As(err, &typ):
		writeError(w, http.StatusBadRequest, "the request is not a valid JSON object: "+err.Error())
	case errors.Is(err, inbox.ErrAnswered), errors.Is(err, inbox.ErrNotFound):
		writeError(w, http.StatusConflict, "item "+rec.Item+" is already answered")
	case errors.Is(err, inbox.ErrBusy):
		writeError(w, http.StatusConflict, err.Error())
	case errors.As(err, &bad):
		writeError(w, http.StatusBadRequest, bad.Msg)
	case errors.As(err, &gh):
		status := http.StatusBadGateway
		if gh.Missing {
			status = http.StatusServiceUnavailable
		}
		writeError(w, status, gh.Msg)
	default:
		writeError(w, http.StatusInternalServerError, err.Error())
	}
}

// choiceBody is the body of the kind's own path that a generic {"choice": id} stands for, or nil once it has written the refusal: 409 for an item that is no longer the one the card showed, before the id is judged, then 400.
func (s *Service) choiceBody(w http.ResponseWriter, rec needRec, raw json.RawMessage) []byte {
	var cur *inbox.Item
	for _, it := range s.Inbox.List() {
		if it.Session == rec.Session && it.ID == rec.Item {
			cur = &it
			break
		}
	}
	if cur == nil || !rec.sameItem(*cur) {
		writeError(w, http.StatusConflict, "item "+rec.Item+" is already answered")
		return nil
	}
	var id string
	if json.Unmarshal(raw, &id) == nil {
		if b, ok := needcard.AnswerBody(*cur, id); ok {
			return b
		}
	}
	writeError(w, http.StatusBadRequest, "that card takes no such choice")
	return nil
}

// sameItem is whether the item is the one the card was posted for.
func (r needRec) sameItem(it inbox.Item) bool {
	return needcard.Created(it.Created) == r.Created && (r.Sig == "" || r.Sig == needSig(it.Field(it.Kind)))
}

// cardResolver reads cards by the same rule that posts them.
func (s *Service) cardResolver() needcard.Resolver {
	cfg := s.Config()
	return needcard.Resolver{Configured: func(fleet string) bool { _, _, ok := cfg.ChatFleet(fleet); return ok }}
}
