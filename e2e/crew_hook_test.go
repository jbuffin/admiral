package e2e

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// Crew-aware `admiral hook` (#152 AC3, spec #72 story 12 and 21-23): a lead's
// Stop leaves @admiral-state alone while a crew boat works, and sets needs_you
// once none does; @admiral-subagents follows SubagentStart and SubagentStop. The
// seam is the built binary on a private tmux socket, run as the harness's hook
// would: TMUX names the socket (the hook runs bare tmux) and TMUX_PANE the pane.
// No cue runs, so the test makes each pane working itself.

func TestCrewHook_LeadWaitsForWorkingCrew(t *testing.T) {
	w := newWorld(t)
	w.start()
	ship := w.launch(w.order("work", "claude", "alpha", noSkill, "go"))
	w.waitFile(ship.Session, "cues-done")
	sess := ship.Session
	dir := filepath.Join(w.root, "anywhere")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"scout", "mate"} {
		w.expectExit(crewCall(w, sess, "", "launch", "--name", name, "--dir", dir, "--", "go"), 0, "crew launch "+name)
		w.waitFile(sess+"/crew/"+name, "cues-done")
	}
	shipPane := strings.TrimSpace(w.mustTmux("display-message", "-p", "-t", "="+sess+":0", "#{pane_id}"))
	scout, mate := w.paneOf(sess, "scout"), w.paneOf(sess, "mate")

	hook := func(pane, event, stdin string) {
		t.Helper()
		r := w.admiralWith(cliOpts{env: []string{"TMUX=" + w.sock() + ",0,0", "TMUX_PANE=" + pane}, stdin: stdin}, "hook", event)
		w.expectExit(r, 0, "admiral hook "+event)
	}
	opt := func(pane, name string) string {
		out, _ := w.tmux("show-options", "-pqv", "-t", pane, name)
		return out
	}
	wantState := func(pane, who, state, reason string) {
		t.Helper()
		if s, r := opt(pane, "@admiral-state"), opt(pane, "@admiral-reason"); s != state || r != reason {
			t.Errorf("%s: @admiral-state %q reason %q, want %q and %q", who, s, r, state, reason)
		}
	}
	const prompt = `{"hook_event_name":"UserPromptSubmit","prompt":"go"}`
	const stop = `{"hook_event_name":"Stop","background_tasks":[],"session_crons":[]}`

	// Both the ship and scout are working; the ship's turn ends.
	hook(shipPane, "UserPromptSubmit", prompt)
	hook(scout.pane, "UserPromptSubmit", prompt)
	// mate has had no hook yet, so its 10 s launch window would hold the lead.
	hook(mate.pane, "Stop", stop)
	hook(shipPane, "Stop", stop)
	wantState(shipPane, "the lead's Stop while scout works", "working", "")

	// Scout's own Stop is needs_you, and the lead's next Stop follows it.
	hook(scout.pane, "Stop", stop)
	wantState(scout.pane, "a crew boat's own Stop", "needs_you", "stop")
	hook(shipPane, "Stop", stop)
	wantState(shipPane, "the lead's Stop once no crew boat works", "needs_you", "stop")

	// An anchored crew boat that died while working doesn't hold the lead.
	hook(shipPane, "UserPromptSubmit", prompt)
	hook(mate.pane, "UserPromptSubmit", prompt)
	pid, err := strconv.Atoi(mate.pid)
	if err != nil {
		t.Fatal(err)
	}
	if err := syscall.Kill(pid, syscall.SIGKILL); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(10 * time.Second)
	for w.mustTmux("display-message", "-p", "-t", mate.pane, "#{pane_dead}") != "1" {
		if time.Now().After(deadline) {
			t.Fatalf("mate's pane never died")
		}
		time.Sleep(50 * time.Millisecond)
	}
	if got := opt(mate.pane, "@admiral-state"); got != "working" {
		t.Fatalf("the dead pane's @admiral-state = %q, want it to stay working", got)
	}
	hook(shipPane, "Stop", stop)
	wantState(shipPane, "the lead's Stop with only an anchored crew boat", "needs_you", "stop")

	// @admiral-subagents on the ship's pane; subagent events never change the state.
	subs := func() []map[string]string {
		t.Helper()
		raw := opt(shipPane, "@admiral-subagents")
		if raw == "" {
			return nil
		}
		var l []map[string]string
		if err := json.Unmarshal([]byte(raw), &l); err != nil {
			t.Fatalf("@admiral-subagents = %q, not a JSON array: %v", raw, err)
		}
		return l
	}
	hook(shipPane, "SubagentStart", `{"hook_event_name":"SubagentStart","agent_id":"a0f790cb4a518e0f9","agent_type":"Explore"}`)
	l := subs()
	if len(l) != 1 || l[0]["id"] != "a0f790cb4a518e0f9" || l[0]["type"] != "Explore" {
		t.Fatalf("after SubagentStart @admiral-subagents = %v, want one Explore entry", l)
	}
	if _, err := time.Parse(time.RFC3339, l[0]["start"]); err != nil {
		t.Errorf("start %q is not RFC 3339", l[0]["start"])
	}
	wantState(shipPane, "the ship after SubagentStart", "needs_you", "stop")
	hook(shipPane, "SubagentStop", `{"hook_event_name":"SubagentStop","agent_id":"a0f790cb4a518e0f9","agent_type":"Explore","background_tasks":[]}`)
	if raw := opt(shipPane, "@admiral-subagents"); raw != "" {
		t.Errorf("after SubagentStop @admiral-subagents = %q, want it unset", raw)
	}
	wantState(shipPane, "the ship after SubagentStop", "needs_you", "stop")
}

// A Stop that waits on a Monitor (#110): Commodore's coordinator ends its turn
// to wait for its watchdog's events, so a Stop leaves @admiral-state alone while
// a Monitor the session armed still runs. A Monitor reports as a "shell" task,
// so only the id its PostToolUse gave (kept in @admiral-monitors) tells it from
// a background Bash, which still doesn't hold the ship.
func TestHook_StopWaitsForMonitor(t *testing.T) {
	w := newWorld(t)
	w.start()
	ship := w.launch(w.order("work", "claude", "alpha", noSkill, "go"))
	w.waitFile(ship.Session, "cues-done")
	pane := strings.TrimSpace(w.mustTmux("display-message", "-p", "-t", "="+ship.Session+":0", "#{pane_id}"))

	hook := func(event, stdin string) {
		t.Helper()
		r := w.admiralWith(cliOpts{env: []string{"TMUX=" + w.sock() + ",0,0", "TMUX_PANE=" + pane}, stdin: stdin}, "hook", event)
		w.expectExit(r, 0, "admiral hook "+event)
	}
	opt := func(name string) string {
		out, _ := w.tmux("show-options", "-pqv", "-t", pane, name)
		return out
	}
	wantState := func(who, state, reason string) {
		t.Helper()
		if s, r := opt("@admiral-state"), opt("@admiral-reason"); s != state || r != reason {
			t.Errorf("%s: @admiral-state %q reason %q, want %q and %q", who, s, r, state, reason)
		}
	}
	monitor := func(id string, persistent bool) string {
		return `{"hook_event_name":"PostToolUse","tool_name":"Monitor","tool_input":{"command":"watch.sh"},"tool_response":{"taskId":"` + id + `","timeoutMs":1800000,"persistent":` + strconv.FormatBool(persistent) + `}}`
	}
	// The live check's Stop on claude 2.1.310: a background Bash and a Monitor.
	stop := func(running ...string) string {
		var tasks []string
		for _, id := range running {
			tasks = append(tasks, `{"id":"`+id+`","type":"shell","status":"running","description":"d","command":"c"}`)
		}
		return `{"hook_event_name":"Stop","background_tasks":[` + strings.Join(tasks, ",") + `],"session_crons":[]}`
	}
	const prompt = `{"hook_event_name":"UserPromptSubmit","prompt":"go"}`

	hook("UserPromptSubmit", prompt)
	hook("PostToolUse", monitor("bwatch01", false))
	if got := opt("@admiral-monitors"); got != "bwatch01" {
		t.Fatalf("after the Monitor's PostToolUse @admiral-monitors = %q, want bwatch01", got)
	}
	hook("Stop", stop("bdevsrv1", "bwatch01"))
	wantState("a Stop while the Monitor runs", "working", "")

	// The Monitor's event wakes the session; its next Stop has the Monitor gone.
	hook("UserPromptSubmit", prompt)
	hook("Stop", stop("bdevsrv1"))
	wantState("a Stop with only a background Bash running", "needs_you", "stop")
	if got := opt("@admiral-monitors"); got != "" {
		t.Errorf("after the Monitor ended @admiral-monitors = %q, want it unset", got)
	}

	// A persistent Monitor has no timeout to wake the session, so it never holds.
	hook("UserPromptSubmit", prompt)
	hook("PostToolUse", monitor("bwatch02", true))
	hook("Stop", stop("bwatch02"))
	wantState("a Stop while a persistent Monitor runs", "needs_you", "stop")

	// A Stop with nothing running clears an id left behind.
	hook("PostToolUse", monitor("bwatch03", false))
	hook("Stop", stop())
	wantState("a Stop with no task running", "needs_you", "stop")
	if got := opt("@admiral-monitors"); got != "" {
		t.Errorf("after a Stop with no task running @admiral-monitors = %q, want it unset", got)
	}
}
