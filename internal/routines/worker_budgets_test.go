// The worker cap counts live ships only, and the wall clock stops a worker ship once, on the scheduler's clock.
package routines_test

import (
	"context"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jbuffin/admiral/internal/launcher"
	"github.com/jbuffin/admiral/internal/routines"
	"github.com/jbuffin/admiral/internal/standing"
)

type endCall struct {
	session string
	minutes int
}

// budgetShips is the host's ship list as the launches make it: each launch is listed at the clock's time with the status a test sets.
type budgetShips struct {
	mu       sync.Mutex
	h        *harness
	launched map[string]time.Time
	status   map[string]string
	extra    []routines.ListedShip
	ends     []endCall
	visible  int // the host lists a launch only once the event that made it is done
}

// fire sends the event.
func (b *budgetShips) fire() {
	b.h.sched.HandleEvent(evKeyless())
	b.mu.Lock()
	b.visible = len(b.h.ships.requests())
	b.mu.Unlock()
}

func (b *budgetShips) setStatus(session, status string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.status[session] = status
}

func (b *budgetShips) endCalls() []endCall {
	b.mu.Lock()
	defer b.mu.Unlock()
	return append([]endCall(nil), b.ends...)
}

func (b *budgetShips) end(session string, minutes int) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.ends = append(b.ends, endCall{session, minutes})
	return nil
}

func (b *budgetShips) list() []routines.ListedShip {
	var out []routines.ListedShip
	b.mu.Lock()
	visible := b.visible
	b.mu.Unlock()
	for i, req := range b.h.ships.requests() {
		if i >= visible {
			break
		}
		session := "ship-" + strconv.Itoa(i+1)
		b.mu.Lock()
		st, at := b.status[session], b.launched[session]
		b.mu.Unlock()
		if st == "" {
			st = "underway"
		}
		out = append(out, routines.ListedShip{Session: session, Fleet: "work", Routine: req.RoutineKey, Launched: at.UTC().Format(time.RFC3339), Status: st})
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	return append(out, b.extra...)
}

// budgetRun is a fleet of A4 event routines at the given cap and wall clock, over a ship list that follows the launches.
func budgetRun(t *testing.T, concurrent, wallClock, nRoutines int) (*harness, []routines.Routine, *budgetShips) {
	t.Helper()
	h := newHarness(t)
	l := standing.PresetLimits(standing.Home)
	l.Ceilings.T1 = standing.Ceiling{Level: standing.A4}
	l.Rates = standing.Rates{LaunchesPerHour: 100, LaunchesPerSenderPerDay: 100, Concurrent: concurrent}
	l.WallClockMinutes = wallClock
	h.withEvents(l)
	h.proveRestricted("2.1.300", true, true)
	var rs []routines.Routine
	for i := 0; i < nRoutines; i++ {
		rs = append(rs, h.onRoutine(routines.Act, func(r *routines.Routine) { r.Name = "Event sweep " + strconv.Itoa(i) }))
	}
	h.sched.Enrich = func(routines.Event) routines.Enrichment {
		return routines.Enrichment{Text: evText, Summary: "Eng asks for a review."}
	}
	b := &budgetShips{h: h, launched: map[string]time.Time{}, status: map[string]string{}}
	h.ships.launch = func(n int, _ launcher.Request) (routines.ShipRef, *launcher.Error) {
		session := "ship-" + strconv.Itoa(n)
		b.mu.Lock()
		b.launched[session] = h.clock.Now()
		b.mu.Unlock()
		return routines.ShipRef{Session: session, Status: "underway"}, nil
	}
	h.sched.Live = routines.EventLive{Listed: b.list}
	h.sched.Listed = b.list
	h.sched.EndHarness = b.end
	return h, rs, b
}

// evKeyless is an event whose ref carries no dedupe key, so only the caps decide.
func evKeyless() routines.Event {
	ev := evFrom("Eng")
	ev.Ref = "https://tracker.example.test/items/7"
	return ev
}

// outcomes counts the launched and the concurrent-demoted records over the routines.
func (h *harness) outcomes(rs []routines.Routine) (launched int, demoted []routines.Trigger) {
	h.t.Helper()
	for _, r := range rs {
		for _, rec := range h.records(r) {
			switch {
			case rec.Outcome == routines.Launched:
				launched++
			case rec.Outcome == routines.Proposed && strings.Contains(rec.Reason, "concurrent"):
				demoted = append(demoted, rec)
			}
		}
	}
	return launched, demoted
}

func TestOnlyUnderwayAndNeedsYouWorkerShipsHoldAWorkerSlot(t *testing.T) {
	for _, c := range []struct {
		first        string // the first ship's status when the second event comes
		wantLaunched int
	}{
		{"underway", 1},
		{"needs_you", 1},
		{"anchored", 2},
		{"sailing_off", 2},
		{"moored", 2},
	} {
		t.Run(c.first, func(t *testing.T) {
			h, rs, b := budgetRun(t, 1, 60, 1)
			b.fire()
			b.setStatus("ship-1", c.first)
			b.fire()
			launched, demoted := h.outcomes(rs)
			if launched != c.wantLaunched {
				t.Fatalf("%d launched with the first ship %s and concurrent 1, want %d; demoted: %d", launched, c.first, c.wantLaunched, len(demoted))
			}
			if wantDemoted := 2 - c.wantLaunched; len(demoted) != wantDemoted {
				t.Errorf("%d events demoted to a proposal for concurrent, want %d", len(demoted), wantDemoted)
			}
		})
	}
}

func TestTheCapLaunchesUpToItAndProposesTheRestWithTheConcurrentReason(t *testing.T) {
	h, rs, b := budgetRun(t, 2, 60, 3)
	b.fire()
	launched, demoted := h.outcomes(rs)
	if launched != 2 || len(demoted) != 1 {
		t.Fatalf("%d launched and %d proposed for concurrent, want 2 and 1 under a cap of 2", launched, len(demoted))
	}
}

func TestAWorkerShipPastTheWallClockIsEndedOnceAndItsRecordFailsWithTheReason(t *testing.T) {
	h, rs, b := budgetRun(t, 100, 60, 1)
	b.fire()
	h.wantLaunches(1)

	h.tickAt(day0.Add(59 * time.Minute))
	if got := b.endCalls(); len(got) != 0 {
		t.Fatalf("ended %v at 59 minutes of 60, want nothing", got)
	}
	h.tickAt(day0.Add(61 * time.Minute))
	if got := b.endCalls(); len(got) != 1 || got[0] != (endCall{"ship-1", 60}) {
		t.Fatalf("ended %v at 61 minutes, want ship-1 once with the 60-minute limit", got)
	}
	rec := h.latest(rs[0])
	if rec.Outcome != routines.Failed || rec.Reason != "stopped at the 60-minute wall clock" {
		t.Errorf("record = %s %q, want failed with %q", rec.Outcome, rec.Reason, "stopped at the 60-minute wall clock")
	}
	// the listed ship is still underway here, as it is until the host reports the dead pane
	h.tickAt(day0.Add(62 * time.Minute))
	h.tickAt(day0.Add(90 * time.Minute))
	if got := b.endCalls(); len(got) != 1 {
		t.Errorf("ended %v after later ticks, want the one call", got)
	}
}

func TestTheWallClockReadsTheFleetsOwnLimit(t *testing.T) {
	h, _, b := budgetRun(t, 100, 30, 1)
	b.fire()
	h.tickAt(day0.Add(29 * time.Minute))
	if got := b.endCalls(); len(got) != 0 {
		t.Fatalf("ended %v at 29 minutes of 30, want nothing", got)
	}
	h.tickAt(day0.Add(31 * time.Minute))
	if got := b.endCalls(); len(got) != 1 || got[0] != (endCall{"ship-1", 30}) {
		t.Errorf("ended %v at 31 minutes, want ship-1 with the 30-minute limit", got)
	}
}

func TestTheWallClockLeavesScheduledHandAndPromotedShipsAlone(t *testing.T) {
	h, _, b := budgetRun(t, 100, 60, 1)
	h.act(nil) // a scheduled routine, due 09:00
	h.tickAt(time.Date(2026, 11, 12, 9, 0, 0, 0, time.UTC))
	h.wantLaunches(1) // the scheduled run's ship-1
	b.fire()
	h.wantLaunches(2) // the worker ship-2, launched at the same minute
	at := h.clock.Now().UTC().Format(time.RFC3339)
	b.mu.Lock()
	b.extra = []routines.ListedShip{
		{Session: "hand-1", Fleet: "work", Launched: at, Status: "underway"},
		{Session: "swift-heron", Fleet: "work", Launched: at, Status: "underway"}, // a promoted ship has no routine key
	}
	b.mu.Unlock()

	h.tickAt(h.clock.Now().Add(61 * time.Minute))
	if got := b.endCalls(); len(got) != 1 || got[0].session != "ship-2" {
		t.Errorf("ended %v at 61 minutes, want only the worker ship ship-2 (not the scheduled ship-1, hand-1 or the promoted swift-heron)", got)
	}
}

func TestAnEventOnlyFleetsShipIsEndedByTheRunLoopAtItsDeadlineNotAtTheIdleWake(t *testing.T) {
	h, _, b := budgetRun(t, 100, 20, 1)
	b.fire() // no scheduled routine, so nothing else is due and the loop would sleep an hour
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { h.sched.Run(ctx); close(done) }()
	t.Cleanup(func() { cancel(); <-done })

	eventually(t, "the run loop to end the ship once its 20 minutes pass", func() bool {
		h.clock.set(day0.Add(21 * time.Minute))
		return len(b.endCalls()) > 0
	})
	if got := b.endCalls(); len(got) != 1 || got[0] != (endCall{"ship-1", 20}) {
		t.Errorf("ended %v, want ship-1 once with the 20-minute limit", got)
	}
}

// promoteCounts launches a record three hours ago, promotes it madeAgo before now, and reads the fleet's counts at now.
func promoteCounts(t *testing.T, outcome routines.Outcome, madeAgo time.Duration) (standing.Counts, routines.Trigger) {
	t.Helper()
	r := newRig(t)
	now := r.now
	rec, err := r.store.AppendTrigger("work", routines.Trigger{Routine: "ev", Outcome: outcome, Tier: "T1", Sender: "bob", Event: "https://x/1",
		At: now.Add(-3 * time.Hour), Ship: "swift-heron", WorkerLaunched: true})
	if err != nil {
		t.Fatal(err)
	}
	r.now = now.Add(-madeAgo)
	if err := r.store.Promote("work", rec.ID, "calm-otter"); err != nil {
		t.Fatal(err)
	}
	got, err := r.store.CountsFor("work", "bob", now, time.UTC)
	if err != nil {
		t.Fatal(err)
	}
	recs, err := r.store.Records("work")
	if err != nil || len(recs) != 1 {
		t.Fatalf("records = %+v, %v, want the one", recs, err)
	}
	return got, recs[0]
}

func TestAPromoteCountsTowardTheHourFromWhenItWasMadeWhateverTheRecordsOutcome(t *testing.T) {
	for _, outcome := range []routines.Outcome{routines.Launched, routines.Failed} {
		t.Run(string(outcome), func(t *testing.T) {
			got, rec := promoteCounts(t, outcome, 30*time.Minute)
			if got.LaunchesLastHour != 1 {
				t.Errorf("LaunchesLastHour = %d for a %s record promoted 30 minutes ago, want 1", got.LaunchesLastHour, outcome)
			}
			wantSender := 0 // only the original launch is the sender's; the promote adds none
			if outcome == routines.Launched {
				wantSender = 1
			}
			if got.SenderLaunchesToday != wantSender || got.WorkerShips != 0 {
				t.Errorf("counts = %+v, want the sender count %d and no worker ships: a promote is not the sender's launch", got, wantSender)
			}
			if rec.PromotedAt.IsZero() {
				t.Errorf("the record carries no promoted_at, so the hour cannot be counted from it: %+v", rec)
			}
		})
	}
}

func TestAPromoteMadeMoreThanAnHourAgoNoLongerCounts(t *testing.T) {
	got, _ := promoteCounts(t, routines.Launched, 2*time.Hour)
	if got.LaunchesLastHour != 0 {
		t.Errorf("LaunchesLastHour = %d for a promote made 2 hours ago, want 0", got.LaunchesLastHour)
	}
}
