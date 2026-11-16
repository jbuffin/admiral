package inbox

import (
	"errors"
	"net/http"
	"time"

	"github.com/jbuffin/admiral/internal/crewapi"
	"github.com/jbuffin/admiral/internal/outcomes"
)

// AnswerMission keeps or drops a proposed mission. fileWritten says whether this Keep created the file; one already
// there with the same text counts as kept without a write.
func (s *Service) AnswerMission(id, decision string) (fileWritten bool, err error) {
	return s.answerMission(id, decision, true)
}

// answerMission asks for a configured fleet only to Keep, so Remove works with the fleet gone.
func (s *Service) answerMission(id, decision string, needFleet bool) (fileWritten bool, err error) {
	if decision != "keep" && decision != "drop" {
		return false, invalid("decision is keep or drop")
	}
	err = s.writeAnswered(AdmiralSession, id, KindMission, func(it Item) (any, []outcomes.Record, error) {
		o, _ := it.MissionObject()
		if decision == "keep" {
			var fleet FleetInfo
			if s.Fleet != nil {
				fleet, _ = s.Fleet(o.Fleet)
			}
			if fleet.Key == "" {
				return nil, nil, &crewRefusedError{Msg: "fleet " + o.Fleet + " is no longer configured; nothing was changed"}
			}
			w, err := crewapi.KeepMission(s.Home, fleet.Key, o.Name, o.Text)
			var refused *crewapi.MissionRefusedError
			switch {
			case errors.As(err, &refused):
				return nil, nil, &crewRefusedError{Msg: refused.Msg}
			case err != nil:
				return nil, nil, err
			}
			fileWritten = w
		}
		file := struct {
			Version     int    `json:"version"`
			ID          string `json:"id"`
			Kind        string `json:"kind"`
			Answered    string `json:"answered"`
			Decision    string `json:"decision"`
			FileWritten bool   `json:"file_written"`
		}{1, id, KindMission, time.Now().UTC().Format(time.RFC3339), decision, fileWritten}
		return file, []outcomes.Record{{Ship: o.Ship, Event: "mission_" + crewVerb[decision], Fields: map[string]any{"item": id, "kind": KindMission, "name": o.Name}}}, nil
	})
	if err == nil {
		s.Refresh()
	}
	return fileWritten, err
}

func (s *Service) serveMission(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var req struct {
		Decision string `json:"decision"`
	}
	if !decode(w, r, 1<<16, &req) {
		return
	}
	written, err := s.AnswerMission(id, req.Decision)
	if err != nil {
		respond(w, AdmiralSession, id, ReviewResult{}, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"answered": id, "file_written": written})
}
