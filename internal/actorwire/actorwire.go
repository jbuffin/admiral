// Package actorwire builds the actor guard from the services whose fleets it fences.
package actorwire

import (
	"github.com/jbuffin/admiral/internal/actor"
	"github.com/jbuffin/admiral/internal/campaigns/follow"
	"github.com/jbuffin/admiral/internal/inbox"
)

// Deps are the lookups the guard needs, each a method of a running service.
type Deps struct {
	FleetKeys  func() []string
	ActorToken func(fleet string) string
	ShipFleet  func(session string) (string, bool)
	Inbox      func() []inbox.Item
	Source     func(key string) (fleet string, ok bool)
	Campaign   func(id string) (follow.Followed, bool)
}

// Guard is the guard a server runs: a thing with no fleet recorded counts as unknown.
func Guard(d Deps) *actor.Guard {
	return &actor.Guard{
		Tokens: func(token string) (string, bool) {
			return actor.Lookup(token, d.FleetKeys(), d.ActorToken)
		},
		ShipFleet: d.ShipFleet,
		ItemFleet: func(session, id string) (string, bool) {
			for _, it := range d.Inbox() {
				if it.Session == session && it.ID == id {
					return it.Fleet, it.Fleet != ""
				}
			}
			return "", false
		},
		SourceFleet: func(key string) (string, bool) {
			fleet, ok := d.Source(key)
			return fleet, ok && fleet != ""
		},
		CampaignFleet: func(id string) (string, bool) {
			f, ok := d.Campaign(id)
			return f.Fleet, ok && f.Fleet != ""
		},
	}
}
