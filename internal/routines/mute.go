package routines

import (
	"net/http"
	"strings"
	"unicode"

	"github.com/jbuffin/admiral/internal/standing"
)

const maxLogin = 100

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
	case req.By == "sender" && !plainLogin(req.Value), req.By == "dedupe_key" && !ValidDedupeKey(req.Value):
		writeError(w, http.StatusBadRequest, "value: not a sender or a dedupe key")
		return
	}
	if _, ok := s.Config.Current().Fleet(fleet); !ok {
		fail(w, ErrUnknownFleet)
		return
	}
	field := "senders"
	if req.By == "dedupe_key" {
		field = "dedupe_keys"
	}
	if _, err := s.Config.AppendSkip(fleet, field, standing.Scoped{Value: req.Value, Routine: list[i].ID}); err != nil {
		configFail(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// plainLogin is true for a login short enough and free of control and format runes, which would let a sender look like someone else.
func plainLogin(s string) bool {
	if len(s) > maxLogin {
		return false
	}
	for _, c := range s {
		if unicode.IsControl(c) || unicode.Is(unicode.Cf, c) {
			return false
		}
	}
	return true
}
