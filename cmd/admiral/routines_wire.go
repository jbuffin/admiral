package main

import (
	"context"
	"time"

	"github.com/jbuffin/admiral/internal/config"
	"github.com/jbuffin/admiral/internal/inbox"
	"github.com/jbuffin/admiral/internal/launcher"
	"github.com/jbuffin/admiral/internal/routines"
	"github.com/jbuffin/admiral/internal/ships"
)

type realClock struct{}

func (realClock) Now() time.Time                         { return time.Now() }
func (realClock) After(d time.Duration) <-chan time.Time { return time.After(d) }

// routineShips adapts the ship service to what the routines scheduler needs.
type routineShips struct{ svc *ships.Service }

func (r routineShips) Launch(ctx context.Context, req launcher.Request) (routines.ShipRef, *launcher.Error) {
	sh, lerr := r.svc.Launch(ctx, req)
	if lerr != nil {
		return routines.ShipRef{}, lerr
	}
	return routines.ShipRef{Session: sh.Session, Status: sh.Status}, nil
}

func (r routineShips) Holder(key string) (routines.ShipRef, bool) {
	sh, ok := r.svc.Holder(key)
	return routines.ShipRef{Session: sh.Session, Status: sh.Status}, ok
}

// EndedFailed reads an anchored ship's exit: a non-zero code or a signal.
func (r routineShips) EndedFailed(session string) bool {
	sh, ok := r.svc.Get(session)
	return ok && (sh.ExitCode != nil && *sh.ExitCode != 0 || sh.ExitSignal != nil)
}

// wireStandingOrders gives the launcher the fleet's Guidance and the routines service its Limits and judge reads; the launcher is built before the store.
func wireStandingOrders(l *launcher.Launcher, store *routines.Store, svc *routines.Service, cfg *config.Manager) {
	l.Guidance = func(fleet string) (string, error) {
		o, err := store.StandingOrders(fleet)
		return o.Guidance, err
	}
	svc.Config = cfg
}

// routineProposer files an Ask first run in the Inbox.
type routineProposer struct{ inbox *inbox.Service }

func (p routineProposer) Propose(fleet string, r routines.Routine, t routines.Trigger, expires time.Time) error {
	_, err := p.inbox.PublishRoutineProposal(inbox.RoutineProposal{Fleet: fleet, Routine: r.ID, RoutineName: r.Name,
		Trigger: t.ID, Action: t.Action, ExpiresAt: expires})
	return err
}

// wakeOn calls fn each time the channel a resource set's channel closes, until ctx ends.
func wakeOn(ctx context.Context, changed func() <-chan struct{}, fns ...func()) {
	for {
		ch := changed()
		select {
		case <-ctx.Done():
			return
		case <-ch:
			for _, fn := range fns {
				fn()
			}
		}
	}
}
