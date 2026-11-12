package probes_test

import (
	"context"
	"encoding/json"
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

// fenceHarness stands in for Claude Code run with the chat's fence flags. Given --mcp-config it
// logs what it got under $PROBE_FAKE_LOG, then acts out $PROBE_FAKE_MODE: the probe's server
// writes called-<nonce> for a tools/call and the model's shell command writes ran-<nonce>; the
// stand-in does both itself and prints a stream-json init record.
const fenceHarness = `#!/bin/sh
if [ "$1" = "--version" ]; then echo "${FAKE_VERSION:-2.1.300 (fake)}"; exit 0; fi
log=$PROBE_FAKE_LOG
mkdir -p "$log"
cfg=
prev=
for a in "$@"; do
  [ "$prev" = --mcp-config ] && cfg=$a
  [ "$prev" = --settings ] && cp "$a" "$log/settings.json" 2>/dev/null
  prev=$a
done
[ -n "$cfg" ] || exit 0
printf '%s\0' "$@" >"$log/argv"
env >"$log/env"
pwd -P >"$log/cwd"
if [ -f "$cfg" ]; then cp "$cfg" "$log/mcp.json"; : >"$log/config-existed"; fi
name=$(tr -d ' \n\t' <"$cfg" | sed 's/.*"mcpServers":{"\([^"]*\)".*/\1/')
called=$ADMIRAL_PROBE_DIR/called-$ADMIRAL_PROBE_NONCE
ran=$ADMIRAL_PROBE_DIR/ran-$ADMIRAL_PROBE_NONCE
init() { printf '{"type":"system","subtype":"init","tools":[%s],"mcp_servers":[%s]}\n' "$1" "$2"; }
mine="{\"name\":\"$name\",\"status\":\"connected\"}"
case "$PROBE_FAKE_MODE" in
pass) : >"$called"; init "\"mcp__$name__ping\"" "$mine" ;;
builtin-listed) : >"$called"; init "\"Bash\",\"mcp__$name__ping\"" "$mine" ;;
second-server) : >"$called"; init "\"mcp__$name__ping\",\"mcp__other__run\"" "$mine,{\"name\":\"other\",\"status\":\"connected\"}" ;;
shell-ran) : >"$called"; : >"$ran"; init "\"mcp__$name__ping\"" "$mine" ;;
no-call) init "\"mcp__$name__ping\"" "$mine" ;;
no-init) : >"$called"; echo "all done" ;;
error) echo "boom: not logged in" >&2; exit 3 ;;
hang) sleep 30 ;;
esac
exit 0
`

type fenceRun struct {
	home   string
	log    string
	report probes.Report
	err    error
}

func runFence(t *testing.T, mode string, timeout time.Duration, fleetArgs ...string) fenceRun {
	t.Helper()
	exe := filepath.Join(t.TempDir(), "claude")
	if err := os.WriteFile(exe, []byte(fenceHarness), 0o755); err != nil {
		t.Fatal(err)
	}
	f := fenceRun{home: home(t), log: t.TempDir()}
	fleet := config.Fleet{Key: "work", Harnesses: []config.Harness{{Name: "claude", Executable: exe, Found: true, Args: fleetArgs,
		Env: map[string]string{"PROBE_FAKE_LOG": f.log, "PROBE_FAKE_MODE": mode}}}}
	r := probes.New(f.home, probes.ClaudeMCPFence())
	if timeout > 0 {
		r.Timeout = timeout
	}
	f.report, f.err = r.Run(context.Background(), fleet, "claude")
	return f
}

func (f fenceRun) line(t *testing.T) probes.Line {
	t.Helper()
	if f.err != nil {
		t.Fatalf("Run: %v", f.err)
	}
	return line(t, f.report, capabilities.BotMCPFence)
}

func (f fenceRun) read(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(f.log, name))
	if err != nil {
		t.Fatalf("the harness never logged %s: it was not run with --mcp-config: %v", name, err)
	}
	return string(b)
}

func TestFenceProbePassesOnAFencedRunAndStoresIt(t *testing.T) {
	f := runFence(t, "pass", 0)
	l := f.line(t)
	if l.Status != capabilities.Passed || l.Detail == "" {
		t.Fatalf("line = %+v, want passed with a detail", l)
	}
	if !capabilities.Open(f.home).Proven("work", "claude", capabilities.BotMCPFence) {
		t.Error("the passed result is not in the store")
	}
}

func TestFenceProbeRunsClaudeWithTheChatsFenceFlags(t *testing.T) {
	f := runFence(t, "pass", 0, "--allowedTools", "Bash", "--mcp-config", "/elsewhere/mcp.json")
	if l := f.line(t); l.Status != capabilities.Passed {
		t.Fatalf("line = %+v, want passed", l)
	}
	argv := strings.Split(strings.TrimSuffix(f.read(t, "argv"), "\x00"), "\x00")
	if i := slices.Index(argv, "--tools"); i < 0 || i+1 >= len(argv) || argv[i+1] != "" {
		t.Errorf("argv = %q, want --tools followed by an empty element", argv)
	}
	if !slices.Contains(argv, "--strict-mcp-config") {
		t.Errorf("argv = %q, want --strict-mcp-config", argv)
	}
	if i := slices.Index(argv, "--permission-mode"); i < 0 || i+1 >= len(argv) || argv[i+1] != "dontAsk" {
		t.Errorf("argv = %q, want --permission-mode dontAsk", argv)
	}
	if !slices.Contains(argv, "-p") {
		t.Errorf("argv = %q, want -p: the probe runs headless", argv)
	}
	configs := 0
	for _, a := range argv {
		if a == "--mcp-config" {
			configs++
		}
		if a == "--allowedTools" || a == "Bash" || a == "/elsewhere/mcp.json" {
			t.Errorf("argv = %q carries the dock's %q: the probe runs exactly the chat's fence", argv, a)
		}
	}
	if configs != 1 {
		t.Errorf("argv = %q has %d --mcp-config flags, want 1", argv, configs)
	}
	f.read(t, "config-existed")
}

func TestFenceProbeConfigHasOneServerAndSettingsAllowOnlyIt(t *testing.T) {
	f := runFence(t, "pass", 0)
	if l := f.line(t); l.Status != capabilities.Passed {
		t.Fatalf("line = %+v, want passed", l)
	}
	var cfg struct {
		Servers map[string]struct {
			Command string `json:"command"`
		} `json:"mcpServers"`
	}
	if err := json.Unmarshal([]byte(f.read(t, "mcp.json")), &cfg); err != nil || len(cfg.Servers) != 1 {
		t.Fatalf("mcp.json = %q (%v), want one server", f.read(t, "mcp.json"), err)
	}
	var name string
	for n, s := range cfg.Servers {
		name = n
		if s.Command == "" {
			t.Errorf("server %s has no command", n)
		}
	}
	var s struct {
		Permissions struct {
			Allow []string `json:"allow"`
		} `json:"permissions"`
	}
	if err := json.Unmarshal([]byte(f.read(t, "settings.json")), &s); err != nil {
		t.Fatal(err)
	}
	if want := []string{"mcp__" + name + "__*"}; !slices.Equal(s.Permissions.Allow, want) {
		t.Errorf("permissions.allow = %q, want %q", s.Permissions.Allow, want)
	}
}

func TestFenceProbeLeavesNothingBehind(t *testing.T) {
	f := runFence(t, "pass", 0)
	if l := f.line(t); l.Status != capabilities.Passed {
		t.Fatalf("line = %+v, want passed", l)
	}
	cwd := strings.TrimSpace(f.read(t, "cwd"))
	if _, err := os.Stat(cwd); err == nil {
		t.Errorf("the probe's run directory %s is still there", cwd)
	}
	if _, err := os.Stat(filepath.Join(f.home, "chat")); err == nil {
		t.Error("the probe wrote into the home's chat directory")
	}
}

func TestFenceProbeFailuresEachSayWhy(t *testing.T) {
	want := map[string]string{
		"builtin-listed": "built-in tool",
		"second-server":  "other",
		"shell-ran":      "ran the shell command",
		"no-call":        "never called the probe server's tool",
		"no-init":        "init record",
		"error":          "claude failed: boom: not logged in",
	}
	seen := map[string]string{}
	for mode, text := range want {
		t.Run(mode, func(t *testing.T) {
			f := runFence(t, mode, 0)
			l := f.line(t)
			f.read(t, "argv") // the stand-in ran, so the failure is the probe's judgement
			if l.Status != capabilities.Failed {
				t.Fatalf("status = %v, want failed (only what Admiral observes counts)", l.Status)
			}
			if l.Detail == "" || strings.Contains(l.Detail, "\n") || !strings.Contains(l.Detail, text) {
				t.Errorf("detail = %q, want one line saying %q", l.Detail, text)
			}
			if capabilities.Open(f.home).Proven("work", "claude", capabilities.BotMCPFence) {
				t.Error("a failed run is stored as proven")
			}
			seen[mode] = l.Detail
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

func TestFenceProbeThatHangsFailsAtTheTimeLimit(t *testing.T) {
	start := time.Now()
	f := runFence(t, "hang", 3*time.Second)
	l := f.line(t)
	f.read(t, "argv")
	if l.Status != capabilities.Failed {
		t.Errorf("line = %+v, want failed", l)
	}
	if time.Since(start) > 20*time.Second {
		t.Error("the probe outlived its time limit")
	}
}

func TestTheFenceProbeIsRegisteredWithTheDefaultProbes(t *testing.T) {
	exe := filepath.Join(t.TempDir(), "claude")
	if err := os.WriteFile(exe, []byte(fenceHarness), 0o755); err != nil {
		t.Fatal(err)
	}
	log := t.TempDir()
	fleet := config.Fleet{Key: "work", Harnesses: []config.Harness{{Name: "claude", Executable: exe, Found: true,
		Env: map[string]string{"PROBE_FAKE_LOG": log, "PROBE_FAKE_MODE": "pass"}}}}
	rep, err := probes.NewDefault(home(t)).Run(context.Background(), fleet, "claude")
	if err != nil {
		t.Fatal(err)
	}
	if l := line(t, rep, capabilities.BotMCPFence); l.Status != capabilities.Passed {
		t.Errorf("bot.mcp_fence = %+v, want passed: the default run does not include the fence probe", l)
	}
	codex := filepath.Join(t.TempDir(), "codex")
	if err := os.WriteFile(codex, []byte(fenceHarness), 0o755); err != nil {
		t.Fatal(err)
	}
	cfleet := config.Fleet{Key: "work", Harnesses: []config.Harness{{Name: "codex", Executable: codex, Found: true,
		Env: map[string]string{"PROBE_FAKE_LOG": log, "PROBE_FAKE_MODE": "pass"}}}}
	crep, err := probes.NewDefault(home(t)).Run(context.Background(), cfleet, "codex")
	if err != nil {
		t.Fatal(err)
	}
	if l := line(t, crep, capabilities.BotMCPFence); l.Status != capabilities.NotChecked {
		t.Errorf("codex bot.mcp_fence = %v, want not_checked: only Claude's fence is probed", l.Status)
	}
}
