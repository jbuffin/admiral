package e2e

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// The autopilot args allowlist and harness refusal on `admiral crew launch`
// (#175, unit u2). A caller with ADMIRAL_AUTOPILOT=1 may pass only the listed
// flags, and only claude launches. The seam is the built binary.

func TestCrew_LaunchUnderAutopilot(t *testing.T) {
	w := newWorld(t)
	w.writeCue("hook working")
	w.start()
	ship := w.launch(w.order("work", "claude", "alpha", noSkill, "go"))
	w.waitFile(ship.Session, "cues-done")
	sess := ship.Session
	dir := filepath.Join(w.root, "anywhere")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	launch := func(w *world, autopilot bool, extra ...string) cliResult {
		w.t.Helper()
		if !autopilot {
			return crewCall(w, sess, "", append([]string{"launch", "--dir", dir}, extra...)...)
		}
		return w.admiralWith(cliOpts{env: []string{"ADMIRAL_SHIP=" + sess, "ADMIRAL_AUTOPILOT=1"}},
			append([]string{"crew", "launch", "--dir", dir}, extra...)...)
	}
	crewWindows := func(w *world) int {
		n := 0
		for _, p := range w.crewPanes(sess) {
			if p.crew != "" {
				n++
			}
		}
		return n
	}

	t.Run("the args allowlist refuses what loosens the dock and passes the listed flags", func(t *testing.T) {
		w := w.sub(t)
		before := crewWindows(w)
		r := launch(w, true, "--name", "loose", "--arg", "--dangerously-skip-permissions", "--", "hi")
		w.expectExit(r, 1, "autopilot with --dangerously-skip-permissions")
		if !strings.Contains(r.stderr, "--dangerously-skip-permissions") {
			t.Errorf("stderr = %q, want it to name --dangerously-skip-permissions", r.stderr)
		}
		if after := crewWindows(w); after != before {
			t.Errorf("a refused launch left %d new crew windows", after-before)
		}

		w.expectExit(launch(w, true, "--name", "ok", "--arg", "--model", "--arg", "opus", "--", "do it"), 0, "autopilot with --model opus")
		got := w.record(sess + "/crew/ok").argv
		want := []string{"--fa-claude", "--settings", filepath.Join(w.home, "harness", "claude-settings.json"), "--model", "opus", "--", "do it"}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("claude argv = %q, want %q", got, want)
		}
	})

	t.Run("only claude launches", func(t *testing.T) {
		w := w.sub(t)
		before := crewWindows(w)
		for _, h := range []string{"codex", "copilot"} {
			r := launch(w, true, "--name", "other-"+h, "--harness", h, "--", "hi")
			w.expectExit(r, 1, "autopilot --harness "+h)
			if !strings.Contains(r.stderr, h) {
				t.Errorf("%s stderr = %q, want it to name the harness", h, r.stderr)
			}
		}
		if after := crewWindows(w); after != before {
			t.Errorf("refused launches left %d new crew windows", after-before)
		}
		w.expectExit(launch(w, true, "--name", "cl", "--", "hi"), 0, "autopilot claude")

		// Without autopilot, the same launches succeed.
		for _, h := range []string{"codex", "copilot"} {
			w.expectExit(launch(w, false, "--name", "free-"+h, "--harness", h, "--", "hi"), 0, "--harness "+h+" without autopilot")
		}
	})
}
