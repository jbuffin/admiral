package routines_test

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jbuffin/admiral/internal/inbox"
	"github.com/jbuffin/admiral/internal/launcher"
	"github.com/jbuffin/admiral/internal/routines"
	"github.com/jbuffin/admiral/internal/server"
)

type notice struct {
	fleet, routine string
	outcome        routines.Outcome
}

type fakeNotifier struct {
	mu  sync.Mutex
	got []notice
}

func (f *fakeNotifier) Notify(fleet string, r routines.Routine, t routines.Trigger) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.got = append(f.got, notice{fleet, r.ID, t.Outcome})
}

func (f *fakeNotifier) outcomes() []routines.Outcome {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []routines.Outcome
	for _, n := range f.got {
		out = append(out, n.outcome)
	}
	return out
}

// proposed saves a daily 09:00 Ask first routine and runs it once, so one proposal is open.
func (h *harness) proposed(mod func(*routines.Routine)) (routines.Routine, routines.Trigger) {
	h.t.Helper()
	r := h.act(func(r *routines.Routine) {
		r.Before = routines.AskFirst
		if mod != nil {
			mod(r)
		}
	})
	h.tickAt(at(0, 9, 0))
	got := h.proposer.proposals()
	if len(got) != 1 {
		h.t.Fatalf("%d proposals after the first run, want 1", len(got))
	}
	return r, got[0].trigger
}

func TestAProposalClosesAtTheNextRunOrAfter24HoursWhicheverComesFirst(t *testing.T) {
	now := at(0, 9, 0)
	if got := routines.ProposalExpiry(now.Add(2*time.Hour), now); !got.Equal(now.Add(2 * time.Hour)) {
		t.Errorf("a run in 2 hours: expiry = %v, want that run", got)
	}
	if got := routines.ProposalExpiry(now.Add(48*time.Hour), now); !got.Equal(now.Add(24 * time.Hour)) {
		t.Errorf("a run in 48 hours: expiry = %v, want 24 hours out", got)
	}
	if got := routines.ProposalExpiry(time.Time{}, now); !got.Equal(now.Add(24 * time.Hour)) {
		t.Errorf("no next run: expiry = %v, want 24 hours out", got)
	}
}

func TestEachAskFirstRunHandsTheProposerOneProposalWithItsExpiry(t *testing.T) {
	cases := map[string]struct {
		when string
		want time.Time
	}{
		"the next run is two hours away": {"0 9,11 * * *", at(0, 11, 0)},
		"the next run is a month away":   {"0 9 12 * *", at(1, 9, 0)},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			h := newHarness(t)
			r, trig := h.proposed(func(r *routines.Routine) { r.When = c.when })
			got := h.proposer.proposals()[0]
			if got.fleet != "work" || got.routineID != r.ID || trig.Outcome != routines.Proposed || trig.ID == "" {
				t.Errorf("proposal = %+v, want the fleet, the routine and a proposed record with an id", got)
			}
			if !got.expires.Equal(c.want) {
				t.Errorf("expires = %v, want %v", got.expires, c.want)
			}
		})
	}
}

func TestHoldFireTurnsAScheduledLaunchIntoOneProposalAndTurningItOffLeavesTheProposalOpen(t *testing.T) {
	h := newHarness(t)
	r := h.act(nil)
	if _, err := h.store.SetHold("work", true, routines.Engineer); err != nil {
		t.Fatal(err)
	}
	h.tickAt(at(0, 9, 0))
	h.wantLaunches(0)
	got := h.proposer.proposals()
	if len(got) != 1 || !got[0].expires.Equal(at(1, 9, 0)) {
		t.Fatalf("proposals = %+v, want one that closes at the next run", got)
	}
	if _, err := h.store.SetHold("work", false, routines.Engineer); err != nil {
		t.Fatal(err)
	}
	h.tickAt(at(0, 9, 30))
	h.wantLaunches(0)
	if n := len(h.proposer.proposals()); n != 1 {
		t.Errorf("%d proposals after Hold fire went off, want the same one", n)
	}
	if _, err := h.sched.Accept(context.Background(), "work", r.ID, got[0].trigger.ID); err != nil {
		t.Fatalf("Accept after Hold fire went off: %v", err)
	}
	h.wantLaunches(1)
}

func TestAcceptLaunchesOnceThroughTheGatesUnderTheRoutinesKeyAndRecordsItCameFromAProposal(t *testing.T) {
	h := newHarness(t)
	r, trig := h.proposed(nil)
	h.clock.set(at(0, 9, 30))
	rec, err := h.sched.Accept(context.Background(), "work", r.ID, trig.ID)
	if err != nil {
		t.Fatalf("Accept: %v", err)
	}
	h.wantLaunches(1)
	req := h.ships.requests()[0]
	if !req.Autopilot || req.RoutineKey != "routine:"+r.ID || req.Fleet != "work" || req.Harness != "claude" || req.Text != "sweep the board" {
		t.Errorf("launch request = %+v, want an autopilot launch of the routine's launch under routine:%s", req, r.ID)
	}
	if len(req.ExtraArgs) != 2 || req.ExtraArgs[0] != "--effort" {
		t.Errorf("ExtraArgs = %v, want the routine's args", req.ExtraArgs)
	}
	if rec.Outcome != routines.Launched || rec.Via != routines.ViaProposal || rec.Ship != "ship-1" || rec.ID == "" || req.TriggerID != rec.ID {
		t.Errorf("record = %+v with trigger %q, want a launched record from a proposal for ship-1 whose id the launch carried", rec, req.TriggerID)
	}
	if got := h.ofOutcome(r, routines.Launched); len(got) != 1 || got[0].Via != routines.ViaProposal {
		t.Errorf("launched records = %+v, want one marked as from a proposal", got)
	}
}

func TestAcceptLaunchesEvenWhileHoldFireIsOnBecauseTheEngineerSaidYes(t *testing.T) {
	h := newHarness(t)
	r, trig := h.proposed(nil)
	if _, err := h.store.SetHold("", true, routines.Engineer); err != nil {
		t.Fatal(err)
	}
	if _, err := h.sched.Accept(context.Background(), "work", r.ID, trig.ID); err != nil {
		t.Fatalf("Accept under Hold fire: %v", err)
	}
	h.wantLaunches(1)
}

func TestAcceptRefusesWithErrBusyAndNeverWaitsWhenAShipHoldsTheKeyOrThereIsNoRoom(t *testing.T) {
	cases := map[string]func(h *harness, r routines.Routine){
		"a ship holds the key": func(h *harness, r routines.Routine) {
			h.ships.set(func() {
				h.ships.holders = map[string]routines.ShipRef{"routine:" + r.ID: {Session: "ship-9", Status: "underway"}}
			})
		},
		"no ship slot is free": func(h *harness, _ routines.Routine) {
			h.ships.set(func() {
				h.ships.launch = func(int, launcher.Request) (routines.ShipRef, *launcher.Error) {
					return routines.ShipRef{}, &launcher.Error{Status: http.StatusConflict, Msg: launcher.AutopilotCapText, Wait: "Waiting for a free ship slot"}
				}
			})
		},
	}
	for name, block := range cases {
		t.Run(name, func(t *testing.T) {
			h := newHarness(t)
			r, trig := h.proposed(nil)
			block(h, r)
			before := len(h.ships.requests())
			if _, err := h.sched.Accept(context.Background(), "work", r.ID, trig.ID); !errors.Is(err, routines.ErrBusy) {
				t.Fatalf("Accept = %v, want ErrBusy", err)
			}
			h.ships.set(func() { h.ships.holders, h.ships.launch = nil, nil })
			launchesAfterRefusal := len(h.ships.requests())
			h.sched.Changed()
			h.tickAt(at(0, 9, 5))
			if n := len(h.ships.requests()); n != launchesAfterRefusal {
				t.Errorf("%d launches after the refusal, then %d once room opened: a refused accept must not wait", launchesAfterRefusal-before, n-before)
			}
			if _, err := h.sched.Accept(context.Background(), "work", r.ID, trig.ID); err != nil {
				t.Fatalf("a second Accept once there is room: %v", err)
			}
		})
	}
}

func TestAcceptRunsTheFireTimeGatesAndAFailureLaunchesNothing(t *testing.T) {
	h := newHarness(t)
	r, trig := h.proposed(nil)
	h.prove("work", false, true)
	_, err := h.sched.Accept(context.Background(), "work", r.ID, trig.ID)
	if err == nil || errors.Is(err, routines.ErrBusy) || err.Error() == "not implemented" {
		t.Fatalf("Accept with an unproven guard = %v, want the gate's refusal", err)
	}
	h.wantLaunches(0)
	if rec := h.latest(r); rec.Outcome != routines.Failed || !strings.Contains(strings.ToLower(rec.Reason), "guard") {
		t.Errorf("record = %+v, want failed with a reason naming the guard", rec)
	}
}

func TestAcceptOfAnUnknownRoutineIsNotFoundAndLaunchesNothing(t *testing.T) {
	h := newHarness(t)
	_, trig := h.proposed(nil)
	if _, err := h.sched.Accept(context.Background(), "work", "nope", trig.ID); !errors.Is(err, routines.ErrNotFound) {
		t.Fatalf("Accept = %v, want ErrNotFound", err)
	}
	h.wantLaunches(0)
}

func TestAcceptOfAPromptRoutinesProposalSendsItsTurnOnce(t *testing.T) {
	h := newHarness(t)
	r, trig := h.proposed(func(r *routines.Routine) { r.Do = routines.Do{Prompt: "check the board"} })
	if _, err := h.sched.Accept(context.Background(), "work", r.ID, trig.ID); err != nil {
		t.Fatalf("Accept: %v", err)
	}
	h.wantLaunches(0)
	h.chat.mu.Lock()
	defer h.chat.mu.Unlock()
	if len(h.chat.sent) != 1 || h.chat.sent[0][0] != "work" || h.chat.sent[0][1] != "[Routine: Nightly sweep] check the board" {
		t.Errorf("chat turns = %q, want the one marked with the routine's name", h.chat.sent)
	}
}

func TestAProposalNobodyAnsweredIsRecordedExpiredOnceAndNeverAgain(t *testing.T) {
	h := newHarness(t)
	r, _ := h.proposed(func(r *routines.Routine) { r.When = "0 9 12 * *" })
	h.tickAt(at(1, 8, 59))
	if got := h.ofOutcome(r, routines.Skipped); len(got) != 0 {
		t.Fatalf("a proposal still open was recorded: %+v", got)
	}
	h.tickAt(at(1, 9, 1))
	h.tickAt(at(1, 9, 5))
	got := h.ofOutcome(r, routines.Skipped)
	if len(got) != 1 || !strings.Contains(got[0].Reason, "proposal expired") {
		t.Fatalf("skipped records = %+v, want one saying the proposal expired", got)
	}
	if n := len(h.proposer.proposals()); n != 1 {
		t.Errorf("%d proposals, want no new one before the next run", n)
	}
}

func TestANewProposalForARoutineRecordsThePreviousOneExpired(t *testing.T) {
	h := newHarness(t)
	r, _ := h.proposed(nil)
	h.tickAt(at(1, 9, 0))
	if n := len(h.proposer.proposals()); n != 2 {
		t.Fatalf("%d proposals, want the second run's too", n)
	}
	if got := h.ofOutcome(r, routines.Skipped); len(got) != 1 || !strings.Contains(got[0].Reason, "proposal expired") {
		t.Errorf("skipped records = %+v, want one for the replaced proposal", got)
	}
}

func TestAnAcceptedProposalIsNeverRecordedExpired(t *testing.T) {
	h := newHarness(t)
	r, trig := h.proposed(func(r *routines.Routine) { r.When = "0 9 12 * *" })
	h.clock.set(at(0, 9, 30))
	if _, err := h.sched.Accept(context.Background(), "work", r.ID, trig.ID); err != nil {
		t.Fatalf("Accept: %v", err)
	}
	h.tickAt(at(1, 9, 1))
	if got := h.ofOutcome(r, routines.Skipped); len(got) != 0 {
		t.Errorf("skipped records = %+v, want none: the proposal was accepted", got)
	}
}

func TestNotifyMePushesLaunchedProposedFailedAndBreakerOnlyWhenItIsOn(t *testing.T) {
	cases := []struct {
		name   string
		mode   routines.Mode
		fail   bool
		days   int
		notify bool
		want   []routines.Outcome
	}{
		{"launched", routines.Act, false, 1, true, []routines.Outcome{routines.Launched}},
		{"proposed", routines.AskFirst, false, 1, true, []routines.Outcome{routines.Proposed}},
		{"failed", routines.Act, true, 1, true, []routines.Outcome{routines.Failed}},
		{"three failures and the breaker", routines.Act, true, 4, true,
			[]routines.Outcome{routines.Failed, routines.Failed, routines.Failed, routines.Breaker, routines.Proposed}},
		{"reported is not pushed", routines.ReportOnly, false, 1, true, nil},
		{"launched with Notify me off", routines.Act, false, 1, false, nil},
		{"proposed with Notify me off", routines.AskFirst, false, 1, false, nil},
		{"failed with Notify me off", routines.Act, true, 1, false, nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			h := newHarness(t)
			n := &fakeNotifier{}
			h.sched.Notifier = n
			h.act(func(r *routines.Routine) { r.Before, r.Notify = c.mode, c.notify })
			if c.fail {
				failLaunches(h, 1, 3)
			}
			for d := 0; d < c.days; d++ {
				h.tickAt(at(d, 9, 0))
			}
			got, want := map[routines.Outcome]int{}, map[routines.Outcome]int{}
			for _, o := range n.outcomes() {
				got[o]++
			}
			for _, o := range c.want {
				want[o]++
			}
			if !maps.Equal(got, want) {
				t.Fatalf("pushed outcomes = %v, want %v", n.outcomes(), c.want)
			}
		})
	}
}

func TestAWatchSurvivesTheShipsHostBeingLostAndTheFailureIsRecordedWhenItIsBack(t *testing.T) {
	h := newHarness(t)
	known := true
	h.sched.HostKnown = func() bool { return known }
	r := h.act(nil)
	key := "routine:" + r.ID
	h.tickAt(at(0, 9, 0))
	h.ships.set(func() { h.ships.holders = map[string]routines.ShipRef{key: {Session: "ship-1", Status: "underway"}} })
	h.tickAt(at(0, 9, 1)) // the ship is listed, so the watch has seen it
	known = false
	h.ships.set(func() { h.ships.holders, h.ships.failed = nil, map[string]bool{"ship-1": true} })
	h.tickAt(at(0, 9, 2))
	known = true
	h.ships.set(func() { h.ships.holders = map[string]routines.ShipRef{key: {Session: "ship-1", Status: "anchored"}} })
	h.tickAt(at(0, 9, 3))
	if got := h.ofOutcome(r, routines.Failed); len(got) != 1 {
		t.Fatalf("records = %+v, want the launch turned failed after the host came back", h.records(r))
	}
}

func TestAWaitingRunIsDroppedWithARecordOnceItIsTwelveHoursLate(t *testing.T) {
	h := newHarness(t)
	r := h.act(nil)
	h.ships.set(func() {
		h.ships.launch = func(int, launcher.Request) (routines.ShipRef, *launcher.Error) {
			return routines.ShipRef{}, &launcher.Error{Status: http.StatusConflict, Msg: launcher.AutopilotCapText, Wait: "Waiting for a free ship slot"}
		}
	})
	h.tickAt(at(0, 9, 0))
	h.sched.Changed()
	h.tickAt(at(0, 20, 59))
	h.wantLaunches(2) // still inside the window: retried
	h.sched.Changed()
	h.tickAt(at(0, 21, 0))
	h.wantLaunches(2)
	var dropped []routines.Trigger
	for _, rec := range h.records(r) {
		if rec.Outcome == routines.Skipped || rec.Outcome == routines.Missed {
			dropped = append(dropped, rec)
		}
	}
	if len(dropped) != 1 {
		t.Fatalf("records = %+v, want exactly one saying the late run was dropped", h.records(r))
	}
	h.sched.Changed()
	h.tickAt(at(0, 21, 5))
	h.wantLaunches(2)
}

func TestARoutineEditedWhileItsRunWaitsWritesItsNewOutcomeOnce(t *testing.T) {
	for _, mode := range []routines.Mode{routines.ReportOnly, routines.AskFirst} {
		t.Run(string(mode), func(t *testing.T) {
			h := newHarness(t)
			r := h.act(nil)
			h.ships.set(func() {
				h.ships.launch = func(int, launcher.Request) (routines.ShipRef, *launcher.Error) {
					return routines.ShipRef{}, &launcher.Error{Status: http.StatusConflict, Msg: launcher.AutopilotCapText, Wait: "Waiting for a free ship slot"}
				}
			})
			h.tickAt(at(0, 9, 0))
			edit := h.stored(r)
			edit.Before = mode
			if _, err := h.store.Update("work", r.ID, edit); err != nil {
				t.Fatal(err)
			}
			for i := 1; i <= 3; i++ {
				h.sched.Changed()
				h.tickAt(at(0, 9, 5*i))
			}
			want := routines.Reported
			if mode == routines.AskFirst {
				want = routines.Proposed
			}
			if got := h.ofOutcome(r, want); len(got) != 1 {
				t.Fatalf("%d %s records after three changes, want 1: %+v", len(got), want, h.records(r))
			}
		})
	}
}

func TestARoutineNameWithAControlCharacterOrBracketOrOverEightyRunesIsRefused(t *testing.T) {
	r := newRig(t)
	long := strings.Repeat("é", 80)
	ok := []string{"Nightly sweep", "Café (weekly) sweep", long}
	bad := []string{"a\u2028b", "a\u2029b", "a\u202eb", "a\u2066b", "a\u200bb", "a\ufeffb", "Nightly\nsweep", "a\x00b", "a\tb", "x\u007fy", "[Routine: x] sweep", "a[b", "a]b", long + "é", " \n "}
	for _, name := range ok {
		in := launchRoutine()
		in.Name = name
		if _, err := r.store.Create("work", in); err != nil {
			t.Errorf("Create(%q) = %v, want it saved", name, err)
		}
	}
	for _, name := range bad {
		in := launchRoutine()
		in.Name = name
		if _, err := r.store.Create("work", in); !errors.Is(err, routines.ErrInvalid) {
			t.Errorf("Create(%q) = %v, want ErrInvalid", name, err)
		}
	}
	saved := r.create("work", launchRoutine())
	up := saved
	up.Name = "[Routine: x]"
	if _, err := r.store.Update("work", saved.ID, up); !errors.Is(err, routines.ErrInvalid) {
		t.Errorf("Update to a bracketed name = %v, want ErrInvalid", err)
	}
}

func TestAnAdmiralBotSwitchOffSaysSoAndTheEngineersSwitchesDoNot(t *testing.T) {
	a := newAPI(t)
	id := a.post()["id"].(string)
	a.bot = true
	code, body := a.do("POST", "/api/fleets/work/routines/"+id+"/off", "")
	if code != http.StatusOK || body["enabled"] != false || body["off_by"] != "admiralbot" {
		t.Fatalf("AdmiralBot's off = %d %v, want enabled false and off_by admiralbot", code, body)
	}
	a.bot = false
	code, body = a.do("POST", "/api/fleets/work/routines/"+id+"/on", "")
	if code != http.StatusOK || body["enabled"] != true || body["off_by"] != nil {
		t.Fatalf("the engineer's on = %d %v, want it on with no off_by", code, body)
	}
	code, body = a.do("POST", "/api/fleets/work/routines/"+id+"/off", "")
	if code != http.StatusOK || body["enabled"] != false || body["off_by"] != nil {
		t.Fatalf("the engineer's off = %d %v, want no off_by", code, body)
	}
}

func TestAnAdmiralBotSwitchOffShowsInTheRoutinesList(t *testing.T) {
	a := newAPI(t)
	id := a.post()["id"].(string)
	a.bot = true
	a.do("POST", "/api/fleets/work/routines/"+id+"/off", "")
	code, body := a.do("GET", "/api/fleets/work/routines", "")
	list, _ := body["routines"].([]any)
	if code != http.StatusOK || len(list) != 1 {
		t.Fatalf("GET routines = %d %v", code, body)
	}
	if first, _ := list[0].(map[string]any); first["off_by"] != "admiralbot" {
		t.Errorf("listed routine = %v, want off_by admiralbot", list[0])
	}
}

func TestADismissedProposalIsRecordedDismissedAndItsExpiryWritesNothing(t *testing.T) {
	h := newHarness(t)
	r, trig := h.proposed(func(r *routines.Routine) { r.When = "0 9 12 * *" })
	h.clock.set(at(0, 10, 0))
	h.sched.Dismiss("work", r.ID, trig.ID)
	h.tickAt(at(1, 9, 5))
	got := h.ofOutcome(r, routines.Skipped)
	if len(got) != 1 || !strings.Contains(got[0].Reason, "dismissed") {
		t.Fatalf("skipped records = %+v, want exactly one saying the proposal was dismissed", got)
	}
}

func TestADismissOfAnOlderProposalLeavesTheNewerOneToExpireOnItsOwn(t *testing.T) {
	h := newHarness(t)
	r, first := h.proposed(nil)
	h.tickAt(at(1, 9, 0))
	h.sched.Dismiss("work", r.ID, first.ID)
	if got := h.ofOutcome(r, routines.Skipped); len(got) != 1 || strings.Contains(got[0].Reason, "dismissed") {
		t.Fatalf("skipped records = %+v, want only the replacement's, nothing for the stale dismiss", got)
	}
}

func TestAcceptRefusesWithErrOffWhenTheRoutineIsSwitchedOffAndLaunchesNothing(t *testing.T) {
	h := newHarness(t)
	r, trig := h.proposed(nil)
	if _, err := h.store.SetEnabledBy("work", r.ID, false, routines.Engineer); err != nil {
		t.Fatal(err)
	}
	if _, err := h.sched.Accept(context.Background(), "work", r.ID, trig.ID); !errors.Is(err, routines.ErrOff) {
		t.Fatalf("Accept on a routine that is off = %v, want ErrOff", err)
	}
	h.wantLaunches(0)
}

// failingProposer refuses every proposal.
type failingProposer struct{}

func (failingProposer) Propose(string, routines.Routine, routines.Trigger, time.Time) error {
	return errors.New("disk full")
}

func TestProposedPushesOnlyAfterTheProposerTookTheProposal(t *testing.T) {
	h := newHarness(t)
	n := &fakeNotifier{}
	h.sched.Notifier = n
	h.sched.Proposer = failingProposer{}
	h.act(func(r *routines.Routine) { r.Before, r.Notify = routines.AskFirst, true })
	h.tickAt(at(0, 9, 0))
	if got := n.outcomes(); len(got) != 0 {
		t.Errorf("pushed %v after the Proposer refused, want nothing", got)
	}
}

// inboxProposer is the Proposer main wires: the real inbox.
type inboxProposer struct {
	in      *inbox.Service
	entered chan struct{}
	once    sync.Once
}

func (p *inboxProposer) Propose(fleet string, r routines.Routine, t routines.Trigger, expires time.Time) error {
	p.once.Do(func() { close(p.entered) })
	_, err := p.in.PublishRoutineProposal(inbox.RoutineProposal{Fleet: fleet, Routine: r.ID, RoutineName: r.Name, Trigger: t.ID, Action: t.Action, ExpiresAt: expires})
	return err
}

func TestATicksProposalAndALaunchOfTheOpenProposalDoNotDeadlock(t *testing.T) {
	h := newHarness(t)
	in := &inbox.Service{Home: h.home, Hub: server.NewHub()}
	failLaunches(h, 1, 1<<30) // a refused launch leaves the item open, so the next proposal has one to close
	base := time.Now().UTC().Truncate(24 * time.Hour).Add(9 * time.Hour)
	h.clock.set(base.Add(-time.Hour))
	r := h.act(func(r *routines.Routine) { r.Before = routines.AskFirst })
	h.sched.Proposer = &inboxProposer{in: in, entered: make(chan struct{})}
	h.tickAt(base)
	open := "routine-" + h.latest(r).ID
	prop := &inboxProposer{in: in, entered: make(chan struct{})} // signals when the next tick reaches its proposal
	h.sched.Proposer = prop

	inLaunch := make(chan struct{})
	in.LaunchRoutine = func(ctx context.Context, p inbox.RoutineProposal) (any, int, string) {
		// holds the answer lock; the scheduler's lock is taken only once the tick is inside its proposal
		close(inLaunch)
		<-prop.entered
		_, err := h.sched.Accept(ctx, p.Fleet, p.Routine, p.Trigger)
		return nil, http.StatusBadGateway, fmt.Sprint(err)
	}
	h.clock.set(base.Add(24 * time.Hour))
	done := make(chan struct{}, 2)
	go func() { _, _ = in.LaunchRoutineItem(context.Background(), open); done <- struct{}{} }()
	select {
	case <-inLaunch: // the launch holds the answer lock before the tick starts
	case <-time.After(10 * time.Second):
		t.Fatal("the launch never reached its hook")
	}
	go func() { _ = h.sched.Tick(context.Background()); done <- struct{}{} }()
	for i := 0; i < 2; i++ {
		select {
		case <-done:
		case <-time.After(10 * time.Second):
			t.Fatal("deadlock: a tick's proposal and a launch of the open proposal wait on each other")
		}
	}
}
