package e2e

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// `admiral crew launch --arg` and `--role` (#175, unit u1). The seam is the
// built binary and the fake harness's recorded argv. Every crew boat of one
// test shares FAKE_CUE, so each boat's argv is read only after its record.

func TestCrew_LaunchArgsAndRole(t *testing.T) {
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
	launch := func(w *world, extra ...string) cliResult {
		w.t.Helper()
		return crewCall(w, sess, "", append([]string{"launch", "--dir", dir}, extra...)...)
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

	t.Run("--arg values follow the hook flags and come right before the prompt", func(t *testing.T) {
		w := w.sub(t)
		w.expectExit(launch(w, "--name", "cl", "--arg", "--model", "--arg", "opus", "--arg", "--advisor", "--arg", "opus", "--", "do it"), 0, "claude with --arg")
		cl := w.record(sess + "/crew/cl")
		want := []string{"--fa-claude", "--settings", filepath.Join(w.home, "harness", "claude-settings.json"),
			"--model", "opus", "--advisor", "opus", "--", "do it"}
		if !reflect.DeepEqual(cl.argv, want) {
			t.Errorf("claude argv = %q, want %q", cl.argv, want)
		}

		w.expectExit(launch(w, "--name", "cx", "--harness", "codex", "--arg", "-m", "--arg", "x", "--", "do it"), 0, "codex with --arg")
		cx := w.record(sess + "/crew/cx").argv
		if n := len(cx); n < 5 || !reflect.DeepEqual(cx[n-4:], []string{"-m", "x", "--", "do it"}) || cx[0] != "--fa-codex" || !has(cx, `tui.notification_method="bel"`) {
			t.Errorf("codex argv = %q, want its dock args and hook flags, then -m x, then -- and the prompt", cx)
		}

		w.expectExit(launch(w, "--name", "cp", "--harness", "copilot", "--arg", "--model", "--arg", "x", "--", "do it"), 0, "copilot with --arg")
		cp := w.record(sess + "/crew/cp").argv
		if n := len(cp); n < 4 || !reflect.DeepEqual(cp[n-3:], []string{"--model", "x", "--interactive=do it"}) || cp[0] != "--fa-copilot" {
			t.Errorf("copilot argv = %q, want its dock args and hook flags, then --model x, then --interactive=<prompt>", cp)
		}
	})

	t.Run("--role is a window option and a list field", func(t *testing.T) {
		w := w.sub(t)
		w.expectExit(launch(w, "--name", "rev", "--role", "reviewer", "--", "review"), 0, "launch --role reviewer")
		w.expectExit(launch(w, "--name", "norole", "--", "plain"), 0, "launch without --role")
		w.waitFile(sess+"/crew/rev", "cues-done")
		w.waitFile(sess+"/crew/norole", "cues-done")
		win := "=" + sess + ":" + w.paneOf(sess, "rev").window
		if got := w.mustTmux("show", "-wv", "-t", win, "@admiral-role"); got != "reviewer" {
			t.Errorf("rev @admiral-role = %q, want reviewer", got)
		}
		if opts := w.waitFile(sess+"/crew/rev", "window-options"); !strings.Contains(opts, "@admiral-role reviewer") {
			t.Errorf("window options at harness start lack @admiral-role reviewer:\n%s", opts)
		}
		rows := crewList(w, sess)
		if got, ok := rows["rev"].raw["role"]; !ok || got != "reviewer" {
			t.Errorf("rev role = %v (present %v), want reviewer", got, ok)
		}
		if got, ok := rows["norole"].raw["role"]; !ok || got != nil {
			t.Errorf("norole role = %v (present %v), want null", got, ok)
		}

		// Option text is untrusted: a role that fails the shape reads as null.
		w.mustTmux("set", "-w", "-t", win, "@admiral-role", "Bad Role")
		if got, ok := crewList(w, sess)["rev"].raw["role"]; !ok || got != nil {
			t.Errorf("rev role after a bad option = %v (present %v), want null", got, ok)
		}
	})

	t.Run("a bad --role is a usage error and launches nothing", func(t *testing.T) {
		w := w.sub(t)
		before := crewWindows(w)
		r := launch(w, "--name", "badrole", "--role", "Bad_Role", "--", "hi")
		w.expectExit(r, 2, "launch --role Bad_Role")
		if strings.Contains(r.stderr, "not defined") || !strings.Contains(r.stderr, "role") {
			t.Errorf("stderr = %q, want a message about the role shape, not an unknown flag", r.stderr)
		}
		if after := crewWindows(w); after != before {
			t.Errorf("a refused launch left %d new crew windows", after-before)
		}
	})

	t.Run("values that override or disable the hooks are refused", func(t *testing.T) {
		w := w.sub(t)
		before := crewWindows(w)
		cases := []struct {
			harness string
			args    []string
			value   string
		}{
			{"claude", []string{"--arg", "--settings", "--arg", "x"}, "--settings"},
			{"claude", []string{"--arg", "--bare"}, "--bare"},
			{"codex", []string{"--arg", "-c", "--arg", "hooks.Stop=[]"}, "hooks.Stop=[]"},
			{"copilot", []string{"--arg", "--plugin-dir", "--arg", "/tmp/x"}, "--plugin-dir"},
			{"claude", []string{"--arg=--"}, "--"},
		}
		for _, c := range cases {
			args := append([]string{"--name", "refused", "--harness", c.harness}, c.args...)
			r := launch(w, append(args, "--", "hi")...)
			w.expectExit(r, 1, c.harness+" "+strings.Join(c.args, " "))
			if !strings.Contains(r.stderr, c.value) {
				t.Errorf("%s %q stderr = %q, want it to name %q", c.harness, c.args, r.stderr, c.value)
			}
		}
		// Always, autopilot or not.
		auto := w.admiralWith(cliOpts{env: []string{"ADMIRAL_SHIP=" + sess, "ADMIRAL_AUTOPILOT=1"}},
			"crew", "launch", "--name", "refused", "--dir", dir, "--arg", "--settings", "--arg", "x", "--", "hi")
		w.expectExit(auto, 1, "an autopilot ship passing --settings")
		if !strings.Contains(auto.stderr, "--settings") {
			t.Errorf("autopilot stderr = %q, want it to name --settings", auto.stderr)
		}
		// `--arg --` splits at the CLI's own first --, so it is a usage error.
		w.expectExit(launch(w, "--name", "refused", "--arg", "--", "hi"), 2, "--arg -- (space form)")
		if after := crewWindows(w); after != before {
			t.Errorf("refused launches left %d new crew windows", after-before)
		}
	})
}

// keep encoding/json in use for a role field decoded as a string pointer.
var _ = json.Marshal
