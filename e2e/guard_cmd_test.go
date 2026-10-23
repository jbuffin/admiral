package e2e

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"
)

// `admiral guard pretooluse` (#115): the built binary with a Claude PreToolUse
// payload on stdin and the environment Admiral gives a ship. The rules are
// tested in internal/cli; here the seam is what a harness sees (the exit code
// and stderr) and what lands in the outcome log.

const guardReason = "Autopilot ships leave reviews PENDING. The engineer submits them from the inbox."

// guardSecret is in the refused command, so a test can see it never reaches the log.
const guardSecret = "xyzzy-distinct-text"

func guardPayload(command string) string {
	b, _ := json.Marshal(map[string]any{"hook_event_name": "PreToolUse", "tool_name": "Bash",
		"tool_input": map[string]any{"command": command}, "cwd": "/"})
	return string(b)
}

var (
	guardDeny  = guardPayload("gh pr review 12 --approve -b " + guardSecret)
	guardAllow = guardPayload("gh pr review 12 --comment -b hi")
)

// shipEnv is the ship contract Admiral sets for a ship under home (#112), and
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
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
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
			w.t.Fatalf("admiral guard pretooluse: %v", err)
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

// outcomeLines are the raw lines of every month file under home/outcomes.
func outcomeLines(t *testing.T, home string) []string {
	t.Helper()
	files, _ := filepath.Glob(filepath.Join(home, "outcomes", "*.jsonl"))
	lines := []string{}
	for _, f := range files {
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		for _, l := range strings.Split(string(b), "\n") {
			if l != "" {
				lines = append(lines, l)
			}
		}
	}
	return lines
}

// A refusal: exit 2, the reason alone on stderr, and one guard_denied line
// with the ship, the tool and the rule, and nothing of the command.
func TestGuardCmdRefusesOnAnAutopilotShip(t *testing.T) {
	w := newWorld(t)
	home := t.TempDir()
	res := w.runGuardCmd(guardDeny, shipEnv(home, "saltpaw-ii", "ADMIRAL_AUTOPILOT=1")...)
	if res.code != 2 {
		t.Errorf("exit %d, want 2 (stderr %q)", res.code, res.stderr)
	}
	if strings.TrimSuffix(res.stderr, "\n") != guardReason || res.stdout != "" {
		t.Errorf("stderr %q, stdout %q, want the reason line alone on stderr", res.stderr, res.stdout)
	}
	lines := outcomeLines(t, home)
	if len(lines) != 1 {
		t.Fatalf("the outcome log holds %d line(s) %q, want 1", len(lines), lines)
	}
	var rec map[string]any
	if err := json.Unmarshal([]byte(lines[0]), &rec); err != nil {
		t.Fatal(err)
	}
	if rec["event"] != "guard_denied" || rec["ship"] != "saltpaw-ii" || rec["tool"] != "Bash" || rec["rule"] != "gh_pr_review" || rec["v"] != float64(1) {
		t.Errorf("record = %v, want event guard_denied, ship saltpaw-ii, tool Bash, rule gh_pr_review, v 1", rec)
	}
	if ts, _ := rec["ts"].(string); !strings.HasSuffix(ts, "Z") {
		t.Errorf("ts = %v, want an RFC 3339 UTC time", rec["ts"])
	} else if file := filepath.Join(home, "outcomes", ts[:7]+".jsonl"); !exists(file) {
		t.Errorf("%s doesn't exist: the file is named by the month of ts", file)
	}
	if strings.Contains(lines[0], guardSecret) || strings.Contains(lines[0], "pr review") {
		t.Errorf("the line holds text of the command: %s", lines[0])
	}
}

// The ship's home is the one Admiral set in ADMIRAL_REPORT; an ADMIRAL_HOME a
// fleet's env points elsewhere does not move the record.
func TestGuardCmdRecordsUnderTheReportHome(t *testing.T) {
	w := newWorld(t)
	home, other := t.TempDir(), t.TempDir()
	res := w.runGuardCmd(guardDeny, shipEnv(home, "mistral", "ADMIRAL_AUTOPILOT=1", "ADMIRAL_HOME="+other)...)
	if res.code != 2 {
		t.Errorf("exit %d, want 2 (stderr %q)", res.code, res.stderr)
	}
	if n := len(outcomeLines(t, home)); n != 1 {
		t.Errorf("the log under the ADMIRAL_REPORT home holds %d line(s), want 1", n)
	}
	if n := len(outcomeLines(t, other)); n != 0 {
		t.Errorf("the log under ADMIRAL_HOME holds %d line(s), want none", n)
	}
}

// A failed write, or no home to write in, still refuses, and adds nothing to
// stderr, which carries only the reason.
func TestGuardCmdRefusesWhenItCannotRecord(t *testing.T) {
	w := newWorld(t)
	blocked := t.TempDir()
	if err := os.WriteFile(filepath.Join(blocked, "outcomes"), []byte("a file where the directory belongs"), 0o600); err != nil {
		t.Fatal(err)
	}
	for name, env := range map[string][]string{
		"an outcomes directory that cannot be written": shipEnv(blocked, "mistral", "ADMIRAL_AUTOPILOT=1"),
		"no ADMIRAL_REPORT and no ADMIRAL_HOME":        {"ADMIRAL_SHIP=mistral", "ADMIRAL_AUTOPILOT=1"},
		"no ADMIRAL_SHIP":                              {"ADMIRAL_REPORT=" + filepath.Join(t.TempDir(), "reports", "x.json"), "ADMIRAL_AUTOPILOT=1"},
	} {
		t.Run(name, func(t *testing.T) {
			res := w.at(t).runGuardCmd(guardDeny, env...)
			if res.code != 2 || strings.TrimSuffix(res.stderr, "\n") != guardReason {
				t.Errorf("exit %d, stderr %q, want exit 2 and the reason line alone", res.code, res.stderr)
			}
		})
	}
	if _, err := os.Stat(filepath.Join(w.userHom, ".admiral")); err == nil {
		t.Errorf("the guard created ~/.admiral under the user's HOME")
	}
}

// Off outside autopilot, and quiet when it allows: no output, no log.
func TestGuardCmdAllowsQuietly(t *testing.T) {
	w := newWorld(t)
	cases := []struct {
		name, payload string
		env           []string
	}{
		{"a refused payload without ADMIRAL_AUTOPILOT", guardDeny, nil},
		{"a refused payload with ADMIRAL_AUTOPILOT=0", guardDeny, []string{"ADMIRAL_AUTOPILOT=0"}},
		{"an allowed payload on an autopilot ship", guardAllow, []string{"ADMIRAL_AUTOPILOT=1"}},
		{"a payload that is not JSON without ADMIRAL_AUTOPILOT", `{"tool_name":`, nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			home := t.TempDir()
			res := w.at(t).runGuardCmd(c.payload, append(shipEnv(home, "saltpaw-ii"), c.env...)...)
			res.requireQuiet(t)
			if lines := outcomeLines(t, home); len(lines) != 0 {
				t.Errorf("the outcome log holds %q, want nothing", lines)
			}
			if exists(filepath.Join(home, "outcomes")) {
				t.Errorf("the guard created an outcomes directory")
			}
		})
	}
}

// `admiral outcomes` prints the guard's line like any other record.
func TestGuardCmdLineShowsInOutcomes(t *testing.T) {
	w := newWorld(t)
	res := w.runGuardCmd(guardDeny, shipEnv(w.home, "saltpaw-ii", "ADMIRAL_AUTOPILOT=1")...)
	if res.code != 2 {
		t.Fatalf("exit %d, want 2 (stderr %q)", res.code, res.stderr)
	}
	r := w.admiral("outcomes")
	w.expectExit(r, 0, "admiral outcomes")
	if !strings.Contains(r.stdout, `"event":"guard_denied"`) || !strings.Contains(r.stdout, `"rule":"gh_pr_review"`) {
		t.Errorf("admiral outcomes printed:\n%s\nwant the guard_denied line", r.stdout)
	}
	// `guard` is internal like `hook`: no help text names it.
	if help := w.admiral("nosuchcommand", "--help"); strings.Contains(help.both(), "guard") {
		t.Errorf("the command list names guard:\n%s", help.both())
	}
	if byShip := w.admiral("outcomes", "--ship", "saltpaw-ii").stdout; !strings.Contains(byShip, `"guard_denied"`) {
		t.Errorf("admiral outcomes --ship saltpaw-ii printed:\n%s\nwant the guard_denied line", byShip)
	}
}

// AC4, the settings half: the generated file has a PreToolUse group for Bash and
// one for GitHub's MCP review tools, each running the guard, beside the groups
// that were there.
func TestGuardIsInTheGeneratedSettings(t *testing.T) {
	w := newWorld(t)
	w.start()
	var settings struct {
		Hooks map[string][]struct {
			Matcher string `json:"matcher"`
			Hooks   []struct {
				Type, Command string
				Timeout       int    `json:"timeout"`
				OnFailure     string `json:"onFailure"`
			} `json:"hooks"`
		} `json:"hooks"`
	}
	raw := w.readFile(filepath.Join(w.home, "harness", "claude-settings.json"))
	if err := json.Unmarshal([]byte(raw), &settings); err != nil {
		t.Fatalf("claude-settings.json: %v\n%s", err, raw)
	}
	guards := map[string]int{}
	working := 0
	for _, g := range settings.Hooks["PreToolUse"] {
		for _, h := range g.Hooks {
			switch h.Command {
			case "admiral guard pretooluse":
				guards[g.Matcher]++
				if h.Type != "command" {
					t.Errorf("the guard handler of matcher %q has type %q, want command", g.Matcher, h.Type)
				}
				if h.Timeout != 10 || h.OnFailure != "block" {
					t.Errorf("the guard handler of matcher %q has timeout %d and onFailure %q, want 10 and block: a timed-out hook would let the call through", g.Matcher, h.Timeout, h.OnFailure)
				}
			case "admiral hook PreToolUse":
				working++
			}
		}
	}
	for _, m := range []string{"Bash", "^mcp__.*pull_request_review.*$"} {
		if guards[m] != 1 {
			t.Errorf("PreToolUse has %d group(s) with matcher %q running admiral guard pretooluse, want 1\n%s", guards[m], m, raw)
		}
		delete(guards, m)
	}
	// #120: one more group, for the MCP tools that merge a pull request or
	// enable auto-merge, which matches those and leaves the read tools alone.
	if len(guards) != 1 {
		t.Errorf("PreToolUse has the guard on matchers %v beside Bash and the review tools, want exactly one merge matcher\n%s", guards, raw)
	}
	for m, n := range guards {
		re, err := regexp.Compile(m)
		if err != nil {
			t.Errorf("the merge matcher %q is not a regular expression: %v", m, err)
			continue
		}
		if n != 1 {
			t.Errorf("the merge matcher %q has %d guard handlers, want 1", m, n)
		}
		for _, tool := range []string{"mcp__github__merge_pull_request", "mcp__github__enable_pull_request_auto_merge", "mcp__github__enable_auto_merge"} {
			if !re.MatchString(tool) {
				t.Errorf("the merge matcher %q does not match %s", m, tool)
			}
		}
		for _, tool := range []string{"Read", "mcp__github__get_pull_request", "mcp__github__create_pull_request_review"} {
			if re.MatchString(tool) && tool != "mcp__github__create_pull_request_review" {
				t.Errorf("the merge matcher %q matches %s, want only merge tools", m, tool)
			}
		}
	}
	if working != 2 {
		t.Errorf("PreToolUse has %d `admiral hook PreToolUse` handlers, want the 2 that were there", working)
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

// AC4: on an autopilot ship the stub Claude's PreToolUse for Bash calls the
// guard, which exits 2 with the reason and leaves a guard_denied line for the
// ship; the working hook beside it still runs and exits 0.
func TestShipGuardRefusesOnAnAutopilotShip(t *testing.T) {
	w := newWorld(t)
	w.writeCue(guardShipCue)
	w.repo("r1", remoteOf("r1"))
	p := w.newTP("a")
	p.mainOrders(apOrder("o1", remoteOf("r1")))
	w.installPlugin("testplug", "testplug")
	w.writeAutopilotConfig(map[string]any{"a": tpAutopilot(p, "main", apEntry(true, 1, nil))}, 0)
	w.start()
	ship := w.waitShipCount(15*time.Second, "o1's ship", fromOrder("o1"), 1)[0]
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

// The same cue on a ship launched by hand: the guard runs and lets it through,
// and nothing is recorded.
func TestShipGuardLeavesAHandLaunchedShipAlone(t *testing.T) {
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
	time.Sleep(500 * time.Millisecond)
	if got := eventsOf(w.shipLog(ship.Session)); slices.Contains(got, "guard_denied") {
		t.Errorf("records of the hand-launched ship = %v, want no guard_denied", got)
	}
}
