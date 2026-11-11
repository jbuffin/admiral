// Package harnesscaps serves the probed capabilities of each fleet's harnesses. They are
// not the server API capabilities that GET /api/version lists.
package harnesscaps

import (
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/jbuffin/admiral/internal/capabilities"
	"github.com/jbuffin/admiral/internal/config"
	"github.com/jbuffin/admiral/internal/probes"
	"github.com/jbuffin/admiral/internal/server"
)

// Service serves GET /api/harness-capabilities and POST /api/harness-capabilities/check.
type Service struct {
	Home   string
	Config func() *config.Config
	// Runner is the server's one runner, so concurrent checks of a harness share a run.
	Runner *probes.Runner
}

func (s *Service) Routes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/harness-capabilities", s.list)
	mux.HandleFunc("POST /api/harness-capabilities/check", s.check)
}

func (s *Service) Connected(c *server.Client) {}

type capabilityJSON struct {
	ID        string     `json:"id"`
	Label     string     `json:"label"`
	Status    string     `json:"status"`
	CheckedAt *time.Time `json:"checked_at"`
	Detail    string     `json:"detail"`
}

type featureJSON struct {
	ID           string           `json:"id"`
	Label        string           `json:"label"`
	Capabilities []capabilityJSON `json:"capabilities"`
}

type harnessJSON struct {
	Fleet      string        `json:"fleet,omitempty"`
	Harness    string        `json:"harness"`
	Version    string        `json:"version"`
	DetectedAt *time.Time    `json:"detected_at"`
	Features   []featureJSON `json:"features"`
}

type fleetJSON struct {
	Key       string        `json:"key"`
	Name      string        `json:"name"`
	Harnesses []harnessJSON `json:"harnesses"`
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}

func (s *Service) harness(store *capabilities.Store, fleetKey, name string) harnessJSON {
	snap, _ := store.Snapshot(fleetKey, name) // an unreadable store reads as nothing checked
	h := harnessJSON{Harness: name, Version: snap.Version}
	if !snap.DetectedAt.IsZero() {
		h.DetectedAt = &snap.DetectedAt
	}
	for _, feat := range capabilities.Table() {
		f := featureJSON{ID: feat.ID, Label: feat.Label}
		for _, c := range feat.Capabilities {
			st := snap.States[c.ID]
			cj := capabilityJSON{ID: string(c.ID), Label: c.Label, Status: string(st.Status)}
			switch {
			case st.Result != nil:
				cj.Detail = st.Result.Detail
				if !st.Result.At.IsZero() {
					cj.CheckedAt = &st.Result.At
				}
			case snap.Version == "" && !snap.DetectedAt.IsZero() && c.ID == capabilities.AutopilotGuard:
				cj.Detail = snap.VersionError
				if cj.Detail == "" {
					cj.Detail = "version could not be detected"
				}
			}
			f.Capabilities = append(f.Capabilities, cj)
		}
		h.Features = append(h.Features, f)
	}
	return h
}

func (s *Service) list(w http.ResponseWriter, r *http.Request) {
	store := capabilities.Open(s.Home)
	out := []fleetJSON{}
	for _, f := range s.Config().Fleets {
		fj := fleetJSON{Key: f.Key, Name: f.Name, Harnesses: []harnessJSON{}}
		for _, h := range f.Harnesses {
			fj.Harnesses = append(fj.Harnesses, s.harness(store, f.Key, h.Name))
		}
		out = append(out, fj)
	}
	writeJSON(w, http.StatusOK, map[string]any{"fleets": out})
}

func (s *Service) check(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Fleet   string `json:"fleet"`
		Harness string `json:"harness"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<16)).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "the body must be JSON")
		return
	}
	if req.Fleet == "" || req.Harness == "" {
		writeError(w, http.StatusBadRequest, "fleet and harness are required")
		return
	}
	fleet, ok := s.Config().Fleet(req.Fleet)
	if !ok {
		writeError(w, http.StatusNotFound, "fleet "+req.Fleet+" not found")
		return
	}
	if _, ok := fleet.Harness(req.Harness); !ok {
		writeError(w, http.StatusNotFound, "fleet "+fleet.Name+" has no "+req.Harness+" dock")
		return
	}
	// The runner detaches its flight from this request, so a closed tab stores nothing.
	if _, err := s.Runner.Run(r.Context(), fleet, req.Harness); err != nil {
		if errors.Is(err, r.Context().Err()) && r.Context().Err() != nil {
			return
		}
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	h := s.harness(capabilities.Open(s.Home), fleet.Key, req.Harness)
	h.Fleet = fleet.Key
	writeJSON(w, http.StatusOK, h)
}
