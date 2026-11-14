// Stop all worker-launched stops the live ships an event launched, per fleet or across fleets, and hold fire reports how many there are.
package routines_test

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"

	"github.com/jbuffin/admiral/internal/routines"
)

type stopRig struct {
	*undoRig
	listed []routines.ListedShip
	play   routines.Routine
}

// newStopRig has a worker ship in each state in the work fleet, one in play, and a scheduled, a hand and a promoted ship that no event launched.
func newStopRig(t *testing.T) *stopRig {
	t.Helper()
	u := newUndoRig(t)
	s := &stopRig{undoRig: u}
	u.rig.prove("play", true, true)
	s.play = u.rig.create("play", launchRoutine())
	launch := func(fleet string, r routines.Routine, ship, event, status string) {
		t.Helper()
		if _, err := u.rig.store.AppendTrigger(fleet, routines.Trigger{Routine: r.ID, Outcome: routines.Launched, Tier: "T1", Event: event, Sender: "Eng", Ship: ship, WorkerLaunched: event != ""}); err != nil {
			t.Fatal(err)
		}
		s.listed = append(s.listed, routines.ListedShip{Session: ship, Fleet: fleet, Routine: "routine:" + r.ID, Launched: u.rig.now.UTC().Format("2006-01-02T15:04:05Z07:00"), Status: status})
	}
	launch("work", u.routine, "w-underway", "https://x/1", "underway")
	launch("work", u.other, "w-needs-you", "https://x/2", "needs_you")
	launch("work", u.other, "w-anchored", "https://x/3", "anchored")
	launch("work", u.routine, "w-scheduled", "", "underway")
	launch("play", s.play, "p-underway", "https://x/4", "underway")
	s.listed = append(s.listed,
		routines.ListedShip{Session: "w-hand", Fleet: "work", Status: "underway"},
		routines.ListedShip{Session: "w-promoted", Fleet: "work", Status: "underway"})
	mux := http.NewServeMux()
	(&routines.Service{Store: u.rig.store, Stop: u.stop.stop, Listed: func() []routines.ListedShip { return s.listed }}).Routes(mux)
	u.srv.Close()
	u.srv = httptest.NewServer(mux)
	t.Cleanup(u.srv.Close)
	return s
}

func sessionsOf(t *testing.T, body map[string]any, key string) []string {
	t.Helper()
	list, _ := body[key].([]any)
	out := make([]string, 0, len(list))
	for _, v := range list {
		s, ok := v.(string)
		if !ok {
			t.Fatalf("%s holds %v, want session names", key, v)
		}
		out = append(out, s)
	}
	slices.Sort(out)
	return out
}

func TestStopAllWorkerShipsInAFleetStopsOnlyThatFleetsLiveEventShips(t *testing.T) {
	s := newStopRig(t)
	code, body := s.do("POST", "/api/fleets/work/worker-ships/stop", "")
	if code != http.StatusOK {
		t.Fatalf("POST = %d %v", code, body)
	}
	want := []string{"w-needs-you", "w-underway"}
	if got := sessionsOf(t, body, "stopped"); !slices.Equal(got, want) {
		t.Errorf("stopped = %v, want %v (not the anchored, scheduled, hand or promoted ship, nor play's)", got, want)
	}
	if got := s.stop.calls; len(got) != 2 {
		t.Errorf("Stop was called for %v, want the two live worker ships", got)
	}
}

func TestStopAllWorkerShipsAcrossFleetsStopsEveryFleetsLiveEventShips(t *testing.T) {
	s := newStopRig(t)
	code, body := s.do("POST", "/api/worker-ships/stop", "")
	if code != http.StatusOK {
		t.Fatalf("POST = %d %v", code, body)
	}
	want := []string{"p-underway", "w-needs-you", "w-underway"}
	if got := sessionsOf(t, body, "stopped"); !slices.Equal(got, want) {
		t.Errorf("stopped = %v, want %v", got, want)
	}
}

func TestStopAllWorkerShipsReportsAShipThatWontStopAndStillStopsTheRest(t *testing.T) {
	s := newStopRig(t)
	s.stop.set(errors.New("tmux said no"))
	code, body := s.do("POST", "/api/fleets/work/worker-ships/stop", "")
	if code != http.StatusOK {
		t.Fatalf("POST = %d %v, want 200 with the failures listed", code, body)
	}
	if got := sessionsOf(t, body, "failed"); len(got) != 2 {
		t.Errorf("failed = %v, want both ships", got)
	}
	if got := sessionsOf(t, body, "stopped"); len(got) != 0 {
		t.Errorf("stopped = %v, want none", got)
	}
	if s.stop.n() != 2 {
		t.Errorf("Stop was called %d times, want it tried on both ships", s.stop.n())
	}
}

func TestStopAllWorkerShipsWithNothingLiveAnswersOKAndEmpty(t *testing.T) {
	u := newUndoRig(t)
	mux := http.NewServeMux()
	(&routines.Service{Store: u.rig.store, Stop: u.stop.stop, Listed: func() []routines.ListedShip { return nil }}).Routes(mux)
	u.srv.Close()
	u.srv = httptest.NewServer(mux)
	t.Cleanup(u.srv.Close)
	code, body := u.do("POST", "/api/fleets/work/worker-ships/stop", "")
	if code != http.StatusOK || len(sessionsOf(t, body, "stopped")) != 0 {
		t.Errorf("POST = %d %v, want 200 and nothing stopped", code, body)
	}
}

func TestStopAllWorkerShipsInAnUnknownFleetIs404(t *testing.T) {
	s := newStopRig(t)
	if code, body := s.do("POST", "/api/fleets/nope/worker-ships/stop", ""); code != http.StatusNotFound {
		t.Errorf("POST = %d %v, want 404", code, body)
	}
}

func TestHoldFireReportsTheLiveWorkerShipsOfTheFleetAndOfAllFleets(t *testing.T) {
	s := newStopRig(t)
	code, body := s.do("GET", "/api/fleets/work/hold-fire", "")
	if code != http.StatusOK || body["worker_ships"] != float64(2) || body["all_worker_ships"] != float64(3) {
		t.Errorf("GET fleet hold-fire = %d %v, want worker_ships 2 and all_worker_ships 3 (work's underway and needs_you; plus play's)", code, body)
	}
	code, body = s.do("GET", "/api/hold-fire", "")
	if code != http.StatusOK || body["worker_ships"] != float64(3) {
		t.Errorf("GET hold-fire = %d %v, want worker_ships 3", code, body)
	}
}
