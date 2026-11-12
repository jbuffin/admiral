package ships

import (
	"net/http"
	"testing"

	"github.com/jbuffin/admiral/internal/config"
	"github.com/jbuffin/admiral/internal/launcher"
	"github.com/jbuffin/admiral/internal/sessionhost"
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

func TestARoutineLaunchBeforeShipsAreKnownIsAWaitNotARefusal(t *testing.T) {
	s := &Service{
		Host:     sessionhost.New("tmux", t.TempDir()+"/tmux.sock"), // never run: not ready
		Launcher: &launcher.Launcher{Config: func() *config.Config { return &config.Config{} }},
	}
	_, lerr := s.LaunchOrder(launcher.Request{Fleet: "work", Harness: "claude", Dir: t.TempDir(),
		Skill: launcher.Skill{Kind: "none"}, RoutineKey: "routine:r1"})
	if lerr == nil || lerr.Status != http.StatusServiceUnavailable || lerr.Wait == "" {
		t.Fatalf("LaunchOrder before the host is ready = %+v, want a 503 that waits", lerr)
	}
}
