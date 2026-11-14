package e2e

import (
	"encoding/json"
	"net/http"
	"testing"
	"time"
)

type runJSON struct {
	ID      string `json:"id"`
	Outcome string `json:"outcome"`
	Tier    string `json:"tier"`
	Ship    string `json:"ship"`
	Reason  string `json:"reason"`
	// Restricted is the profile an event ship launched on, empty for a normal ship.
	Restricted string `json:"restricted"`
	Undone     bool   `json:"undone"`
}

func (w *world) routineRuns(id string) []runJSON {
	w.t.Helper()
	code, body := send(w.t, http.MethodGet, w.in.LocalURL+"/api/fleets/work/routines/"+id+"/runs", nil)
	if code != http.StatusOK {
		w.t.Fatalf("GET runs = %d: %s", code, body)
	}
	var wrap struct {
		Runs []runJSON `json:"runs"`
	}
	if err := json.Unmarshal([]byte(body), &wrap); err != nil {
		w.t.Fatalf("runs is not JSON: %v\n%s", err, body)
	}
	return wrap.Runs
}

func TestARoutineAboutAMinuteAheadLaunchesAnAutopilotShipOnTime(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	w.proveGuard()
	w.writeCue("wait go", "exit 0")
	w.start()

	// the next minute that is at least 5 s away, in the zone the server reads a When in
	due := time.Now().Add(5 * time.Second).Truncate(time.Minute).Add(time.Minute).In(time.Local)
	code, body := send(t, http.MethodPost, w.in.LocalURL+"/api/fleets/work/routines", map[string]any{
		"name": "Every minute test", "enabled": true, "when": due.Format("4 15") + " * * *", "before_acting": "act",
		"do": map[string]any{"launch": map[string]any{"harness": "claude", "dir": w.checkout("sched"), "text": "go"}},
	})
	if code != http.StatusCreated && code != http.StatusOK {
		t.Fatalf("POST routine = %d: %s", code, body)
	}
	var made struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal([]byte(body), &made); err != nil || made.ID == "" {
		t.Fatalf("created routine has no id: %v\n%s", err, body)
	}

	holdThat(t, "the routine launched before its time", func() bool { return time.Now().After(due) || len(w.routineRuns(made.ID)) == 0 })

	var run runJSON
	deadline := due.Add(30 * time.Second)
	for run.Ship == "" {
		if time.Now().After(deadline) {
			t.Fatalf("no launched record 30 s after %v: %+v", due, w.routineRuns(made.ID))
		}
		for _, r := range w.routineRuns(made.ID) {
			if r.Outcome == "launched" {
				run = r
			}
		}
		time.Sleep(200 * time.Millisecond)
	}
	if run.Tier != "T0" {
		t.Errorf("record tier = %q, want T0", run.Tier)
	}
	if env := w.record(run.Ship).env; env["ADMIRAL_AUTOPILOT"] != "1" {
		t.Errorf("the routine's ship env ADMIRAL_AUTOPILOT = %q, want 1", env["ADMIRAL_AUTOPILOT"])
	}
}
