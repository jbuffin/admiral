// An event ship Admiral stops at its wall clock stays anchored and frees its slot, and Stop all worker-launched ends the live ones.
package e2e

import (
	"net/http"
	"strings"
	"testing"
	"time"
)

// budgetLimits is the draft-fix ceiling every event routine here launches under, with the caps a test sets.
func budgetLimits(concurrent, wallClock int) map[string]any {
	return map[string]any{
		"ceilings":           map[string]any{"t2": map[string]any{"level": "A3", "restricted": "draft_fix"}},
		"rates":              map[string]any{"launches_per_hour": 100, "launches_per_sender_per_day": 100, "concurrent": concurrent},
		"wall_clock_minutes": wallClock,
	}
}

// firstRun is the routine's first record of the outcome.
func (w *world) firstRun(routine, outcome string) (runJSON, bool) {
	for _, r := range w.routineRuns(routine) {
		if r.Outcome == outcome {
			return r, true
		}
	}
	return runJSON{}, false
}

func TestAnEventShipPastItsWallClockIsStoppedAndAnchoredAndItsFailedRecordFreesTheCap(t *testing.T) {
	t.Parallel()
	w, g := lookoutWorldWith(t, budgetLimits(1, 1))
	w.seedRestricted("work")
	w.extra = append(w.extra, "ADMIRAL_WALL_CLOCK_MINUTE=3s")
	w.start()
	a := w.eventRoutine("Sweep A", "act")
	b := w.eventRoutine("Sweep B", "act")

	// the cap of one: one routine's event launches, the other's is a proposal that says why
	var first, second string
	var run runJSON
	waitCond(t, 30*time.Second, "one routine to launch and the other to be proposed", func() bool {
		w.srcReq(http.MethodPost, "/gh/refresh", map[string]any{})
		ra, okA := w.firstRun(a, "launched")
		rb, okB := w.firstRun(b, "launched")
		switch {
		case okA:
			first, second, run = a, b, ra
		case okB:
			first, second, run = b, a, rb
		default:
			return false
		}
		held, ok := w.firstRun(second, "proposed")
		return ok && strings.Contains(held.Reason, "concurrent")
	})

	// the wall clock ends the harness and keeps the pane
	var stopped shipJSON
	w.waitShipCount(30*time.Second, "the first ship to be anchored by the wall clock", func(s shipJSON) bool {
		if s.Session == run.Ship && s.Status == "anchored" {
			stopped = s
			return true
		}
		return false
	}, 1)
	if stopped.ExitSignal == nil || stopped.StoppedBy != "wall_clock:1" {
		t.Errorf("ship = %+v, want an exit signal and stopped_by wall_clock:1", stopped)
	}
	waitCond(t, 30*time.Second, "the first record to read failed with the wall-clock reason", func() bool {
		for _, r := range w.routineRuns(first) {
			if r.ID == run.ID {
				return r.Outcome == "failed" && r.Reason == "stopped at the 1-minute wall clock"
			}
		}
		return false
	})

	// the anchored ship no longer holds a worker slot: a new event on the other routine launches
	again := ghNode("github.com", "acme/api", 8, true, "Fix the harbour lantern")
	again["author"] = map[string]any{"login": "mallory-the-stranger"}
	again["timelineItems"] = map[string]any{"nodes": []map[string]any{{"__typename": "ReviewRequestedEvent",
		"createdAt": time.Now().Add(2 * time.Hour).UTC().Format(time.RFC3339), "actor": map[string]any{"login": "mallory-the-stranger"},
		"requestedReviewer": map[string]any{"login": "fake-viewer"}}}}
	g.search("review", "review-requested:@me", again)
	waitCond(t, 30*time.Second, "the second routine to launch once the first ship's slot is free", func() bool {
		w.srcReq(http.MethodPost, "/gh/refresh", map[string]any{})
		_, ok := w.firstRun(second, "launched")
		return ok
	})
}

func TestStopAllWorkerLaunchedEndsTheLiveEventShipsAndHoldFireCountsThem(t *testing.T) {
	t.Parallel()
	w, _ := lookoutWorldWith(t, budgetLimits(3, 60))
	w.seedRestricted("work")
	w.start()
	id := w.eventRoutine("Sweep", "act")
	run := w.waitRun(id, "launched")
	w.waitShipCount(30*time.Second, "the event ship to be live", func(s shipJSON) bool { return s.Session == run.Ship && live(s) }, 1)

	code, body := send(t, http.MethodGet, w.in.LocalURL+"/api/fleets/work/hold-fire", nil)
	if code != http.StatusOK || !strings.Contains(body, `"worker_ships":1`) {
		t.Fatalf("GET hold-fire = %d %s, want worker_ships 1", code, body)
	}
	code, body = send(t, http.MethodPost, w.in.LocalURL+"/api/fleets/work/worker-ships/stop", nil)
	if code != http.StatusOK || !strings.Contains(body, run.Ship) {
		t.Fatalf("POST worker-ships/stop = %d %s, want 200 naming %s", code, body, run.Ship)
	}
	waitCond(t, 30*time.Second, "the stopped ship to leave the live list", func() bool {
		return len(w.shipsWhere(func(s shipJSON) bool { return s.Session == run.Ship && live(s) })) == 0
	})
	code, body = send(t, http.MethodGet, w.in.LocalURL+"/api/fleets/work/hold-fire", nil)
	if code != http.StatusOK || !strings.Contains(body, `"worker_ships":0`) {
		t.Errorf("GET hold-fire after = %d %s, want worker_ships 0", code, body)
	}
}
