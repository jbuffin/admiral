package push

import (
	"context"
	"encoding/json"
	"time"

	"github.com/jbuffin/admiral/internal/inbox"
	"github.com/jbuffin/admiral/internal/server"
	"github.com/jbuffin/admiral/internal/ships"
)

// FollowersConfig is what Followers reads from outside. Zero Now is time.Now, an empty Boot is a fresh id, nil Ready is ready.
type FollowersConfig struct {
	Now   func() time.Time
	Boot  string
	Ready func() bool
}

// Followers sends push events to the bearer-paired sockets that follow them and keeps the ring they catch up from.
type Followers struct{ cfg FollowersConfig }

func NewFollowers(cfg FollowersConfig) *Followers { return &Followers{cfg: cfg} }

// Deliver is the Sink side: a Push or a Clear becomes a ring entry and goes to every follower.
func (f *Followers) Deliver(Decided) {}

// Snapshot tells the followers the ships and the Inbox items the Watcher has now processed.
type Snapshotter interface {
	Snapshot(shipList []ships.Ship, items []inbox.Item)
}

func (f *Followers) Snapshot(shipList []ships.Ship, items []inbox.Item) {}

// Handle is the message handler for "push_follow".
func (f *Followers) Handle(ctx context.Context, c *server.Client, raw json.RawMessage) {}
