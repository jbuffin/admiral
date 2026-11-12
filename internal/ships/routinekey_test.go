package ships

import (
	"testing"

	"github.com/jbuffin/admiral/internal/launcher"
)

func routineKey(id string) func(*launcher.Request) {
	return func(q *launcher.Request) { q.RoutineKey = "routine:" + id }
}

func TestASecondLaunchOfARoutineWhileItsShipIsListedIsRefusedNamingTheShip(t *testing.T) {
	r := newWakeRig(t, 0)
	first := r.mustLaunch(t, routineKey("r1"))
	r.wantHeld(t, routineKey("r1"), first)
}

func TestAShipThatHoldsARoutineKeyIsFoundByIt(t *testing.T) {
	r := newWakeRig(t, 0)
	first := r.mustLaunch(t, routineKey("r1"))
	waitUntil(t, "the ship listed with its routine key", func() bool { _, ok := r.s.Holder("routine:r1"); return ok })
	got, _ := r.s.Holder("routine:r1")
	if got.Session != first.Session || got.Status == "" {
		t.Errorf("Holder = %+v, want the ship %s with its status", got, first.Session)
	}
	if _, ok := r.s.Holder("routine:r2"); ok {
		t.Error("Holder found a ship for a key nothing holds")
	}
}

func TestTheLaunchedOutcomePointsAtItsTriggerRecord(t *testing.T) {
	r := newWakeRig(t, 0)
	got := r.mustLaunch(t, func(q *launcher.Request) { q.RoutineKey, q.TriggerID = "routine:r1", "trig-7" })
	var rec []map[string]any
	waitUntil(t, "the launched record", func() bool { rec = outcomeLines(t, r.home, got.Session, "launched"); return len(rec) == 1 })
	trig, _ := rec[0]["trigger"].(map[string]any)
	if trig["fleet"] != "work" || trig["id"] != "trig-7" {
		t.Errorf("launched record trigger = %v, want {fleet: work, id: trig-7}", rec[0]["trigger"])
	}
}
