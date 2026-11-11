package probes_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jbuffin/admiral/internal/capabilities"
	"github.com/jbuffin/admiral/internal/config"
	"github.com/jbuffin/admiral/internal/probes"
)

// guardHarness stands in for Claude Code. In headless mode (-p) it logs what it was given
// under $PROBE_FAKE_LOG, then acts out $PROBE_FAKE_MODE with the probe's nonce and directory.
const guardHarness = `#!/bin/sh
if [ "$1" = "--version" ]; then echo "${FAKE_VERSION:-2.1.300 (fake)}"; exit 0; fi
log=$PROBE_FAKE_LOG
mkdir -p "$log"
printf '%s\0' "$@" >"$log/argv"
env >"$log/env"
pwd -P >"$log/cwd"
ls -A >"$log/cwdlist"
command -v admiral >"$log/admiral" 2>&1
prev=
for a in "$@"; do
  [ "$prev" = --settings ] && cp "$a" "$log/settings.json" 2>/dev/null
  [ "$prev" = -p ] && prompt=$a
  prev=$a
done
printf '%s' "$prompt" >"$log/prompt"
sentinel=$ADMIRAL_PROBE_DIR/ran-$ADMIRAL_PROBE_NONCE
denied=$ADMIRAL_PROBE_DIR/denied-$ADMIRAL_PROBE_NONCE
seen=$ADMIRAL_PROBE_DIR/seen-$ADMIRAL_PROBE_NONCE
case "$PROBE_FAKE_MODE" in
pass) : >"$seen"; : >"$denied"; echo "The command was blocked by a hook." ;;
skip-hook-says-denied) : >"$sentinel"; echo "The command was denied by the hook." ;;
no-tool) echo "I will not run that." ;;
different-command) : >"$seen"; : >"$sentinel"; echo done ;;
different-command-failed) : >"$seen"; echo "it failed" ;;
ran-anyway) : >"$seen"; : >"$denied"; : >"$sentinel"; echo done ;;
error) echo "boom: not logged in" >&2; exit 3 ;;
error-stdout) echo "Not logged in · Please run /login"; exit 1 ;;
hang) sleep 30 ;;
esac
exit 0
`

type guardRun struct {
	home, log string
	report    probes.Report
	err       error
	took      time.Duration
}

func runGuard(t *testing.T, mode string, timeout time.Duration, fleetEnv map[string]string) guardRun {
	t.Helper()
	exe := filepath.Join(t.TempDir(), "claude")
	if err := os.WriteFile(exe, []byte(guardHarness), 0o755); err != nil {
		t.Fatal(err)
	}
	g := guardRun{home: home(t), log: t.TempDir()}
	env := map[string]string{"PROBE_FAKE_LOG": g.log, "PROBE_FAKE_MODE": mode, "ADMIRAL_AUTOPILOT": "0"}
	for k, v := range fleetEnv {
		env[k] = v
	}
	fleet := config.Fleet{Key: "work", Harnesses: []config.Harness{{Name: "claude", Executable: exe, Found: true, Env: env}}}
	r := probes.New(g.home, probes.ClaudeGuard())
	if timeout > 0 {
		r.Timeout = timeout
	}
	start := time.Now()
	g.report, g.err = r.Run(context.Background(), fleet, "claude")
	g.took = time.Since(start)
	return g
}

func (g guardRun) guard(t *testing.T) probes.Line {
	t.Helper()
	if g.err != nil {
		t.Fatalf("Run: %v", g.err)
	}
	return line(t, g.report, capabilities.AutopilotGuard)
}

func (g guardRun) read(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(g.log, name))
	if err != nil {
		t.Fatalf("the harness never logged %s: it was not run headless: %v", name, err)
	}
	return string(b)
}

func TestGuardProbePassesOnlyWithTheDenialRecordAndNoSentinel(t *testing.T) {
	g := runGuard(t, "pass", 0, nil)
	l := g.guard(t)
	if l.Status != capabilities.Passed || l.Detail == "" {
		t.Fatalf("line = %+v, want passed with a detail", l)
	}
	if !capabilities.Open(g.home).Proven("work", "claude", capabilities.AutopilotGuard) {
		t.Error("the passed result is not in the store")
	}
}

func TestGuardProbeFailuresEachSayWhy(t *testing.T) {
	details := map[string]string{}
	want := map[string]string{
		"skip-hook-says-denied": "without running the guard hook",
		"no-tool":               "never called the Bash tool",
		"different-command":     "different command",
		"ran-anyway":            "after the guard hook denied it",
		"error":                 "claude failed: boom: not logged in",
		"error-stdout":          "claude failed: Not logged in",
	}
	for _, mode := range []string{"skip-hook-says-denied", "no-tool", "different-command", "ran-anyway", "error", "error-stdout"} {
		t.Run(mode, func(t *testing.T) {
			g := runGuard(t, mode, 0, nil)
			l := g.guard(t)
			if l.Status != capabilities.Failed {
				t.Fatalf("status = %v, want failed (the model's words and exit code are never evidence)", l.Status)
			}
			if l.Detail == "" || strings.Contains(l.Detail, "\n") {
				t.Errorf("detail = %q, want one line of text", l.Detail)
			}
			if st, _ := capabilities.Open(g.home).Status("work", "claude", capabilities.AutopilotGuard); st != capabilities.Failed {
				t.Errorf("stored status = %v, want failed", st)
			}
			if !strings.Contains(l.Detail, want[mode]) {
				t.Errorf("detail = %q, want it to say %q", l.Detail, want[mode])
			}
			details[mode] = l.Detail
		})
	}
	seen := map[string]string{}
	for mode, d := range details {
		if other, dup := seen[d]; dup {
			t.Errorf("%s and %s fail with the same detail %q: each way to fail gets its own", mode, other, d)
		}
		seen[d] = mode
	}
}

func TestGuardProbeTimesOutAsAFailureAndKillsTheHarness(t *testing.T) {
	g := runGuard(t, "hang", 1500*time.Millisecond, nil)
	l := g.guard(t)
	if l.Status != capabilities.Failed || l.Detail == "" {
		t.Errorf("line = %+v, want failed with a detail", l)
	}
	if g.took > 10*time.Second {
		t.Errorf("Run took %v for a 1.5s timeout", g.took)
	}
}

func TestGuardProbeRunsTheHarnessHeadlessOnTheCheapestModelWithBashAllowed(t *testing.T) {
	g := runGuard(t, "pass", 0, nil)
	g.guard(t)
	argv := strings.Split(strings.TrimSuffix(g.read(t, "argv"), "\x00"), "\x00")
	pair := func(flag string, ok func(string) bool) bool {
		for i := 0; i+1 < len(argv); i++ {
			if argv[i] == flag && ok(argv[i+1]) {
				return true
			}
		}
		return false
	}
	has := func(flag string) bool {
		for _, a := range argv {
			if a == flag {
				return true
			}
		}
		return false
	}
	if !pair("--model", func(v string) bool { return v == "haiku" }) {
		t.Errorf("argv %q: want --model haiku", argv)
	}
	if !pair("--allowedTools", func(v string) bool { return strings.Contains(v, "Bash") }) && !pair("--allowed-tools", func(v string) bool { return strings.Contains(v, "Bash") }) {
		t.Errorf("argv %q: want Bash allowed, so a missing hook shows as the call running and not as a permission refusal", argv)
	}
	if !has("-p") && !has("--print") {
		t.Errorf("argv %q: want headless mode (-p)", argv)
	}
	prompt := g.read(t, "prompt")
	nonce := ""
	for _, kv := range strings.Split(g.read(t, "env"), "\n") {
		if v, ok := strings.CutPrefix(kv, probes.NonceEnv+"="); ok {
			nonce = v
		}
	}
	if len(nonce) < 8 {
		t.Fatalf("%s = %q in the harness's env, want a fresh nonce", probes.NonceEnv, nonce)
	}
	cwd := strings.TrimSpace(g.read(t, "cwd"))
	if want := probes.Command(cwd, nonce); !strings.Contains(prompt, want) {
		t.Errorf("prompt %q does not hold the command %q the guard denies", prompt, want)
	}
}

func TestGuardProbeEnvIsScrubbedAndAutopilotIsOurs(t *testing.T) {
	for k, v := range map[string]string{"ADMIRAL_SHIP": "ghost", "ADMIRAL_REPORT": "/nowhere/reports/ghost.json",
		"ADMIRAL_INBOX": "/nowhere/inbox", "TMUX": "/nowhere/sock,1,0", "TMUX_PANE": "%9",
		"CLAUDECODE": "1", "CLAUDE_CODE_CHILD_SESSION": "1"} {
		t.Setenv(k, v)
	}
	g := runGuard(t, "pass", 0, nil)
	g.guard(t)
	env := map[string]string{}
	for _, kv := range strings.Split(g.read(t, "env"), "\n") {
		if k, v, ok := strings.Cut(kv, "="); ok {
			env[k] = v
		}
	}
	for _, k := range []string{"ADMIRAL_SHIP", "ADMIRAL_REPORT", "ADMIRAL_INBOX", "TMUX", "TMUX_PANE", "CLAUDECODE", "CLAUDE_CODE_CHILD_SESSION"} {
		if v, set := env[k]; set {
			t.Errorf("%s=%q reaches the probe's harness: it would record an outcome against a real ship or mark the engineer's pane", k, v)
		}
	}
	if env["ADMIRAL_AUTOPILOT"] != "1" {
		t.Errorf("ADMIRAL_AUTOPILOT = %q, want 1 even though the fleet's env says 0 (the guard allows everything without it)", env["ADMIRAL_AUTOPILOT"])
	}
	if env["PROBE_FAKE_MODE"] != "pass" {
		t.Error("the fleet's own env did not reach the harness")
	}
}

func TestGuardProbeWorksOnAHomeNoServerEverStarted(t *testing.T) {
	g := runGuard(t, "pass", 0, nil)
	g.guard(t)
	if _, err := os.Stat(g.home); err != nil {
		t.Fatalf("the home was not created: %v", err)
	}
	settings := g.read(t, "settings.json")
	var s struct {
		Hooks map[string][]struct {
			Matcher string
			Hooks   []struct{ Command string }
		}
	}
	if err := json.Unmarshal([]byte(settings), &s); err != nil {
		t.Fatalf("--settings file at run time is not the hook bundle: %v\n%s", err, settings)
	}
	guarded := false
	for _, grp := range s.Hooks["PreToolUse"] {
		for _, h := range grp.Hooks {
			guarded = guarded || (strings.Contains(h.Command, "guard pretooluse") && strings.Contains(grp.Matcher, "Bash"))
		}
	}
	if !guarded {
		t.Errorf("the settings the harness got hold no Bash guard hook:\n%s", settings)
	}
	if got := strings.TrimSpace(g.read(t, "admiral")); !strings.HasPrefix(got, "/") {
		t.Errorf("`admiral` does not resolve on the harness's PATH (command -v: %q), so the hook command cannot run", got)
	}
}

func TestGuardProbeUsesAnEmptyThrowawayDirectoryAndLeavesNothingBehind(t *testing.T) {
	g := runGuard(t, "pass", 0, nil)
	g.guard(t)
	cwd := strings.TrimSpace(g.read(t, "cwd"))
	if strings.TrimSpace(g.read(t, "cwdlist")) != "" {
		t.Errorf("the probe's directory was not empty:\n%s", g.read(t, "cwdlist"))
	}
	for d := cwd; d != filepath.Dir(d); d = filepath.Dir(d) {
		if _, err := os.Stat(filepath.Join(d, ".git")); err == nil {
			t.Errorf("the probe ran inside a repository at %s", d)
		}
	}
	if _, err := os.Stat(cwd); err == nil {
		t.Errorf("the probe's directory %s was not removed", cwd)
	}
	for _, name := range []string{"outcomes", "inbox", "ships", "reports"} {
		if _, err := os.Stat(filepath.Join(g.home, name)); err == nil {
			t.Errorf("the probe left %s/ in the home", name)
		}
	}
}

func TestGuardProbeDoesNotRunForOtherHarnesses(t *testing.T) {
	exe := filepath.Join(t.TempDir(), "codex")
	if err := os.WriteFile(exe, []byte(guardHarness), 0o755); err != nil {
		t.Fatal(err)
	}
	logDir := t.TempDir()
	fleet := config.Fleet{Key: "work", Harnesses: []config.Harness{{Name: "codex", Executable: exe, Found: true,
		Env: map[string]string{"PROBE_FAKE_LOG": logDir, "PROBE_FAKE_MODE": "pass"}}}}
	rep, err := probes.New(home(t), probes.ClaudeGuard()).Run(context.Background(), fleet, "codex")
	if err != nil {
		t.Fatal(err)
	}
	if l := line(t, rep, capabilities.AutopilotGuard); l.Status != capabilities.NotChecked {
		t.Errorf("codex guard = %v, want not_checked: only Claude's probe exists", l.Status)
	}
	if _, err := os.Stat(filepath.Join(logDir, "argv")); err == nil {
		t.Error("the Claude probe ran the codex executable")
	}
}

func TestGuardProbeFailsNamingAFleetArgThatWouldDisableTheHook(t *testing.T) {
	exe := filepath.Join(t.TempDir(), "claude")
	if err := os.WriteFile(exe, []byte(guardHarness), 0o755); err != nil {
		t.Fatal(err)
	}
	log := t.TempDir()
	fleet := config.Fleet{Key: "work", Harnesses: []config.Harness{{Name: "claude", Executable: exe, Found: true,
		Args: []string{"--bare"}, Env: map[string]string{"PROBE_FAKE_LOG": log, "PROBE_FAKE_MODE": "pass"}}}}
	rep, err := probes.New(home(t), probes.ClaudeGuard()).Run(context.Background(), fleet, "claude")
	if err != nil {
		t.Fatal(err)
	}
	l := line(t, rep, capabilities.AutopilotGuard)
	if l.Status != capabilities.Failed || !strings.Contains(l.Detail, "--bare") {
		t.Errorf("line = %+v, want failed naming --bare, never passed", l)
	}
	if _, err := os.Stat(filepath.Join(log, "argv")); err == nil {
		t.Error("claude was run although the fleet's args disable the hook")
	}
}

func TestGuardProbeSeenWithoutSentinelOrDenialIsADifferentCommand(t *testing.T) {
	l := runGuard(t, "different-command-failed", 0, nil).guard(t)
	if l.Status != capabilities.Failed || !strings.Contains(l.Detail, "different command") {
		t.Errorf("line = %+v, want failed as a different command", l)
	}
}
