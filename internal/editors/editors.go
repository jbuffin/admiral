// Package editors tells a client which editors it can open a ship's folder in.
package editors

import (
	"net/http"

	"github.com/jbuffin/admiral/internal/server"
)

// Service answers GET /api/editors. Local says whether the request comes from this Mac itself;
// VSCode says whether VS Code is installed here, and is the seam tests set.
type Service struct {
	Local  func(*http.Request) bool
	VSCode func() bool
}

func (s *Service) Routes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/editors", func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "not implemented", http.StatusNotImplemented)
	})
}

func (s *Service) Connected(*server.Client) {}

var _ server.Module = (*Service)(nil)
