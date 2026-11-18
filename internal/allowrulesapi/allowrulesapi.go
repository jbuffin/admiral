// Package allowrulesapi serves a fleet's allow rules and skipped allow rule proposals to its settings
// page, where either can be removed. Rules are added only from the Inbox, so there is no add or edit route.
package allowrulesapi

import (
	"encoding/json"
	"net/http"

	"github.com/jbuffin/admiral/internal/allowrules"
	"github.com/jbuffin/admiral/internal/config"
	"github.com/jbuffin/admiral/internal/server"
)

type Service struct {
	Config func() *config.Config
	// Store should be the launcher's, so removing a Codex rule rewrites the fleet's rules file.
	Store *allowrules.Store
}

func (s *Service) Routes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/fleets/{key}/allow-rules", s.list)
	mux.HandleFunc("POST /api/fleets/{key}/allow-rules/remove", s.change(s.Store.Remove))
	mux.HandleFunc("POST /api/fleets/{key}/allow-rules/skipped/remove", s.change(s.Store.Unskip))
}

func (s *Service) Connected(*server.Client) {}

func (s *Service) fleet(w http.ResponseWriter, r *http.Request) (string, bool) {
	key := r.PathValue("key")
	if _, ok := s.Config().Fleet(key); !ok {
		writeError(w, http.StatusNotFound, "fleet "+key+" not found")
		return "", false
	}
	return key, true
}

func (s *Service) list(w http.ResponseWriter, r *http.Request) {
	if key, ok := s.fleet(w, r); ok {
		s.writeList(w, key)
	}
}

func (s *Service) writeList(w http.ResponseWriter, key string) {
	fl, err := s.Store.List(key)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, fl)
}

func (s *Service) change(apply func(fleet string, r allowrules.Rule) error) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		key, ok := s.fleet(w, r)
		if !ok {
			return
		}
		var body struct {
			Rule *allowrules.Rule `json:"rule"`
		}
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10)).Decode(&body); err != nil || body.Rule == nil {
			writeError(w, http.StatusBadRequest, "the body needs the rule to remove, as the list gave it")
			return
		}
		if err := body.Rule.Check(); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		if err := apply(key, *body.Rule); err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		s.writeList(w, key)
	}
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}
