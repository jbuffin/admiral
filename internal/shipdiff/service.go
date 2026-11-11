package shipdiff

import (
	"encoding/json"
	"net/http"

	"github.com/jbuffin/admiral/internal/server"
	"github.com/jbuffin/admiral/internal/ships"
)

type Service struct {
	Ships *ships.Service
}

func (s *Service) Routes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/ships/{session}/changes", s.serveChanges)
}

func (s *Service) serveChanges(w http.ResponseWriter, r *http.Request) {
	dir, ok := s.Ships.Checkout(r.PathValue("session"))
	if !ok {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
		return
	}
	res, err := Compute(r.Context(), dir)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "could not read the changes"})
		return
	}
	writeJSON(w, http.StatusOK, res)
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func (s *Service) Connected(*server.Client) {}
