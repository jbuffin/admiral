package routines_test

import (
	"context"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jbuffin/admiral/internal/launcher"
	"github.com/jbuffin/admiral/internal/routines"
)

var day0 = time.Date(2026, 11, 12, 8, 0, 0, 0, time.UTC)

func at(day, hour, min int) time.Time {
	return time.Date(2026, 11, 12+day, hour, min, 0, 0, time.UTC)
}

type waiter struct {
	at time.Time
	ch chan time.Time
}

// fakeClock fires a timer only when a test moves the time past it.
type fakeClock struct {
	mu      sync.Mutex
	now     time.Time
	waiters []*waiter
	armed   int // After calls so far
}

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *fakeClock) After(d time.Duration) <-chan time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.armed++
	w := &waiter{at: c.now.Add(d), ch: make(chan time.Time, 1)}
	if d <= 0 {
		w.ch <- c.now
		return w.ch
	}
	c.waiters = append(c.waiters, w)
	return w.ch
}

func (c *fakeClock) set(t time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = t
	keep := c.waiters[:0]
	for _, w := range c.waiters {
		if w.at.After(t) {
			keep = append(keep, w)
		} else {
			w.ch <- t
		}
	}
	c.waiters = keep
}

func (c *fakeClock) armedCount() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.armed
}

func (c *fakeClock) hasDeadline(t time.Time) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, w := range c.waiters {
		if w.at.Equal(t) {
			return true
		}
	}
	return false
}

type fakeShips struct {
	mu      sync.Mutex
	calls   []launcher.Request
	launch  func(n int, req launcher.Request) (routines.ShipRef, *launcher.Error)
	holders map[string]routines.ShipRef
	failed  map[string]bool
}

func (f *fakeShips) Launch(_ context.Context, req launcher.Request) (routines.ShipRef, *launcher.Error) {
	f.mu.Lock()
	f.calls = append(f.calls, req)
	n, fn := len(f.calls), f.launch
	f.mu.Unlock()
	if fn != nil {
		return fn(n, req)
	}
	return routines.ShipRef{Session: "ship-" + strconv.Itoa(n), Status: "underway"}, nil
}

func (f *fakeShips) Holder(key string) (routines.ShipRef, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	s, ok := f.holders[key]
	return s, ok
}

func (f *fakeShips) EndedFailed(session string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.failed[session]
}

func (f *fakeShips) set(fn func()) {
	f.mu.Lock()
	defer f.mu.Unlock()
	fn()
}

func (f *fakeShips) requests() []launcher.Request {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]launcher.Request(nil), f.calls...)
}

func (f *fakeShips) callsFor(key string) int {
	n := 0
	for _, c := range f.requests() {
		if c.RoutineKey == key {
			n++
		}
	}
	return n
}

type fakeChat struct {
	mu   sync.Mutex
	sent [][2]string
	err  error
}

func (f *fakeChat) Send(fleet, text string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.err != nil {
		return f.err
	}
	f.sent = append(f.sent, [2]string{fleet, text})
	return nil
}

type proposal struct {
	fleet     string
	routineID string
	trigger   routines.Trigger
}

type fakeProposer struct {
	mu  sync.Mutex
	got []proposal
}

func (f *fakeProposer) Propose(fleet string, r routines.Routine, t routines.Trigger) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.got = append(f.got, proposal{fleet, r.ID, t})
	return nil
}

func (f *fakeProposer) proposals() []proposal {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]proposal(nil), f.got...)
}

type harness struct {
	*rig
	clock    *fakeClock
	ships    *fakeShips
	chat     *fakeChat
	proposer *fakeProposer
	sched    *routines.Scheduler
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	h := &harness{rig: newRig(t), clock: &fakeClock{now: day0}, ships: &fakeShips{}, chat: &fakeChat{}, proposer: &fakeProposer{}}
	h.store.Now = h.clock.Now
	h.sched = &routines.Scheduler{Store: h.store, Clock: h.clock, Ships: h.ships, Chat: h.chat, Proposer: h.proposer}
	return h
}

// act saves a daily 09:00 routine that launches with no one's yes.
func (h *harness) act(mod func(*routines.Routine)) routines.Routine {
	h.t.Helper()
	in := launchRoutine()
	in.When, in.Before = "0 9 * * *", routines.Act
	if mod != nil {
		mod(&in)
	}
	return h.create("work", in)
}

func (h *harness) tickAt(now time.Time) {
	h.t.Helper()
	h.clock.set(now)
	if err := h.sched.Tick(context.Background()); err != nil {
		h.t.Fatalf("Tick at %v: %v", now, err)
	}
}

func (h *harness) records(r routines.Routine) []routines.Trigger {
	h.t.Helper()
	got, err := h.store.Recent("work", r.ID, 0)
	if err != nil {
		h.t.Fatal(err)
	}
	return got
}

func (h *harness) ofOutcome(r routines.Routine, o routines.Outcome) []routines.Trigger {
	var out []routines.Trigger
	for _, rec := range h.records(r) {
		if rec.Outcome == o {
			out = append(out, rec)
		}
	}
	return out
}

func (h *harness) latest(r routines.Routine) routines.Trigger {
	h.t.Helper()
	got := h.records(r)
	if len(got) == 0 {
		h.t.Fatal("the routine has no trigger record")
	}
	return got[0]
}

func (h *harness) stored(r routines.Routine) routines.Routine {
	h.t.Helper()
	list, err := h.store.List("work")
	if err != nil {
		h.t.Fatal(err)
	}
	for _, x := range list {
		if x.ID == r.ID {
			return x
		}
	}
	h.t.Fatal("the routine is gone")
	return routines.Routine{}
}

func (h *harness) wantLaunches(n int) {
	h.t.Helper()
	if got := len(h.ships.requests()); got != n {
		h.t.Fatalf("%d launches, want %d", got, n)
	}
}

func eventually(t *testing.T, what string, ok func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !ok() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for: %s", what)
		}
		time.Sleep(2 * time.Millisecond)
	}
}

func TestARoutineFiresWhenItsTimeComesAndNotBefore(t *testing.T) {
	h := newHarness(t)
	r := h.act(nil)

	h.tickAt(at(0, 8, 59))
	h.wantLaunches(0)
	h.tickAt(at(0, 9, 0))
	h.wantLaunches(1)
	h.tickAt(at(0, 9, 0))
	h.wantLaunches(1) // the same run is not fired twice
	h.tickAt(at(1, 9, 0).Add(40 * time.Second))
	h.wantLaunches(2)
	if got := h.ofOutcome(r, routines.Launched); len(got) != 2 {
		t.Fatalf("%d launched records, want 2: %+v", len(got), h.records(r))
	}
}

func TestAFiredLaunchIsAnAutopilotShipUnderTheRoutinesKeyWithItsTriggerRecord(t *testing.T) {
	h := newHarness(t)
	r := h.act(func(r *routines.Routine) {
		r.Do.Launch.Resources = []string{"phone"}
		r.Do.Launch.Skill = launcher.Skill{Kind: "skill", Name: "sweep"}
	})
	h.tickAt(at(0, 9, 0))
	h.wantLaunches(1)
	req := h.ships.requests()[0]
	if !req.Autopilot || req.Fleet != "work" || req.Harness != "claude" || req.Dir != "/tmp/repo" || req.Text != "sweep the board" ||
		req.Model != "sonnet" || req.Skill.Kind != "skill" || req.Skill.Name != "sweep" {
		t.Errorf("launch request = %+v, want the routine's launch as an autopilot ship", req)
	}
	if len(req.ExtraArgs) != 2 || req.ExtraArgs[0] != "--effort" || req.ExtraArgs[1] != "high" {
		t.Errorf("ExtraArgs = %v, want the routine's args", req.ExtraArgs)
	}
	if len(req.Resources) != 1 || req.Resources[0] != "phone" {
		t.Errorf("Resources = %v, want [phone]", req.Resources)
	}
	if req.RoutineKey != "routine:"+r.ID {
		t.Errorf("RoutineKey = %q, want routine:%s", req.RoutineKey, r.ID)
	}
	rec := h.latest(r)
	if rec.Outcome != routines.Launched || rec.Tier != "T0" || rec.Ship != "ship-1" || rec.ID == "" || req.TriggerID != rec.ID {
		t.Errorf("record = %+v and request trigger %q, want a T0 launched record for ship-1 whose id the launch carried", rec, req.TriggerID)
	}
}

func TestAnEmptySkillLaunchesAsJustLaunch(t *testing.T) {
	h := newHarness(t)
	h.act(nil)
	h.tickAt(at(0, 9, 0))
	h.wantLaunches(1)
	if k := h.ships.requests()[0].Skill.Kind; k != "none" {
		t.Errorf("skill kind = %q, want none", k)
	}
}

func TestAnActRoutineWithAPromptSendsAdmiralBotATurnMarkedWithItsName(t *testing.T) {
	h := newHarness(t)
	r := h.act(func(r *routines.Routine) { r.Do = routines.Do{Prompt: "check the board"} })
	h.tickAt(at(0, 9, 0))
	h.wantLaunches(0)
	if len(h.chat.sent) != 1 || h.chat.sent[0][0] != "work" ||
		!strings.HasPrefix(h.chat.sent[0][1], "[Routine: Nightly sweep] ") || !strings.Contains(h.chat.sent[0][1], "check the board") {
		t.Fatalf("chat turns = %q, want one to work marked [Routine: Nightly sweep] holding the prompt", h.chat.sent)
	}
	if rec := h.latest(r); rec.Outcome != routines.Launched || rec.Tier != "T0" || rec.Ship != "" {
		t.Errorf("record = %+v, want a T0 launched record with no ship", rec)
	}
}

func TestAPromptWhileTheChatIsNotRunningIsFailedAndStartsNothing(t *testing.T) {
	h := newHarness(t)
	h.chat.err = errors.New("the chat is not running")
	r := h.act(func(r *routines.Routine) { r.Do = routines.Do{Prompt: "check the board"} })
	h.tickAt(at(0, 9, 0))
	if rec := h.latest(r); rec.Outcome != routines.Failed || !strings.Contains(rec.Reason, "not running") {
		t.Errorf("record = %+v, want failed with the chat's reason", rec)
	}
}

func TestReportOnlyRecordsWhatItWouldDoAndLaunchesNothing(t *testing.T) {
	h := newHarness(t)
	r := h.act(func(r *routines.Routine) { r.Before = routines.ReportOnly })
	h.tickAt(at(0, 9, 0))
	h.wantLaunches(0)
	if got := h.proposer.proposals(); len(got) != 0 {
		t.Errorf("Report only proposed %d times, want none", len(got))
	}
	if rec := h.latest(r); rec.Outcome != routines.Reported || rec.Tier != "T0" || !strings.Contains(rec.Action, "/tmp/repo") {
		t.Errorf("record = %+v, want a T0 reported record whose Action names what it would launch", rec)
	}
}

func TestAskFirstRecordsProposedAndHandsOffToTheProposerWithoutLaunching(t *testing.T) {
	h := newHarness(t)
	r := h.act(func(r *routines.Routine) { r.Before = routines.AskFirst })
	h.tickAt(at(0, 9, 0))
	h.wantLaunches(0)
	rec := h.latest(r)
	got := h.proposer.proposals()
	if rec.Outcome != routines.Proposed || len(got) != 1 || got[0].fleet != "work" || got[0].routineID != r.ID || got[0].trigger.ID != rec.ID {
		t.Fatalf("record %+v and proposals %+v, want one proposed record handed to the Proposer", rec, got)
	}
}

func TestHoldFireTurnsActIntoAskFirstButNotReportOnlyAndLeavesTheStoredSetting(t *testing.T) {
	for _, global := range []bool{false, true} {
		h := newHarness(t)
		act := h.act(nil)
		report := h.act(func(r *routines.Routine) { r.Before = routines.ReportOnly })
		fleet := "work"
		if global {
			fleet = ""
		}
		if _, err := h.store.SetHold(fleet, true, routines.Engineer); err != nil {
			t.Fatal(err)
		}
		h.tickAt(at(0, 9, 0))
		h.wantLaunches(0)
		if rec := h.latest(act); rec.Outcome != routines.Proposed || len(h.proposer.proposals()) != 1 {
			t.Errorf("global=%v: Act under Hold fire = %+v with %d proposals, want proposed once", global, rec, len(h.proposer.proposals()))
		}
		if rec := h.latest(report); rec.Outcome != routines.Reported {
			t.Errorf("global=%v: Report only under Hold fire = %+v, want reported", global, rec)
		}
		if h.stored(act).Before != routines.Act {
			t.Errorf("global=%v: Hold fire changed the stored setting", global)
		}
	}
}

func TestAHoldFireThatCannotBeReadHoldsTheRun(t *testing.T) {
	h := newHarness(t)
	r := h.act(nil)
	if err := os.MkdirAll(filepath.Join(h.home, "routines", "work", "hold.json"), 0o700); err != nil {
		t.Fatal(err)
	}
	h.tickAt(at(0, 9, 0))
	h.wantLaunches(0)
	if rec := h.latest(r); rec.Outcome != routines.Proposed {
		t.Errorf("record = %+v, want proposed: an unreadable Hold fire is held", rec)
	}
}

func TestARunIsSkippedWhileAShipHoldsTheRoutinesKeyWhateverItsStatus(t *testing.T) {
	h := newHarness(t)
	r := h.act(nil)
	h.ships.set(func() {
		h.ships.holders = map[string]routines.ShipRef{"routine:" + r.ID: {Session: "ship-9", Status: "anchored"}}
	})
	h.tickAt(at(0, 9, 0))
	h.wantLaunches(0)
	if rec := h.latest(r); rec.Outcome != routines.Skipped || !strings.Contains(rec.Reason, "anchored") {
		t.Fatalf("record = %+v, want skipped with a reason naming the ship's status", rec)
	}
	h.ships.set(func() { h.ships.holders = nil })
	h.tickAt(at(1, 9, 0))
	h.wantLaunches(1)
}

func TestARunThatFindsNoRoomWaitsAndLaunchesOnceWhenRoomOpens(t *testing.T) {
	cases := map[string]*launcher.Error{
		"cap":       {Status: http.StatusConflict, Msg: launcher.AutopilotCapText + " of 2 ships", Wait: "Waiting for a free ship slot"},
		"resources": {Status: http.StatusConflict, Msg: `resource "phone" is in use`, Wait: "Waiting for phone (in use)"},
	}
	for name, refusal := range cases {
		t.Run(name, func(t *testing.T) {
			h := newHarness(t)
			r := h.act(nil)
			h.ships.set(func() {
				h.ships.launch = func(int, launcher.Request) (routines.ShipRef, *launcher.Error) { return routines.ShipRef{}, refusal }
			})
			h.tickAt(at(0, 9, 0))
			h.wantLaunches(1)
			if got := h.records(r); len(got) != 0 {
				t.Fatalf("a waiting run wrote %+v, want no record until it has launched", got)
			}
			h.ships.set(func() { h.ships.launch = nil })
			h.sched.Changed()
			h.tickAt(at(0, 9, 5))
			h.wantLaunches(2)
			if got := h.ofOutcome(r, routines.Launched); len(got) != 1 || len(h.records(r)) != 1 {
				t.Fatalf("records = %+v, want exactly one launched", h.records(r))
			}
			h.sched.Changed()
			h.tickAt(at(0, 9, 10))
			h.wantLaunches(2) // not queued twice
		})
	}
}

func TestTheNextScheduledRunSupersedesARunStillWaiting(t *testing.T) {
	h := newHarness(t)
	r := h.act(nil)
	full := func(int, launcher.Request) (routines.ShipRef, *launcher.Error) {
		return routines.ShipRef{}, &launcher.Error{Status: http.StatusConflict, Msg: launcher.AutopilotCapText, Wait: "Waiting for a free ship slot"}
	}
	h.ships.set(func() { h.ships.launch = full })
	h.tickAt(at(0, 9, 0))
	h.tickAt(at(1, 9, 0))
	h.ships.set(func() { h.ships.launch = nil })
	h.sched.Changed()
	h.tickAt(at(1, 9, 5))
	if got := h.ofOutcome(r, routines.Launched); len(got) != 1 {
		t.Fatalf("%d launched records, want one: the second run replaced the first, which was still waiting", len(got))
	}
	if got := h.ofOutcome(r, routines.Failed); len(got) != 0 {
		t.Errorf("a superseded wait failed: %+v", got)
	}
}

func seedLaunched(t *testing.T, h *harness, r routines.Routine, when time.Time) {
	t.Helper()
	if _, err := h.store.AppendTrigger("work", routines.Trigger{Routine: r.ID, Outcome: routines.Launched, Tier: "T0", At: when, Ship: "old"}); err != nil {
		t.Fatal(err)
	}
}

func (h *harness) downFor(r routines.Routine, startAt time.Time) {
	h.t.Helper()
	h.clock.set(startAt)
	if err := h.sched.CatchUp(context.Background()); err != nil {
		h.t.Fatalf("CatchUp: %v", err)
	}
}

func TestARunMissedWhileDownByLessThanTwelveHoursGetsOneLateRun(t *testing.T) {
	h := newHarness(t)
	r := h.act(nil)
	seedLaunched(t, h, r, at(-2, 9, 0)) // 2026-11-10, the last run before Admiral went down
	h.downFor(r, at(-1, 11, 0))         // 2026-11-11 11:00: the 09:00 run is two hours old
	h.wantLaunches(1)
	if got := h.ofOutcome(r, routines.Launched); len(got) != 2 || got[0].Tier != "T0" {
		t.Errorf("records = %+v, want the seeded launch and the late one", h.records(r))
	}
}

func TestARunMissedByMoreThanTwelveHoursIsRecordedMissedAndNotLaunched(t *testing.T) {
	h := newHarness(t)
	r := h.act(nil)
	seedLaunched(t, h, r, at(-2, 9, 0))
	h.downFor(r, at(-1, 22, 0)) // 13 hours late
	h.wantLaunches(0)
	if got := h.ofOutcome(r, routines.Missed); len(got) != 1 || got[0].Tier != "T0" {
		t.Fatalf("records = %+v, want one missed", h.records(r))
	}
	h.tickAt(at(0, 9, 0))
	h.wantLaunches(1) // the schedule goes on
}

func TestSeveralMissedRunsGetAtMostOneLateRun(t *testing.T) {
	h := newHarness(t)
	r := h.act(nil)
	seedLaunched(t, h, r, at(-2, 9, 0))
	h.downFor(r, at(1, 9, 30)) // 11-11, 11-12 and 11-13 at 09:00 were all missed; the last is 30 minutes old
	h.wantLaunches(1)
	if got := h.ofOutcome(r, routines.Missed); len(got) == 0 {
		t.Errorf("records = %+v, want the older runs recorded missed", h.records(r))
	}
	if got := h.ofOutcome(r, routines.Launched); len(got) != 2 {
		t.Errorf("%d launched records, want the seeded one and one late run", len(got))
	}
}

func TestARoutineSavedAfterItsTimeTodayIsNotMissedOnStart(t *testing.T) {
	h := newHarness(t)
	h.clock.set(at(0, 9, 30))
	r := h.act(nil) // saved at 09:30, after today's 09:00
	h.downFor(r, at(0, 10, 0))
	h.wantLaunches(0)
	if got := h.records(r); len(got) != 0 {
		t.Errorf("records = %+v, want none", got)
	}
}

func TestAFireTimeRefusalIsAFailedRecordAndTheLaunchNeverHappens(t *testing.T) {
	cases := []struct {
		name   string
		break_ func(h *harness, r routines.Routine)
		want   string
	}{
		{"a guard that is no longer proven", func(h *harness, _ routines.Routine) { h.prove("work", false, true) }, "guard"},
		{"an advisor that is no longer proven", func(h *harness, _ routines.Routine) { h.prove("work", true, false) }, "advisor"},
		{"an arg that is off the allowlist in the stored file", func(h *harness, _ routines.Routine) {
			path := filepath.Join(h.home, "routines", "work", "routines.json")
			b, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, []byte(strings.Replace(string(b), "--effort", "--dangerously-skip-permissions", 1)), 0o600); err != nil {
				t.Fatal(err)
			}
		}, "allowlist"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			h := newHarness(t)
			r := h.act(nil)
			c.break_(h, r)
			h.tickAt(at(0, 9, 0))
			h.wantLaunches(0)
			if rec := h.latest(r); rec.Outcome != routines.Failed || rec.Tier != "T0" || !strings.Contains(strings.ToLower(rec.Reason), c.want) {
				t.Errorf("record = %+v, want failed with a reason naming %q", rec, c.want)
			}
		})
	}
}

func TestAnUnprovenGuardAtFireTimeLeavesTheCouldntLaunchUnattendedNotice(t *testing.T) {
	h := newHarness(t)
	h.act(nil)
	h.prove("work", false, true)
	h.tickAt(at(0, 9, 0))
	entries, _ := os.ReadDir(filepath.Join(h.home, "inbox", "_admiral"))
	if len(entries) != 1 {
		t.Errorf("%d Inbox notices, want the one the autopilot gate posts", len(entries))
	}
}

// failLaunches makes launch calls from through to (counting from 1) fail with a plain error.
func failLaunches(h *harness, from, to int) {
	h.ships.set(func() {
		h.ships.launch = func(n int, _ launcher.Request) (routines.ShipRef, *launcher.Error) {
			if n >= from && n <= to {
				return routines.ShipRef{}, &launcher.Error{Status: http.StatusInternalServerError, Msg: "boom"}
			}
			return routines.ShipRef{Session: "ship-" + strconv.Itoa(n), Status: "underway"}, nil
		}
	})
}

func (h *harness) wantTripped(r routines.Routine, launchesBefore int) {
	h.t.Helper()
	h.tickAt(at(3, 9, 0))
	h.wantLaunches(launchesBefore)
	if got := h.stored(r).Before; got != routines.AskFirst {
		h.t.Errorf("stored Before acting = %q, want ask_first after three in a row", got)
	}
	b := h.ofOutcome(r, routines.Breaker)
	if len(b) != 1 || !strings.Contains(strings.ToLower(b[0].Action), "ask first") {
		h.t.Errorf("breaker records = %+v, want one naming the change to Ask first", b)
	}
	if got := h.proposer.proposals(); len(got) != 1 {
		h.t.Errorf("%d proposals, want the next run proposed", len(got))
	}
}

func TestThreeRefusedLaunchesInARowSetAskFirstAndTheNextRunIsProposed(t *testing.T) {
	h := newHarness(t)
	r := h.act(nil)
	failLaunches(h, 1, 3)
	for d := 0; d < 3; d++ {
		h.tickAt(at(d, 9, 0))
	}
	if got := h.ofOutcome(r, routines.Failed); len(got) != 3 || !strings.Contains(got[0].Reason, "boom") {
		t.Fatalf("records = %+v, want three failed ones with the reason", h.records(r))
	}
	h.wantTripped(r, 3)
}

func TestASuccessfulLaunchResetsTheBreakersCount(t *testing.T) {
	h := newHarness(t)
	r := h.act(nil)
	failLaunches(h, 1, 5)
	h.ships.set(func() {
		inner := h.ships.launch
		h.ships.launch = func(n int, q launcher.Request) (routines.ShipRef, *launcher.Error) {
			if n == 3 {
				return routines.ShipRef{Session: "ship-3", Status: "underway"}, nil
			}
			return inner(n, q)
		}
	})
	for d := 0; d < 5; d++ { // fail, fail, success, fail, fail
		h.tickAt(at(d, 9, 0))
	}
	if got := h.stored(r).Before; got != routines.Act {
		t.Errorf("stored Before acting = %q, want act: no three in a row", got)
	}
	if got := h.ofOutcome(r, routines.Breaker); len(got) != 0 {
		t.Errorf("breaker records = %+v, want none", got)
	}
}

func TestThreeUndoneLaunchesInARowSetAskFirst(t *testing.T) {
	h := newHarness(t)
	r := h.act(nil)
	for d := 0; d < 3; d++ {
		h.tickAt(at(d, 9, 0))
		if err := h.store.Undo("work", h.latest(r).ID); err != nil {
			t.Fatal(err)
		}
	}
	h.wantTripped(r, 3)
}

func TestThreeLaunchesWhoseShipsEndedFailedInARowSetAskFirst(t *testing.T) {
	h := newHarness(t)
	r := h.act(nil)
	for d := 0; d < 3; d++ {
		h.tickAt(at(d, 9, 0))
		rec := h.latest(r)
		h.ships.set(func() { h.ships.failed = map[string]bool{rec.Ship: true} })
		h.sched.Changed()
		h.tickAt(at(d, 9, 1))
		if got := h.latest(r); got.ID != rec.ID || got.Outcome != routines.Failed {
			t.Fatalf("after ship %s ended failed, its record = %+v, want it turned failed", rec.Ship, got)
		}
	}
	h.wantTripped(r, 3)
}

func TestRunSleepsUntilTheNextRunAndFollowsRoutineChangesWithoutARestart(t *testing.T) {
	h := newHarness(t)
	a := h.act(nil)                                                                            // 09:00
	sentinel := h.act(func(r *routines.Routine) { r.Name, r.When = "Sentinel", "0 11 * * *" }) // 11:00
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { defer close(done); h.sched.Run(ctx) }()
	t.Cleanup(func() { cancel(); <-done })
	keyA, keyS := "routine:"+a.ID, "routine:"+sentinel.ID

	// armed returns once the loop has gone round again after before.
	settle := func(before int) {
		t.Helper()
		eventually(t, "the scheduler to go round again", func() bool { return h.clock.armedCount() > before })
	}
	eventually(t, "a timer for the earliest next run, 09:00", func() bool { return h.clock.hasDeadline(at(0, 9, 0)) })
	before := h.clock.armedCount()
	h.clock.set(at(0, 9, 0))
	settle(before)
	if h.ships.callsFor(keyA) != 1 || h.ships.callsFor(keyS) != 0 {
		t.Fatalf("at 09:00 launches were A=%d sentinel=%d, want 1 and 0", h.ships.callsFor(keyA), h.ships.callsFor(keyS))
	}

	// off: A skips its next run while the sentinel still fires
	if _, err := h.store.SetEnabled("work", a.ID, false); err != nil {
		t.Fatal(err)
	}
	before = h.clock.armedCount()
	h.sched.Changed()
	settle(before)
	before = h.clock.armedCount()
	h.clock.set(at(0, 11, 0))
	settle(before)
	before = h.clock.armedCount()
	h.clock.set(at(1, 11, 0))
	settle(before)
	if h.ships.callsFor(keyA) != 1 || h.ships.callsFor(keyS) != 2 {
		t.Fatalf("with A off launches were A=%d sentinel=%d, want 1 and 2", h.ships.callsFor(keyA), h.ships.callsFor(keyS))
	}

	// on again, and its When edited to 12:00
	if _, err := h.store.SetEnabled("work", a.ID, true); err != nil {
		t.Fatal(err)
	}
	edited := a
	edited.When = "0 12 * * *"
	if _, err := h.store.Update("work", a.ID, edited); err != nil {
		t.Fatal(err)
	}
	h.sched.Changed()
	eventually(t, "a timer for the edited time", func() bool { return h.clock.hasDeadline(at(1, 12, 0)) })
	before = h.clock.armedCount()
	h.clock.set(at(1, 12, 0))
	settle(before)
	if h.ships.callsFor(keyA) != 2 {
		t.Fatalf("A launches = %d after its edited time, want 2", h.ships.callsFor(keyA))
	}

	// a routine saved while running
	c, err := h.store.Create("work", func() routines.Routine {
		in := launchRoutine()
		in.Name, in.When, in.Before = "Late", "0 15 * * *", routines.Act
		return in
	}())
	if err != nil {
		t.Fatal(err)
	}
	h.sched.Changed()
	eventually(t, "a timer for the new routine", func() bool { return h.clock.hasDeadline(at(1, 15, 0)) })
	before = h.clock.armedCount()
	h.clock.set(at(1, 15, 0))
	settle(before)
	if h.ships.callsFor("routine:"+c.ID) != 1 {
		t.Fatalf("the new routine launched %d times, want 1", h.ships.callsFor("routine:"+c.ID))
	}

	// deleted: its next run never comes
	if err := h.store.Delete("work", c.ID); err != nil {
		t.Fatal(err)
	}
	before = h.clock.armedCount()
	h.sched.Changed()
	settle(before)
	before = h.clock.armedCount()
	h.clock.set(at(2, 16, 0))
	settle(before)
	if h.ships.callsFor("routine:"+c.ID) != 1 || h.ships.callsFor(keyS) != 3 {
		t.Fatalf("after delete launches were new=%d sentinel=%d, want 1 and 3", h.ships.callsFor("routine:"+c.ID), h.ships.callsFor(keyS))
	}

	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not return when its context ended")
	}
	h.clock.set(at(3, 16, 0))
	if got := h.ships.callsFor(keyS); got != 3 {
		t.Errorf("sentinel launched after Run stopped: %d launches, want 3", got)
	}
}

func TestRunCatchesUpMissedRunsBeforeItWaits(t *testing.T) {
	h := newHarness(t)
	r := h.act(nil)
	seedLaunched(t, h, r, at(-2, 9, 0))
	h.clock.set(at(-1, 11, 0))
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { defer close(done); h.sched.Run(ctx) }()
	t.Cleanup(func() { cancel(); <-done })
	eventually(t, "the late run", func() bool { return h.ships.callsFor("routine:"+r.ID) == 1 })
}
