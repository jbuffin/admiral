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

// maxInstructionsBody bounds the instructions read before their own cap: 2,000 code points is at most 8 KB.
const maxInstructionsBody = 16 << 10

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
	if !s.noteFleetOK(w, fleet) {
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

// noteFleet is the fleet for a notes or instructions route, or writes the refusal. These sit
// outside route so they stay served while the chat is off; only a case clash refuses, since two
// keys would share one chat directory.
func (s *Service) noteFleet(w http.ResponseWriter, r *http.Request) (string, bool) {
	fleet := r.PathValue("fleet")
	if _, ok := s.Config().Fleet(fleet); !ok || !chatlog.Valid(fleet) {
		writeError(w, http.StatusNotFound, "no fleet "+fleet)
		return "", false
	}
	return fleet, s.noteFleetOK(w, fleet)
}

func (s *Service) noteFleetOK(w http.ResponseWriter, fleet string) bool {
	cfg := s.Config()
	if f, ok := cfg.Fleet(fleet); ok {
		if why := cfg.KeyClash(f); why != "" {
			writeError(w, http.StatusConflict, why)
			return false
		}
	}
	return true
}

// serveNotes lists a fleet's open notes.
func (s *Service) serveNotes(w http.ResponseWriter, r *http.Request) {
	fleet, ok := s.noteFleet(w, r)
	if !ok {
		return
	}
	notes, err := s.notes().Notes(fleet)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"notes": notes})
}

func writeInstructions(w http.ResponseWriter, text string) {
	writeJSON(w, http.StatusOK, map[string]any{"text": text, "max": ledger.MaxInstructionChars})
}

// writeLedgerError maps a ledger refusal to refusedStatus.
func writeLedgerError(w http.ResponseWriter, err error, refusedStatus int) {
	var refused *ledger.RefusedError
	switch {
	case errors.Is(err, ledger.ErrNotFound):
		writeError(w, http.StatusNotFound, "no open note with that id")
	case errors.As(err, &refused):
		writeError(w, refusedStatus, refused.Msg)
	default:
		writeError(w, http.StatusInternalServerError, err.Error())
	}
}

func (s *Service) serveInstructions(w http.ResponseWriter, r *http.Request) {
	fleet, ok := s.noteFleet(w, r)
	if !ok {
		return
	}
	text, err := s.notes().Instructions(fleet)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeInstructions(w, text)
}

func (s *Service) serveInstructionsSet(w http.ResponseWriter, r *http.Request) {
	fleet, ok := s.noteFleet(w, r)
	if !ok {
		return
	}
	b, err := io.ReadAll(io.LimitReader(r.Body, maxInstructionsBody+1))
	var body struct{ Text *string }
	if err != nil || len(b) > maxInstructionsBody || json.Unmarshal(b, &body) != nil || body.Text == nil {
		writeError(w, http.StatusBadRequest, `send {"text": "the instructions"}`)
		return
	}
	text, err := s.notes().SetInstructions(fleet, *body.Text)
	if err != nil {
		writeLedgerError(w, err, http.StatusUnprocessableEntity)
		return
	}
	writeInstructions(w, text)
}

func (s *Service) serveNoteDelete(w http.ResponseWriter, r *http.Request) {
	fleet, ok := s.noteFleet(w, r)
	if !ok {
		return
	}
	if err := s.notes().Cancel(fleet, r.PathValue("id")); err != nil {
		writeLedgerError(w, err, http.StatusConflict)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Service) serveNotePromote(w http.ResponseWriter, r *http.Request) {
	fleet, ok := s.noteFleet(w, r)
	if !ok {
		return
	}
	text, err := s.notes().Promote(fleet, r.PathValue("id"))
	if err != nil {
		writeLedgerError(w, err, http.StatusConflict)
		return
	}
	writeInstructions(w, text)
}
