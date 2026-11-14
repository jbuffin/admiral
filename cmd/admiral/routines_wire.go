package main

import (
	"context"
	"errors"
	"log"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/jbuffin/admiral/internal/capabilities"
	"github.com/jbuffin/admiral/internal/config"
	"github.com/jbuffin/admiral/internal/inbox"
	"github.com/jbuffin/admiral/internal/judge"
	"github.com/jbuffin/admiral/internal/launcher"
	"github.com/jbuffin/admiral/internal/lookout"
	"github.com/jbuffin/admiral/internal/routines"
	"github.com/jbuffin/admiral/internal/sessionhost"
	"github.com/jbuffin/admiral/internal/ships"
	"github.com/jbuffin/admiral/internal/sources"
	"github.com/jbuffin/admiral/internal/standing"
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

// Stop ends the ship, and reports ErrShipNotListed for one the host does not list.
func (r routineShips) Stop(session string) error {
	err := r.svc.Stop(session)
	if errors.Is(err, ships.ErrNotFound) {
		return routines.ErrShipNotListed
	}
	return err
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
		Trigger: t.ID, Action: t.Action, Reason: t.Reason, ExpiresAt: expires})
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

// listedShips is what the routines read of every ship the host lists.
func listedShips(list func() []ships.Ship) func() []routines.ListedShip {
	return func() []routines.ListedShip {
		var out []routines.ListedShip
		for _, sh := range list() {
			out = append(out, routines.ListedShip{Session: sh.Session, Fleet: sh.Fleet, Routine: sh.Routine, Launched: sh.Launched, Status: sh.Status})
		}
		return out
	}
}

// wallClockEnder ends a ship's harness at the wall clock; its pane stays, so the ship shows anchored.
func wallClockEnder(host *sessionhost.Host) func(session string, minutes int) error {
	return func(session string, minutes int) error {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		return host.EndHarness(ctx, session, "wall_clock:"+strconv.Itoa(minutes), 5*time.Second)
	}
}

// wireEvents routes each source event to the scheduler, and answers an event routine's catalog check from the sources.
// A held ship's dedupe_key is read from the trigger record that launched it.
func wireEvents(sup *sources.Supervisor, store *routines.Store, sched *routines.Scheduler, list func() []ships.Ship, lk *lookout.Lookout, acts *lookout.Acts) {
	sched.Enrich = lk.Enrich
	sched.Acts = acts
	listed := listedShips(list)
	sched.Listed = listed
	sched.Live = routines.EventLive{Listed: listed, Ships: func(fleet string) []routines.LiveShip {
		recs, err := store.Records(fleet)
		if err != nil {
			recs = nil
		}
		return routines.EventShips(fleet, recs, listed())
	}}
	store.Events = func(fleet, source string) ([]routines.EventKind, bool) {
		kinds, ok := sup.Events(fleet, source)
		out := make([]routines.EventKind, len(kinds))
		for i, k := range kinds {
			out[i] = routines.EventKind{Kind: k.Kind, Title: k.Title, Panel: k.Panel}
		}
		return out, ok
	}
	sched.Own = func(_, ref string) bool { return ships.OwnsRef(list(), ref) }
	// one worker keeps events in order and off the source's read loop; a full queue drops the event
	queue := make(chan routines.Event, 256)
	go func() {
		for ev := range queue {
			sched.HandleEvent(ev)
		}
	}()
	sup.OnEvent = func(source string, ev sources.Event) {
		fleet, ok := sup.SourceFleet(source)
		if !ok {
			return
		}
		select {
		case queue <- routines.Event{Fleet: fleet, Source: source, Kind: ev.Kind, Panel: ev.Panel, Ref: ev.Ref, Title: ev.Title,
			Actor: ev.Actor, Author: ev.Author, Viewer: ev.Viewer, Verified: ev.Verified}:
		default:
			log.Printf("source event dropped: queue full (%s %s)", source, ev.Kind)
		}
	}
}

// newLookout builds the Lookout for a fleet's judge harness; the item's text is read through the fleet's GitHub client.
func newLookout(home string, cfg *config.Manager, repos config.RepoLookup, sup *sources.Supervisor) *lookout.Lookout {
	return &lookout.Lookout{
		Choose: func(fleet string) (judge.Choice, error) {
			f, ok := cfg.Current().Fleet(fleet)
			if !ok {
				return judge.Choice{}, errors.New("no such fleet")
			}
			return judge.Choose(f, capabilities.Open(home))
		},
		Limits: func(fleet string) standing.Limits {
			f, _ := cfg.Current().Fleet(fleet)
			return f.Limits
		},
		Repos: repos,
		Item: func(ctx context.Context, ev routines.Event) (string, string, error) {
			u, err := url.Parse(ev.Ref)
			if err != nil {
				return "", "", err
			}
			c, ok := sup.GitHubClient(ev.Fleet, u.Hostname())
			if !ok {
				return "", "", errors.New("no GitHub client for the fleet")
			}
			token, err := c.Token(ctx)
			if err != nil {
				return "", "", err
			}
			return c.ItemText(ctx, token, ev.Ref)
		},
	}
}

// launchEvent runs an event proposal's Launch through the scheduler's gate.
func launchEvent(sched *routines.Scheduler) func(context.Context, inbox.EventProposal) (any, int, string) {
	return func(ctx context.Context, p inbox.EventProposal) (any, int, string) {
		t, err := sched.LaunchProposed(ctx, p.Fleet, p.Routine, p.Trigger)
		switch {
		case errors.Is(err, routines.ErrBusy), errors.Is(err, routines.ErrOff), errors.Is(err, routines.ErrNotIssued):
			return nil, http.StatusConflict, err.Error()
		case errors.Is(err, routines.ErrNotFound), errors.Is(err, routines.ErrExpired):
			return nil, http.StatusGone, err.Error()
		case err != nil:
			return nil, http.StatusBadGateway, err.Error()
		}
		return map[string]any{"session": t.Ship}, 0, ""
	}
}
