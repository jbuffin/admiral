package e2e

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

// The guard's rules and its outcome record are unit tests in internal/cli; here
// it runs from a ship's real PreToolUse hook.

const guardReason = "Autopilot ships leave reviews PENDING. The engineer submits them from the inbox."

func guardPayload(command string) string {
	b, _ := json.Marshal(map[string]any{"hook_event_name": "PreToolUse", "tool_name": "Bash",
		"tool_input": map[string]any{"command": command}, "cwd": "/"})
	return string(b)
}

// shipEnv is the ship contract Admiral sets for a ship under home, and
// none of ADMIRAL_HOME: a ship's hook finds its home through ADMIRAL_REPORT.
func shipEnv(home, ship string, extra ...string) []string {
	return append([]string{"ADMIRAL_SHIP=" + ship, "ADMIRAL_REPORT=" + filepath.Join(home, "reports", ship+".json")}, extra...)
}

type guardResult struct {
	stdout, stderr string
	code           int
}

// runGuardCmd runs `admiral guard pretooluse` with the payload on stdin, the
// world's environment less ADMIRAL_HOME and ADMIRAL_AUTOPILOT, then extra.
func (w *world) runGuardCmd(payload string, extra ...string) guardResult {
	w.t.Helper()
	ctx := testCtx(w.t)
	cmd := exec.CommandContext(ctx, binaryPath(w.t), "guard", "pretooluse")
	cmd.Dir = w.userHom
	cmd.Stdin = strings.NewReader(payload)
	for _, kv := range w.baseEnv() {
		// A suite run inside a ship must not lend its ship contract to the command.
		switch k, _, _ := strings.Cut(kv, "="); k {
		case "ADMIRAL_HOME", "ADMIRAL_AUTOPILOT", "ADMIRAL_SHIP", "ADMIRAL_REPORT", "ADMIRAL_INBOX":
		default:
			cmd.Env = append(cmd.Env, kv)
		}
	}
	cmd.Env = append(cmd.Env, extra...)
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	res := guardResult{}
	if err := cmd.Run(); err != nil {
		ee, ok := err.(*exec.ExitError)
		if !ok || ctx.Err() != nil {
			failRequest(w.t, "admiral guard pretooluse", err)
		}
		res.code = ee.ExitCode()
	}
	res.stdout, res.stderr = out.String(), errb.String()
	return res
}

func (r guardResult) requireQuiet(t *testing.T) {
	t.Helper()
	if r.code != 0 || r.stdout != "" || r.stderr != "" {
		t.Errorf("exit %d, stdout %q, stderr %q, want exit 0 and no output", r.code, r.stdout, r.stderr)
	}
}

// guardShipCue is a Claude PreToolUse for a refused Bash command, as the stub
// Claude fires it.
const guardShipCue = `event PreToolUse {"hook_event_name":"PreToolUse","tool_name":"Bash","tool_input":{"command":"gh pr review 1 --approve"}}`

// handlersOf reads the handlers the fake ran: each line is the event, the exit
// code and the command, with the stderr in handler-<n>.stderr.
func handlersOf(t *testing.T, dir string) (lines [][3]string, stderrs []string) {
	t.Helper()
	for i, l := range strings.Split(strings.TrimSuffix(readIf(filepath.Join(dir, "handlers")), "\n"), "\n") {
		if l == "" {
			continue
		}
		f := strings.SplitN(l, "\t", 3)
		if len(f) != 3 {
			t.Fatalf("handlers line %q is not <event>, <exit code>, <command> separated by tabs", l)
		}
		lines = append(lines, [3]string{f[0], f[1], f[2]})
		stderrs = append(stderrs, readIf(filepath.Join(dir, "handler-"+itoa(i+1)+".stderr")))
	}
	return lines, stderrs
}

// On an autopilot ship the stub Claude's PreToolUse for Bash calls the
// guard, which exits 2 with the reason and leaves a guard_denied line for the
// ship; the working hook beside it still runs and exits 0.
func TestShipGuardRefusesOnAnAutopilotShip(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	w.proveGuard()
	w.writeCue(guardShipCue)
	w.start()
	ship := w.launch(w.autopilotLaunch("claude", "guardap", nil))
	r := w.record(ship.Session)
	if r.env["ADMIRAL_AUTOPILOT"] != "1" {
		t.Fatalf("ship env ADMIRAL_AUTOPILOT = %q, want 1", r.env["ADMIRAL_AUTOPILOT"])
	}
	w.waitFile(ship.Session, "cues-done")

	handlers, stderrs := handlersOf(t, r.dir)
	var guardRuns, workingRuns int
	for i, h := range handlers {
		switch h[2] {
		case "admiral guard pretooluse":
			guardRuns++
			if h[0] != "PreToolUse" || h[1] != "2" {
				t.Errorf("guard handler = %q, want event PreToolUse and exit code 2", h)
			}
			if strings.TrimSuffix(stderrs[i], "\n") != guardReason {
				t.Errorf("guard handler stderr = %q, want the reason line", stderrs[i])
			}
		case "admiral hook PreToolUse":
			workingRuns++
			if h[1] != "0" {
				t.Errorf("working hook = %q, want exit code 0", h)
			}
		}
	}
	if guardRuns != 1 || workingRuns != 1 {
		t.Errorf("handlers run for the Bash PreToolUse = %q, want one guard and one working hook", handlers)
	}
	if got := readIf(filepath.Join(r.dir, "event-error")); got != "" {
		t.Errorf("the fake reported a fault: %s", got)
	}
	if lines := strings.Count(readIf(filepath.Join(r.dir, "events-done")), "\n"); lines != 1 {
		t.Errorf("events-done has %d line(s), want one per cue", lines)
	}
	if got := readIf(filepath.Join(r.dir, "event-stdout")); got != "" {
		t.Errorf("event-stdout = %q, want nothing", got)
	}

	rec := w.waitEvent(ship.Session, "guard_denied", 1)
	d := lastOf(rec, "guard_denied")
	if d["tool"] != "Bash" || d["rule"] != "gh_pr_review" {
		t.Errorf("guard_denied = %v, want tool Bash and rule gh_pr_review", d)
	}
	for _, m := range w.outcomeLog() {
		if b, _ := json.Marshal(m); strings.Contains(string(b), "pr review") {
			t.Errorf("an outcome line holds the command: %s", b)
		}
	}
}

// A scout's guard runs from the built binary with only ADMIRAL_SCOUT naming the checkout: a Write into it
// exits 2 with a reason that names no path and leaves a guard_denied line with the scout rule, a Write
// outside it passes.
func TestScoutGuardRefusesAWriteIntoTheCheckoutThroughTheBinary(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	checkout := filepath.Join(root, "checkout")
	if err := os.Mkdir(checkout, 0o755); err != nil {
		t.Fatal(err)
	}
	write := func(path string) string {
		b, _ := json.Marshal(map[string]any{"hook_event_name": "PreToolUse", "tool_name": "Write",
			"tool_input": map[string]any{"file_path": path, "content": "x"}, "cwd": checkout})
		return string(b)
	}
	env := shipEnv(w.home, "scoutship", "ADMIRAL_SCOUT="+checkout)

	res := w.runGuardCmd(write(filepath.Join(checkout, "secret-name.txt")), env...)
	if res.code != 2 || res.stdout != "" || strings.TrimSpace(res.stderr) == "" || strings.Contains(res.stderr, "secret-name") {
		t.Errorf("Write into the checkout: exit %d, stdout %q, stderr %q, want exit 2, no stdout and a reason that names no path", res.code, res.stdout, res.stderr)
	}
	var denied []outRec
	for _, m := range w.outcomeLog() {
		if m["event"] == "guard_denied" {
			denied = append(denied, m)
		}
	}
	if len(denied) != 1 || denied[0]["ship"] != "scoutship" || denied[0]["tool"] != "Write" || denied[0]["rule"] != "scout_edit" {
		t.Errorf("guard_denied lines = %v, want one for scoutship, tool Write, rule scout_edit", denied)
	}
	if b, _ := json.Marshal(w.outcomeLog()); strings.Contains(string(b), "secret-name") {
		t.Errorf("an outcome line holds the path: %s", b)
	}

	w.runGuardCmd(write(filepath.Join(root, "notes.md")), env...).requireQuiet(t)
}

// The same cue on a ship launched by hand: the guard runs and lets it through,
// and nothing is recorded.
func TestShipGuardLeavesAHandLaunchedShipAlone(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	w.writeCue(guardShipCue)
	w.start()
	ship := w.launch(w.order("work", "claude", "guardhand", noSkill, "x"))
	r := w.record(ship.Session)
	w.waitFile(ship.Session, "cues-done")

	handlers, stderrs := handlersOf(t, r.dir)
	ran := false
	for i, h := range handlers {
		if h[2] == "admiral guard pretooluse" {
			ran = true
			if h[1] != "0" || stderrs[i] != "" {
				t.Errorf("guard handler = %q with stderr %q, want exit 0 and no output", h, stderrs[i])
			}
		}
	}
	if !ran {
		t.Errorf("handlers run = %q, want the guard among them", handlers)
	}
	holdClean(t, 500*time.Millisecond, func() string {
		if got := eventsOf(w.shipLog(ship.Session)); slices.Contains(got, "guard_denied") {
			return fmt.Sprintf("records of the hand-launched ship = %v, want no guard_denied", got)
		}
		return ""
	})
}
