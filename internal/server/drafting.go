package server

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/jbuffin/admiral/internal/config"
	"github.com/jbuffin/admiral/internal/repofinder"
	"github.com/jbuffin/admiral/internal/skillindex"
)

type draftingDock struct {
	Harness string   `json:"harness"`
	Names   []string `json:"names"`
}

// serveDraftingGet answers the fleet's drafting skill and, per dock in dock order, the installed names that carry it.
func (s *Server) serveDraftingGet(w http.ResponseWriter, r *http.Request) {
	if s.Config == nil {
		http.NotFound(w, r)
		return
	}
	var fleet config.Fleet
	found := false
	for _, f := range s.Config.Current().Fleets {
		if f.Key == r.PathValue("key") {
			fleet, found = f, true
		}
	}
	if !found {
		writeJSONError(w, http.StatusNotFound, "no such fleet "+r.PathValue("key"))
		return
	}
	dir := r.URL.Query().Get("dir")
	if dir != "" {
		var listed bool
		if dir, listed = repofinder.Contains(repofinder.List(repofinder.FleetRoots(fleet.SearchRoots, fleet.CloneRoot)), dir); !listed {
			writeJSONError(w, http.StatusBadRequest, "directory "+dir+" is not a checkout or search root of fleet "+fleet.Name)
			return
		}
	}
	docks := []draftingDock{}
	if fleet.DraftingSkill != "" {
		for _, h := range fleet.Harnesses {
			if names := skillindex.Carrying(h.Name, h.Environ(), dir, fleet.DraftingSkill); len(names) > 0 {
				docks = append(docks, draftingDock{Harness: h.Name, Names: names})
			}
		}
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	_ = json.NewEncoder(w).Encode(map[string]any{"skill": fleet.DraftingSkill, "harnesses": docks})
}

// serveDraftingPut saves drafting.skill; an empty name clears it.
func (s *Server) serveDraftingPut(w http.ResponseWriter, r *http.Request) {
	if s.Config == nil {
		http.NotFound(w, r)
		return
	}
	var req struct {
		Skill string `json:"skill"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&req); err != nil {
		writeJSONError(w, http.StatusBadRequest, "request is not valid JSON: "+err.Error())
		return
	}
	if req.Skill != "" && !skillindex.SafeInvocation(req.Skill) {
		writeJSONError(w, http.StatusBadRequest, "skill is not a safe skill name")
		return
	}
	err := s.Config.SetFleetFields(r.PathValue("key"), config.FleetFields{DraftingSkill: &req.Skill})
	switch {
	case errors.Is(err, config.ErrNoFleet):
		writeJSONError(w, http.StatusNotFound, err.Error())
	case errors.Is(err, config.ErrConflict):
		writeJSONError(w, http.StatusConflict, err.Error())
	case err != nil:
		writeJSONError(w, http.StatusBadRequest, err.Error())
	default:
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-store")
		_ = json.NewEncoder(w).Encode(s.Config.View())
	}
}
