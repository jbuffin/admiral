package e2e

import (
	"encoding/json"
	"net/http"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/jbuffin/admiral/internal/capabilities"
)

// seedScoutGuard stores a passed scout guard probe for claude in each fleet, beside proveGuard's autopilot proof.
func (w *world) seedScoutGuard(fleets ...string) {
	w.t.Helper()
	store, now := capabilities.Open(w.home), time.Now()
	for _, f := range fleets {
		if err := store.RecordVersion(f, "claude", fakeVersion, now); err != nil {
			w.t.Fatal(err)
		}
		res := capabilities.Result{Passed: true, At: now, Detail: "seeded by the test"}
		if err := store.Record(f, "claude", fakeVersion, capabilities.ScoutGuard, res); err != nil {
			w.t.Fatal(err)
		}
	}
}

func (w *world) scoutLaunch(harness, repo string) map[string]any {
	return w.autopilotLaunch(harness, repo, map[string]any{"scout": true})
}

func preToolUseCue(tool string, input map[string]any) string {
	b, _ := json.Marshal(map[string]any{"hook_event_name": "PreToolUse", "tool_name": tool, "tool_input": input})
	return "event PreToolUse " + string(b)
}

// A scout launched through the API runs the real guard from its PreToolUse hooks: each change it tries is refused with
// the scout rule, its stop ends the ship, and the ship never shows as anchored or moored.
func TestAScoutLaunchedThroughTheAPIIsGuardedAndSailsAtItsStop(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	w.proveGuard()
	w.seedScoutGuard(defaultFleets...)
	checkout := w.checkout("scoutrepo")
	w.writeCue(
		preToolUseCue("Bash", map[string]any{"command": "git commit -m x"}),
		preToolUseCue("Bash", map[string]any{"command": "git push origin main"}),
		preToolUseCue("Bash", map[string]any{"command": "gh pr create --title x --body y"}),
		preToolUseCue("Write", map[string]any{"file_path": filepath.Join(checkout, "found.txt"), "content": "x"}),
		`event Stop {"background_tasks":[]}`,
	)
	w.start()
	ship := w.launch(w.scoutLaunch("claude", "scoutrepo"))
	r := w.record(ship.Session)
	if r.env["ADMIRAL_AUTOPILOT"] != "1" || r.env["ADMIRAL_SCOUT"] == "" {
		t.Fatalf("scout env: ADMIRAL_AUTOPILOT=%q ADMIRAL_SCOUT=%q, want 1 and the checkout", r.env["ADMIRAL_AUTOPILOT"], r.env["ADMIRAL_SCOUT"])
	}
	if got, _ := filepath.EvalSymlinks(checkout); r.env["ADMIRAL_SCOUT"] != got {
		t.Errorf("ADMIRAL_SCOUT = %q, want the resolved checkout %q", r.env["ADMIRAL_SCOUT"], got)
	}

	denied := w.waitEvent(ship.Session, "guard_denied", 4)
	var rules []string
	for _, m := range denied {
		if m["event"] == "guard_denied" {
			rule, _ := m["rule"].(string)
			rules = append(rules, rule)
		}
	}
	for _, rule := range rules {
		if !strings.HasPrefix(rule, "scout_") {
			t.Errorf("guard_denied rules = %v, want every one a scout rule", rules)
			break
		}
	}
	// A handler's line is appended only after it exits, so the last guard exit can trail its guard_denied record.
	var handlers [][3]string
	exit2 := 0
	for end := time.Now().Add(10 * time.Second); exit2 < 4 && time.Now().Before(end); time.Sleep(50 * time.Millisecond) {
		handlers, _ = handlersOf(t, r.dir)
		exit2 = 0
		for _, h := range handlers {
			if h[2] == "admiral guard pretooluse" && h[1] == "2" {
				exit2++
			}
		}
	}
	if exit2 != 4 {
		t.Errorf("guard handlers that exited 2 = %d (%q), want 4: commit, push, PR create and the Write", exit2, handlers)
	}

	seen := map[string]bool{}
	gone := false
	for end := time.Now().Add(10 * time.Second); !gone && time.Now().Before(end); time.Sleep(50 * time.Millisecond) {
		code, body := send(t, http.MethodGet, w.in.LocalURL+"/api/ships", nil)
		var list struct {
			Ships []struct{ Session, Status string } `json:"ships"`
		}
		if code != http.StatusOK || json.Unmarshal([]byte(body), &list) != nil {
			continue
		}
		gone = true
		for _, s := range list.Ships {
			if s.Session == ship.Session {
				seen[s.Status] = true
				gone = false
			}
		}
	}
	if !gone {
		t.Fatalf("the scout is still listed 10s after its stop, with statuses %v", seen)
	}
	if seen["anchored"] || seen["moored"] {
		t.Errorf("the scout showed statuses %v, want it never anchored or moored", seen)
	}
	// The outcome writer is asynchronous: the ship can leave the list before its stopped record is written.
	log := w.waitEvent(ship.Session, "stopped", 1)
	stopped := lastOf(log, "stopped")
	if stopped == nil || stopped["scout"] != true {
		t.Errorf("stopped record = %v, want one marked as a scout's end", stopped)
	}
	if names := eventsOf(log); slices.Contains(names, "moored") {
		t.Errorf("records = %v, want no moored", names)
	}
}

// The scout guard has no proof for codex, so a scout on a codex dock is refused at the door.
func TestAScoutOnACodexDockIsRefusedAndNamesCodex(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	w.proveGuard()
	w.seedScoutGuard(defaultFleets...)
	w.writeCue("hook working")
	w.start()
	code, body := w.post(w.scoutLaunch("codex", "scoutrepo"))
	if code != http.StatusBadRequest || !strings.Contains(strings.ToLower(body), "codex") || !strings.Contains(strings.ToLower(body), "scout") {
		t.Errorf("POST /api/ships scout on codex = %d %s, want 400 naming codex and the scout", code, body)
	}
}

// admiral crew launch reads ADMIRAL_SCOUT from the scout ship's environment: a boat on a harness with no proven scout guard is
// refused and a boat on claude is launched.
func TestACrewBoatFromAScoutNeedsTheScoutGuardOfItsHarness(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	w.proveGuard()
	w.seedScoutGuard(defaultFleets...)
	w.writeCue("hook working")
	w.start()
	ship := w.launch(w.scoutLaunch("claude", "scoutrepo"))
	w.waitFile(ship.Session, "cues-done")
	scoutDir := w.record(ship.Session).env["ADMIRAL_SCOUT"]
	if scoutDir == "" {
		t.Fatal("the scout ship has no ADMIRAL_SCOUT, so the boat below would not be a scout's")
	}
	dir := w.checkout("boatdir")
	env := []string{"ADMIRAL_SHIP=" + ship.Session, "ADMIRAL_AUTOPILOT=1", "ADMIRAL_SCOUT=" + scoutDir}

	r := w.admiralWith(cliOpts{env: env}, "crew", "launch", "--dir", dir, "--name", "other", "--harness", "codex", "--", "hi")
	w.expectExit(r, 1, "a scout's crew boat on codex")
	if !strings.Contains(strings.ToLower(r.stderr), "scout") {
		t.Errorf("stderr = %q, want it to say the scout guard is not proven for codex", r.stderr)
	}
	w.expectExit(w.admiralWith(cliOpts{env: env}, "crew", "launch", "--dir", dir, "--name", "mate", "--", "hi"), 0, "a scout's crew boat on claude")
}
