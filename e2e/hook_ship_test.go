package e2e

import (
	"os"
	"path/filepath"
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
