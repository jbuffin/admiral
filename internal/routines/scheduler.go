package routines

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/jbuffin/admiral/internal/autopilotgate"
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

const (
	// lateRunWindow is how old a missed run may be and still run once on start.
	lateRunWindow = 12 * time.Hour
	// breakerRun is how many launches in a row may fail or be undone before Before acting drops to Ask first.
	breakerRun = 3
	// watchFor is how long a launched ship is watched for ending failed.
	watchFor = 7 * 24 * time.Hour
	// maxMissedRecords bounds the `missed` records one catch-up writes for a routine that fires every minute.
	maxMissedRecords = 20
	idleWake         = time.Hour
)

// Scheduler fires every fleet's enabled routines when their When comes up.
type Scheduler struct {
	Store    *Store
	Clock    Clock
	Ships    Ships
	Chat     Chat
	Proposer Proposer
	Zone     *time.Location // the zone a When is read in; main passes time.Local

	once  sync.Once
	wake  chan struct{}
	dirty atomic.Bool // a change since the last tick; retries waiting runs

	mu      sync.Mutex // serializes Tick and CatchUp, and guards the maps below
	last    map[string]time.Time
	waiting map[string]time.Time
	watched map[string]watch
	seeded  bool
}

type watch struct {
	fleet, routine, session string
	at                      time.Time
}

func (s *Scheduler) init() {
	s.once.Do(func() {
		s.wake = make(chan struct{}, 1)
		s.last, s.waiting, s.watched = map[string]time.Time{}, map[string]time.Time{}, map[string]watch{}
	})
}

func (s *Scheduler) zone() *time.Location {
	if s.Zone != nil {
		return s.Zone
	}
	return time.Local
}

// Changed wakes the scheduler: a routine was saved, deleted or switched, or a ship or resource changed.
func (s *Scheduler) Changed() {
	s.init()
	s.dirty.Store(true)
	select {
	case s.wake <- struct{}{}:
	default:
	}
}

// Run catches up missed runs, then ticks until ctx ends, sleeping until the earliest next run or a Changed.
func (s *Scheduler) Run(ctx context.Context) {
	s.init()
	_ = s.CatchUp(ctx)
	for ctx.Err() == nil {
		d := idleWake
		if next := s.nextDue(); !next.IsZero() {
			d = next.Sub(s.Clock.Now())
		}
		select {
		case <-ctx.Done():
			return
		case <-s.wake:
		case <-s.Clock.After(d):
		}
		if ctx.Err() == nil {
			_ = s.Tick(ctx)
		}
	}
}

// routinesOf lists every fleet's enabled routines that have a readable When.
func (s *Scheduler) routinesOf() []fleetRoutine {
	var out []fleetRoutine
	for _, key := range s.Store.FleetKeys() {
		list, err := s.Store.List(key)
		if err != nil {
			continue
		}
		for _, r := range list {
			if r.Enabled {
				out = append(out, fleetRoutine{key, r})
			}
		}
	}
	return out
}

type fleetRoutine struct {
	fleet string
	r     Routine
}

// baseline is the instant a routine's next run is counted from: its latest record, or its creation, or its last save.
func (s *Scheduler) baseline(fleet string, r Routine, now time.Time, withMemory bool) time.Time {
	base := r.CreatedAt
	if recs, err := s.Store.Recent(fleet, r.ID, 0); err == nil {
		var latest time.Time
		for _, rec := range recs {
			// a breaker record marks a setting change, not a run
			if rec.Outcome != Breaker && rec.At.After(latest) {
				latest = rec.At
			}
		}
		if !latest.IsZero() {
			base = latest
		}
	}
	// a save in the future can only be a clock that went back; it doesn't move the baseline
	if !r.UpdatedAt.After(now) && r.UpdatedAt.After(base) {
		base = r.UpdatedAt
	}
	if l, ok := s.last[r.ID]; withMemory && ok && l.After(base) {
		base = l
	}
	return base
}

// nextDue is the earliest next run across every enabled routine, or zero.
func (s *Scheduler) nextDue() time.Time {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.Clock.Now()
	var best time.Time
	for _, fr := range s.routinesOf() {
		sched, err := ParseCron(fr.r.When)
		if err != nil {
			continue
		}
		next := sched.Next(s.baseline(fr.fleet, fr.r, now, true).In(s.zone()))
		if !next.IsZero() && (best.IsZero() || next.Before(best)) {
			best = next
		}
	}
	return best
}

// runsBetween is every run of sched after base up to and including now, oldest first; the newest are kept when there are very many.
func runsBetween(sched Schedule, base, now time.Time) []time.Time {
	var runs []time.Time
	for t := sched.Next(base); !t.IsZero() && !t.After(now); t = sched.Next(t) {
		runs = append(runs, t)
		if len(runs) > 5000 {
			runs = runs[len(runs)-2500:]
		}
	}
	return runs
}

// CatchUp handles the runs missed while the server was down: one late run under 12 hours old, `missed` records for the rest.
func (s *Scheduler) CatchUp(ctx context.Context) error {
	s.init()
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.Clock.Now()
	for _, fr := range s.routinesOf() {
		sched, err := ParseCron(fr.r.When)
		if err != nil {
			continue
		}
		runs := runsBetween(sched, s.baseline(fr.fleet, fr.r, now, false).In(s.zone()), now)
		if len(runs) == 0 {
			continue
		}
		latest := runs[len(runs)-1]
		older := runs[:len(runs)-1]
		if now.Sub(latest) >= lateRunWindow {
			older = runs
		}
		if len(older) > maxMissedRecords {
			older = older[len(older)-maxMissedRecords:]
		}
		for _, run := range older {
			if _, err := s.Store.AppendTrigger(fr.fleet, Trigger{Routine: fr.r.ID, Outcome: Missed, Tier: "T0", At: run,
				Reason: "Admiral was not running at " + run.In(s.zone()).Format("2006-01-02 15:04")}); err != nil {
				return err
			}
		}
		s.last[fr.r.ID] = latest
		if now.Sub(latest) < lateRunWindow {
			s.fire(ctx, fr.fleet, fr.r, latest, now)
		}
	}
	return nil
}

// Tick does everything due at Clock.Now(): routines whose time came, waiting runs, and the breaker's count.
func (s *Scheduler) Tick(ctx context.Context) error {
	s.init()
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.Clock.Now()
	retry := s.dirty.Swap(false)
	s.seedWatch(now)
	live := map[string]bool{}
	for _, fr := range s.routinesOf() {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		live[fr.r.ID] = true
		r := s.trip(fr.fleet, fr.r, now)
		sched, err := ParseCron(r.When)
		if err != nil {
			continue
		}
		base := s.baseline(fr.fleet, r, now, true).In(s.zone())
		if due := sched.Next(base); !due.IsZero() && !due.After(now) {
			// every run that came due since the last tick is one run: the newest
			for n := sched.Next(due); !n.IsZero() && !n.After(now); n = sched.Next(due) {
				due = n
			}
			delete(s.waiting, r.ID)
			s.last[r.ID] = due
			s.fire(ctx, fr.fleet, r, due, now)
			continue
		}
		if run, ok := s.waiting[r.ID]; ok && retry {
			s.fire(ctx, fr.fleet, r, run, now)
		}
	}
	for id := range s.waiting {
		if !live[id] {
			delete(s.waiting, id)
		}
	}
	// after the breaker's count above, so a failure counts from the next tick on
	s.markEnded(now)
	return nil
}

// seedWatch starts watching the ships of recent launches the log already holds, once, so a restart doesn't lose them.
func (s *Scheduler) seedWatch(now time.Time) {
	if s.seeded {
		return
	}
	s.seeded = true
	for _, fr := range s.routinesOf() {
		recs, err := s.Store.Recent(fr.fleet, fr.r.ID, 50)
		if err != nil {
			continue
		}
		for _, rec := range recs {
			if rec.Outcome == Launched && rec.Ship != "" && !rec.Undone && now.Sub(rec.At) < watchFor {
				s.watched[rec.ID] = watch{fr.fleet, fr.r.ID, rec.Ship, rec.At}
			}
		}
	}
}

// markEnded turns the record of every watched launch whose ship ended failed into a failed one.
func (s *Scheduler) markEnded(now time.Time) {
	for id, w := range s.watched {
		switch {
		case s.Ships.EndedFailed(w.session):
			if err := s.Store.MarkFailed(w.fleet, id, "the ship ended failed"); err == nil || err == ErrNotFound {
				delete(s.watched, id)
			}
		case now.Sub(w.at) > watchFor:
			delete(s.watched, id)
		}
	}
}

// trip sets Before acting to Ask first when the last launches in a row all failed or were undone.
func (s *Scheduler) trip(fleet string, r Routine, now time.Time) Routine {
	if r.Before != Act {
		return r
	}
	recs, err := s.Store.Recent(fleet, r.ID, 0)
	if err != nil {
		return r
	}
	run := 0
count:
	for _, rec := range recs {
		switch {
		case rec.Outcome == Failed, rec.Outcome == Launched && rec.Undone:
			run++
		case rec.Outcome == Launched, rec.Outcome == Breaker:
			break count
		}
	}
	if run < breakerRun {
		return r
	}
	got, err := s.Store.SetBefore(fleet, r.ID, AskFirst)
	if err != nil {
		return r
	}
	_, _ = s.Store.AppendTrigger(fleet, Trigger{Routine: r.ID, Outcome: Breaker, Tier: "T0", At: now,
		Reason: fmt.Sprintf("%d launches in a row failed or were undone", run),
		Action: "Before acting changed from Act to Ask first"})
	return got
}

// fire runs one scheduled run of r at its due instant.
func (s *Scheduler) fire(ctx context.Context, fleet string, r Routine, due, now time.Time) {
	rec := Trigger{Routine: r.ID, Tier: "T0", At: now}
	l := r.Do.Launch
	key := routineKey(r.ID)
	if l != nil {
		if held, ok := s.Ships.Holder(key); ok {
			rec.Outcome, rec.Reason = Skipped, fmt.Sprintf("the last run's ship %s is still here (%s)", held.Session, held.Status)
			s.record(fleet, rec)
			delete(s.waiting, r.ID)
			return
		}
	}
	holdOn, err := s.Store.HoldOn(fleet)
	if err != nil {
		holdOn = true
	}
	rec.Action = describe(r)
	switch Effective(r.Before, holdOn) {
	case ReportOnly:
		rec.Outcome = Reported
		s.record(fleet, rec)
	case AskFirst:
		rec.Outcome = Proposed
		if saved, ok := s.record(fleet, rec); ok && s.Proposer != nil {
			_ = s.Proposer.Propose(fleet, r, saved)
		}
	default:
		if l == nil {
			s.prompt(fleet, r, rec)
		} else {
			s.launch(ctx, fleet, r, *l, rec, due)
		}
	}
}

func routineKey(id string) string { return "routine:" + id }

func (s *Scheduler) record(fleet string, rec Trigger) (Trigger, bool) {
	saved, err := s.Store.AppendTrigger(fleet, rec)
	return saved, err == nil
}

func (s *Scheduler) prompt(fleet string, r Routine, rec Trigger) {
	delete(s.waiting, r.ID)
	if err := s.Chat.Send(fleet, "[Routine: "+r.Name+"] "+r.Do.Prompt); err != nil {
		rec.Outcome, rec.Reason = Failed, err.Error()
	} else {
		rec.Outcome = Launched
	}
	s.record(fleet, rec)
}

func (s *Scheduler) launch(ctx context.Context, fleet string, r Routine, l Launch, rec Trigger, due time.Time) {
	fail := func(reason string) {
		delete(s.waiting, r.ID)
		rec.Outcome, rec.Reason = Failed, reason
		s.record(fleet, rec)
	}
	f, ok := s.Store.Fleets(fleet)
	if !ok {
		fail("the fleet is gone")
		return
	}
	// the gates are checked again now: a guard probe or the allowlist may have changed since the routine was saved
	if err := autopilotgate.Check(s.Store.Home, f, l.Harness); err != nil {
		fail(err.Error())
		return
	}
	dock, ok := f.Harness(l.Harness)
	if !ok {
		fail(fmt.Sprintf("fleet %s has no %s dock", fleet, l.Harness))
		return
	}
	if err := launcher.AutopilotArgs(l.Harness, dock.Args, l.Args); err != nil {
		fail("args are off the allowlist: " + err.Error())
		return
	}
	skill := l.Skill
	if skill.Kind == "" {
		skill.Kind = "none"
	}
	id := newID()
	ref, lerr := s.Ships.Launch(ctx, launcher.Request{
		Fleet: fleet, Harness: l.Harness, Dir: l.Dir, Text: l.Text, Model: l.Model, Effort: l.Effort,
		Skill: skill, Resources: l.Resources, ExtraArgs: l.Args, Autopilot: true,
		RoutineKey: routineKey(r.ID), TriggerID: id,
	})
	switch {
	case lerr != nil && lerr.Wait != "":
		s.waiting[r.ID] = due
		return
	case lerr != nil:
		fail(lerr.Msg)
		return
	}
	delete(s.waiting, r.ID)
	rec.ID, rec.Outcome, rec.Ship = id, Launched, ref.Session
	if saved, ok := s.record(fleet, rec); ok && ref.Session != "" {
		s.watched[saved.ID] = watch{fleet, r.ID, ref.Session, rec.At}
	}
}

// describe says what a run would do, for a record's Action.
func describe(r Routine) string {
	if l := r.Do.Launch; l != nil {
		text := strings.Join(strings.Fields(l.Text), " ")
		if len(text) > 80 {
			text = text[:80] + "…"
		}
		return fmt.Sprintf("launch %s in %s: %s", l.Harness, l.Dir, text)
	}
	return "send AdmiralBot a prompt"
}
