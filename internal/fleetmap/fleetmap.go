// Package fleetmap serves the reads of one fleet's map: its repos and checkouts, what it may clone, and its docks.
// Every route is keyed by the fleet, so a read never carries another fleet's data.
package fleetmap

import (
	"encoding/json"
	"net/http"
	"os"

	"github.com/jbuffin/admiral/internal/config"
	"github.com/jbuffin/admiral/internal/harnesscaps"
	"github.com/jbuffin/admiral/internal/launcher"
	"github.com/jbuffin/admiral/internal/repofinder"
	"github.com/jbuffin/admiral/internal/server"
)

type Service struct {
	Home   string
	Config func() *config.Config
}

func (s *Service) Routes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/fleets/{key}/repos", s.repos)
	mux.HandleFunc("GET /api/fleets/{key}/cloneable", s.cloneable)
	mux.HandleFunc("GET /api/fleets/{key}/docks", s.docks)
}

// Connected sends nothing: these are reads.
func (s *Service) Connected(*server.Client) {}

func (s *Service) fleet(w http.ResponseWriter, r *http.Request) (config.Fleet, bool) {
	key := r.PathValue("key")
	f, ok := s.Config().Fleet(key)
	if !ok {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "fleet " + key + " not found"})
	}
	return f, ok
}

func (s *Service) repos(w http.ResponseWriter, r *http.Request) {
	f, ok := s.fleet(w, r)
	if !ok {
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"repos": repofinder.List(repofinder.FleetRoots(f.SearchRoots, f.CloneRoot))})
}

// cloneable answers the rule for what Admiral clones, not a list: no network call is made.
func (s *Service) cloneable(w http.ResponseWriter, r *http.Request) {
	f, ok := s.fleet(w, r)
	if !ok {
		return
	}
	home, _ := os.UserHomeDir()
	owners := f.Owners
	if owners == nil {
		owners = []string{}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"hosts":      launcher.GitHubHosts(s.Config(), f),
		"clone_root": repofinder.CloneRoot(f.CloneRoot, f.SearchRoots, home),
		"owners":     owners,
	})
}

func (s *Service) docks(w http.ResponseWriter, r *http.Request) {
	f, ok := s.fleet(w, r)
	if !ok {
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"docks": (&harnesscaps.Service{Home: s.Home}).Docks(f)})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
