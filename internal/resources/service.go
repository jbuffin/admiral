package resources

import (
	"encoding/json"
	"net/http"

	"github.com/jbuffin/admiral/internal/server"
)

// Service is the resources' HTTP and WebSocket surface: GET /api/resources and
// the `resources` message, which has the same body.
type Service struct {
	Set *Set
	Hub *server.Hub
}

func (s *Service) message() any {
	return map[string]any{"type": "resources", "resources": s.Set.State()}
}

// Broadcast pushes the resources to every client that doesn't have them yet.
func (s *Service) Broadcast() { s.Hub.BroadcastOnce("resources", s.message) }

// Connected sends a new client the resources.
func (s *Service) Connected(c *server.Client) { _ = c.SendOnce("resources", s.message) }

// Routes registers the resources API.
func (s *Service) Routes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/resources", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"resources": s.Set.State()})
	})
}
