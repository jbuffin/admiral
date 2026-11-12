package probes_test

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/jbuffin/admiral/internal/capabilities"
	"github.com/jbuffin/admiral/internal/config"
	"github.com/jbuffin/admiral/internal/launcher"
	"github.com/jbuffin/admiral/internal/probes"
)

// Event and log shapes as the real Copilot 1.0.109 prints them (an MCP tool is toolName "<server>-<tool>" with mcpServerName and mcpToolName).
// An unfenced run, asked for a shell, a web fetch and a sub-agent, printed the tool.execution_start lines below with no mcpServerName, and no
// permission.* event or disabled-tools list in the -p stream.
var (
	// copilotEvents are the JSONL lines the fake prints; the shapes stand for what Copilot emits.
	copilotEvents = struct {
		reply    func(text string) string
		shell    string
		web      string
		subAgent string
		patch    string
		toolCall func(server, tool string) string
	}{
		reply: func(text string) string {
			return `{"type":"assistant.message","data":{"content":"` + text + `"}}`
		},
		shell:    `{"type":"tool.execution_start","data":{"toolCallId":"call_F69c","toolName":"bash","arguments":{"command":"touch x"},"turnId":"1","model":"gpt-5.6-luna","toolTitle":"Running command"}}`,
		web:      `{"type":"tool.execution_start","data":{"toolCallId":"call_h6Rs","toolName":"web_fetch","arguments":{"url":"https://example.com","max_length":2000},"turnId":"2","model":"gpt-5.6-luna","toolTitle":"Fetching web content"}}`,
		subAgent: `{"type":"tool.execution_start","data":{"toolCallId":"call_z40a","toolName":"task","arguments":{"description":"Create sentinel file","agent_type":"task"},"turnId":"2","model":"gpt-5.6-luna"}}`,
		patch:    `{"type":"tool.execution_start","data":{"toolCallId":"custom_call_XjCD","toolName":"apply_patch","arguments":"*** Begin Patch\n*** End Patch\n","turnId":"1","model":"gpt-5.6-luna","toolTitle":"Applying patch"}}`,
		toolCall: func(server, tool string) string {
			return `{"type":"tool.execution_start","data":{"toolName":"` + server + `-` + tool + `","mcpServerName":"` + server + `","mcpToolName":"` + tool + `"}}`
		},
	}
	// copilotProbeHomeFiles are the files a probe may put in its throwaway home before it runs (the login is in the Keychain).
	copilotProbeHomeFiles = []string{"copilot-instructions.md"}
	// copilotProbeBareFlags and copilotProbeFlagValues are the fence flags the probe's argv must carry.
	copilotProbeBareFlags  = []string{"--disable-builtin-mcps", "--no-ask-user", "--disallow-temp-dir", "--sync-session=off"}
	copilotProbeFlagValues = map[string]string{"--allow-tool": "probe", "--output-format": "json", "--log-level": "all"}
	// copilotProbeEnvDropped are the variables a probe must not pass on, from Admiral's env or the dock's.
	copilotProbeEnvDropped = []string{"COPILOT_GITHUB_TOKEN", "GH_TOKEN", "GITHUB_TOKEN", "COPILOT_PROVIDER_BASE_URL",
		"COPILOT_PROVIDER_API_KEY", "COPILOT_PROVIDERS_CONFIG", "COPILOT_SKILLS_DIRS", "COPILOT_CUSTOM_INSTRUCTIONS_DIRS", "COPILOT_ALLOW_ALL"}
	// Each mode's per-probe detail text: the probe says why it failed in these words (lower case match).
	copilotFenceFailDetail = map[string]string{
		"shell-ran":    "shell",
		"shell-event":  "bash",
		"patch":        "apply_patch",
		"tools-sent":   "sent tools other",
		"no-log":       "log does not say",
		"permission":   "permission",
		"foreign-hook": "hook other",
		"web":          "web_fetch",
		"sub-agent":    "task",
		"other-tool":   "other",
		"no-call":      "never called",
		"error":        "copilot failed: boom: not logged in",
	}
	copilotHomeFailDetail = map[string]string{
		"login-failed": "login",
		"no-reply":     "no reply",
		"plugin-dir":   "installed-plugins",
		"other-server": "other",
	}
)

// copilotFake stands in for Copilot. `--version` prints $FAKE_VERSION. Any other run logs its argv, env and COPILOT_HOME
// under $PROBE_FAKE_LOG, finds the probe server's dir and nonce in --additional-mcp-config (inline JSON or @file),
// acts out $PROBE_FAKE_MODE, and prints the event lines the test staged: events-fence when a probe server is
// configured, events-home when not.
const copilotFake = `#!/bin/sh
if [ "$1" = "--version" ]; then echo "${FAKE_VERSION:-GitHub Copilot CLI 9.9.9}"; exit 0; fi
log=$PROBE_FAKE_LOG
mkdir -p "$log"
printf '%s\0' "$@" >"$log/argv"
logdir=""
env >"$log/env"
printf '%s' "$COPILOT_HOME" >"$log/copilot-home"
ls -A "$COPILOT_HOME" >"$log/home-listing"
text=$(printf '%s\n' "$@")
for a in "$@"; do case "$a" in @*) text="$text $(cat "${a#@}" 2>/dev/null)";; esac; done
dir=$(echo "$text" | sed -n 's/.*ADMIRAL_PROBE_DIR"\{0,1\} *[:=] *"\([^"]*\)".*/\1/p' | head -n1)
nonce=$(echo "$text" | sed -n 's/.*ADMIRAL_PROBE_NONCE"\{0,1\} *[:=] *"\([^"]*\)".*/\1/p' | head -n1)
while [ $# -gt 0 ]; do case "$1" in --log-dir) logdir=$2;; esac; shift; done
if [ -n "$dir" ] && [ -n "$logdir" ] && [ "$PROBE_FAKE_MODE" != no-log ]; then
  mkdir -p "$logdir"
  extra=""
  [ "$PROBE_FAKE_MODE" = tools-sent ] && extra='"bash:aaaa1111", '
  printf '2026-11-12T23:31:45.619Z [DEBUG] [rust:model_wire] Wire request: {\n  "initiator": "user",\n  "copilotToolsFingerprint": {\n    "count": 1,\n    "deferred": 0,\n    "digest": "14fcc048",\n    "canonicalDigest": "b87850dc",\n    "tools": [\n      %s"probe-ping:42282fc8"\n    ]\n  }\n}\n' "$extra" >"$logdir/process-1.log"
fi
if [ -n "$dir" ]; then
  mkdir -p "$COPILOT_HOME/session-state/s1"
  echo '{"type":"hook.start","data":{"hookInvocationId":"0e80","hookType":"sessionStart"}}' >"$COPILOT_HOME/session-state/s1/events.jsonl"
  [ "$PROBE_FAKE_MODE" = foreign-hook ] && echo '{"type":"hook.start","data":{"hookInvocationId":"0e81","hookType":"sessionEnd"}}' >>"$COPILOT_HOME/session-state/s1/events.jsonl"
fi
case "$PROBE_FAKE_MODE" in
error) echo "boom: not logged in" >&2; exit 3 ;;
login-failed) echo "Error: not logged in, run copilot login" >&2; exit 1 ;;
hang) sleep 30 ;;
plugin-dir) mkdir -p "$COPILOT_HOME/installed-plugins/github" ;;
shell-ran) [ -n "$dir" ] && : >"$dir/ran-$nonce" ;;
esac
case "$PROBE_FAKE_MODE" in
no-call|error|login-failed|hang) ;;
*) [ -n "$dir" ] && : >"$dir/called-$nonce" ;;
esac
if [ -n "$dir" ]; then cat "$log/events-fence"; else cat "$log/events-home"; fi
exit 0
`

type copilotProbeRun struct {
	home     string
	engineer string
	log      string
	report   probes.Report
	err      error
}

type copilotProbeOpts struct {
	timeout    time.Duration
	dockArgs   []string
	useDefault bool
}

func stageCopilotProbe(t *testing.T, log, mode string) {
	t.Helper()
	e := copilotEvents
	fence := []string{e.toolCall("probe", "ping"), e.reply("done")}
	home := []string{e.reply("pong")}
	switch mode {
	case "shell-ran", "shell-event":
		fence = append([]string{e.shell}, fence...)
	case "patch":
		fence = append([]string{e.patch}, fence...)
	case "permission":
		fence = append([]string{`{"type":"permission.requested","data":{"toolName":"bash"}}`}, fence...)
	case "web":
		fence = append([]string{e.web}, fence...)
	case "sub-agent":
		fence = append([]string{e.subAgent}, fence...)
	case "other-tool":
		fence = append([]string{e.toolCall("other", "run")}, fence...)
	case "no-call":
		fence = []string{e.reply("done")}
	case "no-reply":
		home = nil
	case "other-server":
		home = append([]string{e.toolCall("other", "run")}, home...)
	}
	for name, lines := range map[string][]string{"events-fence": fence, "events-home": home} {
		if err := os.WriteFile(filepath.Join(log, name), []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func runCopilotProbe(t *testing.T, p probes.Probe, mode string, o copilotProbeOpts) copilotProbeRun {
	t.Helper()
	exe := filepath.Join(t.TempDir(), "copilot")
	if err := os.WriteFile(exe, []byte(copilotFake), 0o755); err != nil {
		t.Fatal(err)
	}
	c := copilotProbeRun{home: home(t), engineer: t.TempDir(), log: t.TempDir()}
	t.Setenv("HOME", t.TempDir())
	t.Setenv("COPILOT_HOME", c.engineer)
	t.Setenv("GH_TOKEN", "fake-token-ambient")
	t.Setenv("COPILOT_PROVIDER_BASE_URL", "http://example.invalid/ambient")
	if err := os.WriteFile(filepath.Join(c.engineer, "mcp-config.json"), []byte(`{"mcpServers":{"engineer_only":{"command":"/bin/false"}}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	stageCopilotProbe(t, c.log, mode)
	dockEnv := map[string]string{"PROBE_FAKE_LOG": c.log, "PROBE_FAKE_MODE": mode, "COPILOT_HOME": c.engineer, "ADMIRAL_SHIP": "stray-ship"}
	for _, k := range copilotProbeEnvDropped {
		dockEnv[k] = "fake-" + strings.ToLower(k)
	}
	fleet := config.Fleet{Key: "work", Harnesses: []config.Harness{{Name: "copilot", Executable: exe, Found: true, Args: o.dockArgs, Env: dockEnv}}}
	r := probes.New(c.home, p)
	if o.useDefault {
		r = probes.NewDefault(c.home)
	}
	if o.timeout > 0 {
		r.Timeout = o.timeout
	}
	c.report, c.err = r.Run(context.Background(), fleet, "copilot")
	return c
}

func (c copilotProbeRun) line(t *testing.T, id capabilities.ID) probes.Line {
	t.Helper()
	if c.err != nil {
		t.Fatalf("Run: %v", c.err)
	}
	return line(t, c.report, id)
}

func (c copilotProbeRun) read(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(c.log, name))
	if err != nil {
		t.Fatalf("the harness never logged %s: it was not run: %v", name, err)
	}
	return string(b)
}

func (c copilotProbeRun) argv(t *testing.T) []string {
	return strings.Split(strings.TrimSuffix(c.read(t, "argv"), "\x00"), "\x00")
}

func TestCopilotFenceProbePassesOnAFencedRunAndStoresItUnderTheDetectedVersion(t *testing.T) {
	c := runCopilotProbe(t, probes.CopilotMCPFence(), "pass", copilotProbeOpts{})
	l := c.line(t, capabilities.BotMCPFence)
	if l.Status != capabilities.Passed || l.Detail == "" {
		t.Fatalf("line = %+v, want passed with a detail", l)
	}
	if c.report.Version != "GitHub Copilot CLI 9.9.9" {
		t.Errorf("report version = %q, want the harness's own", c.report.Version)
	}
	snap, err := capabilities.Open(c.home).Snapshot("work", "copilot")
	if err != nil {
		t.Fatal(err)
	}
	if snap.Version != "GitHub Copilot CLI 9.9.9" || snap.States[capabilities.BotMCPFence].Status != capabilities.Passed {
		t.Errorf("store: version %q, status %v; want the detected version and passed", snap.Version, snap.States[capabilities.BotMCPFence].Status)
	}
}

func TestCopilotFenceProbeRunsNonInteractiveJSONWithTheChatsFenceAndNotTheDocksArgs(t *testing.T) {
	c := runCopilotProbe(t, probes.CopilotMCPFence(), "pass", copilotProbeOpts{dockArgs: []string{"--allow-all", "--yolo", "--add-dir", "/elsewhere"}})
	if l := c.line(t, capabilities.BotMCPFence); l.Status != capabilities.Passed {
		t.Fatalf("line = %+v, want passed", l)
	}
	argv := c.argv(t)
	if !slices.Contains(argv, "-p") && !slices.Contains(argv, "--prompt") {
		t.Errorf("argv = %q, want a non-interactive -p run", argv)
	}
	for _, a := range argv {
		if a == "--allow-all" || a == "--yolo" || a == "--add-dir" || a == "/elsewhere" || a == "--allow-all-tools" {
			t.Errorf("argv = %q carries %q: the probe proves the chat's own fence and never the dock's args", argv, a)
		}
	}
	for _, f := range copilotProbeBareFlags {
		if !slices.Contains(argv, f) {
			t.Errorf("argv = %q lacks %s", argv, f)
		}
	}
	for f, want := range copilotProbeFlagValues {
		i := slices.Index(argv, f)
		if i < 0 || i+1 >= len(argv) || argv[i+1] != want {
			if !slices.Contains(argv, f+"="+want) {
				t.Errorf("argv = %q lacks %s %s", argv, f, want)
			}
		}
	}
	avail := slices.Index(argv, "--available-tools")
	if avail < 0 && !slices.ContainsFunc(argv, func(a string) bool { return strings.HasPrefix(a, "--available-tools=") }) {
		t.Errorf("argv = %q lacks --available-tools naming only the probe server's tools", argv)
	}
	for _, f := range []string{"--max-ai-credits", "--usage-output-file", "--model"} {
		if !slices.ContainsFunc(argv, func(a string) bool { return a == f || strings.HasPrefix(a, f+"=") }) {
			t.Errorf("argv = %q lacks %s", argv, f)
		}
	}
	small, _, _ := config.ModelPair("copilot")
	if !slices.Contains(argv, small) && !slices.Contains(argv, "--model="+small) {
		t.Errorf("argv = %q, want the cheapest model %q", argv, small)
	}
	if p := argv[len(argv)-1]; !strings.Contains(p, "web") || !strings.Contains(p, "sub-agent") {
		if i := slices.Index(argv, "-p"); i < 0 || i+1 >= len(argv) || !strings.Contains(argv[i+1], "sub-agent") {
			t.Errorf("the probe's prompt does not ask for a web fetch and a sub-agent (argv %q)", argv)
		}
	}
	joined := strings.Join(argv, "\n")
	for _, f := range []string{"--plugin-dir", "--log-dir", "--excluded-tools="} {
		if !strings.Contains(joined, f) {
			t.Errorf("argv = %q lacks %s: the probe runs in the chat's configuration", argv, f)
		}
	}
	if !strings.Contains(joined, "ADMIRAL_PROBE_DIR") {
		t.Errorf("argv = %q carries no probe MCP server (--additional-mcp-config)", argv)
	}
}

func TestCopilotProbesRunInAThrowawayHomeWithTheFenceEnvAndRemoveIt(t *testing.T) {
	for name, p := range map[string]probes.Probe{"fence": probes.CopilotMCPFence(), "config home": probes.CopilotConfigHome()} {
		t.Run(name, func(t *testing.T) {
			c := runCopilotProbe(t, p, "pass", copilotProbeOpts{})
			if c.err != nil {
				t.Fatal(c.err)
			}
			home := c.read(t, "copilot-home")
			if home == "" || home == c.engineer {
				t.Fatalf("the probe ran with COPILOT_HOME = %q, want a throwaway home, never the engineer's %q", home, c.engineer)
			}
			for _, e := range strings.Fields(c.read(t, "home-listing")) {
				if !slices.Contains(copilotProbeHomeFiles, e) {
					t.Errorf("the throwaway home held %q at start: only %v may be there", e, copilotProbeHomeFiles)
				}
			}
			if _, err := os.Stat(home); err == nil {
				t.Errorf("the throwaway home %s is still there", home)
			}
			env := "\n" + c.read(t, "env")
			for _, k := range append(slices.Clone(capabilities.ScrubbedEnv), copilotProbeEnvDropped...) {
				if strings.Contains(env, "\n"+k+"=") {
					t.Errorf("the probe's env holds %s", k)
				}
			}
			if !strings.Contains(env, "\nCOPILOT_HOME="+home+"\n") {
				t.Errorf("the probe's env lacks COPILOT_HOME=%s", home)
			}
			if !strings.Contains(env, "\nCOPILOT_PLUGIN_DIR_ONLY=true\n") {
				t.Error("the probe's env lacks COPILOT_PLUGIN_DIR_ONLY=true")
			}
			if b, _ := os.ReadFile(filepath.Join(c.engineer, "mcp-config.json")); !strings.Contains(string(b), "engineer_only") {
				t.Error("the engineer's mcp-config.json changed")
			}
		})
	}
}

func TestCopilotFenceProbeFailuresEachSayWhy(t *testing.T) {
	seen := map[string]string{}
	for mode, text := range copilotFenceFailDetail {
		t.Run(mode, func(t *testing.T) {
			c := runCopilotProbe(t, probes.CopilotMCPFence(), mode, copilotProbeOpts{})
			l := c.line(t, capabilities.BotMCPFence)
			if l.Status != capabilities.Failed {
				t.Fatalf("status = %v, want failed", l.Status)
			}
			if l.Detail == "" || strings.Contains(l.Detail, "\n") || !strings.Contains(strings.ToLower(l.Detail), text) {
				t.Errorf("detail = %q, want one line saying %q", l.Detail, text)
			}
			if capabilities.Open(c.home).Proven("work", "copilot", capabilities.BotMCPFence) {
				t.Error("a failed run is stored as proven")
			}
			seen[mode] = l.Detail
			c.read(t, "argv") // the stand-in ran, so the failure is the probe's judgement
		})
	}
	for a, da := range seen {
		for b, db := range seen {
			if a < b && da == db {
				t.Errorf("%s and %s fail with the same detail %q", a, b, da)
			}
		}
	}
}

func TestCopilotConfigHomeProbePassesWhenTheLoginWorksAndNothingElseLoads(t *testing.T) {
	c := runCopilotProbe(t, probes.CopilotConfigHome(), "pass", copilotProbeOpts{})
	l := c.line(t, capabilities.BotConfigHome)
	if l.Status != capabilities.Passed || l.Detail == "" {
		t.Fatalf("line = %+v, want passed with a detail", l)
	}
	snap, err := capabilities.Open(c.home).Snapshot("work", "copilot")
	if err != nil {
		t.Fatal(err)
	}
	if snap.Version != "GitHub Copilot CLI 9.9.9" || snap.States[capabilities.BotConfigHome].Status != capabilities.Passed {
		t.Errorf("store: version %q, status %v; want the detected version and passed", snap.Version, snap.States[capabilities.BotConfigHome].Status)
	}
	if argv := c.argv(t); !slices.Contains(argv, "-p") && !slices.Contains(argv, "--prompt") {
		t.Errorf("argv = %q, want a non-interactive -p run", argv)
	}
}

func TestCopilotConfigHomeProbeFailuresEachSayWhy(t *testing.T) {
	seen := map[string]string{}
	for mode, text := range copilotHomeFailDetail {
		t.Run(mode, func(t *testing.T) {
			c := runCopilotProbe(t, probes.CopilotConfigHome(), mode, copilotProbeOpts{})
			l := c.line(t, capabilities.BotConfigHome)
			if l.Status != capabilities.Failed {
				t.Fatalf("status = %v, want failed", l.Status)
			}
			if l.Detail == "" || strings.Contains(l.Detail, "\n") || !strings.Contains(strings.ToLower(l.Detail), text) {
				t.Errorf("detail = %q, want one line saying %q", l.Detail, text)
			}
			seen[mode] = l.Detail
			c.read(t, "argv")
		})
	}
	for a, da := range seen {
		for b, db := range seen {
			if a < b && da == db {
				t.Errorf("%s and %s fail with the same detail %q", a, b, da)
			}
		}
	}
}

func TestCopilotProbesThatHangFailAtTheTimeLimit(t *testing.T) {
	start := time.Now()
	c := runCopilotProbe(t, probes.CopilotMCPFence(), "hang", copilotProbeOpts{timeout: 3 * time.Second})
	l := c.line(t, capabilities.BotMCPFence)
	c.read(t, "argv")
	if l.Status != capabilities.Failed {
		t.Errorf("line = %+v, want failed", l)
	}
	if time.Since(start) > 20*time.Second {
		t.Error("the probe outlived its time limit")
	}
}

func TestTheCopilotProbesAreRegisteredWithTheDefaultProbes(t *testing.T) {
	c := runCopilotProbe(t, probes.Probe{}, "pass", copilotProbeOpts{useDefault: true})
	if c.err != nil {
		t.Fatal(c.err)
	}
	for _, id := range []capabilities.ID{capabilities.BotMCPFence, capabilities.BotConfigHome} {
		if l := line(t, c.report, id); l.Status != capabilities.Passed {
			t.Errorf("copilot %s = %+v, want passed: the default run does not include its probe", id, l)
		}
		if !capabilities.Open(c.home).Proven("work", "copilot", id) {
			t.Errorf("copilot %s is not stored as proven", id)
		}
	}
}

func TestCopilotFenceProbeRunsWithEveryFlagOfTheSharedFenceTable(t *testing.T) {
	fence := launcher.CopilotFenceArgs("probe")
	if len(fence) == 0 {
		t.Fatal("launcher.CopilotFenceArgs(probe) is empty: the fence is one table the chat and the probes share")
	}
	c := runCopilotProbe(t, probes.CopilotMCPFence(), "pass", copilotProbeOpts{})
	if l := c.line(t, capabilities.BotMCPFence); l.Status != capabilities.Passed {
		t.Fatalf("line = %+v, want passed", l)
	}
	argv := c.argv(t)
	for _, a := range fence {
		if !slices.Contains(argv, a) {
			t.Errorf("argv %q lacks %q from the shared fence table", argv, a)
		}
	}
}
