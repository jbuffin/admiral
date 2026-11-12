package push_test

import (
	"strings"
	"testing"

	"github.com/jbuffin/admiral/internal/inbox"
	"github.com/jbuffin/admiral/internal/push"
	"github.com/jbuffin/admiral/internal/routines"
)

func routineNotifier(r *rig) *push.RoutineNotifier {
	return &push.RoutineNotifier{W: r.w, FleetName: func(string) string { return "Work" }}
}

func TestARoutineRecordPushesWithTheTapTargetItsOutcomeHas(t *testing.T) {
	rt := routines.Routine{ID: "r1", Name: "Nightly sweep", Notify: true}
	for _, c := range []struct {
		name                 string
		tr                   routines.Trigger
		target, session, itm string
	}{
		{"launched with its ship", routines.Trigger{ID: "t1", Routine: "r1", Outcome: routines.Launched, Ship: "dory"}, push.TargetShip, "dory", ""},
		{"proposed opens the proposal", routines.Trigger{ID: "t2", Routine: "r1", Outcome: routines.Proposed}, push.TargetPeek, inbox.AdmiralSession, "routine-t2"},
		{"failed with no ship has no tap target", routines.Trigger{ID: "t3", Routine: "r1", Outcome: routines.Failed, Reason: "no room"}, "", "", ""},
		{"breaker has no tap target", routines.Trigger{ID: "t4", Routine: "r1", Outcome: routines.Breaker}, "", "", ""},
	} {
		t.Run(c.name, func(t *testing.T) {
			r := newRig(t)
			routineNotifier(r).Notify("work", rt, c.tr)
			got := r.rec.Events()
			if len(got) != 1 {
				t.Fatalf("events = %+v, want one", got)
			}
			ev := got[0].Event
			if ev.Kind != push.RoutineNotify || ev.Fleet != "work" || ev.Name == "" || ev.Text == "" || ev.Key == "" {
				t.Errorf("event = %+v, want a RoutineNotify for fleet work with a name, text and key", ev)
			}
			if ev.Target != c.target || ev.Session != c.session || ev.Item != c.itm {
				t.Errorf("tap = target %q session %q item %q, want %q %q %q", ev.Target, ev.Session, ev.Item, c.target, c.session, c.itm)
			}
			if got[0].Outcome != push.Push {
				t.Errorf("outcome = %v, want push", got[0].Outcome)
			}
		})
	}
}

func TestARoutineRecordThatIsNotAnOutcomeWorthHearingPushesNothing(t *testing.T) {
	rt := routines.Routine{ID: "r1", Name: "Nightly sweep", Notify: true}
	r := newRig(t)
	n := routineNotifier(r)
	n.Notify("work", rt, routines.Trigger{ID: "t1", Routine: "r1", Outcome: routines.Launched, Ship: "dory"}) // the one that must push
	for i, o := range []routines.Outcome{routines.Skipped, routines.Missed, routines.Reported} {
		n.Notify("work", rt, routines.Trigger{ID: "x" + string(rune('a'+i)), Routine: "r1", Outcome: o})
	}
	if got := r.rec.Events(); len(got) != 1 {
		t.Errorf("events = %+v, want only the launched one", got)
	}
}

func TestARoutineRecordRespectsTheFleetNotifySwitchAndTheWatchedWindow(t *testing.T) {
	rt := routines.Routine{ID: "r1", Name: "Nightly sweep", Notify: true}
	tr := routines.Trigger{ID: "t1", Routine: "r1", Outcome: routines.Launched, Ship: "dory"}
	r := newRig(t)
	routineNotifier(r).Notify("work", rt, tr)
	if got := r.rec.Events(); len(got) != 1 {
		t.Fatalf("with Notify on: %+v, want one event", got)
	}
	off := newRig(t)
	off.notify["work"] = false
	routineNotifier(off).Notify("work", rt, tr)
	if got := off.rec.Events(); len(got) != 0 {
		t.Errorf("fleet Notify off: %+v, want nothing", got)
	}
	watched := newRig(t)
	watched.watching = true
	routineNotifier(watched).Notify("work", rt, tr)
	if got := watched.rec.Events(); len(got) != 1 || got[0].Outcome != push.Alert {
		t.Errorf("watched window: %+v, want one alert", got)
	}
}

func TestAProposalItemInTheInboxAddsNoEventSoHoldFireDoesNotBuzzOnEveryHeldRun(t *testing.T) {
	r := newRig(t).started(nil, nil)
	r.w.Inbox([]inbox.Item{item(t, inbox.AdmiralSession, "routine-t1", "routine_proposal", "Run Nightly sweep?")})
	r.w.Inbox([]inbox.Item{
		item(t, inbox.AdmiralSession, "routine-t1", "routine_proposal", "Run Nightly sweep?"),
		item(t, "s-Dory", "q-1", "question", "Which way?"),
	})
	got := r.rec.Events()
	if len(got) != 1 || got[0].Event.Item != "q-1" {
		t.Errorf("events = %+v, want only the question's", got)
	}
}

func TestTheBreakerPushSaysTheRoutineNowAsksFirstNotThatItIsOff(t *testing.T) {
	r := newRig(t)
	routineNotifier(r).Notify("work", routines.Routine{ID: "r1", Name: "Nightly sweep", Notify: true}, routines.Trigger{ID: "t4", Routine: "r1", Outcome: routines.Breaker})
	got := r.rec.Events()
	if len(got) != 1 || !strings.Contains(got[0].Event.Text, "now asks first") || strings.Contains(got[0].Event.Text, "turned off") {
		t.Errorf("events = %+v, want text saying it now asks first", got)
	}
}
