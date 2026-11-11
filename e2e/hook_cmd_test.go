package e2e

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

// `admiral hook <event>` as a harness runs it: the built binary with a stdin
// payload and a recording tmux first on PATH. Which state each event writes is
// a unit test in internal/cli.

const (
	claudeStopSubagent = `{"hook_event_name":"Stop","stop_hook_active":false,"last_assistant_message":"The Explore agent is still running","background_tasks":[{"id":"a0f790cb4a518e0f9","type":"subagent","status":"running","description":"Read notes.txt first line","agent_type":"Explore"}],"session_crons":[]}`
	claudeStopNone     = `{"hook_event_name":"Stop","last_assistant_message":"alpha","background_tasks":[],"session_crons":[]}`
)

// A5: nothing the harness sends can make `admiral hook` fail, block or write.
func TestHookNeverFails(t *testing.T) {
	w := newWorld(t)
	for _, c := range []struct{ name, event, payload, pane string }{
		{"empty stdin", "Stop", "", "%7"},
		{"malformed JSON", "Stop", `{"hook_event_name":"Stop","background_tasks":[`, "%7"},
		{"not an object", "Stop", `["Stop"]`, "%7"},
		{"unknown event", "NoSuchEvent", claudeStopNone, "%7"},
		{"no TMUX_PANE", "Stop", claudeStopNone, ""},
	} {
		t.Run(c.name, func(t *testing.T) {
			res := w.at(t).runHook(c.event, c.payload, c.pane)
			res.requireQuiet(t)
			if len(res.calls) != 0 {
				t.Errorf("wrote %q, want no tmux call", res.calls)
			}
		})
	}
	t.Run("no event", func(t *testing.T) {
		res := w.at(t).runHook("", claudeStopNone, "%7")
		res.requireQuiet(t)
		if len(res.calls) != 0 {
			t.Errorf("wrote %q, want no tmux call", res.calls)
		}
	})
}

// A11: `hook` is internal, so no help text names it.
func TestHookIsNotListed(t *testing.T) {
	w := newWorld(t)
	r := w.admiral("nosuchcommand", "--help")
	w.expectExit(r, 0, "admiral nosuchcommand --help")
	if strings.Contains(r.both(), "hook") {
		t.Errorf("the command list names hook:\n%s", r.both())
	}
}

type hookResult struct {
	stdout, stderr string
	code           int
	calls          [][]string // each tmux call, without the program name
}

func (r hookResult) requireQuiet(t *testing.T) {
	t.Helper()
	if r.code != 0 {
		t.Errorf("exit %d, want 0 (stderr %q)", r.code, r.stderr)
	}
	if r.stdout != "" {
		t.Errorf("stdout %q, want nothing: some harnesses read hook stdout as a decision", r.stdout)
	}
}

// tmuxSet is one `set` command of a tmux call, flags in any order.
type tmuxSet struct {
	name, target, option, value string
	pane, unset, onlyIfUnset    bool
	quiet                       bool
}

func parseTmuxSet(argv []string) tmuxSet {
	var s tmuxSet
	if len(argv) == 0 {
		return s
	}
	s.name = argv[0]
	var rest []string
	for i := 1; i < len(argv); i++ {
		switch a := argv[i]; a {
		case "-p":
			s.pane = true
		case "-u":
			s.unset = true
		case "-o":
			s.onlyIfUnset = true
		case "-q":
			s.quiet = true
		case "-t":
			i++
			if i < len(argv) {
				s.target = argv[i]
			}
		default:
			rest = append(rest, a)
		}
	}
	if len(rest) > 0 {
		s.option = rest[0]
	}
	if len(rest) > 1 {
		s.value = rest[1]
	}
	return s
}

// runHook runs `admiral hook <event>` with the payload on stdin and $TMUX_PANE
// set to pane (unset when empty), with a recording tmux first on PATH.
func (w *world) runHook(event, payload, pane string) hookResult {
	w.t.Helper()
	dir := filepath.Join(w.root, "rectmux")
	rec := filepath.Join(w.t.TempDir(), "calls")
	w.writeRaw(filepath.Join(dir, "tmux"), "#!/bin/sh\nprintf '%s\\0' \"$@\" >>\"$FAKE_TMUX_RECORD\"\nprintf '\\n' >>\"$FAKE_TMUX_RECORD\"\n", 0o755)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	args := []string{"hook"}
	if event != "" {
		args = append(args, event)
	}
	cmd := exec.CommandContext(ctx, binaryPath(w.t), args...)
	cmd.Dir = w.userHom
	cmd.Stdin = strings.NewReader(payload)
	for _, kv := range w.baseEnv() {
		if k, _, _ := strings.Cut(kv, "="); k != "PATH" {
			cmd.Env = append(cmd.Env, kv)
		}
	}
	cmd.Env = append(cmd.Env, "PATH="+dir+":"+os.Getenv("PATH"), "FAKE_TMUX_RECORD="+rec)
	if pane != "" {
		cmd.Env = append(cmd.Env, "TMUX_PANE="+pane)
	}
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	res := hookResult{}
	if err := cmd.Run(); err != nil {
		ee, ok := err.(*exec.ExitError)
		if !ok || ctx.Err() != nil {
			w.t.Fatalf("admiral hook %s: %v", event, err)
		}
		res.code = ee.ExitCode()
	}
	res.stdout, res.stderr = out.String(), errb.String()
	res.calls = withoutActivityStamp(w.t, attachCalls(w, rec), pane)
	return res
}

// withoutActivityStamp checks, then drops, the `set -p -t <pane> @admiral-activity
// <RFC 3339>` that opens every hook call (the crew boat's last_activity), in the
// same call as any state write, so the table above stays about the state and
// reason a hook writes.
func withoutActivityStamp(t *testing.T, calls [][]string, pane string) [][]string {
	t.Helper()
	var out [][]string
	for _, c := range calls {
		if len(c) > 0 && c[0] == "-u" {
			continue // a read (harness, crew, subagents): the recording tmux answers none
		}
		if len(c) < 6 || c[0] != "set" || c[1] != "-p" || c[2] != "-t" || c[3] != pane || c[4] != "@admiral-activity" {
			t.Errorf("hook call %q does not open with `set -p -t %s @admiral-activity <time>`", c, pane)
			continue
		}
		if _, err := time.Parse(time.RFC3339, c[5]); err != nil {
			t.Errorf("hook call %q: the stamp is not RFC 3339", c)
		}
		c = c[6:]
		if len(c) > 0 && c[0] == ";" {
			c = c[1:]
		}
		// The @admiral-subagents and @admiral-monitors writes are covered by
		// TestCrewHook_LeadWaitsForWorkingCrew and TestHook_StopWaitsForMonitor.
		var kept []string
		var cmd []string
		for _, a := range append(c, ";") {
			if a != ";" {
				cmd = append(cmd, a)
				continue
			}
			if len(cmd) > 0 && !slices.Contains(cmd, "@admiral-subagents") && !slices.Contains(cmd, "@admiral-monitors") {
				if len(kept) > 0 {
					kept = append(kept, ";")
				}
				kept = append(kept, cmd...)
			}
			cmd = nil
		}
		if len(kept) > 0 {
			out = append(out, kept)
		}
	}
	return out
}
