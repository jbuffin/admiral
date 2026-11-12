package e2e

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestHoldFireTurnsADueRoutineIntoAnInboxProposalThatLaunchesOneAutopilotShipWhenAccepted(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	w.proveGuard()
	w.writeCue("wait go", "exit 0")
	w.start()

	if code, body := send(t, http.MethodPost, w.in.LocalURL+"/api/fleets/work/hold-fire/on", nil); code != http.StatusOK {
		t.Fatalf("POST hold-fire/on = %d: %s", code, body)
	}
	due := time.Now().Add(5 * time.Second).Truncate(time.Minute).Add(time.Minute).In(time.Local)
	code, body := send(t, http.MethodPost, w.in.LocalURL+"/api/fleets/work/routines", map[string]any{
		"name": "Held sweep", "enabled": true, "when": due.Format("4 15") + " * * *", "before_acting": "act",
		"do": map[string]any{"launch": map[string]any{"harness": "claude", "dir": w.checkout("held"), "text": "go"}},
	})
	if code != http.StatusCreated && code != http.StatusOK {
		t.Fatalf("POST routine = %d: %s", code, body)
	}

	var proposal inboxItem
	deadline := due.Add(30 * time.Second)
	for proposal.ID == "" {
		if time.Now().After(deadline) {
			t.Fatalf("no routine proposal in the Inbox 30 s after %v: %+v", due, w.in.inbox(t))
		}
		for _, it := range w.in.inbox(t) {
			if it.Kind == "routine_proposal" {
				proposal = it
			}
		}
		time.Sleep(200 * time.Millisecond)
	}
	if proposal.Fleet != "work" || !strings.Contains(proposal.Title, "Held sweep") {
		t.Errorf("proposal = %+v, want fleet work and a title naming the routine", proposal)
	}
	holdThat(t, "a held routine launched a ship before it was accepted", func() bool { return len(w.in.ships(t)) == 0 })

	launch := w.in.LocalURL + "/api/inbox/_admiral/" + proposal.ID + "/launch"
	if code, body := send(t, http.MethodPost, launch, map[string]any{}); code != http.StatusCreated {
		t.Fatalf("accept = %d, want 201: %s", code, body)
	}
	ships := w.waitShipCount(30*time.Second, "the accepted proposal's ship", func(shipJSON) bool { return true }, 1)
	if env := w.record(ships[0].Session).env; env["ADMIRAL_AUTOPILOT"] != "1" {
		t.Errorf("the accepted proposal's ship env ADMIRAL_AUTOPILOT = %q, want 1", env["ADMIRAL_AUTOPILOT"])
	}
	if code, body := send(t, http.MethodPost, launch, map[string]any{}); code != http.StatusConflict {
		t.Errorf("second accept = %d, want 409: %s", code, body)
	}
	holdThat(t, "a second accept launched another ship", func() bool { return len(w.in.ships(t)) == 1 })

	var runs []runJSON
	for _, r := range w.routineRuns(routineIDNamed(t, w, "Held sweep")) {
		if r.Outcome == "launched" {
			runs = append(runs, r)
		}
	}
	if len(runs) != 1 || runs[0].Ship != ships[0].Session {
		t.Errorf("launched records = %+v, want one for ship %s", runs, ships[0].Session)
	}
}

func routineIDNamed(t *testing.T, w *world, name string) string {
	t.Helper()
	code, body := send(t, http.MethodGet, w.in.LocalURL+"/api/fleets/work/routines", nil)
	if code != http.StatusOK {
		t.Fatalf("GET routines = %d: %s", code, body)
	}
	var wrap struct {
		Routines []struct{ ID, Name string } `json:"routines"`
	}
	if err := json.Unmarshal([]byte(body), &wrap); err != nil {
		t.Fatalf("routines is not JSON: %v\n%s", err, body)
	}
	for _, r := range wrap.Routines {
		if r.Name == name {
			return r.ID
		}
	}
	t.Fatalf("no routine named %q in %s", name, body)
	return ""
}
