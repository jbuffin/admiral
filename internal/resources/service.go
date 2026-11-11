package resources

import (
	"encoding/json"
	"net/http"

	"github.com/jbuffin/admiral/internal/server"
)

// Service serves GET /api/resources and the `resources` message with the same body.
type Service struct {
	Set *Set
	Hub *server.Hub
}

func (s *Service) message() any {
	return map[string]any{"type": "resources", "resources": s.Set.State()}
}

func (s *Service) Broadcast() { s.Hub.BroadcastOnce("resources", s.message) }

func (s *Service) Connected(c *server.Client) { _ = c.SendOnce("resources", s.message) }

func (s *Service) Routes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/resources", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"resources": s.Set.State()})
	})
}
