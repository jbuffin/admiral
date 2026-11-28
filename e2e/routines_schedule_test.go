package e2e

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type runJSON struct {
	ID      string `json:"id"`
	Outcome string `json:"outcome"`
	Tier    string `json:"tier"`
	Ship    string `json:"ship"`
	Reason  string `json:"reason"`
	// HeldBy is the ship already on the event's PR or issue, on a skipped record.
	HeldBy string `json:"held_by"`
	// Verdict, CheckID and ReviewerReason are the step reviewer's answer on an event record; Costs are the judge calls' prices.
	Verdict        string `json:"verdict"`
	CheckID        string `json:"check_id"`
	ReviewerReason string `json:"reviewer_reason"`
	Costs          []struct {
		Call string  `json:"call"`
		USD  float64 `json:"usd"`
	} `json:"costs"`
	// Restricted is the profile an event ship launched on, empty for a normal ship.
	Restricted string `json:"restricted"`
	Undone     bool   `json:"undone"`
	// Promoted and PromotedShip say the engineer started a normal ship from the restricted ship's work.
	Promoted     bool   `json:"promoted"`
	PromotedShip string `json:"promoted_ship"`
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

func (w *world) fleetRoutineRuns(fleet, id string) []runJSON {
	w.t.Helper()
	code, body := send(w.t, http.MethodGet, w.in.LocalURL+"/api/fleets/"+fleet+"/routines/"+id+"/runs", nil)
	if code != http.StatusOK {
		w.t.Fatalf("GET %s runs = %d: %s", fleet, code, body)
	}
	var wrap struct {
		Runs []runJSON `json:"runs"`
	}
	if err := json.Unmarshal([]byte(body), &wrap); err != nil {
		w.t.Fatalf("runs is not JSON: %v\n%s", err, body)
	}
	return wrap.Runs
}

// routineClock gives the world's routine scheduler a clock only the test moves, starting at the wall clock, and returns
// the next minute boundary on it, in the zone the server reads a When in.
func (w *world) routineClock() time.Time {
	w.t.Helper()
	now := time.Now()
	w.setClock(now)
	w.extra = append(w.extra, "ADMIRAL_E2E_CLOCK="+filepath.Join(w.root, "routine-clock"))
	return now.Truncate(time.Minute).Add(time.Minute).In(time.Local)
}

// setClock moves the routine scheduler's clock to at.
func (w *world) setClock(at time.Time) {
	w.t.Helper()
	path := filepath.Join(w.root, "routine-clock")
	if err := os.WriteFile(path+".tmp", []byte(at.Format(time.RFC3339Nano)), 0o600); err != nil {
		w.t.Fatal(err)
	}
	if err := os.Rename(path+".tmp", path); err != nil {
		w.t.Fatal(err)
	}
}

func (w *world) makeRoutine(fleet string, routine map[string]any) string {
	w.t.Helper()
	code, body := send(w.t, http.MethodPost, w.in.LocalURL+"/api/fleets/"+fleet+"/routines", routine)
	if code != http.StatusCreated && code != http.StatusOK {
		w.t.Fatalf("POST %s routine = %d: %s", fleet, code, body)
	}
	var made struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal([]byte(body), &made); err != nil || made.ID == "" {
		w.t.Fatalf("created routine has no id: %v\n%s", err, body)
	}
	return made.ID
}

// mcpCall has this fleet's fake chat send one tools/call by the cue `mcp <name>`: the request is the file <rec>/<name>, the reply <name>.reply.
func (f *chatDock) mcpCall(name, tool string, args map[string]any) mcpReply {
	f.t.Helper()
	req, err := json.Marshal(map[string]any{"name": tool, "arguments": args})
	if err != nil {
		f.t.Fatal(err)
	}
	atomicWrite(f.t, filepath.Join(f.recDir(), name), string(req))
	reply := filepath.Join(f.recDir(), name+".reply")
	for end := time.Now().Add(20 * time.Second); ; time.Sleep(50 * time.Millisecond) {
		raw, err := os.ReadFile(reply)
		if err == nil && len(raw) > 0 {
			var r mcpReply
			if err := json.Unmarshal(raw, &r); err != nil {
				f.t.Fatalf("the fake's reply to %s %s is not a tool result: %v\n%s", name, tool, err, raw)
			}
			return r
		}
		if time.Now().After(end) {
			mcpErr, _ := os.ReadFile(filepath.Join(f.recDir(), "mcp-error"))
			f.t.Fatalf("within 20s the fake chat never answered `mcp %s` (%s); the cue's errors: %s", name, tool, mcpErr)
		}
	}
}

func TestADigestRoutineQueuesATurnOnlyInTheFleetWithSomethingNewAndAdmiralBotCannotActDuringIt(t *testing.T) {
	t.Parallel()
	cue := func(f *chatDock) []string {
		lines := chatCue(f)
		if f.fleet == "alpha" {
			// the turn never starts (the prompt stamp does not move), so the gate that refuses writes stays closed for the call below
			return append(lines[:3:3], "mcp refused")
		}
		return lines
	}
	w, chats := newFleetsWorld(t, cue, fleetSpec{key: "alpha", model: "sonnet", effort: "high"}, fleetSpec{key: "beta", model: "sonnet", effort: "high"})
	alpha, beta := chats["alpha"], chats["beta"]
	due := w.routineClock()
	w.start()
	alpha.start()
	beta.start()
	alpha.waitSession(10*time.Second, "the chat started", func(s chatSession) bool { return s.Running && s.Error == "" })
	beta.waitSession(10*time.Second, "the chat started", func(s chatSession) bool { return s.Running && s.Error == "" })

	when := due.Format("4 15") + " * * *"
	// the other routine is made first, so on the tick it reports before the digest looks for what is new
	w.makeRoutine("alpha", map[string]any{"name": "Other", "enabled": true, "when": when, "before_acting": "report_only", "do": map[string]any{"prompt": "sweep"}})
	alphaDigest := w.makeRoutine("alpha", map[string]any{"name": "Morning digest", "enabled": true, "when": when, "before_acting": "act", "do": map[string]any{"digest": map[string]any{"extra": "lead with PRs"}}})
	betaDigest := w.makeRoutine("beta", map[string]any{"name": "Morning digest", "enabled": true, "when": when, "before_acting": "act", "do": map[string]any{"digest": map[string]any{}}})

	lastOutcome := func(fleet, id string) runJSON {
		runs := w.fleetRoutineRuns(fleet, id)
		if len(runs) == 0 {
			return runJSON{}
		}
		return runs[0]
	}
	w.setClock(due)
	for _, d := range []struct{ fleet, id string }{{"alpha", alphaDigest}, {"beta", betaDigest}} {
		waitCond(t, "a run record for the "+d.fleet+" digest", func() bool { return lastOutcome(d.fleet, d.id).Outcome != "" })
	}

	if got := lastOutcome("alpha", alphaDigest); got.Outcome != "queued" {
		t.Errorf("alpha digest run = %+v, want queued: the fleet had a routine run to report", got)
	}
	if got := lastOutcome("beta", betaDigest); got.Outcome != "skipped" || got.Reason != "nothing new" {
		t.Errorf("beta digest run = %+v, want skipped for %q", got, "nothing new")
	}

	for end := time.Now().Add(15 * time.Second); !strings.Contains(alpha.stdin(), "[Digest: Morning digest]"); time.Sleep(100 * time.Millisecond) {
		if time.Now().After(end) {
			t.Fatalf("within 15s alpha's chat was never typed a [Digest: Morning digest] turn; stdin: %q", alpha.stdin())
		}
	}
	turn := alpha.stdin()
	for _, want := range []string{"lead with PRs", "Other"} {
		if !strings.Contains(turn, want) {
			t.Errorf("the digest turn does not hold %q: %q", want, turn)
		}
	}
	if got := beta.stdin(); strings.Contains(got, "[Digest:") {
		t.Errorf("beta had nothing new yet its chat was typed %q", got)
	}

	r := alpha.mcpCall("refused", "save_note", map[string]any{"text": "a fact AdmiralBot tries to save during the digest"})
	if !r.IsError || !strings.Contains(r.text(), "can only read and talk") {
		t.Errorf("a write during the digest turn = %+v, want an error saying the turn can only read and talk", r)
	}
}

func TestARoutineAboutAMinuteAheadLaunchesAnAutopilotShipOnTime(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	w.proveGuard()
	w.writeCue("wait go", "exit 0")
	due := w.routineClock()
	w.start()

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

	w.setClock(due.Add(-time.Second))
	holdThat(t, "the routine launched before its time", func() bool { return len(w.routineRuns(made.ID)) == 0 })

	w.setClock(due)
	var run runJSON
	waitCond(t, "the routine's launched run", func() bool {
		for _, r := range w.routineRuns(made.ID) {
			if r.Outcome == "launched" {
				run = r
			}
		}
		return run.Ship != ""
	})
	if run.Tier != "T0" {
		t.Errorf("record tier = %q, want T0", run.Tier)
	}
	if env := w.record(run.Ship).env; env["ADMIRAL_AUTOPILOT"] != "1" {
		t.Errorf("the routine's ship env ADMIRAL_AUTOPILOT = %q, want 1", env["ADMIRAL_AUTOPILOT"])
	}
}
