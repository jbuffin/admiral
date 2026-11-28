// Package attention works out what waits on the engineer and serves the count on the WebSocket.
package attention

import (
	"net/http"

	"github.com/jbuffin/admiral/internal/inbox"
	"github.com/jbuffin/admiral/internal/server"
	"github.com/jbuffin/admiral/internal/ships"
)

// Count is how many things wait on the engineer across every fleet.
func Count(shipList []ships.Ship, items []inbox.Item) int { return 0 }

// Service sends the count to every client on connect and whenever it changes.
type Service struct {
	Hub   *server.Hub
	Ships func() []ships.Ship
	Items func() []inbox.Item
}

func (s *Service) Routes(mux *http.ServeMux) {}

func (s *Service) Connected(c *server.Client) {}

// Changed re-works the count after a ships or inbox change.
func (s *Service) Changed() {}
