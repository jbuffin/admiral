package routines

import (
	"context"
	"errors"
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

// Proposer takes a run that needs the engineer's yes and makes it an Inbox proposal that closes at expires.
type Proposer interface {
	Propose(fleet string, r Routine, t Trigger, expires time.Time) error
}

// Notifier hears each record of a routine whose Notify me is on, for the outcomes that push.
type Notifier interface {
	Notify(fleet string, r Routine, t Trigger)
}

// ErrOff is an Accept for a routine that is switched off; the proposal stays open until it expires.
var ErrOff = errors.New("the routine is switched off")

// ErrBusy is an Accept that found no room, or a ship still holding the routine's key; the proposal stays open.
var ErrBusy = errors.New("the run has no room or its last ship is still here")

const (
	// lateRunWindow is how old a missed run may be and still run once on start.
	lateRunWindow = 12 * time.Hour
	// breakerRun is how many launches in a row may fail or be undone before Before acting drops to Ask first.
	breakerRun = 3
	// watchFor is how long a launched ship is watched for ending failed.
	watchFor = 7 * 24 * time.Hour
	// maxMissedRecords bounds the `missed` records one catch-up writes for a routine that fires every minute.
	maxMissedRecords = 20
	// listGrace is how long a fresh launch may go unlisted before its watch is dropped.
	listGrace = 5 * time.Minute
	idleWake  = time.Hour
)

// Scheduler fires every fleet's enabled routines when their When comes up.
type Scheduler struct {
	Store    *Store
	Clock    Clock
	Ships    Ships
	Chat     Chat
	Proposer Proposer
	Notifier Notifier
	// HostKnown is false while the ships host cannot list ships; a nil one is always known.
	HostKnown func() bool
	Zone      *time.Location // the zone a When is read in; main passes time.Local
	// Ready, when set, closes once the ships host knows the fleet; Run catches up only after it.
	Ready <-chan struct{}

	once  sync.Once
	wake  chan struct{}
	dirty atomic.Bool // a change since the last tick; retries waiting runs

	mu      sync.Mutex // serializes Tick and CatchUp, and guards the maps below
	last    map[string]time.Time
	waiting map[string]waitRun
	watched map[string]watch
	// proposals is each routine's open proposal, so one nobody answered is recorded expired once.
	proposals map[string]openProposal
	// toPropose holds the proposals a tick made; they go to the Proposer after mu is released, because the Inbox
	// takes its own lock and a launch from the Inbox takes mu.
	toPropose []proposeJob
	seeded    bool
}

type proposeJob struct {
	fleet string
	r     Routine
	t     Trigger
	until time.Time
	now   time.Time
}

type openProposal struct {
	fleet, trigger string
	expires        time.Time
}

// waitRun is a run that found no room and is retried when something changes.
type waitRun struct {
	fleet string
	due   time.Time
	why   string
}

type watch struct {
	fleet, routine, session string
	at                      time.Time
	seen                    bool // the ship has been listed holding the routine's key
}

func (s *Scheduler) init() {
	s.once.Do(func() {
		s.wake = make(chan struct{}, 1)
		s.last, s.waiting, s.watched = map[string]time.Time{}, map[string]waitRun{}, map[string]watch{}
		s.proposals = map[string]openProposal{}
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
	if s.Ready != nil {
		select {
		case <-ctx.Done():
			return
		case <-s.Ready:
		}
	}
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
	err := s.catchUp(ctx)
	s.handOff()
	return err
}

func (s *Scheduler) catchUp(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.Clock.Now()
	for _, fr := range s.routinesOf() {
		fr.r = s.trip(fr.fleet, fr.r, now)
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
	err := s.tick(ctx)
	s.handOff()
	return err
}

func (s *Scheduler) tick(ctx context.Context) error {
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
			s.dropWait(r.ID, "the next run came")
			s.last[r.ID] = due
			s.fire(ctx, fr.fleet, r, due, now)
			continue
		}
		if w, ok := s.waiting[r.ID]; ok {
			switch {
			case now.Sub(w.due) >= lateRunWindow:
				s.dropWait(r.ID, "it was 12 hours late")
			case retry:
				s.fire(ctx, fr.fleet, r, w.due, now)
			}
		}
	}
	for id := range s.waiting {
		if !live[id] {
			s.dropWait(id, "the routine was turned off or deleted")
		}
	}
	// after the runs above: an expiry record must not move the baseline a run is counted from
	s.expireProposals(now)
	// after the breaker's count above, so a failure counts from the next tick on
	s.markEnded(now)
	return nil
}

// expireProposals records each open proposal whose time has passed, once.
func (s *Scheduler) expireProposals(now time.Time) {
	for id, p := range s.proposals {
		if now.Before(p.expires) || s.proposing(id) {
			continue
		}
		delete(s.proposals, id)
		s.record(p.fleet, Trigger{Routine: id, Outcome: Skipped, Tier: "T0", At: p.expires,
			Reason: "proposal expired: nobody answered it before it closed"})
	}
}

// dropWait ends a run that was still waiting for room and says so in the log.
func (s *Scheduler) dropWait(id, because string) {
	w, ok := s.waiting[id]
	if !ok {
		return
	}
	delete(s.waiting, id)
	s.record(w.fleet, Trigger{Routine: id, Outcome: Skipped, Tier: "T0", At: s.Clock.Now(),
		Reason: fmt.Sprintf("the run was still waiting (%s) when %s", strings.ToLower(w.why), because)})
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
				s.watched[rec.ID] = watch{fleet: fr.fleet, routine: fr.r.ID, session: rec.Ship, at: rec.At}
			}
		}
	}
}

// markEnded turns the record of every watched launch whose ship ended failed into a failed one.
func (s *Scheduler) markEnded(now time.Time) {
	// while the host cannot list ships every key looks free, which would drop a real watch
	if s.HostKnown != nil && !s.HostKnown() {
		return
	}
	for id, w := range s.watched {
		// session names are reused: only the ship that still holds this routine's key is this launch's
		held, ok := s.Ships.Holder(routineKey(w.routine))
		switch {
		case ok && held.Session == w.session:
			w.seen = true
			s.watched[id] = w
		case ok || w.seen || now.Sub(w.at) > listGrace:
			delete(s.watched, id)
			continue
		default:
			continue // a fresh launch the host has not listed yet
		}
		switch {
		case s.Ships.EndedFailed(w.session):
			if err := s.Store.MarkFailed(w.fleet, id, "the ship ended failed"); err == nil || err == ErrNotFound {
				delete(s.watched, id)
				if err == nil {
					s.notifyEnded(w, id)
				}
			}
		case now.Sub(w.at) > watchFor:
			delete(s.watched, id)
		}
	}
}

// notifyEnded tells the engineer a launched ship ended failed, when its routine asks to be heard.
func (s *Scheduler) notifyEnded(w watch, id string) {
	list, err := s.Store.List(w.fleet)
	if err != nil {
		return
	}
	if i := indexOf(list, w.routine); i >= 0 {
		s.notify(w.fleet, list[i], Trigger{ID: id, Routine: w.routine, Outcome: Failed, Tier: "T0", Ship: w.session,
			At: s.Clock.Now(), Reason: "the ship ended failed"})
	}
}

// notify hands a record to the Notifier when the routine's Notify me is on.
func (s *Scheduler) notify(fleet string, r Routine, t Trigger) {
	switch t.Outcome {
	case Launched, Proposed, Failed, Breaker:
	default:
		return
	}
	if r.Notify && s.Notifier != nil {
		s.Notifier.Notify(fleet, r, t)
	}
}

// recordFor writes a record of routine r and tells the Notifier about it.
func (s *Scheduler) recordFor(fleet string, r Routine, rec Trigger) (Trigger, bool) {
	saved, ok := s.record(fleet, rec)
	if ok {
		s.notify(fleet, r, saved)
	}
	return saved, ok
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
	s.recordFor(fleet, got, Trigger{Routine: r.ID, Outcome: Breaker, Tier: "T0", At: now,
		Reason: fmt.Sprintf("%d launches in a row failed or were undone", run),
		Action: "Before acting changed from Act to Ask first"})
	return got
}

// fire runs one scheduled run of r at its due instant.
func (s *Scheduler) fire(ctx context.Context, fleet string, r Routine, due, now time.Time) {
	rec := Trigger{Routine: r.ID, Tier: "T0", At: now}
	switch r.Before {
	case ReportOnly, AskFirst, Act:
	default:
		// a hand-edited or damaged file must not launch: only an explicit Act does
		delete(s.waiting, r.ID)
		rec.Outcome, rec.Reason = Failed, "before_acting is not valid"
		s.recordFor(fleet, r, rec)
		return
	}
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
		delete(s.waiting, r.ID)
		rec.Outcome = Reported
		s.recordFor(fleet, r, rec)
	case AskFirst:
		delete(s.waiting, r.ID)
		s.propose(fleet, r, rec, now)
	default: // Act
		if l == nil {
			_ = s.prompt(fleet, r, rec)
		} else {
			_ = s.launch(ctx, fleet, r, *l, rec, due, false)
		}
	}
}

// propose records an Ask first run and queues it for the Proposer, which handOff calls once mu is free.
func (s *Scheduler) propose(fleet string, r Routine, rec Trigger, now time.Time) {
	rec.Outcome = Proposed
	var next time.Time
	if sched, err := ParseCron(r.When); err == nil {
		next = sched.Next(now.In(s.zone()))
	}
	saved, ok := s.record(fleet, rec)
	if !ok || s.Proposer == nil {
		return
	}
	s.toPropose = append(s.toPropose, proposeJob{fleet, r, saved, ProposalExpiry(next, now), now})
}

func (s *Scheduler) proposing(id string) bool {
	for _, j := range s.toPropose {
		if j.r.ID == id {
			return true
		}
	}
	return false
}

// handOff gives the queued proposals to the Proposer without holding mu, then closes each routine's previous open one.
func (s *Scheduler) handOff() {
	s.mu.Lock()
	jobs := s.toPropose
	s.toPropose = nil
	s.mu.Unlock()
	for _, j := range jobs {
		if s.Proposer.Propose(j.fleet, j.r, j.t, j.until) != nil {
			continue
		}
		s.mu.Lock()
		if prev, ok := s.proposals[j.r.ID]; ok {
			at := prev.expires
			if at.After(j.now) {
				at = j.now
			}
			s.record(prev.fleet, Trigger{Routine: j.r.ID, Outcome: Skipped, Tier: "T0", At: at,
				Reason: "proposal expired: a newer run replaced it"})
		}
		s.proposals[j.r.ID] = openProposal{j.fleet, j.t.ID, j.until}
		s.mu.Unlock()
		s.notify(j.fleet, j.r, j.t)
	}
}

// Dismiss records that the engineer dismissed a routine's open proposal, so its expiry writes nothing.
func (s *Scheduler) Dismiss(fleet, routineID, triggerID string) {
	s.init()
	s.mu.Lock()
	defer s.mu.Unlock()
	if p, ok := s.proposals[routineID]; ok && p.trigger == triggerID {
		delete(s.proposals, routineID)
		s.record(fleet, Trigger{Routine: routineID, Outcome: Skipped, Tier: "T0", At: s.Clock.Now(),
			Reason: "proposal dismissed by the engineer"})
	}
}

func routineKey(id string) string { return "routine:" + id }

func (s *Scheduler) record(fleet string, rec Trigger) (Trigger, bool) {
	saved, err := s.Store.AppendTrigger(fleet, rec)
	return saved, err == nil
}

func (s *Scheduler) prompt(fleet string, r Routine, rec Trigger) error {
	delete(s.waiting, r.ID)
	err := s.Chat.Send(fleet, "[Routine: "+r.Name+"] "+r.Do.Prompt)
	if err != nil {
		rec.Outcome, rec.Reason = Failed, err.Error()
	} else {
		rec.Outcome = Launched
	}
	s.recordFor(fleet, r, rec)
	return err
}

// launch runs the fire-time gates and starts the ship. A run that finds no room waits, unless it is an accepted proposal: that refuses with ErrBusy.
func (s *Scheduler) launch(ctx context.Context, fleet string, r Routine, l Launch, rec Trigger, due time.Time, accepted bool) error {
	fail := func(reason string) error {
		delete(s.waiting, r.ID)
		rec.Outcome, rec.Reason = Failed, reason
		s.recordFor(fleet, r, rec)
		return errors.New(reason)
	}
	f, ok := s.Store.Fleets(fleet)
	if !ok {
		return fail("the fleet is gone")
	}
	// the gates are checked again now: a guard probe or the allowlist may have changed since the routine was saved
	if err := autopilotgate.Check(s.Store.Home, f, l.Harness); err != nil {
		return fail(err.Error())
	}
	dock, ok := f.Harness(l.Harness)
	if !ok {
		return fail(fmt.Sprintf("fleet %s has no %s dock", fleet, l.Harness))
	}
	if err := launcher.AutopilotArgs(l.Harness, dock.Args, l.Args); err != nil {
		return fail("args are off the allowlist: " + err.Error())
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
	case lerr != nil && lerr.Wait != "" && accepted:
		return ErrBusy
	case lerr != nil && lerr.Wait != "":
		s.waiting[r.ID] = waitRun{fleet, due, lerr.Wait}
		return nil
	case lerr != nil:
		return fail(lerr.Msg)
	}
	delete(s.waiting, r.ID)
	rec.ID, rec.Outcome, rec.Ship = id, Launched, ref.Session
	if saved, ok := s.recordFor(fleet, r, rec); ok && ref.Session != "" {
		s.watched[saved.ID] = watch{fleet: fleet, routine: r.ID, session: ref.Session, at: rec.At}
	}
	return nil
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

// Accept launches the run a proposal asked for, through the gates of a scheduled launch, and never waits.
func (s *Scheduler) Accept(ctx context.Context, fleet, routineID, triggerID string) (Trigger, error) {
	s.init()
	s.mu.Lock()
	defer s.mu.Unlock()
	list, err := s.Store.List(fleet)
	if err != nil {
		return Trigger{}, err
	}
	i := indexOf(list, routineID)
	if i < 0 {
		return Trigger{}, ErrNotFound
	}
	r := list[i]
	if !r.Enabled {
		return Trigger{}, ErrOff
	}
	now := s.Clock.Now()
	rec := Trigger{Routine: r.ID, Tier: "T0", At: now, Via: ViaProposal, Action: describe(r)}
	if l := r.Do.Launch; l != nil {
		if _, held := s.Ships.Holder(routineKey(r.ID)); held {
			return Trigger{}, ErrBusy
		}
		if err := s.launch(ctx, fleet, r, *l, rec, now, true); err != nil {
			return Trigger{}, err
		}
	} else if err := s.prompt(fleet, r, rec); err != nil {
		return Trigger{}, err
	}
	if p, ok := s.proposals[r.ID]; ok && p.trigger == triggerID {
		delete(s.proposals, r.ID)
	}
	recs, err := s.Store.Recent(fleet, r.ID, 1)
	if err != nil || len(recs) == 0 {
		return Trigger{}, err
	}
	return recs[0], nil
}
