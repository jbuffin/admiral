package chat

import (
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/jbuffin/admiral/internal/chatlog"
	"github.com/jbuffin/admiral/internal/inbox"
	"github.com/jbuffin/admiral/internal/needcard"
	"github.com/jbuffin/admiral/internal/sessionhost"
	"github.com/jbuffin/admiral/internal/ships"
)

// stopPass posts a card for each ship's stop that has none, then settles the open ones whose stop has ended.
func (s *Service) stopPass() {
	if s.Ships == nil {
		return
	}
	n := s.needs()
	cfg := s.Config()
	list := s.Ships()
	byName := make(map[string]ships.Ship, len(list))
	for _, sh := range list {
		byName[sh.Session] = sh
		kind, identity := needcard.StopKind(sh.Reason), needcard.StopIdentity(sh)
		if sh.Status != ships.StatusNeedsYou || kind == "" || identity == "" || sh.Fleet == "" || sessionhost.IsChatSession(sh.Session) || sh.Session == inbox.AdmiralSession {
			continue
		}
		if _, _, ok := cfg.ChatFleet(sh.Fleet); !ok {
			continue
		}
		id := needcard.StopCardID(sh.Session, identity)
		if _, ok := n.get(id); ok {
			continue
		}
		answerable := kind != needcard.StopNotStarted && needcard.StopAnswerable(sh)
		card := map[string]any{"ship": sh.Session, "item": identity, "item_kind": kind, "answerable": answerable, "state": NeedOpen}
		if kind == needcard.StopPermission && strings.HasPrefix(sh.Reason, "permission:") {
			card["tool"] = strings.TrimPrefix(sh.Reason, "permission:")
		}
		raw, err := json.Marshal(card)
		if err != nil {
			continue
		}
		at, err := time.Parse(time.RFC3339Nano, sh.StopSince)
		if err != nil {
			at = time.Now()
		}
		// As for an Inbox card: reserved before the append, saved after it.
		if !n.reserve(id, needRec{Fleet: sh.Fleet, Session: sh.Session, Item: identity, Created: needcard.Created(at), Kind: kind, Reason: sh.Reason, Answerable: answerable, State: NeedOpen}) {
			continue
		}
		if _, err := s.history().Append(sh.Fleet, chatlog.Entry{ID: id, Kind: chatlog.KindNeedCard, Text: stopTitle(sh, kind), At: at, Card: raw}); err != nil {
			log.Printf("admiral: stop card for %s: %v", sh.Session, err)
			n.release(id)
			continue
		}
		n.commit()
	}
	for _, id := range n.openStopIDs() {
		s.settleStop(id, byName)
	}
}

func stopTitle(sh ships.Ship, kind string) string {
	name := sh.Name
	if name == "" {
		name = sh.Session
	}
	switch kind {
	case needcard.StopPermission:
		return name + " asks to run a tool"
	case needcard.StopTurn:
		return name + " finished its turn"
	}
	return name + " has not started"
}

func (n *needStore) openStopIDs() []string {
	n.mu.Lock()
	defer n.mu.Unlock()
	var ids []string
	for id, r := range n.recs {
		if r.State == NeedOpen && needcard.StopKinds[r.Kind] {
			ids = append(ids, id)
		}
	}
	return ids
}

// settleStop ends an open stop card whose ship no longer waits on that stop: answered through a ships route, settled in the terminal, or moved on.
// A card the chat's own route is answering stays open: that route settles it.
func (s *Service) settleStop(id string, byName map[string]ships.Ship) {
	n := s.needs()
	r, ok := n.get(id)
	if !ok || r.State != NeedOpen || n.tapping(r.Session, r.Item) {
		return
	}
	sh, listed := byName[r.Session]
	switch {
	case listed && sh.Status == ships.StatusNeedsYou && needcard.StopIdentity(sh) == r.Item && sh.Reason == r.Reason:
		// The stop still stands: a mark with no end to the stop (an Escape at an idle prompt) answers nothing.
	case listed && sh.StopAnswered != nil && sh.StopAnswered.StopID == r.Item:
		if sh.StopAnswered.Via == "admiralbot" {
			n.settle(id, NeedAnswered, AnsweredAdmiralBot)
		} else {
			n.settle(id, NeedAnswered, AnsweredInbox)
		}
	case !listed || sh.Status != ships.StatusNeedsYou && sh.Status != ships.StatusUnderway:
		n.settle(id, NeedMovedOn, "")
	default:
		n.settle(id, NeedSettledTerminal, "")
	}
}

func (s *Service) serveStopCardAnswer(w http.ResponseWriter, r *http.Request, n *needStore, cardID string, rec needRec) {
	var req struct {
		Choice string `json:"choice"`
		Text   string `json:"text"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 256<<10)).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "the request is not a valid JSON object: "+err.Error())
		return
	}
	if rec.State != NeedOpen {
		writeError(w, http.StatusConflict, "item "+rec.Item+" is already answered")
		return
	}
	switch {
	case rec.Kind == needcard.StopNotStarted:
		writeError(w, http.StatusBadRequest, "that ship has not started: answer it in its Terminal tab")
		return
	case !rec.Answerable:
		writeError(w, http.StatusBadRequest, "that prompt is answered in the terminal")
		return
	case rec.Kind == needcard.StopPermission && (req.Text != "" || req.Choice != "allow" && req.Choice != "deny"):
		writeError(w, http.StatusBadRequest, "a permission card takes a choice of allow or deny")
		return
	case rec.Kind == needcard.StopTurn && (req.Choice != "" || strings.TrimSpace(req.Text) == ""):
		writeError(w, http.StatusBadRequest, "a finished turn takes the text of its next prompt")
		return
	case s.AnswerStop == nil:
		writeError(w, http.StatusInternalServerError, "ships cannot be answered here")
		return
	}
	n.setTapping(rec.Session, rec.Item, true)
	defer n.setTapping(rec.Session, rec.Item, false)
	err := s.AnswerStop(r.Context(), rec.Session, ships.StopAnswer{StopID: rec.Item, Reason: rec.Reason, Choice: req.Choice, Text: req.Text})
	var refused *ships.StopRefused
	switch {
	case err == nil:
		n.settle(cardID, NeedAnswered, AnsweredChat)
		writeJSON(w, http.StatusOK, map[string]string{"answered": rec.Item})
	case errors.Is(err, ships.ErrStopMoved), errors.Is(err, ships.ErrNotFound):
		writeError(w, http.StatusConflict, "item "+rec.Item+" is already answered")
	case errors.As(err, &refused):
		writeError(w, http.StatusBadRequest, refused.Msg)
	default:
		writeError(w, http.StatusInternalServerError, err.Error())
	}
}
