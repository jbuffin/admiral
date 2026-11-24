package push_test

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/jbuffin/admiral/internal/inbox"
	"github.com/jbuffin/admiral/internal/push"
	"github.com/jbuffin/admiral/internal/sessionhost"
	"github.com/jbuffin/admiral/internal/ships"
)

type rig struct {
	t        *testing.T
	w        *push.Watcher
	rec      *push.Recorder
	watching bool
	notify   map[string]bool // a fleet missing from it has Notify on
}

func newRig(t *testing.T) *rig {
	r := &rig{t: t, rec: &push.Recorder{}, notify: map[string]bool{}}
	r.w = push.NewWatcher(
		func() bool { return r.watching },
		func(fleet string) bool { on, ok := r.notify[fleet]; return !ok || on },
		r.rec,
	)
	return r
}

// started feeds the snapshots Admiral sees at startup, which are all seen, not new.
func (r *rig) started(sh []ships.Ship, items []inbox.Item) *rig {
	r.w.Ships(sh)
	r.w.Inbox(items)
	if n := len(r.rec.Events()); n != 0 {
		r.t.Fatalf("startup produced %d events, want none", n)
	}
	return r
}

func ship(name, status, reason string) ships.Ship {
	return ships.Ship{Session: "s-" + name, Name: name, Fleet: "work", FleetName: "Work", Status: status, Reason: reason,
		Crew: []ships.CrewBoat{}}
}

func needsYou(name, reason string) ships.Ship { return ship(name, ships.StatusNeedsYou, reason) }

func working(name string) ships.Ship { return ship(name, ships.StatusUnderway, "") }

func (r *rig) count() int { return len(r.rec.Events()) }

func TestShipThatNeedsYouForAQuestionIsOneEventEachTimeItComesBack(t *testing.T) {
	r := newRig(t).started([]ships.Ship{working("Dory")}, nil)

	r.w.Ships([]ships.Ship{needsYou("Dory", "question")})
	if got := r.rec.Events(); len(got) != 1 || got[0].Event.Name != "Dory" || got[0].Event.Fleet != "work" || got[0].Outcome != push.Push {
		t.Fatalf("after the question: %+v, want one Push event for Dory in work", got)
	}

	r.w.Ships([]ships.Ship{needsYou("Dory", "question")})
	if r.count() != 1 {
		t.Errorf("still waiting made %d events, want 1 in all", r.count())
	}

	r.w.Ships([]ships.Ship{working("Dory")})
	r.w.Ships([]ships.Ship{needsYou("Dory", "question")})
	if r.count() != 2 {
		t.Errorf("leaving and coming back made %d events, want 2 in all", r.count())
	}
}

func TestEveryReasonThatIsAQuestionOrApprovalPushes(t *testing.T) {
	for _, reason := range []string{"question", "permission", "permission:Bash", "notification:permission_prompt", "notification:elicitation_dialog", "bell"} {
		t.Run(reason, func(t *testing.T) {
			r := newRig(t).started([]ships.Ship{working("Dory")}, nil)
			r.w.Ships([]ships.Ship{needsYou("Dory", reason)})
			if r.count() != 1 {
				t.Errorf("reason %q made %d events, want 1", reason, r.count())
			}
		})
	}
}

func TestReasonsThatAreNotAQuestionCountButNeverPush(t *testing.T) {
	for _, reason := range []string{"stop", "idle", "unknown", "crew"} {
		t.Run(reason, func(t *testing.T) {
			r := newRig(t).started([]ships.Ship{working("Dory")}, nil)
			r.w.Ships([]ships.Ship{needsYou("Dory", reason)})
			if r.count() != 0 {
				t.Errorf("reason %q made %d events, want none", reason, r.count())
			}
		})
	}
}

func TestShipThatFinishedItsTurnThenAsksIsAnEvent(t *testing.T) {
	r := newRig(t).started([]ships.Ship{needsYou("Dory", "stop")}, nil)
	r.w.Ships([]ships.Ship{needsYou("Dory", "question")})
	if r.count() != 1 {
		t.Errorf("stop then question made %d events, want 1", r.count())
	}
}

func TestACrewBoatsOwnQuestionPushesTaggedPerBoat(t *testing.T) {
	boat := func(name, reason string) ships.CrewBoat {
		return ships.CrewBoat{Name: name, Status: "needs_you", Reason: reason}
	}
	withCrew := func(boats ...ships.CrewBoat) ships.Ship {
		s := needsYou("Dory", ships.ReasonCrew)
		s.Crew = boats
		return s
	}
	r := newRig(t).started([]ships.Ship{withCrew(boat("scout", "stop"))}, nil)

	r.w.Ships([]ships.Ship{withCrew(boat("scout", "stop"), boat("cook", "question"))})
	r.w.Ships([]ships.Ship{withCrew(boat("scout", "permission"), boat("cook", "question"))})

	got := r.rec.Events()
	if len(got) != 2 {
		t.Fatalf("got %d events, want one per boat that asks: %+v", len(got), got)
	}
	if got[0].Event.Key == got[1].Event.Key || got[0].Event.Key == "" {
		t.Errorf("tags %q and %q, want a different one per boat", got[0].Event.Key, got[1].Event.Key)
	}
}

func TestShipThatAnchorsIsOneEventButASailingOffShipIsNot(t *testing.T) {
	r := newRig(t).started([]ships.Ship{working("Dory"), working("Nemo")}, nil)

	r.w.Ships([]ships.Ship{ship("Dory", ships.StatusAnchored, ""), ship("Nemo", ships.StatusSailingOff, "")})
	r.w.Ships([]ships.Ship{ship("Dory", ships.StatusAnchored, ""), ship("Nemo", ships.StatusSailingOff, "")})

	got := r.rec.Events()
	if len(got) != 1 || got[0].Event.Name != "Dory" || got[0].Event.Kind != push.Anchored {
		t.Fatalf("got %+v, want one Anchored event for Dory", got)
	}
}

func TestMooredShipThatBecomesReadyToMergeIsOneEvent(t *testing.T) {
	moored := func(ready bool) ships.Ship {
		s := ship("Dory", ships.StatusMoored, "")
		s.StackReady, s.PolledAt = ready, "2026-10-22T10:00:00Z"
		return s
	}
	r := newRig(t).started([]ships.Ship{moored(false)}, nil)

	r.w.Ships([]ships.Ship{moored(true)})
	r.w.Ships([]ships.Ship{moored(true)})
	if got := r.rec.Events(); len(got) != 1 || got[0].Event.Kind != push.ReadyToMerge {
		t.Fatalf("got %+v, want one ReadyToMerge event", got)
	}

	r.w.Ships([]ships.Ship{moored(false)})
	r.w.Ships([]ships.Ship{moored(true)})
	if r.count() != 2 {
		t.Errorf("ready, not ready, ready made %d events, want 2", r.count())
	}
}

func TestWhatAlreadyWaitsAtStartupIsSeenNotNew(t *testing.T) {
	waiting := needsYou("Dory", "question")
	anchored := ship("Nemo", ships.StatusAnchored, "")
	ready := ship("Marlin", ships.StatusMoored, "")
	ready.StackReady = true
	r := newRig(t).started(
		[]ships.Ship{waiting, anchored, ready},
		[]inbox.Item{item(t, "s-Dory", "q-1", "question", "Which way?")},
	)

	r.w.Ships([]ships.Ship{waiting, anchored, ready})
	r.w.Inbox([]inbox.Item{item(t, "s-Dory", "q-1", "question", "Which way?")})
	if r.count() != 0 {
		t.Errorf("what was already waiting made %d events, want none", r.count())
	}
}

func TestShipThatMoorsAfterStartupAlreadyReadyToMergeIsNew(t *testing.T) {
	r := newRig(t).started([]ships.Ship{working("Dory")}, nil)
	ready := ship("Nemo", ships.StatusMoored, "")
	ready.StackReady, ready.PolledAt = true, "2026-10-22T10:00:00Z"

	r.w.Ships([]ships.Ship{working("Dory"), ready})
	r.w.Ships([]ships.Ship{working("Dory"), ready})
	if got := r.rec.Events(); len(got) != 1 || got[0].Event.Kind != push.ReadyToMerge {
		t.Errorf("a ship that first showed up already ready made %+v, want one ReadyToMerge", got)
	}

	moored := ready
	moored.Name, moored.Session = "Dory", "s-Dory"
	r.w.Ships([]ships.Ship{moored, ready})
	if r.count() != 2 {
		t.Errorf("a ship working at startup that moored already ready made %d events in all, want 2", r.count())
	}
}

func TestShipLaunchedAfterStartupThatAsksIsNew(t *testing.T) {
	r := newRig(t).started(nil, nil)
	r.w.Ships([]ships.Ship{needsYou("Dory", "question")})
	if r.count() != 1 {
		t.Errorf("a new ship's question made %d events, want 1", r.count())
	}
}

func item(t *testing.T, session, id, kind, title string) inbox.Item {
	t.Helper()
	body := map[string]any{"version": 1, "id": id, "kind": kind, "created": "2026-10-22T10:00:00Z", "title": title}
	switch kind {
	case "question":
		body[kind] = map[string]any{"questions": []any{map[string]any{"id": "1", "text": "Pick", "options": []any{map[string]any{"label": "A"}}}}}
	case "review", "findings":
		body[kind] = map[string]any{"pr_url": "https://example.com/pull/1", "review_id": 1}
	case "notice":
		body[kind] = map[string]any{"body": "FYI"}
	case "allow_rules":
		body[kind] = map[string]any{"fleet": "work", "cards": []any{map[string]any{"id": "c1", "harness": "claude", "repo": "acme/widgets",
			"rule": "Bash(gh pr view:*)", "count": 1, "prompts": []any{map[string]any{"ship": "dory", "id": "p1"}}}}}
	case "routine_proposal":
		body[kind] = map[string]any{"fleet": "work", "routine": "r1", "routine_name": "Nightly sweep", "trigger": "t1", "action": "launch", "expires_at": "2099-01-01T00:00:00Z"}
	case "wake_proposal":
		body[kind] = map[string]any{"ship": "Dory", "session": "dory", "pr": "https://github.com/acme/widgets/pull/7", "login": "reviewer-1",
			"events": []any{map[string]any{"id": "e1", "kind": "review", "at": "2026-10-22T09:05:00Z", "body": "x"}}}
	}
	b, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	it, err := inbox.ParseItem(b, session, id)
	if err != nil {
		t.Fatalf("item %s (%s): %v", id, kind, err)
	}
	it.Fleet = "work"
	return it
}

func TestNewInboxItemsOfAPushingKindAreOneEventEach(t *testing.T) {
	for _, kind := range []string{"question", "findings", "wake_proposal"} {
		t.Run(kind, func(t *testing.T) {
			session := "s-Dory"
			if kind == "wake_proposal" {
				session = inbox.AdmiralSession
			}
			r := newRig(t).started(nil, nil)
			it := item(t, session, "i-1", kind, "Something to look at")

			r.w.Inbox([]inbox.Item{it})
			r.w.Inbox([]inbox.Item{it})
			if got := r.rec.Events(); len(got) != 1 || got[0].Event.Key == "" || got[0].Event.Fleet != "work" {
				t.Fatalf("an item listed twice made %+v, want one tagged event for fleet work", got)
			}
		})
	}
}

func TestInboxItemsThatOnlyCountNeverMakeAnEvent(t *testing.T) {
	r := newRig(t).started(nil, nil)
	r.w.Inbox([]inbox.Item{
		item(t, inbox.AdmiralSession, "n-1", "notice", "Config migrated"),
		item(t, "s-Dory", "r-1", "review", "Review PR 1"),
		item(t, inbox.AdmiralSession, "allow-rules-work", "allow_rules", "Allow rules for work"),
	})
	if r.count() != 0 {
		t.Errorf("a notice and a review made %d events, want none", r.count())
	}
}

func TestAnItemsEventLineIsItsTitle(t *testing.T) {
	r := newRig(t).started(nil, nil)
	r.w.Inbox([]inbox.Item{item(t, "s-Dory", "q-1", "question", "Which way?")})
	got := r.rec.Events()
	if len(got) != 1 || got[0].Event.Text != "Which way?" {
		t.Fatalf("got %+v, want one event whose line is the title", got)
	}
}

func TestChatQuestionNamesAdmiralBot(t *testing.T) {
	r := newRig(t).started(nil, nil)
	it := item(t, sessionhost.ChatSession("work"), "cq-1", "question", "Should I archive the stale branches?")
	it.Name = "AdmiralBot"
	r.w.Inbox([]inbox.Item{it})
	got := r.rec.Events()
	if len(got) != 1 || got[0].Event.Kind != push.ChatQuestion || got[0].Event.Name != "AdmiralBot" {
		t.Fatalf("got %+v, want one ChatQuestion event named AdmiralBot", got)
	}
}

func TestWatchedWindowGetsAnAlertNotAPush(t *testing.T) {
	r := newRig(t).started([]ships.Ship{working("Dory")}, nil)
	r.watching = true
	r.w.Ships([]ships.Ship{needsYou("Dory", "question")})
	if got := r.rec.Events(); len(got) != 1 || got[0].Outcome != push.Alert {
		t.Fatalf("got %+v, want one Alert", got)
	}
}

func TestFleetWithNotifyOffMakesNoEventAndAFleetlessEventStillDoes(t *testing.T) {
	r := newRig(t).started([]ships.Ship{working("Dory")}, nil)
	r.notify["work"] = false
	r.w.Ships([]ships.Ship{needsYou("Dory", "question")})
	if r.count() != 0 {
		t.Fatalf("a fleet with Notify off made %d events, want none", r.count())
	}

	r.notify["work"] = false
	it := item(t, "s-x", "q-9", "question", "No fleet on this one")
	it.Fleet = ""
	r.w.Inbox([]inbox.Item{it})
	if got := r.rec.Events(); len(got) != 1 {
		t.Errorf("an item with no fleet made %d events, want 1: Notify counts as on", len(got))
	}
}

func TestHookPointKindsReachTheSinksThroughEmit(t *testing.T) {
	for _, k := range []push.Kind{push.CodeApproval, push.RoutineNotify} {
		t.Run(string(k), func(t *testing.T) {
			r := newRig(t)
			r.w.Emit(push.Event{Kind: k, Fleet: "work", Key: fmt.Sprint(k, "-1"), Name: "Dory", Text: "needs a look"})
			if got := r.rec.Events(); len(got) != 1 || got[0].Outcome != push.Push {
				t.Errorf("got %+v, want one Push", got)
			}
		})
	}
}

func TestEventsCarryWhereATapLands(t *testing.T) {
	moored := ship("Nemo", ships.StatusMoored, "")
	moored.PolledAt = "2026-10-22T10:00:00Z"
	r := newRig(t).started([]ships.Ship{working("Dory"), moored}, nil)
	r.w.Ships([]ships.Ship{working("Dory"), moored})
	moored.StackReady = true
	boat := needsYou("Marlin", ships.ReasonCrew)
	boat.Crew = []ships.CrewBoat{{Name: "cook", Status: "needs_you", Reason: "question"}}
	r.w.Ships([]ships.Ship{ship("Dory", ships.StatusAnchored, ""), moored, boat})
	r.w.Inbox([]inbox.Item{item(t, "s-Dory", "q-1", "question", "Which way?")})

	got := map[push.Kind]push.Payload{}
	for _, d := range r.rec.Events() {
		got[d.Event.Kind] = push.PayloadOf(d.Event)
	}
	if p := got[push.Anchored]; p.Target != push.TargetShip || p.Session != "s-Dory" || p.Fleet != "work" {
		t.Errorf("anchored payload = %+v, want the ship's own view", p)
	}
	if p := got[push.ReadyToMerge]; p.Target != push.TargetPeek || p.Session != "s-Nemo" || !p.Merge {
		t.Errorf("ready payload = %+v, want a merge peek", p)
	}
	if p := got[push.ShipQuestion]; p.Session != "s-Marlin" || p.Crew != "cook" {
		t.Errorf("crew payload = %+v, want a peek of boat cook", p)
	}
	if p := got[push.InboxQuestion]; p.Session != "s-Dory" || p.Item != "q-1" {
		t.Errorf("item payload = %+v, want a peek of item q-1", p)
	}
}

func TestRestartDoesNotPushReadyToMergeForShipsWhosePollHasNotAnsweredYet(t *testing.T) {
	unpolled := ship("Dory", ships.StatusMoored, "")
	polled := unpolled
	polled.StackReady, polled.PolledAt = true, "2026-10-22T10:00:00Z"
	r := newRig(t).started([]ships.Ship{unpolled}, nil)

	r.w.Ships([]ships.Ship{polled})
	if r.count() != 0 {
		t.Fatalf("the first answered poll of an already ready ship made %d events, want none", r.count())
	}

	notReady := polled
	notReady.StackReady = false
	r.w.Ships([]ships.Ship{notReady})
	r.w.Ships([]ships.Ship{polled})
	if got := r.rec.Events(); len(got) != 1 || got[0].Event.Kind != push.ReadyToMerge {
		t.Errorf("ready turning true after the poll answered made %+v, want one ReadyToMerge", got)
	}
}

func TestWakeProposalFollowsItsShipsFleetsNotify(t *testing.T) {
	dory := ship("Dory", ships.StatusMoored, "")
	dory.Session, dory.Fleet = "dory", "off"
	r := newRig(t).started([]ships.Ship{dory}, nil)
	r.notify["off"] = false

	it := item(t, inbox.AdmiralSession, "wake-1", "wake_proposal", "Feedback on Dory")
	it.Fleet = ""
	r.w.Inbox([]inbox.Item{it})
	if r.count() != 0 {
		t.Fatalf("a wake proposal for a Notify-off fleet's ship made %d events, want none", r.count())
	}
}

type panicSink struct{}

func (panicSink) Deliver(push.Decided) { panic("boom") }

func TestAPanickingSinkDoesNotStopTheNextOneHearingTheEvent(t *testing.T) {
	rec := &push.Recorder{}
	w := push.NewWatcher(func() bool { return false }, func(string) bool { return true }, panicSink{}, rec)
	w.Emit(push.Event{Kind: push.ShipQuestion, Key: "s-1", Name: "Dory", Text: "asks"})
	w.Emit(push.Event{Kind: push.ShipQuestion, Key: "s-2", Name: "Dory", Text: "asks"})
	if got := len(rec.Events()); got != 2 {
		t.Fatalf("the sink after a panicking one heard %d events, want 2", got)
	}
}

var launchedAt = time.Date(2026, 11, 24, 10, 0, 0, 0, time.UTC)

// clocked puts the watcher's clock at launchedAt plus after, and returns a way to move it.
func (r *rig) clocked(after time.Duration) func(time.Duration) {
	now := launchedAt.Add(after)
	r.w.WithClock(func() time.Time { return now })
	return func(to time.Duration) { now = launchedAt.Add(to) }
}

func notStarted(name string) ships.Ship {
	sh := needsYou(name, "not_started")
	sh.Launched = launchedAt.Format(time.RFC3339)
	return sh
}

func TestAShipThatNeverStartedPushesAsAnAskOnceItHasWaitedPastTheGrace(t *testing.T) {
	r := newRig(t).started([]ships.Ship{working("Dory")}, nil)
	move := r.clocked(15 * time.Second)

	r.w.Ships([]ships.Ship{notStarted("Dory")})
	if r.count() != 0 {
		t.Fatalf("pushed at 15 s: %+v, want nothing yet", r.rec.Events())
	}
	if !r.w.Waiting() {
		t.Error("Waiting() = false inside the grace, so nothing would look again")
	}

	// No new event arrives: the feed's own tick looks again.
	move(31 * time.Second)
	r.w.Ships([]ships.Ship{notStarted("Dory")})
	got := r.rec.Events()
	if len(got) != 1 || got[0].Outcome != push.Push || got[0].Event.Text != "needs you to finish starting" {
		t.Errorf("events = %+v, want one Push saying it needs you to finish starting", got)
	}
	if r.w.Waiting() {
		t.Error("Waiting() stays true after the push")
	}
	r.w.Ships([]ships.Ship{notStarted("Dory")})
	if r.count() != 1 {
		t.Errorf("pushed again for the same wait: %d events", r.count())
	}
}

func TestASessionThatStartsInsideTheGraceNeverPushes(t *testing.T) {
	r := newRig(t).started([]ships.Ship{working("Dory")}, nil)
	move := r.clocked(15 * time.Second)

	r.w.Ships([]ships.Ship{notStarted("Dory")})
	move(20 * time.Second)
	r.w.Ships([]ships.Ship{working("Dory")})
	move(40 * time.Second)
	r.w.Ships([]ships.Ship{working("Dory")})
	if r.count() != 0 || r.w.Waiting() {
		t.Errorf("events = %+v waiting %v, want none and not waiting", r.rec.Events(), r.w.Waiting())
	}
}

func TestACrewBoatThatNeverStartedPushesTaggedPerBoatAfterTheGrace(t *testing.T) {
	r := newRig(t).started([]ships.Ship{working("Dory")}, nil)
	move := r.clocked(15 * time.Second)
	lifted := needsYou("Dory", ships.ReasonCrew)
	lifted.Launched = launchedAt.Add(-time.Hour).Format(time.RFC3339)
	lifted.Crew = []ships.CrewBoat{{Name: "mate", Status: "needs_you", Reason: "not_started", Launched: launchedAt.Format(time.RFC3339)}}

	r.w.Ships([]ships.Ship{lifted})
	if r.count() != 0 {
		t.Fatalf("pushed at 15 s: %+v, want nothing yet", r.rec.Events())
	}
	move(31 * time.Second)
	r.w.Ships([]ships.Ship{lifted})
	got := r.rec.Events()
	if len(got) != 1 || got[0].Event.Crew != "mate" || got[0].Event.Text != "mate needs you to finish starting" {
		t.Errorf("events = %+v, want one for boat mate that needs you to finish starting", got)
	}
}

func TestTheFeedLooksAgainAtANotStartedShipWithNoFurtherEvent(t *testing.T) {
	var mu sync.Mutex
	after := 15 * time.Second
	list := []ships.Ship{working("Dory")}
	rec := &push.Recorder{}
	w := push.NewWatcher(func() bool { return false }, func(string) bool { return true }, rec).
		WithClock(func() time.Time { mu.Lock(); defer mu.Unlock(); return launchedAt.Add(after) })
	f := &push.Feed{Watcher: w, Recheck: 10 * time.Millisecond, Items: func() []inbox.Item { return nil },
		Ships: func() []ships.Ship { mu.Lock(); defer mu.Unlock(); return list }}
	ctx, stop := context.WithCancel(context.Background())
	defer stop()
	go f.Run(ctx)

	f.ShipsChanged()
	until := func(what string, ok func() bool) {
		t.Helper()
		for end := time.Now().Add(5 * time.Second); !ok(); time.Sleep(5 * time.Millisecond) {
			if time.Now().After(end) {
				t.Fatalf("never saw: %s", what)
			}
		}
	}
	time.Sleep(50 * time.Millisecond) // the startup snapshot is read before the ship changes
	mu.Lock()
	list = []ships.Ship{notStarted("Dory")}
	mu.Unlock()
	f.ShipsChanged()
	until("the feed waiting on the not-started ship", w.Waiting)
	if n := len(rec.Events()); n != 0 {
		t.Fatalf("pushed inside the grace: %d events", n)
	}

	mu.Lock()
	after = 31 * time.Second
	mu.Unlock()
	until("a push with no further event", func() bool { return len(rec.Events()) == 1 })
}
