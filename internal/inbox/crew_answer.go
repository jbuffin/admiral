package inbox

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/jbuffin/admiral/internal/crew"
	"github.com/jbuffin/admiral/internal/crewapi"
	"github.com/jbuffin/admiral/internal/outcomes"
)

// crewRefusedError is a crew answer that can't be applied as the item stands, with nothing written. Note is the id of
// the first proposed note that doesn't fit.
type crewRefusedError struct{ Msg, Note string }

func (e *crewRefusedError) Error() string { return e.Msg }

// CrewNoteDecision is one note's answer. Replaces tells an absent field (the proposal's value stays) from null (no line is replaced).
type CrewNoteDecision struct {
	Decision string          `json:"decision"`
	Note     *string         `json:"note,omitempty"`
	Scope    *string         `json:"scope,omitempty"`
	Repo     *string         `json:"repo,omitempty"`
	Replaces json.RawMessage `json:"replaces,omitempty"`
}

type CrewNotesRequest struct {
	Notes map[string]CrewNoteDecision `json:"notes"`
}

type crewNoteAnswer struct {
	Decision string `json:"decision"`
	Note     string `json:"note,omitempty"`
	Scope    string `json:"scope,omitempty"`
	Repo     string `json:"repo,omitempty"`
}

var crewVerb = map[string]string{"keep": "kept", "edit": "edited", "drop": "dropped"}

func (s *Service) crewFleet(o CrewObject) (FleetInfo, error) {
	if s.Fleet != nil {
		if f, ok := s.Fleet(o.Fleet); ok && f.Key != "" {
			return f, nil
		}
	}
	return FleetInfo{}, &crewRefusedError{Msg: "fleet " + o.Fleet + " is no longer configured; nothing was changed"}
}

func noteLine(o CrewObject, note string) string {
	return "- " + o.RunDate + " (" + o.Ship + "): " + note
}

// edited is the note the engineer's edit leaves, held to the rules a report's note is.
func edited(o CrewObject, n CrewNote, d CrewNoteDecision) (CrewNote, error) {
	if d.Note != nil {
		n.Note = strings.TrimSpace(*d.Note)
	}
	if d.Scope != nil {
		n.Scope = *d.Scope
	}
	if d.Repo != nil {
		n.Repo = *d.Repo
	}
	switch trimmed := bytes.TrimSpace(d.Replaces); {
	case len(trimmed) == 0:
	case string(trimmed) == "null":
		n.Replaces = nil
	default:
		var line string
		if json.Unmarshal(trimmed, &line) != nil {
			return n, invalid("note %s: replaces is a note line or null", n.ID)
		}
		n.Replaces = &line
	}
	switch {
	case n.Scope != "fleet" && n.Scope != "repo":
		return n, invalid("note %s: scope is fleet or repo", n.ID)
	case n.Scope == "repo" && !repoOK(n.Repo):
		return n, invalid("note %s: repo is <owner>/<repo>", n.ID)
	case n.Note == "" || utf8.RuneCountInString(n.Note) > maxNoteChars || !crew.ValidNote(noteLine(o, n.Note)):
		return n, invalid("note %s: the note is one line of visible text, at most %d characters", n.ID, maxNoteChars)
	case n.Replaces != nil && !crew.ValidNote("- "+*n.Replaces):
		return n, invalid("note %s: replaces is not a note line", n.ID)
	}
	if n.Scope == "fleet" {
		n.Repo = ""
	}
	return n, nil
}

func writeOf(o CrewObject, n CrewNote) crewapi.NoteWrite {
	w := crewapi.NoteWrite{Member: n.Member, Line: noteLine(o, n.Note)}
	if n.Scope == "repo" {
		w.Repo = n.Repo
	}
	if n.Replaces != nil {
		w.Replaces = *n.Replaces
	}
	return w
}

func notesRecord(it Item, o CrewObject, n CrewNote, decision string) outcomes.Record {
	return outcomes.Record{Ship: o.Ship, Event: "crew_note_" + crewVerb[decision], Fields: map[string]any{"item": it.ID, "kind": KindCrewNotes, "note": n.ID, "member": n.Member, "scope": n.Scope}}
}

// writeRefusal says which proposed note a refused write belongs to.
func writeRefusal(err error, ids []string) error {
	var refused *crewapi.NoteRefusedError
	if errors.As(err, &refused) && refused.Index < len(ids) {
		return &crewRefusedError{Msg: "note " + ids[refused.Index] + ": " + refused.Error() + "; nothing was changed. Drop it or edit its scope or replaces.", Note: ids[refused.Index]}
	}
	return err
}

// AnswerCrewNotes writes the kept and edited notes to the fleet's crew, all or none, then publishes the answer. The
// fleet's note files change only here.
func (s *Service) AnswerCrewNotes(id string, req CrewNotesRequest) error {
	err := s.writeAnswered(AdmiralSession, id, KindCrewNotes, func(it Item) (any, []outcomes.Record, error) {
		o, _ := it.CrewObject()
		have := map[string]bool{}
		for _, n := range o.Notes {
			have[n.ID] = true
		}
		for nid := range req.Notes {
			if !have[nid] {
				return nil, nil, invalid("the item has no note %q", nid)
			}
		}
		answers := map[string]crewNoteAnswer{}
		var writes []crewapi.NoteWrite
		var ids []string
		var recs []outcomes.Record
		for _, n := range o.Notes {
			d, ok := req.Notes[n.ID]
			if !ok {
				return nil, nil, invalid("note %s has no decision", n.ID)
			}
			switch d.Decision {
			case "keep", "drop":
				if d.Note != nil || d.Scope != nil || d.Repo != nil || len(d.Replaces) > 0 {
					return nil, nil, invalid("note %s is %s, so it takes no changes", n.ID, d.Decision)
				}
				answers[n.ID] = crewNoteAnswer{Decision: d.Decision}
			case "edit":
				e, err := edited(o, n, d)
				if err != nil {
					return nil, nil, err
				}
				n = e
				answers[n.ID] = crewNoteAnswer{"edit", n.Note, n.Scope, n.Repo}
			default:
				return nil, nil, invalid("note %s: %q is not keep, edit or drop", n.ID, d.Decision)
			}
			if d.Decision != "drop" {
				writes, ids = append(writes, writeOf(o, n)), append(ids, n.ID)
			}
			recs = append(recs, notesRecord(it, o, n, d.Decision))
		}
		fleet, err := s.crewFleet(o)
		if err != nil {
			return nil, nil, err
		}
		if len(writes) > 0 {
			if err := crewapi.ApplyNotes(s.Home, fleet.Key, writes); err != nil {
				return nil, nil, writeRefusal(err, ids)
			}
		}
		file := struct {
			Version  int                       `json:"version"`
			ID       string                    `json:"id"`
			Kind     string                    `json:"kind"`
			Answered string                    `json:"answered"`
			Notes    map[string]crewNoteAnswer `json:"notes"`
		}{1, id, KindCrewNotes, time.Now().UTC().Format(time.RFC3339), answers}
		return file, recs, nil
	})
	if err == nil {
		s.Refresh()
	}
	return err
}

// AnswerCrewMember keeps or drops an ad hoc member. file_removed says whether the ad hoc file is gone. A removal asks
// for no configured fleet, since it only discards: with the fleet gone the file is left where it is.
func (s *Service) AnswerCrewMember(id, decision string) (fileRemoved bool, err error) {
	return s.answerCrewMember(id, decision, true)
}

func (s *Service) answerCrewMember(id, decision string, needFleet bool) (fileRemoved bool, err error) {
	if decision != "keep" && decision != "drop" {
		return false, invalid("decision is keep or drop")
	}
	err = s.writeAnswered(AdmiralSession, id, KindCrewMember, func(it Item) (any, []outcomes.Record, error) {
		o, _ := it.CrewObject()
		fleet, ferr := s.crewFleet(o)
		if ferr != nil && (needFleet || decision == "keep") {
			return nil, nil, ferr
		}
		if decision == "keep" {
			var writes []crewapi.NoteWrite
			var ids []string
			for _, n := range o.Notes {
				writes, ids = append(writes, writeOf(o, n)), append(ids, n.ID)
			}
			var builtIns []string
			if s.BuiltIns != nil {
				builtIns = s.BuiltIns(fleet.Key)
			}
			removed, err := crewapi.KeepAdHoc(s.Home, fleet.Key, o.Member, o.Definition, builtIns, writes)
			var refused *crewapi.MemberRefusedError
			switch {
			case errors.As(err, &refused):
				return nil, nil, &crewRefusedError{Msg: refused.Msg + "; nothing was changed"}
			case err != nil:
				return nil, nil, writeRefusal(err, ids)
			}
			fileRemoved = removed
		} else if ferr == nil {
			removed, err := crewapi.DropAdHoc(s.Home, fleet.Key, o.Member, o.Definition)
			if err != nil {
				return nil, nil, err
			}
			fileRemoved = removed
		}
		file := struct {
			Version     int    `json:"version"`
			ID          string `json:"id"`
			Kind        string `json:"kind"`
			Answered    string `json:"answered"`
			Decision    string `json:"decision"`
			FileRemoved bool   `json:"file_removed"`
		}{1, id, KindCrewMember, time.Now().UTC().Format(time.RFC3339), decision, fileRemoved}
		return file, []outcomes.Record{{Ship: o.Ship, Event: "crew_member_" + crewVerb[decision], Fields: map[string]any{"item": id, "kind": KindCrewMember, "member": o.Member}}}, nil
	})
	if err == nil {
		s.Refresh()
	}
	return fileRemoved, err
}

// dropCrewNotes is Remove on a crew_notes item: every note dropped, which writes nothing and needs no configured fleet.
func (s *Service) dropCrewNotes(id string) error {
	err := s.writeAnswered(AdmiralSession, id, KindCrewNotes, func(it Item) (any, []outcomes.Record, error) {
		o, _ := it.CrewObject()
		answers := map[string]crewNoteAnswer{}
		var recs []outcomes.Record
		for _, n := range o.Notes {
			answers[n.ID] = crewNoteAnswer{Decision: "drop"}
			recs = append(recs, notesRecord(it, o, n, "drop"))
		}
		file := struct {
			Version  int                       `json:"version"`
			ID       string                    `json:"id"`
			Kind     string                    `json:"kind"`
			Answered string                    `json:"answered"`
			Notes    map[string]crewNoteAnswer `json:"notes"`
		}{1, id, KindCrewNotes, time.Now().UTC().Format(time.RFC3339), answers}
		return file, recs, nil
	})
	if err == nil {
		s.Refresh()
	}
	return err
}

func (s *Service) serveCrewNotes(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var req CrewNotesRequest
	if !decode(w, r, 1<<20, &req) {
		return
	}
	respond(w, AdmiralSession, id, ReviewResult{}, s.AnswerCrewNotes(id, req))
}

func (s *Service) serveCrewMember(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var req struct {
		Decision string `json:"decision"`
	}
	if !decode(w, r, 1<<16, &req) {
		return
	}
	removed, err := s.AnswerCrewMember(id, req.Decision)
	if err != nil {
		respond(w, AdmiralSession, id, ReviewResult{}, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"answered": id, "file_removed": removed})
}
