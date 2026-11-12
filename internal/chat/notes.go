package chat

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"

	"github.com/jbuffin/admiral/internal/actor"
	"github.com/jbuffin/admiral/internal/chatlog"
	"github.com/jbuffin/admiral/internal/ledger"
)

// maxNoteBody bounds what is read before the note's own length check.
const maxNoteBody = 8 << 10

// serveNoteAdd saves a note in the token's fleet's ledger, whatever fleet the body names.
func (s *Service) serveNoteAdd(w http.ResponseWriter, r *http.Request) {
	fleet, ok := actor.FleetOf(r.Context())
	if !ok {
		writeError(w, http.StatusForbidden, "only AdmiralBot saves notes here")
		return
	}
	b, err := io.ReadAll(io.LimitReader(r.Body, maxNoteBody+1))
	var body struct{ Text string }
	if err != nil || len(b) > maxNoteBody || json.Unmarshal(b, &body) != nil {
		writeError(w, http.StatusBadRequest, `send {"text": "the fact to remember"}, at most 500 characters`)
		return
	}
	e, err := s.notes().AddNote(fleet, body.Text, actor.Via)
	var refused *ledger.RefusedError
	switch {
	case errors.As(err, &refused):
		writeError(w, http.StatusUnprocessableEntity, refused.Msg)
	case err != nil:
		writeError(w, http.StatusInternalServerError, err.Error())
	default:
		writeJSON(w, http.StatusCreated, e)
	}
}

// serveNotes lists a fleet's open notes. It sits outside route so notes stay listable while the fleet's chat is off.
func (s *Service) serveNotes(w http.ResponseWriter, r *http.Request) {
	fleet := r.PathValue("fleet")
	if _, ok := s.Config().Fleet(fleet); !ok || !chatlog.Valid(fleet) {
		writeError(w, http.StatusNotFound, "no fleet "+fleet)
		return
	}
	notes, err := s.notes().Notes(fleet)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"notes": notes})
}
