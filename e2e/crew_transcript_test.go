package e2e

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// A crew boat's transcript path (#175, ADR 0021): `admiral hook` records it in
// the pane option @admiral-crew-transcript, and `admiral crew list --json` shows
// it as `transcript`, or null. Crew boats only: a ship's pane never gets it. The
// chat's @admiral-transcript on SessionStart is covered by internal/cli's
// TestTranscriptCall and TestChatSession_ConversationSendNewChatAndRestart.

const crewTranscriptOption = "@admiral-crew-transcript"

// transcriptField is the `transcript` of a crew boat's list row; it fails the
// test when the key is missing, since the field is null, not absent.
func transcriptField(w *world, ship, name string) (string, bool) {
	w.t.Helper()
	row, ok := crewList(w, ship)[name]
	if !ok {
		w.t.Fatalf("crew list has no boat %q", name)
	}
	v, present := row.raw["transcript"]
	if !present {
		w.t.Fatalf("crew list --json row of %s lacks %q: %v", name, "transcript", row.raw)
	}
	if v == nil {
		return "", false
	}
	s, isStr := v.(string)
	if !isStr {
		w.t.Fatalf("transcript of %s = %v, want a string or null", name, v)
	}
	return s, true
}

func wantTranscript(w *world, ship, name, want string, wantSet bool, why string) {
	w.t.Helper()
	got, set := transcriptField(w, ship, name)
	if got != want || set != wantSet {
		w.t.Errorf("%s: %s transcript = %q (set %v), want %q (set %v)", why, name, got, set, want, wantSet)
	}
}

func waitEvents(w *world, session string, n int) {
	w.t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		data, _ := os.ReadFile(filepath.Join(w.rec, session, "events-done"))
		if strings.Count(string(data), "\n") >= n {
			return
		}
		if time.Now().After(deadline) {
			w.t.Fatalf("%s ran fewer than %d cue events within 10s: %q", session, n, data)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// Claude records from any event, a later path replaces it, the ship (which runs
// the same cue and gets the same payloads) records nothing, and a boat that was
// never given a path reads null.
func TestCrewTranscript_ClaudeRecordsAndReplaces(t *testing.T) {
	const one, two = "/tmp/admiral u3/one.jsonl", "/tmp/admiral u3/two.jsonl"
	w := newWorld(t)
	w.writeCue(
		`event UserPromptSubmit `+jsonLine(map[string]any{"hook_event_name": "UserPromptSubmit", "transcript_path": one}),
		"wait second",
		`event PostToolUse `+jsonLine(map[string]any{"hook_event_name": "PostToolUse", "tool_name": "Bash", "transcript_path": two}),
		"wait third",
		`event Stop `+jsonLine(map[string]any{"hook_event_name": "Stop", "background_tasks": []any{}, "transcript_path": "relative/x.jsonl"}),
	)
	w.start()
	ship := w.launch(w.order("work", "claude", "alpha", noSkill, "go"))
	sess := ship.Session
	dir := filepath.Join(w.root, "anywhere")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	w.expectExit(crewCall(w, sess, "", "launch", "--name", "cl", "--dir", dir, "--", "go"), 0, "crew launch cl")
	boat := sess + "/crew/cl"
	waitEvents(w, boat, 1)
	waitEvents(w, sess, 1)
	wantTranscript(w, sess, "cl", one, true, "after the first event")

	w.release(boat, "second")
	w.release(sess, "second")
	waitEvents(w, boat, 2)
	waitEvents(w, sess, 2)
	wantTranscript(w, sess, "cl", two, true, "a later event's path replaces it")

	// An event with an invalid path leaves the recorded one.
	w.release(boat, "third")
	w.release(sess, "third")
	waitEvents(w, boat, 3)
	waitEvents(w, sess, 3)
	wantTranscript(w, sess, "cl", two, true, "an invalid path leaves the recorded one")

	// The ship's pane got the same three payloads and records none of them.
	shipPane := strings.TrimSpace(w.mustTmux("display-message", "-p", "-t", "="+sess+":0", "#{pane_id}"))
	if got, _ := w.tmux("show-options", "-pqv", "-t", shipPane, crewTranscriptOption); got != "" {
		t.Errorf("the ship's pane has %s = %q, want it unset", crewTranscriptOption, got)
	}
}

// Every harness's rows, run as the harness's hook would (`admiral hook <Event>`
// on the crew boat's pane), and a value written straight to the option.
func TestCrewTranscript_HooksPerHarnessAndReadBack(t *testing.T) {
	w := newWorld(t)
	w.start()
	ship := w.launch(w.order("work", "claude", "alpha", noSkill, "go"))
	w.waitFile(ship.Session, "cues-done")
	sess := ship.Session
	dir := filepath.Join(w.root, "anywhere")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, b := range []struct{ name, harness string }{{"cl", "claude"}, {"cx", "codex"}, {"cp", "copilot"}, {"fresh", "claude"}, {"0", "claude"}} {
		w.expectExit(crewCall(w, sess, "", "launch", "--name", b.name, "--harness", b.harness, "--dir", dir, "--", "go"), 0, "crew launch "+b.name)
		w.waitFile(sess+"/crew/"+b.name, "cues-done")
	}
	shipPane := strings.TrimSpace(w.mustTmux("display-message", "-p", "-t", "="+sess+":0", "#{pane_id}"))
	hook := func(pane, event, stdin string) {
		t.Helper()
		r := w.admiralWith(cliOpts{env: []string{"TMUX=" + w.sock() + ",0,0", "TMUX_PANE=" + pane}, stdin: stdin}, "hook", event)
		w.expectExit(r, 0, "admiral hook "+event)
	}
	payload := func(kv ...any) string {
		m := map[string]any{}
		for i := 0; i < len(kv); i += 2 {
			m[kv[i].(string)] = kv[i+1]
		}
		return jsonLine(m)
	}

	// Nothing recorded: the key is there and null, for every boat.
	for _, n := range []string{"cl", "cx", "cp", "fresh"} {
		wantTranscript(w, sess, n, "", false, "before any path")
	}

	// Claude: a path with a space; then each invalid path leaves it as it was.
	cl := w.paneOf(sess, "cl").pane
	const good = "/Users/me/My Projects/.claude/x/abc.jsonl"
	hook(cl, "UserPromptSubmit", payload("transcript_path", good))
	wantTranscript(w, sess, "cl", good, true, "a Claude path with a space")
	for name, bad := range map[string]string{
		"relative": "x/abc.jsonl", "not .jsonl": "/a/abc.txt", "control char": "/a/b\u0001c.jsonl",
		"single quote": "/a/it's.jsonl", "semicolon": "/a/b;kill-server.jsonl", // the unit table has the full list
	} {
		hook(cl, "PostToolUse", payload("tool_name", "Bash", "transcript_path", bad))
		wantTranscript(w, sess, "cl", good, true, "an invalid path ("+name+")")
	}

	// A boat named "0": tmux's `if -F` treats a format of "0" as false, so the
	// gate must test for a non-empty name instead.
	const zeroPath = "/tmp/zero/boat.jsonl"
	hook(w.paneOf(sess, "0").pane, "UserPromptSubmit", payload("transcript_path", zeroPath))
	wantTranscript(w, sess, "0", zeroPath, true, "a crew boat named 0")

	// Codex: null records nothing, a path does.
	cx := w.paneOf(sess, "cx").pane
	hook(cx, "Stop", `{"turn_id":"t1","last_assistant_message":"done","transcript_path":null}`)
	wantTranscript(w, sess, "cx", "", false, "Codex's null")
	const codexPath = "/Users/me/.codex/sessions/2026/10/25/rollout-1.jsonl"
	hook(cx, "UserPromptSubmit", payload("turn_id", "t2", "transcript_path", codexPath))
	wantTranscript(w, sess, "cx", codexPath, true, "a Codex path")

	// Copilot: only a lead agentStop records.
	cp := w.paneOf(sess, "cp").pane
	const copilotPath = "/Users/me/.copilot/session-state/s1/events.jsonl"
	hook(cp, "subagentStart", payload("agentId", "788718ef", "agentType", "explore", "transcriptPath", copilotPath))
	hook(cp, "agentStop", payload("agentId", "788718ef", "stopReason", "end_turn", "transcriptPath", copilotPath))
	hook(cp, "subagentStop", payload("agentId", "788718ef", "transcriptPath", copilotPath))
	hook(cp, "userPromptSubmitted", payload("prompt", "go", "transcriptPath", copilotPath))
	wantTranscript(w, sess, "cp", "", false, "Copilot events other than a lead agentStop")
	hook(cp, "agentStop", payload("stopReason", "end_turn", "transcriptPath", copilotPath))
	wantTranscript(w, sess, "cp", copilotPath, true, "a Copilot lead agentStop")

	// The ship's pane never gets the option, whatever its payload carries; nor
	// does it disturb the boats' values.
	hook(shipPane, "UserPromptSubmit", payload("transcript_path", good))
	hook(shipPane, "Stop", payload("background_tasks", []any{}, "transcript_path", good))
	if got, _ := w.tmux("show-options", "-pqv", "-t", shipPane, crewTranscriptOption); got != "" {
		t.Errorf("the ship's pane has %s = %q, want it unset", crewTranscriptOption, got)
	}
	wantTranscript(w, sess, "fresh", "", false, "a boat that was given no path")

	// The option is untrusted text: a value written straight to it is checked
	// again on read.
	fresh := w.paneOf(sess, "fresh").pane
	set := func(v string) { w.mustTmux("set", "-p", "-t", fresh, crewTranscriptOption, v) }
	set("/tmp/ok dir/direct.jsonl")
	wantTranscript(w, sess, "fresh", "/tmp/ok dir/direct.jsonl", true, "a valid value written straight to the option")
	for name, v := range map[string]string{"relative": "rel/x.jsonl", "not .jsonl": "/a/x.txt", "a quote": "/a/it's.jsonl"} {
		set(v)
		wantTranscript(w, sess, "fresh", "", false, "an unchecked value ("+name+") written straight to the option")
	}

	// Crew list's text form is unchanged.
	r := crewCall(w, sess, "", "list")
	w.expectExit(r, 0, "crew list")
	if strings.Contains(r.stdout, "transcript") || strings.Contains(r.stdout, ".jsonl") {
		t.Errorf("crew list text = %q, want no transcript column", r.stdout)
	}
}
