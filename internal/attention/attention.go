// Package attention works out what waits on the engineer and serves the count on the WebSocket.
package attention

import (
	"net/http"

	"github.com/jbuffin/admiral/internal/inbox"
	"github.com/jbuffin/admiral/internal/server"
	"github.com/jbuffin/admiral/internal/ships"
)

// Count is how many things wait on the engineer across every fleet: inbox items other than notices,
// ships stopped for you, waiting crew boats, ready-to-merge ships and re-review ships. A stop counts
// whether or not it was seen.
func Count(shipList []ships.Ship, items []inbox.Item) int {
	n := 0
	for _, it := range items {
		if it.Kind != inbox.KindNotice {
			n++
		}
	}
	for _, s := range shipList {
		if s.Status == ships.StatusNeedsYou && s.Reason != ships.ReasonCrew {
			n++
		}
		if s.Status != ships.StatusSailingOff {
			for _, c := range s.Crew {
				if c.Status == ships.StatusNeedsYou {
					n++
				}
			}
		}
		if s.Status != ships.StatusMoored {
			continue
		}
		reviewing, responded := false, false
		for _, p := range s.PRs {
			if p.Reviewing {
				reviewing = true
				if p.Responded && p.State == "OPEN" {
					responded = true
				}
			}
		}
		if s.StackReady && !reviewing {
			n++
		}
		if responded {
			n++
		}
	}
	return n
}

// Service sends the count to every client on connect and whenever it changes.
type Service struct {
	Hub   *server.Hub
	Ships func() []ships.Ship
	Items func() []inbox.Item
	// Ready is whether the ships and the inbox are known; until then no count is sent, since an empty list would read as zero. Nil means ready.
	Ready func() bool
}

func (s *Service) Routes(mux *http.ServeMux) {}

func (s *Service) ready() bool { return s.Ready == nil || s.Ready() }

func (s *Service) message() any {
	return map[string]any{"type": "attention", "count": Count(s.Ships(), s.Items())}
}

func (s *Service) Connected(c *server.Client) {
	if s.ready() {
		_ = c.SendOnce("attention", s.message)
	}
}

// Changed re-works the count after a ships or inbox change. A client that follows push events gets its count from them.
func (s *Service) Changed() {
	if s.ready() {
		s.Hub.BroadcastOnceWindows("attention", s.message)
	}
}
