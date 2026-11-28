package push_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/jbuffin/admiral/internal/inbox"
	"github.com/jbuffin/admiral/internal/push"
	"github.com/jbuffin/admiral/internal/sessionhost"
	"github.com/jbuffin/admiral/internal/ships"
)

// clears are the Clear outcomes the sink has heard, in order.
func (r *rig) clears() []push.Decided {
	var out []push.Decided
	for _, d := range r.rec.Events() {
		if d.Outcome == push.Clear {
			out = append(out, d)
		}
	}
	return out
}

// pushed is the Push outcomes the sink has heard, in order.
func (r *rig) pushed() []push.Decided {
	var out []push.Decided
	for _, d := range r.rec.Events() {
		if d.Outcome == push.Push {
			out = append(out, d)
		}
	}
	return out
}

// wantOneClearOf fails unless exactly one clear has gone out and it names the pushed event: its tag, kind, fleet and session.
func (r *rig) wantOneClearOf(pushed push.Event) {
	r.t.Helper()
	got := r.clears()
	if len(got) != 1 {
		r.t.Fatalf("got %d clears %+v, want exactly one for %q", len(got), got, pushed.Key)
	}
	e := got[0].Event
	if e.Key != pushed.Key || e.Kind != pushed.Kind || e.Fleet != pushed.Fleet || e.Session != pushed.Session {
		r.t.Errorf("clear names key %q kind %q fleet %q session %q, want key %q kind %q fleet %q session %q",
			e.Key, e.Kind, e.Fleet, e.Session, pushed.Key, pushed.Kind, pushed.Fleet, pushed.Session)
	}
}

func mooredShip(name string, ready bool) ships.Ship {
	s := ship(name, ships.StatusMoored, "")
	s.StackReady, s.PolledAt = ready, "2026-10-22T10:00:00Z"
	return s
}

func TestClearHasAName(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			t.Errorf("Clear.String() panicked: %v, want %q", r, "clear")
		}
	}()
	if got := push.Clear.String(); got != "clear" {
		t.Errorf("Clear.String() = %q, want %q", got, "clear")
	}
}

func TestAnInboxItemThatPushedClearsOnceWhenItIsAnsweredOrRemoved(t *testing.T) {
	for _, c := range []struct{ name, session, kind string }{
		{"question", "s-Dory", "question"},
		{"findings", "s-Dory", "findings"},
		{"wake_proposal", inbox.AdmiralSession, "wake_proposal"},
		{"chat question", sessionhost.ChatSession("work"), "question"},
	} {
		t.Run(c.name, func(t *testing.T) {
			r := newRig(t).started(nil, nil)
			it := item(t, c.session, "i-1", c.kind, "Which way?")
			r.w.Inbox([]inbox.Item{it})
			pushed := r.pushed()
			if len(pushed) != 1 {
				t.Fatalf("got %d pushes, want 1", len(pushed))
			}
			if len(r.clears()) != 0 {
				t.Fatalf("a still-waiting item cleared: %+v", r.clears())
			}

			r.w.Inbox(nil)
			r.wantOneClearOf(pushed[0].Event)

			r.w.Inbox(nil)
			if n := len(r.clears()); n != 1 {
				t.Errorf("a second empty snapshot made %d clears in all, want 1", n)
			}
		})
	}
}

func TestAnInboxItemThatWaitedAtStartupNeverClears(t *testing.T) {
	r := newRig(t).started(nil, []inbox.Item{item(t, "s-Dory", "i-1", "question", "Which way?")})
	r.w.Inbox(nil)
	if got := r.clears(); len(got) != 0 {
		t.Errorf("an item that never pushed cleared: %+v", got)
	}
}

func TestOneInboxItemAnsweredClearsOnlyItsOwnTag(t *testing.T) {
	r := newRig(t).started(nil, nil)
	a, b := item(t, "s-Dory", "i-1", "question", "A?"), item(t, "s-Dory", "i-2", "question", "B?")
	r.w.Inbox([]inbox.Item{a, b})
	r.w.Inbox([]inbox.Item{b})
	got := r.clears()
	if len(got) != 1 || got[0].Event.Key != "s-Dory/i-1" {
		t.Fatalf("got %+v, want one clear tagged s-Dory/i-1", got)
	}
}

func TestANeedsYouStopThatPushedClearsOnceWhenAnsweredOrMovedOn(t *testing.T) {
	for name, next := range map[string]ships.Ship{
		"answered":      working("Dory"),
		"finished turn": needsYou("Dory", "stop"),
		"anchored":      ship("Dory", ships.StatusAnchored, ""),
	} {
		t.Run(name, func(t *testing.T) {
			r := newRig(t).started([]ships.Ship{working("Dory")}, nil)
			r.w.Ships([]ships.Ship{needsYou("Dory", "question")})
			pushed := r.pushed()
			if len(pushed) != 1 {
				t.Fatalf("got %d pushes, want 1", len(pushed))
			}
			r.w.Ships([]ships.Ship{needsYou("Dory", "question")})
			if len(r.clears()) != 0 {
				t.Fatalf("a ship still asking cleared: %+v", r.clears())
			}

			r.w.Ships([]ships.Ship{next})
			var asked []push.Decided
			for _, c := range r.clears() {
				if c.Event.Key == pushed[0].Event.Key {
					asked = append(asked, c)
				}
			}
			if len(asked) != 1 || asked[0].Event.Kind != push.ShipQuestion || asked[0].Event.Fleet != "work" || asked[0].Event.Session != "s-Dory" {
				t.Fatalf("got %+v, want one ShipQuestion clear tagged %q", r.clears(), pushed[0].Event.Key)
			}

			r.w.Ships([]ships.Ship{next})
			n := 0
			for _, c := range r.clears() {
				if c.Event.Key == pushed[0].Event.Key {
					n++
				}
			}
			if n != 1 {
				t.Errorf("a second snapshot made %d clears of the question, want 1 in all", n)
			}
		})
	}
}

func TestACrewBoatThatPushedClearsItsOwnTagWhenItStopsAsking(t *testing.T) {
	boat := func(reason string) ships.CrewBoat {
		return ships.CrewBoat{Name: "cook", Status: "needs_you", Reason: reason}
	}
	withBoat := func(reason string) ships.Ship {
		s := needsYou("Dory", ships.ReasonCrew)
		s.Crew = []ships.CrewBoat{boat(reason)}
		return s
	}
	r := newRig(t).started([]ships.Ship{withBoat("stop")}, nil)
	r.w.Ships([]ships.Ship{withBoat("question")})
	pushed := r.pushed()
	if len(pushed) != 1 || pushed[0].Event.Key != "s-Dory/cook" {
		t.Fatalf("got %+v, want one push tagged s-Dory/cook", pushed)
	}
	r.w.Ships([]ships.Ship{withBoat("stop")})
	r.wantOneClearOf(pushed[0].Event)
}

func TestAShipThatLeavesAnchoredClearsItsAnchoredPushOnce(t *testing.T) {
	r := newRig(t).started([]ships.Ship{working("Dory")}, nil)
	r.w.Ships([]ships.Ship{ship("Dory", ships.StatusAnchored, "")})
	pushed := r.pushed()
	if len(pushed) != 1 || pushed[0].Event.Kind != push.Anchored {
		t.Fatalf("got %+v, want one Anchored push", pushed)
	}
	r.w.Ships([]ships.Ship{ship("Dory", ships.StatusAnchored, "")})
	if len(r.clears()) != 0 {
		t.Fatalf("a still anchored ship cleared: %+v", r.clears())
	}

	r.w.Ships([]ships.Ship{working("Dory")})
	r.wantOneClearOf(pushed[0].Event)
	if pushed[0].Event.Key != "s-Dory:anchored" {
		t.Errorf("pushed tag %q, want s-Dory:anchored", pushed[0].Event.Key)
	}

	r.w.Ships([]ships.Ship{working("Dory")})
	if n := len(r.clears()); n != 1 {
		t.Errorf("a second snapshot made %d clears in all, want 1", n)
	}
}

func TestAShipThatStopsBeingReadyToMergeClearsItsReadyPushOnce(t *testing.T) {
	r := newRig(t).started([]ships.Ship{mooredShip("Dory", false)}, nil)
	r.w.Ships([]ships.Ship{mooredShip("Dory", true)})
	pushed := r.pushed()
	if len(pushed) != 1 || pushed[0].Event.Kind != push.ReadyToMerge {
		t.Fatalf("got %+v, want one ReadyToMerge push", pushed)
	}
	r.w.Ships([]ships.Ship{mooredShip("Dory", true)})
	if len(r.clears()) != 0 {
		t.Fatalf("a still ready ship cleared: %+v", r.clears())
	}

	r.w.Ships([]ships.Ship{mooredShip("Dory", false)})
	r.wantOneClearOf(pushed[0].Event)
	if pushed[0].Event.Key != "s-Dory:ready" {
		t.Errorf("pushed tag %q, want s-Dory:ready", pushed[0].Event.Key)
	}

	r.w.Ships([]ships.Ship{mooredShip("Dory", false)})
	if n := len(r.clears()); n != 1 {
		t.Errorf("a second snapshot made %d clears in all, want 1", n)
	}
}

func TestAShipThatMergesAwayFromReadyClearsItsReadyPush(t *testing.T) {
	r := newRig(t).started([]ships.Ship{mooredShip("Dory", false)}, nil)
	r.w.Ships([]ships.Ship{mooredShip("Dory", true)})
	r.w.Ships([]ships.Ship{working("Dory")})
	if got := r.clears(); len(got) != 1 || got[0].Event.Key != "s-Dory:ready" {
		t.Errorf("got %+v, want one clear tagged s-Dory:ready", got)
	}
}

func TestAShipThatDropsOutOfTheListClearsEveryKeyOfItsThatPushed(t *testing.T) {
	withBoat := needsYou("Dory", "question")
	withBoat.Crew = []ships.CrewBoat{{Name: "cook", Status: "needs_you", Reason: "question"}}
	r := newRig(t).started([]ships.Ship{working("Dory"), working("Nemo")}, nil)
	r.w.Ships([]ships.Ship{withBoat, ship("Nemo", ships.StatusAnchored, "")})
	if n := len(r.pushed()); n != 3 {
		t.Fatalf("got %d pushes, want 3: Dory's question, its boat's, Nemo's anchor", n)
	}

	r.w.Ships(nil)
	tags := map[string]int{}
	for _, c := range r.clears() {
		tags[c.Event.Key]++
	}
	want := map[string]int{"s-Dory": 1, "s-Dory/cook": 1, "s-Nemo:anchored": 1}
	if len(tags) != len(want) {
		t.Fatalf("clears by tag %v, want %v", tags, want)
	}
	for k, n := range want {
		if tags[k] != n {
			t.Errorf("clears of %q = %d, want %d (all: %v)", k, tags[k], n, tags)
		}
	}
}

func TestAKeyThatPushesAgainAfterItClearedClearsAgain(t *testing.T) {
	r := newRig(t).started([]ships.Ship{working("Dory")}, nil)
	for i := 1; i <= 2; i++ {
		r.w.Ships([]ships.Ship{needsYou("Dory", "question")})
		r.w.Ships([]ships.Ship{working("Dory")})
		if n := len(r.clears()); n != i {
			t.Fatalf("after round %d: %d clears, want %d", i, n, i)
		}
	}
}

func TestAKeyThatOnlyAlertedNeverClears(t *testing.T) {
	r := newRig(t).started([]ships.Ship{working("Dory")}, nil)
	r.watching = true
	r.w.Ships([]ships.Ship{needsYou("Dory", "question")})
	r.w.Inbox([]inbox.Item{item(t, "s-Dory", "i-1", "question", "Which way?")})
	if got := r.rec.Events(); len(got) != 2 || got[0].Outcome != push.Alert || got[1].Outcome != push.Alert {
		t.Fatalf("got %+v, want two Alerts", got)
	}
	r.w.Ships([]ships.Ship{working("Dory")})
	r.w.Inbox(nil)
	if got := r.clears(); len(got) != 0 {
		t.Errorf("an alerted key cleared: %+v", got)
	}
}

func TestAKeyDroppedForNotifyOffOrNotPushingNeverClears(t *testing.T) {
	r := newRig(t).started([]ships.Ship{working("Dory"), working("Nemo")}, nil)
	r.notify["work"] = false
	r.w.Ships([]ships.Ship{needsYou("Dory", "question"), ship("Nemo", ships.StatusAnchored, "")})
	r.w.Inbox([]inbox.Item{item(t, "s-Dory", "i-1", "question", "Which way?")})
	if n := len(r.rec.Events()); n != 0 {
		t.Fatalf("Notify off made %d events, want none", n)
	}
	r.w.Ships([]ships.Ship{working("Dory"), working("Nemo")})
	r.w.Inbox(nil)
	if got := r.clears(); len(got) != 0 {
		t.Errorf("a dropped key cleared: %+v", got)
	}
}

func TestAFinishedTurnThatNeverPushedNeverClears(t *testing.T) {
	r := newRig(t).started([]ships.Ship{working("Dory")}, nil)
	r.w.Ships([]ships.Ship{needsYou("Dory", "stop")})
	r.w.Ships([]ships.Ship{working("Dory")})
	if got := r.rec.Events(); len(got) != 0 {
		t.Errorf("a finished turn made %+v, want nothing", got)
	}
}

func TestAKeyThatPushedStillClearsAfterItsFleetSwitchedNotifyOff(t *testing.T) {
	r := newRig(t).started([]ships.Ship{working("Dory")}, nil)
	r.w.Ships([]ships.Ship{needsYou("Dory", "question")})
	r.notify["work"] = false
	r.w.Ships([]ships.Ship{working("Dory")})
	if got := r.clears(); len(got) != 1 || got[0].Event.Key != "s-Dory" {
		t.Errorf("got %+v, want one clear tagged s-Dory: the notification is already on the device", got)
	}
}

func TestAKeyThatPushedStillClearsAfterAWindowBecameWatched(t *testing.T) {
	r := newRig(t).started(nil, nil)
	r.w.Inbox([]inbox.Item{item(t, "s-Dory", "i-1", "question", "Which way?")})
	r.watching = true
	r.w.Inbox(nil)
	if got := r.clears(); len(got) != 1 || got[0].Event.Key != "s-Dory/i-1" {
		t.Errorf("got %+v, want one clear tagged s-Dory/i-1", got)
	}
}

func TestPushOnlyKindsNeverClear(t *testing.T) {
	for _, k := range []push.Kind{push.ReportReply, push.DigestReply, push.RoutineNotify} {
		t.Run(string(k), func(t *testing.T) {
			r := newRig(t).started([]ships.Ship{working("Dory")}, nil)
			r.w.Emit(push.Event{Kind: k, Fleet: "work", Key: string(k) + "-1", Name: "Dory", Text: "done", Session: "s-Dory"})
			if n := len(r.pushed()); n != 1 {
				t.Fatalf("got %d pushes, want 1", n)
			}
			r.w.Ships([]ships.Ship{needsYou("Dory", "question")})
			r.w.Ships(nil)
			r.w.Inbox(nil)
			for _, c := range r.clears() {
				if c.Event.Kind == k {
					t.Errorf("a %s cleared: %+v", k, c)
				}
			}
		})
	}
}

func TestAShipListReadWhileTheHostIsNotReadyClearsNothing(t *testing.T) {
	var mu sync.Mutex
	ready, list, reads, refused := true, []ships.Ship{working("Dory")}, 0, 0
	rec := &push.Recorder{}
	w := push.NewWatcher(func() bool { return false }, func(string) bool { return true }, rec)
	f := &push.Feed{Watcher: w, Items: func() []inbox.Item { return nil },
		Ships: func() []ships.Ship { mu.Lock(); defer mu.Unlock(); reads++; return list },
		ShipsReady: func() bool {
			mu.Lock()
			defer mu.Unlock()
			if !ready {
				refused++
			}
			return ready
		}}
	ctx, stop := context.WithCancel(context.Background())
	defer stop()
	go f.Run(ctx)

	until := func(what string, ok func() bool) {
		t.Helper()
		for end := time.Now().Add(5 * time.Second); !ok(); time.Sleep(5 * time.Millisecond) {
			if time.Now().After(end) {
				t.Fatalf("never saw: %s", what)
			}
		}
	}
	set := func(r bool, l ...ships.Ship) {
		mu.Lock()
		ready, list = r, l
		mu.Unlock()
		f.ShipsChanged()
	}
	count := func(o push.Outcome, key string) (n int) {
		for _, d := range rec.Events() {
			if d.Outcome == o && d.Event.Key == key {
				n++
			}
		}
		return n
	}

	f.ShipsChanged()
	until("the startup snapshot", func() bool { mu.Lock(); defer mu.Unlock(); return reads > 0 })
	set(true, needsYou("Dory", "question"))
	until("Dory's push", func() bool { return count(push.Push, "s-Dory") == 1 })

	set(false)
	until("the feed refusing the list", func() bool { mu.Lock(); defer mu.Unlock(); return refused > 0 })

	set(true, needsYou("Dory", "question"), needsYou("Nemo", "question"))
	until("Nemo's push", func() bool { return count(push.Push, "s-Nemo") == 1 })
	if n := count(push.Clear, "s-Dory"); n != 0 {
		t.Errorf("Dory, still asking, got %d clears across a not-ready list", n)
	}
	if n := count(push.Push, "s-Dory"); n != 1 {
		t.Errorf("Dory pushed %d times, want once", n)
	}
}
