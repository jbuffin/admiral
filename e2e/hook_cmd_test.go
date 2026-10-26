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

// `admiral hook <event>` (#69): the command every harness hook runs. It reads
// the harness's JSON on stdin and sets @admiral-state on $TMUX_PANE, or does
// nothing. The seam is the built binary with a stdin payload and a recording
// tmux first on PATH, so the table below is payload in, state out. The payloads
// are the live-check excerpts in docs/research/crew-boats.md and the Stop
// payloads in the probe (Claude Code 2.1.306), cut to the fields that matter.

const (
	claudeStopSubagent = `{"hook_event_name":"Stop","stop_hook_active":false,"last_assistant_message":"The Explore agent is still running","background_tasks":[{"id":"a0f790cb4a518e0f9","type":"subagent","status":"running","description":"Read notes.txt first line","agent_type":"Explore"}],"session_crons":[]}`
	claudeStopNone     = `{"hook_event_name":"Stop","last_assistant_message":"alpha","background_tasks":[],"session_crons":[]}`
	claudeStopShell    = `{"hook_event_name":"Stop","background_tasks":[{"id":"bvoz9m2jt","type":"shell","status":"running","description":"Ticks every 40s, three total","command":"for i in 1 2 3; do sleep 40; echo tick-$i; done"}],"session_crons":[]}`
	claudeStopShellAnd = `{"hook_event_name":"Stop","background_tasks":[{"id":"bom9319x3","type":"shell","status":"running","description":"Sleep 60","command":"sleep 60"},{"id":"a109","type":"subagent","status":"running","description":"d","agent_type":"general-purpose"}],"session_crons":[]}`
	claudeStopMate     = `{"hook_event_name":"Stop","background_tasks":[{"id":"t1","type":"teammate","status":"running","description":"d"}]}`
	claudeStopDone     = `{"hook_event_name":"Stop","background_tasks":[{"id":"a109","type":"subagent","status":"completed","description":"d","agent_type":"general-purpose"}]}`
	codexStop          = `{"hook_event_name":"Stop","turn_id":"01a15222-ed87","last_assistant_message":"done"}`
)

func TestHookDecision(t *testing.T) {
	const none = ""
	// reason is what @admiral-reason must hold with the state: a code for
	// needs_you, and for working an unset (#113).
	cases := []struct {
		name, event, payload, want, reason string
	}{
		// A1, A2, A12 and the rest of the Stop rule.
		{"claude Stop with a running subagent", "Stop", claudeStopSubagent, none, ""},
		{"claude Stop with background_tasks empty", "Stop", claudeStopNone, "needs_you", "stop"},
		{"claude Stop with only a running shell task", "Stop", claudeStopShell, "needs_you", "stop"},
		{"claude Stop with a running shell and a running subagent", "Stop", claudeStopShellAnd, none, ""},
		{"claude Stop with a running teammate", "Stop", claudeStopMate, none, ""},
		{"claude Stop with a finished subagent", "Stop", claudeStopDone, "needs_you", "stop"},
		{"codex Stop has no background_tasks", "Stop", codexStop, "needs_you", "stop"},
		// The rest of Claude's and Codex's table.
		{"PermissionRequest", "PermissionRequest", `{"hook_event_name":"PermissionRequest","tool_name":"Bash"}`, "needs_you", "permission:Bash"},
		{"PermissionRequest with a tool name that is not a safe token", "PermissionRequest", `{"hook_event_name":"PermissionRequest","tool_name":"Bash; kill-server #{pane_id}"}`, "needs_you", "permission"},
		{"UserPromptSubmit", "UserPromptSubmit", `{"hook_event_name":"UserPromptSubmit","prompt":"go"}`, "working", ""},
		{"PostToolUse", "PostToolUse", `{"hook_event_name":"PostToolUse","tool_name":"Bash"}`, "working", ""},
		{"PostToolUse whose tool_response is a string", "PostToolUse", `{"hook_event_name":"PostToolUse","tool_name":"Read","tool_response":"file text"}`, "working", ""},
		{"PostToolUse of a Monitor", "PostToolUse", `{"hook_event_name":"PostToolUse","tool_name":"Monitor","tool_response":{"taskId":"bp7cr4s34","timeoutMs":60000,"persistent":false}}`, "working", ""},
		{"codex Interrupt", "Interrupt", `{"hook_event_name":"Interrupt","turn_id":"01a15222-ed87","model":"gpt-6-luna"}`, "working", ""},
		// AC3: the question text and tool input never reach tmux.
		{"PreToolUse of AskUserQuestion", "PreToolUse", `{"hook_event_name":"PreToolUse","tool_name":"AskUserQuestion","tool_input":{"questions":[{"question":"Drop the production database?","options":[{"label":"Yes, drop it"}]}]}}`, "needs_you", "question"},
		{"PreToolUse of Bash", "PreToolUse", `{"hook_event_name":"PreToolUse","tool_name":"Bash","tool_input":{"command":"ls"}}`, "working", ""},
		{"PreToolUse of Bash inside a subagent", "PreToolUse", `{"hook_event_name":"PreToolUse","agent_id":"a0f790cb4a518e0f9","agent_type":"Explore","tool_name":"Bash","tool_input":{"command":"find ."}}`, "working", ""},
		{"claude permission_prompt", "Notification", `{"hook_event_name":"Notification","notification_type":"permission_prompt","message":"Claude needs your permission to run rm -rf build"}`, "needs_you", "notification:permission_prompt"},
		{"claude elicitation_dialog", "Notification", `{"hook_event_name":"Notification","notification_type":"elicitation_dialog"}`, "needs_you", "notification:elicitation_dialog"},
		// A3, A4 and Copilot's table.
		{"copilot agentStop inside a subagent", "agentStop", `{"stopReason":"end_turn","stop_hook_active":false,"agentId":"788718ef-39f6-45b1-ade6-7cb742bc6958","agentType":"general-purpose","parentAgentId":"796c5372-28bb-496c-bb5b-f9e223ac6ff8"}`, none, ""},
		{"copilot agentStop of the lead", "agentStop", `{"stopReason":"end_turn","stop_hook_active":false}`, "needs_you", "stop"},
		{"copilot userPromptSubmitted inside a subagent", "userPromptSubmitted", `{"prompt":"go","agentId":"788718ef-39f6-45b1-ade6-7cb742bc6958","agentType":"general-purpose","parentAgentId":"796c5372-28bb-496c-bb5b-f9e223ac6ff8"}`, none, ""},
		{"copilot userPromptSubmitted of the lead", "userPromptSubmitted", `{"prompt":"go"}`, "working", ""},
		{"copilot preToolUse of ask_user", "preToolUse", `{"toolName":"ask_user","toolArgs":{"question":"which?"}}`, "needs_you", "question"},
		{"copilot postToolUse", "postToolUse", `{"toolName":"bash","toolArgs":{"command":"ls"}}`, "working", ""},
		{"copilot permission_prompt", "notification", `{"message":"Run command: sleep 30","title":"Permission needed","hook_event_name":"Notification","notification_type":"permission_prompt"}`, "needs_you", "notification:permission_prompt"},
		{"copilot elicitation_dialog", "notification", `{"hook_event_name":"Notification","notification_type":"elicitation_dialog"}`, "needs_you", "notification:elicitation_dialog"},
	}
	w := newWorld(t)
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			res := w.at(t).runHook(c.event, c.payload, "%7")
			res.requireQuiet(t)
			if c.want == none {
				if len(res.calls) != 0 {
					t.Errorf("admiral hook %s wrote %q, want no tmux call", c.event, res.calls)
				}
				return
			}
			res.requireOneWrite(t, "%7", c.want, c.reason, c.event == "Notification" || c.event == "notification")
		})
	}
}

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

// requireOneWrite checks the hook made a single tmux call holding two `set`
// commands joined by `;`: the pane's @admiral-state, and @admiral-reason set to
// reason (or unset when reason is ""). A needs_you call writes the reason first,
// so the poll never sees the state without it. A reason from a notification
// is written only if unset (`-o`), with `-q` so that an existing one doesn't
// error and cut the state write off.
func (r hookResult) requireOneWrite(t *testing.T, pane, state, reason string, onlyIfUnset bool) {
	t.Helper()
	if len(r.calls) != 1 {
		t.Fatalf("tmux calls = %q, want exactly one", r.calls)
	}
	var sets []tmuxSet
	cmd := []string{}
	for _, a := range append(append([]string{}, r.calls[0]...), ";") {
		if a != ";" {
			cmd = append(cmd, a)
			continue
		}
		sets = append(sets, parseTmuxSet(cmd))
		cmd = nil
	}
	if len(sets) != 2 {
		t.Fatalf("tmux %q holds %d commands, want 2 (reason and state)", r.calls[0], len(sets))
	}
	ri, si := -1, -1
	for i, s := range sets {
		switch s.option {
		case "@admiral-reason":
			ri = i
		case "@admiral-state":
			si = i
		}
		if s.name != "set" || !s.pane || s.target != pane {
			t.Errorf("tmux %q: command %d is not a pane-scoped set on %s", r.calls[0], i, pane)
		}
	}
	if ri < 0 || si < 0 {
		t.Fatalf("tmux %q, want one @admiral-reason and one @admiral-state command", r.calls[0])
	}
	if st := sets[si]; st.value != state || st.unset || st.onlyIfUnset {
		t.Errorf("tmux %q: the state command is %+v, want a plain write of %s", r.calls[0], st, state)
	}
	re := sets[ri]
	switch {
	case reason == "":
		if !re.unset || re.value != "" {
			t.Errorf("tmux %q: the reason command is %+v, want an unset (-u)", r.calls[0], re)
		}
	default:
		if re.unset || re.value != reason || re.onlyIfUnset != onlyIfUnset {
			t.Errorf("tmux %q: the reason command is %+v, want %q written (only if unset: %v)", r.calls[0], re, reason, onlyIfUnset)
		}
		if re.onlyIfUnset && !re.quiet {
			t.Errorf("tmux %q: -o without -q makes tmux fail on a set reason and skip the state write", r.calls[0])
		}
		if state == "needs_you" && ri > si {
			t.Errorf("tmux %q writes the state before the reason", r.calls[0])
		}
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
