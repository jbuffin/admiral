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
	"github.com/jbuffin/admiral/internal/probes"
)

// Placeholders the implementer confirms on the real `codex exec --json` (each a one-line change).
var (
	// codexEvents are the JSONL events the fake prints; the shapes stand for what Codex emits.
	codexEvents = struct {
		reply        func(text string) string
		shell        string
		webSearch    string
		toolCall     func(server, tool string) string
		serverLoaded func(name string) string
	}{
		reply:        func(text string) string { return `{"type":"item.completed","item":{"type":"agent_message","text":"` + text + `"}}` },
		shell:        `{"type":"item.completed","item":{"type":"command_execution","command":"touch x","status":"completed"}}`,
		webSearch:    `{"type":"item.completed","item":{"type":"web_search","query":"x"}}`,
		toolCall:     func(server, tool string) string { return `{"type":"item.completed","item":{"type":"mcp_tool_call","server":"` + server + `","tool":"` + tool + `","status":"completed"}}` },
		serverLoaded: func(name string) string { return `{"type":"mcp.server.loaded","server":"` + name + `"}` },
	}
	// codexProbeHomeFiles are the files besides the auth.json link a probe may put in its throwaway home.
	codexProbeHomeFiles = []string{"config.toml"}
	// codexProbeFence is the flags the probe's argv must carry: the chat's fence, as config key and unquoted value.
	codexProbeFence = [][2]string{{"features.shell_tool", "false"}, {"web_search", "disabled"}, {"features.plugins", "false"}, {"features.apps", "false"}}
	// Each mode's per-probe detail text: the probe says why it failed in these words.
	codexFenceFailDetail = map[string]string{
		"shell-ran":   "shell",
		"shell-event": "shell",
		"web-search":  "web search",
		"other-tool":  "other",
		"no-call":     "never called",
		"error":       "codex failed: boom: not logged in",
	}
	codexHomeFailDetail = map[string]string{
		"login-failed":  "login",
		"no-reply":      "no reply",
		"plugin-loaded": "github",
		"plugin-cache":  "github",
	}
)

// codexHarness stands in for Codex. `--version` prints $FAKE_VERSION. Any other run logs its argv, env and CODEX_HOME
// under $PROBE_FAKE_LOG, finds the probe server's dir and nonce wherever the config puts them (a -c flag or the home's
// config.toml), acts out $PROBE_FAKE_MODE, and prints the event lines the test staged: events-fence when a probe server
// is configured, events-home when not.
const codexHarness = `#!/bin/sh
if [ "$1" = "--version" ]; then echo "${FAKE_VERSION:-codex-cli 9.9.9}"; exit 0; fi
log=$PROBE_FAKE_LOG
mkdir -p "$log"
printf '%s\0' "$@" >"$log/argv"
env >"$log/env"
printf '%s' "$CODEX_HOME" >"$log/codex-home"
ls -A "$CODEX_HOME" >"$log/home-listing"
readlink "$CODEX_HOME/auth.json" >"$log/auth-target"
text=$(printf '%s\n' "$@"; cat "$CODEX_HOME/config.toml" 2>/dev/null)
dir=$(echo "$text" | sed -n 's/.*ADMIRAL_PROBE_DIR"\{0,1\} *= *"\([^"]*\)".*/\1/p' | head -n1)
nonce=$(echo "$text" | sed -n 's/.*ADMIRAL_PROBE_NONCE"\{0,1\} *= *"\([^"]*\)".*/\1/p' | head -n1)
mkdir -p "$CODEX_HOME/tmp"
: >"$CODEX_HOME/tmp/scratch"
case "$PROBE_FAKE_MODE" in
error) echo "boom: not logged in" >&2; exit 3 ;;
login-failed) echo "401 Unauthorized" >&2; exit 1 ;;
hang) sleep 30 ;;
plugin-cache) mkdir -p "$CODEX_HOME/plugins/cache/github" ;;
shell-ran) [ -n "$dir" ] && : >"$dir/ran-$nonce" ;;
esac
case "$PROBE_FAKE_MODE" in
no-call|error|login-failed|hang) ;;
*) [ -n "$dir" ] && : >"$dir/called-$nonce" ;;
esac
if [ -n "$dir" ]; then cat "$log/events-fence"; else cat "$log/events-home"; fi
exit 0
`

type codexRun struct {
	home     string
	engineer string
	log      string
	report   probes.Report
	err      error
}

type codexOpts struct {
	timeout    time.Duration
	noLogin    bool
	dockArgs   []string
	useDefault bool
}

// stage writes the events each mode prints.
func stageCodex(t *testing.T, log, mode string) {
	t.Helper()
	e := codexEvents
	fence := []string{e.toolCall("probe", "ping"), e.reply("done")}
	home := []string{e.reply("pong")}
	switch mode {
	case "shell-ran", "shell-event":
		fence = append([]string{e.shell}, fence...)
	case "web-search":
		fence = append([]string{e.webSearch}, fence...)
	case "other-tool":
		fence = append([]string{e.toolCall("other", "run")}, fence...)
	case "no-call":
		fence = []string{e.reply("done")}
	case "no-reply":
		home = nil
	case "plugin-loaded":
		home = append([]string{e.serverLoaded("github")}, home...)
	}
	for name, lines := range map[string][]string{"events-fence": fence, "events-home": home} {
		if err := os.WriteFile(filepath.Join(log, name), []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func runCodex(t *testing.T, p probes.Probe, mode string, o codexOpts) codexRun {
	t.Helper()
	exe := filepath.Join(t.TempDir(), "codex")
	if err := os.WriteFile(exe, []byte(codexHarness), 0o755); err != nil {
		t.Fatal(err)
	}
	c := codexRun{home: home(t), engineer: t.TempDir(), log: t.TempDir()}
	// Neither the process nor the dock may fall back to the real ~/.codex.
	t.Setenv("HOME", t.TempDir())
	t.Setenv("CODEX_HOME", c.engineer)
	if !o.noLogin {
		if err := os.WriteFile(filepath.Join(c.engineer, "auth.json"), []byte("fake-codex-login"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(c.engineer, "config.toml"), []byte("[mcp_servers.engineer_only]\ncommand = \"/bin/false\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	stageCodex(t, c.log, mode)
	fleet := config.Fleet{Key: "work", Harnesses: []config.Harness{{Name: "codex", Executable: exe, Found: true, Args: o.dockArgs,
		Env: map[string]string{"PROBE_FAKE_LOG": c.log, "PROBE_FAKE_MODE": mode, "CODEX_HOME": c.engineer, "ADMIRAL_SHIP": "stray-ship", "ADMIRAL_REPORT": "/stray"}}}}
	r := probes.New(c.home, p)
	if o.useDefault {
		r = probes.NewDefault(c.home)
	}
	if o.timeout > 0 {
		r.Timeout = o.timeout
	}
	c.report, c.err = r.Run(context.Background(), fleet, "codex")
	return c
}

func (c codexRun) line(t *testing.T, id capabilities.ID) probes.Line {
	t.Helper()
	if c.err != nil {
		t.Fatalf("Run: %v", c.err)
	}
	return line(t, c.report, id)
}

func (c codexRun) read(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(c.log, name))
	if err != nil {
		t.Fatalf("the harness never logged %s: it was not run: %v", name, err)
	}
	return string(b)
}

func (c codexRun) argv(t *testing.T) []string {
	return strings.Split(strings.TrimSuffix(c.read(t, "argv"), "\x00"), "\x00")
}

func TestCodexFenceProbePassesOnAFencedRunAndStoresItUnderTheDetectedVersion(t *testing.T) {
	c := runCodex(t, probes.CodexMCPFence(), "pass", codexOpts{})
	l := c.line(t, capabilities.BotMCPFence)
	if l.Status != capabilities.Passed || l.Detail == "" {
		t.Fatalf("line = %+v, want passed with a detail", l)
	}
	if c.report.Version != "codex-cli 9.9.9" {
		t.Errorf("report version = %q, want the harness's own", c.report.Version)
	}
	snap, err := capabilities.Open(c.home).Snapshot("work", "codex")
	if err != nil {
		t.Fatal(err)
	}
	if snap.Version != "codex-cli 9.9.9" || snap.States[capabilities.BotMCPFence].Status != capabilities.Passed {
		t.Errorf("store: version %q, status %v; want the detected version and passed", snap.Version, snap.States[capabilities.BotMCPFence].Status)
	}
}

func TestCodexFenceProbeRunsExecJSONWithTheChatsFenceAndNotTheDocksArgs(t *testing.T) {
	c := runCodex(t, probes.CodexMCPFence(), "pass", codexOpts{dockArgs: []string{"--search", "--dangerously-bypass-approvals-and-sandbox", "-c", `mcp_servers.dock.command="/bin/dock"`}})
	if l := c.line(t, capabilities.BotMCPFence); l.Status != capabilities.Passed {
		t.Fatalf("line = %+v, want passed", l)
	}
	argv := c.argv(t)
	if len(argv) == 0 || argv[0] != "exec" || !slices.Contains(argv, "--json") {
		t.Errorf("argv = %q, want exec … --json", argv)
	}
	for _, a := range argv {
		if a == "--ignore-user-config" || a == "--search" || strings.Contains(a, "dangerously") || strings.Contains(a, "mcp_servers.dock") {
			t.Errorf("argv = %q carries %q: the probe proves the chat's own config path and never the dock's args", argv, a)
		}
	}
	cfg := map[string]string{}
	for i := 0; i+1 < len(argv); i++ {
		switch argv[i] {
		case "-c", "--config":
			if k, v, ok := strings.Cut(argv[i+1], "="); ok {
				cfg[k] = strings.Trim(v, `"`)
			}
		case "--disable":
			cfg["features."+argv[i+1]] = "false"
		}
	}
	for _, f := range codexProbeFence {
		if cfg[f[0]] != f[1] {
			t.Errorf("config %s = %q, want %q (argv %q)", f[0], cfg[f[0]], f[1], argv)
		}
	}
	small, _, _ := config.ModelPair("codex")
	if i := slices.Index(argv, "-m"); (i < 0 || i+1 >= len(argv) || argv[i+1] != small) && cfg["model"] != small {
		t.Errorf("argv = %q, want the cheapest model %q", argv, small)
	}
}

func TestCodexProbesRunInAThrowawayHomeWithOnlyTheLoginLinkAndRemoveIt(t *testing.T) {
	for name, p := range map[string]probes.Probe{"fence": probes.CodexMCPFence(), "config home": probes.CodexConfigHome()} {
		t.Run(name, func(t *testing.T) {
			c := runCodex(t, p, "pass", codexOpts{})
			if c.err != nil {
				t.Fatal(c.err)
			}
			home := c.read(t, "codex-home")
			if home == "" || home == c.engineer {
				t.Fatalf("the probe ran with CODEX_HOME = %q, want a throwaway home, never the engineer's %q", home, c.engineer)
			}
			for _, e := range strings.Fields(c.read(t, "home-listing")) {
				if e != "auth.json" && !slices.Contains(codexProbeHomeFiles, e) {
					t.Errorf("the throwaway home held %q at start: only auth.json and %v may be there", e, codexProbeHomeFiles)
				}
			}
			if want := filepath.Join(c.engineer, "auth.json"); strings.TrimSpace(c.read(t, "auth-target")) != want {
				t.Errorf("auth.json points at %q, want the engineer's %q", c.read(t, "auth-target"), want)
			}
			if _, err := os.Stat(home); err == nil {
				t.Errorf("the throwaway home %s is still there (Codex writes tmp/ into it)", home)
			}
			env := c.read(t, "env")
			for _, k := range capabilities.ScrubbedEnv {
				if strings.Contains("\n"+env, "\n"+k+"=") {
					t.Errorf("the probe's env holds %s", k)
				}
			}
			if !strings.Contains("\n"+env, "\nCODEX_HOME="+home+"\n") {
				t.Errorf("the probe's env lacks CODEX_HOME=%s", home)
			}
			if b, _ := os.ReadFile(filepath.Join(c.engineer, "config.toml")); !strings.Contains(string(b), "engineer_only") {
				t.Error("the engineer's config.toml changed")
			}
		})
	}
}

func TestCodexFenceProbeFailuresEachSayWhy(t *testing.T) {
	seen := map[string]string{}
	for mode, text := range codexFenceFailDetail {
		t.Run(mode, func(t *testing.T) {
			c := runCodex(t, probes.CodexMCPFence(), mode, codexOpts{})
			l := c.line(t, capabilities.BotMCPFence)
			if l.Status != capabilities.Failed {
				t.Fatalf("status = %v, want failed", l.Status)
			}
			if l.Detail == "" || strings.Contains(l.Detail, "\n") || !strings.Contains(l.Detail, text) {
				t.Errorf("detail = %q, want one line saying %q", l.Detail, text)
			}
			if capabilities.Open(c.home).Proven("work", "codex", capabilities.BotMCPFence) {
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

func TestCodexConfigHomeProbePassesWhenTheLoginWorksAndNothingElseLoads(t *testing.T) {
	c := runCodex(t, probes.CodexConfigHome(), "pass", codexOpts{})
	l := c.line(t, capabilities.BotConfigHome)
	if l.Status != capabilities.Passed || l.Detail == "" {
		t.Fatalf("line = %+v, want passed with a detail", l)
	}
	snap, err := capabilities.Open(c.home).Snapshot("work", "codex")
	if err != nil {
		t.Fatal(err)
	}
	if snap.Version != "codex-cli 9.9.9" || snap.States[capabilities.BotConfigHome].Status != capabilities.Passed {
		t.Errorf("store: version %q, status %v; want the detected version and passed", snap.Version, snap.States[capabilities.BotConfigHome].Status)
	}
	argv := c.argv(t)
	for _, a := range argv {
		if a == "--ignore-user-config" {
			t.Errorf("argv = %q: the probe must prove the chat's config path", argv)
		}
	}
}

func TestCodexConfigHomeProbeFailuresEachSayWhy(t *testing.T) {
	seen := map[string]string{}
	for mode, text := range codexHomeFailDetail {
		t.Run(mode, func(t *testing.T) {
			c := runCodex(t, probes.CodexConfigHome(), mode, codexOpts{})
			l := c.line(t, capabilities.BotConfigHome)
			if l.Status != capabilities.Failed {
				t.Fatalf("status = %v, want failed", l.Status)
			}
			if l.Detail == "" || strings.Contains(l.Detail, "\n") || !strings.Contains(strings.ToLower(l.Detail), text) {
				t.Errorf("detail = %q, want one line saying %q", l.Detail, text)
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

func TestCodexProbesWithoutAnEngineerLoginFailNamingAuthJSON(t *testing.T) {
	for name, p := range map[string]struct {
		p  probes.Probe
		id capabilities.ID
	}{"fence": {probes.CodexMCPFence(), capabilities.BotMCPFence}, "config home": {probes.CodexConfigHome(), capabilities.BotConfigHome}} {
		t.Run(name, func(t *testing.T) {
			c := runCodex(t, p.p, "pass", codexOpts{noLogin: true})
			l := c.line(t, p.id)
			if l.Status != capabilities.Failed || !strings.Contains(l.Detail, "auth.json") {
				t.Errorf("line = %+v, want failed naming auth.json", l)
			}
		})
	}
}

func TestCodexProbesThatHangFailAtTheTimeLimit(t *testing.T) {
	start := time.Now()
	c := runCodex(t, probes.CodexMCPFence(), "hang", codexOpts{timeout: 3 * time.Second})
	l := c.line(t, capabilities.BotMCPFence)
	c.read(t, "argv")
	if l.Status != capabilities.Failed {
		t.Errorf("line = %+v, want failed", l)
	}
	if time.Since(start) > 20*time.Second {
		t.Error("the probe outlived its time limit")
	}
}

func TestTheCodexProbesAreRegisteredWithTheDefaultProbes(t *testing.T) {
	c := runCodex(t, probes.Probe{}, "pass", codexOpts{useDefault: true})
	if c.err != nil {
		t.Fatal(c.err)
	}
	for _, id := range []capabilities.ID{capabilities.BotMCPFence, capabilities.BotConfigHome} {
		if l := line(t, c.report, id); l.Status != capabilities.Passed {
			t.Errorf("codex %s = %+v, want passed: the default run does not include its probe", id, l)
		}
		if !capabilities.Open(c.home).Proven("work", "codex", id) {
			t.Errorf("codex %s is not stored as proven", id)
		}
	}
}
