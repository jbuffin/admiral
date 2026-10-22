package e2e

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// A6 and A7 (#69): a ship's hook command comes from the bundle, finds
// `admiral` through $ADMIRAL_HOME/bin on the ship's PATH, and its write lands
// on the ship's own pane by id. The fake Claude runs the bundle's command for
// each `event` cue with the cue's payload on stdin.

func TestShipHookRunsBundleCommand(t *testing.T) {
	w := newWorld(t)
	// A stale link from an earlier build: every start must point it at the running binary.
	if err := os.MkdirAll(filepath.Join(w.home, "bin"), 0o700); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(w.home, "bin", "admiral")
	if err := os.Symlink("/nonexistent/admiral", link); err != nil {
		t.Fatal(err)
	}
	w.writeCue(
		"event UserPromptSubmit "+`{"hook_event_name":"UserPromptSubmit","prompt":"go"}`,
		"wait go1",
		"event Stop "+claudeStopSubagent,
		"wait go2",
		"event Stop "+claudeStopNone,
	)
	w.start()

	// A7: the link and the PATH.
	want, err := filepath.EvalSymlinks(binaryPath(t))
	if err != nil {
		t.Fatal(err)
	}
	if got, err := filepath.EvalSymlinks(link); err != nil || got != want {
		t.Errorf("%s resolves to %q (%v), want the running binary %s", link, got, err, want)
	}

	ship := w.launch(w.order("work", "claude", "hookship", commodore, "x"))
	r := w.record(ship.Session)
	if first := strings.Split(r.env["PATH"], ":")[0]; first != filepath.Join(w.home, "bin") {
		t.Errorf("ship PATH starts with %q, want %s", first, filepath.Join(w.home, "bin"))
	}

	pane := strings.TrimSpace(w.mustTmux("display-message", "-p", "-t", ship.Session, "#{pane_id}"))
	state := func(id string) string {
		out, err := w.tmux("show-options", "-p", "-v", "-t", id, "@admiral-state")
		if err != nil {
			return "" // unset
		}
		return out
	}
	waitFor := func(what string, ok func() bool) {
		t.Helper()
		deadline := time.Now().Add(10 * time.Second)
		for !ok() {
			if time.Now().After(deadline) {
				t.Fatalf("%s within 10s did not happen; hook errors: %s", what, readIf(filepath.Join(r.dir, "event-error")))
			}
			time.Sleep(50 * time.Millisecond)
		}
	}
	eventsDone := func() int {
		return strings.Count(readIf(filepath.Join(r.dir, "events-done")), "\n")
	}

	// A second window makes another pane the session's active one, so a write
	// without `-t $TMUX_PANE` would miss the ship's pane.
	waitFor("the first event", func() bool { return eventsDone() == 1 })
	w.mustTmux("new-window", "-d", "-t", ship.Session)
	if got := state(pane); got != "working" {
		t.Fatalf("after UserPromptSubmit the ship's pane @admiral-state = %q, want working", got)
	}

	// Stop with a running subagent: the hook runs and writes nothing.
	touch(t, filepath.Join(r.dir, "go1"))
	waitFor("the second event", func() bool { return eventsDone() == 2 })
	if got := state(pane); got != "working" {
		t.Errorf("after Stop with a running subagent @admiral-state = %q, want working", got)
	}

	// Stop with nothing running: needs_you, on the ship's pane by id.
	touch(t, filepath.Join(r.dir, "go2"))
	waitFor("the third event", func() bool { return eventsDone() == 3 })
	if got := state(pane); got != "needs_you" {
		t.Errorf("after Stop with background_tasks empty @admiral-state = %q, want needs_you", got)
	}
	if out := readIf(filepath.Join(r.dir, "event-stdout")); out != "" {
		t.Errorf("the hook printed %q on stdout, want nothing", out)
	}
	for _, id := range strings.Fields(w.mustTmux("list-panes", "-s", "-t", ship.Session, "-F", "#{pane_id}")) {
		if id != pane && state(id) != "" {
			t.Errorf("pane %s got @admiral-state %q: the write must target $TMUX_PANE", id, state(id))
		}
	}
}

// #113: the hook leaves @admiral-reason beside @admiral-state, in real tmux. A
// permission prompt makes Claude fire PermissionRequest and Notification in no
// fixed order, and the permission reason survives either; the next working
// event clears it; nothing of the payload but the tool name is stored.
func TestShipHookReason(t *testing.T) {
	w := newWorld(t)
	const secret = "Drop the production database?"
	w.writeCue(
		"event PermissionRequest "+`{"hook_event_name":"PermissionRequest","tool_name":"Bash","tool_input":{"command":"echo secret-command"}}`,
		"wait go1",
		"event Notification "+`{"hook_event_name":"Notification","notification_type":"permission_prompt","message":"needs permission for secret-command"}`,
		"wait go2",
		"event PostToolUse "+`{"hook_event_name":"PostToolUse","tool_name":"Bash"}`,
		"wait go3",
		"event Notification "+`{"hook_event_name":"Notification","notification_type":"permission_prompt","message":"x"}`,
		"wait go4",
		"event PermissionRequest "+`{"hook_event_name":"PermissionRequest","tool_name":"Edit"}`,
		"wait go5",
		"event PreToolUse "+`{"hook_event_name":"PreToolUse","tool_name":"AskUserQuestion","tool_input":{"questions":[{"question":"`+secret+`"}]}}`,
		"wait go6",
		"event UserPromptSubmit "+`{"hook_event_name":"UserPromptSubmit","prompt":"go"}`,
		"wait go7",
		"event Notification "+`{"hook_event_name":"Notification","notification_type":"elicitation_dialog"}`,
	)
	w.start()
	ship := w.launch(w.order("work", "claude", "hookreason", commodore, "x"))
	r := w.record(ship.Session)
	events := func() int { return strings.Count(readIf(filepath.Join(r.dir, "events-done")), "\n") }
	step := func(n int, state, reason string) {
		t.Helper()
		deadline := time.Now().Add(10 * time.Second)
		for events() < n {
			if time.Now().After(deadline) {
				t.Fatalf("event %d did not finish in 10s; hook errors: %s", n, readIf(filepath.Join(r.dir, "event-error")))
			}
			time.Sleep(50 * time.Millisecond)
		}
		if got := w.paneState(ship.Session); got != state {
			t.Errorf("after event %d @admiral-state = %q, want %q", n, got, state)
		}
		if got := w.paneReason(ship.Session); got != reason {
			t.Errorf("after event %d @admiral-reason = %q, want %q", n, got, reason)
		}
	}
	release := func(n int) { touch(t, filepath.Join(r.dir, "go"+strconv.Itoa(n))) }
	step(1, "needs_you", "permission:Bash")
	release(1)
	step(2, "needs_you", "permission:Bash") // the Notification after it doesn't replace it
	release(2)
	step(3, "working", "")
	release(3)
	step(4, "needs_you", "notification:permission_prompt")
	release(4)
	step(5, "needs_you", "permission:Edit") // the PermissionRequest after a Notification does
	release(5)
	step(6, "needs_you", "question")
	if out := w.mustTmux("show-options", "-p", "-t", ship.Session); strings.Contains(out, secret) || strings.Contains(out, "secret-command") {
		t.Errorf("the pane options hold payload text:\n%s", out)
	}
	release(6)
	step(7, "working", "")
	// A reason left beside working (a lost unset) is kept by the Notification's
	// -o, but -q keeps tmux from skipping the state write that follows it.
	w.mustTmux("set-option", "-p", "-t", ship.Session, "@admiral-reason", "stale")
	release(7)
	step(8, "needs_you", "stale")
}

func readIf(p string) string {
	b, _ := os.ReadFile(p)
	return string(b)
}

func touch(t *testing.T, p string) {
	t.Helper()
	if err := os.WriteFile(p, nil, 0o600); err != nil {
		t.Fatal(err)
	}
}
