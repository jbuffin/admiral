package push_test

import (
	"encoding/json"
	"fmt"
	"testing"

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
