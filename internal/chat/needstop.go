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
	"github.com/jbuffin/admiral/internal/keymap"
	"github.com/jbuffin/admiral/internal/sessionhost"
	"github.com/jbuffin/admiral/internal/ships"
)

// The kinds of a stop card.
const (
	StopPermission = "permission"
	StopTurn       = "stop"
	StopNotStarted = "not_started"
)

// stopKind is the card kind a needs-you reason posts, or "" for a reason that posts none.
func stopKind(reason string) string {
	switch {
	case strings.HasPrefix(reason, "permission:"), reason == "permission", reason == "notification:permission_prompt":
		return StopPermission
	case reason == "stop":
		return StopTurn
	case reason == "not_started":
		return StopNotStarted
	}
	return ""
}

// stopAnswerable is false for a permission prompt the answer route cannot key, which is answered in the terminal.
func stopAnswerable(sh ships.Ship) bool {
	switch sh.Reason {
	case "stop":
		return true
	case "permission:AskUserQuestion", "permission:ExitPlanMode":
		return false
	}
	if !strings.HasPrefix(sh.Reason, "permission:") && !(sh.Harness == "copilot" && sh.Reason == "notification:permission_prompt") {
		return false
	}
	m, ok := keymap.For(sh.Harness)
	return ok && m.CardTool(strings.TrimPrefix(sh.Reason, "permission:"))
}

// stopIdentity is what binds a card to one stop. A ship that was never started has had no hook write, so its launch stands for it.
func stopIdentity(sh ships.Ship) string {
	if sh.StopID != "" {
		return sh.StopID
	}
	if sh.Reason == "not_started" {
		return sh.Launched
	}
	return ""
}

func stopCardID(session, identity string) string {
	return needCardID(session, "stop:"+identity, "")
}

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
		kind, identity := stopKind(sh.Reason), stopIdentity(sh)
		if sh.Status != ships.StatusNeedsYou || kind == "" || identity == "" || sh.Fleet == "" || sessionhost.IsChatSession(sh.Session) || sh.Session == inbox.AdmiralSession {
			continue
		}
		if _, _, ok := cfg.ChatFleet(sh.Fleet); !ok {
			continue
		}
		id := stopCardID(sh.Session, identity)
		if _, ok := n.get(id); ok {
			continue
		}
		answerable := kind != StopNotStarted && stopAnswerable(sh)
		card := map[string]any{"ship": sh.Session, "item": identity, "item_kind": kind, "answerable": answerable, "state": NeedOpen}
		if kind == StopPermission && strings.HasPrefix(sh.Reason, "permission:") {
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
		// The record first: a reader that sees the entry must find its card.
		n.add(id, needRec{Fleet: sh.Fleet, Session: sh.Session, Item: identity, Created: needCreated(at), Kind: kind, Reason: sh.Reason, Answerable: answerable, State: NeedOpen})
		if _, err := s.history().Append(sh.Fleet, chatlog.Entry{ID: id, Kind: chatlog.KindNeedCard, Text: stopTitle(sh, kind), At: at, Card: raw}); err != nil {
			log.Printf("admiral: stop card for %s: %v", sh.Session, err)
			n.forget(id)
		}
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
	case StopPermission:
		return name + " asks to run a tool"
	case StopTurn:
		return name + " finished its turn"
	}
	return name + " has not started"
}

func (n *needStore) openStopIDs() []string {
	n.mu.Lock()
	defer n.mu.Unlock()
	var ids []string
	for id, r := range n.recs {
		if r.State == NeedOpen && r.Kind != "" {
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
	if !ok || r.State != NeedOpen || n.answeringItem(r.Session, r.Item) {
		return
	}
	sh, listed := byName[r.Session]
	switch {
	case listed && sh.Status == ships.StatusNeedsYou && stopIdentity(sh) == r.Item && sh.Reason == r.Reason:
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
	case rec.Kind == StopNotStarted:
		writeError(w, http.StatusBadRequest, "that ship has not started: answer it in its Terminal tab")
		return
	case !rec.Answerable:
		writeError(w, http.StatusBadRequest, "that prompt is answered in the terminal")
		return
	case rec.Kind == StopPermission && (req.Text != "" || req.Choice != "allow" && req.Choice != "deny"):
		writeError(w, http.StatusBadRequest, "a permission card takes a choice of allow or deny")
		return
	case rec.Kind == StopTurn && (req.Choice != "" || strings.TrimSpace(req.Text) == ""):
		writeError(w, http.StatusBadRequest, "a finished turn takes the text of its next prompt")
		return
	case s.AnswerStop == nil:
		writeError(w, http.StatusInternalServerError, "ships cannot be answered here")
		return
	}
	n.setAnswering(rec.Session, rec.Item, true)
	defer n.setAnswering(rec.Session, rec.Item, false)
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
