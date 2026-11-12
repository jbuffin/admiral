package routines

import (
	"context"
	"time"

	"github.com/jbuffin/admiral/internal/launcher"
)

// Clock is the scheduler's time seam; After fires once d has passed on it.
type Clock interface {
	Now() time.Time
	After(d time.Duration) <-chan time.Time
}

// ShipRef names a ship and its status (underway, needs_you, anchored, moored, ...).
type ShipRef struct{ Session, Status string }

// Ships is what the scheduler needs of the ship service.
type Ships interface {
	// Launch is ships.Service.Launch.
	Launch(ctx context.Context, req launcher.Request) (ShipRef, *launcher.Error)
	// Holder is the listed ship, in any status, holding the launch key.
	Holder(key string) (ShipRef, bool)
	// EndedFailed is true once the ship has ended failed (a non-zero exit or a signal).
	EndedFailed(session string) bool
}

// Chat sends a turn to a fleet's AdmiralBot; it errors when the chat is not running.
type Chat interface {
	Send(fleet, text string) error
}

// Proposer takes a run that needs the engineer's yes; u3 makes it an Inbox proposal.
type Proposer interface {
	Propose(fleet string, r Routine, t Trigger) error
}

// Scheduler fires every fleet's enabled routines when their When comes up.
type Scheduler struct {
	Store    *Store
	Clock    Clock
	Ships    Ships
	Chat     Chat
	Proposer Proposer
	Zone     *time.Location // the zone a When is read in; main passes time.Local
}

// Run catches up missed runs, then ticks until ctx ends, sleeping until the earliest next run or a Changed.
func (s *Scheduler) Run(ctx context.Context) {}

// CatchUp handles the runs missed while the server was down: one late run under 12 hours old, `missed` records for the rest.
func (s *Scheduler) CatchUp(ctx context.Context) error { return nil }

// Tick does everything due at Clock.Now(): routines whose time came, waiting runs, and the breaker's count.
func (s *Scheduler) Tick(ctx context.Context) error { return nil }

// Changed wakes the scheduler: a routine was saved, deleted or switched, or a ship or resource changed.
func (s *Scheduler) Changed() {}
