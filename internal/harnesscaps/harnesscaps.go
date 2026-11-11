// Package harnesscaps serves the probed capabilities of each fleet's harnesses.
package harnesscaps

import (
	"net/http"

	"github.com/jbuffin/admiral/internal/config"
	"github.com/jbuffin/admiral/internal/probes"
	"github.com/jbuffin/admiral/internal/server"
)

// Service serves GET /api/harness-capabilities and POST /api/harness-capabilities/check.
type Service struct {
	Home   string
	Config func() *config.Config
	Runner *probes.Runner
}

func (s *Service) Routes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/harness-capabilities", s.notBuilt)
	mux.HandleFunc("POST /api/harness-capabilities/check", s.notBuilt)
}

func (s *Service) Connected(c *server.Client) {}

// notBuilt is a STUB: the routes are not built yet.
func (s *Service) notBuilt(w http.ResponseWriter, r *http.Request) {
	http.Error(w, `{"error":"not built"}`, http.StatusNotImplemented)
}
