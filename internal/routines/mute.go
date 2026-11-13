package routines

import (
	"encoding/json"
	"net/http"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/jbuffin/admiral/internal/config"
	"github.com/jbuffin/admiral/internal/standing"
)

const maxMuteValue = 200

// serveMute adds a skip-filter entry scoped to one routine: by sender or by dedupe_key. Adding an entry only tightens
// the fleet's Limits, and a second press adds nothing.
func (s *Service) serveMute(w http.ResponseWriter, r *http.Request) {
	fleet := r.PathValue("key")
	list, err := s.Store.List(fleet)
	if err != nil {
		fail(w, err)
		return
	}
	i := indexOf(list, r.PathValue("id"))
	if i < 0 {
		fail(w, ErrNotFound)
		return
	}
	var req struct {
		By    string `json:"by"`
		Value string `json:"value"`
	}
	if !decodeBody(w, r, &req) {
		return
	}
	switch {
	case req.By != "sender" && req.By != "dedupe_key":
		writeError(w, http.StatusBadRequest, "by: mute by sender or dedupe_key")
		return
	case strings.TrimSpace(req.Value) == "" || req.By == "sender" && req.Value == UnknownLogin:
		writeError(w, http.StatusBadRequest, "value: name a sender or a dedupe key to mute")
		return
	case utf8.RuneCountInString(req.Value) > maxMuteValue || strings.ContainsFunc(req.Value, func(r rune) bool { return r < ' ' || r == 0x7f }):
		writeError(w, http.StatusBadRequest, "value: not a sender or a dedupe key")
		return
	}
	f, ok := s.Config.Current().Fleet(fleet)
	if !ok {
		fail(w, ErrUnknownFleet)
		return
	}
	s.ordersMu.Lock()
	defer s.ordersMu.Unlock()
	f, _ = s.Config.Current().Fleet(fleet)
	field, have := "senders", f.Limits.Skip.Senders
	if req.By == "dedupe_key" {
		field, have = "dedupe_keys", f.Limits.Skip.DedupeKeys
	}
	entry := standing.Scoped{Value: req.Value, Routine: list[i].ID}
	if slices.Contains(have, entry) {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	raw, err := json.Marshal(map[string]any{field: append(slices.Clone(have), entry)})
	if err != nil {
		fail(w, err)
		return
	}
	if err := s.Config.SetFleetFields(fleet, config.FleetFields{Limits: map[string]json.RawMessage{"skip": raw}}); err != nil {
		configFail(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
