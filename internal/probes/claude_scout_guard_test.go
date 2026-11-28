package probes_test

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/jbuffin/admiral/internal/capabilities"
	"github.com/jbuffin/admiral/internal/config"
	"github.com/jbuffin/admiral/internal/probes"
)

// scoutHarness stands in for Claude Code asked to Write one file into the scout checkout: the guard's marker
// files are scout-seen-<nonce> and scout-denied-<nonce> in the probe dir, the file is <checkout>/scout-<nonce>.
const scoutHarness = `#!/bin/sh
if [ "$1" = "--version" ]; then echo "2.1.300 (fake)"; exit 0; fi
log=$PROBE_FAKE_LOG
mkdir -p "$log"
printf '%s\0' "$@" >"$log/argv"
env >"$log/env"
pwd -P >"$log/cwd"
prev=
for a in "$@"; do
  [ "$prev" = --settings ] && cp "$a" "$log/settings.json" 2>/dev/null
  [ "$prev" = -p ] && prompt=$a
  prev=$a
done
printf '%s' "$prompt" >"$log/prompt"
file=$ADMIRAL_SCOUT/scout-$ADMIRAL_PROBE_NONCE
seen=$ADMIRAL_PROBE_DIR/scout-seen-$ADMIRAL_PROBE_NONCE
denied=$ADMIRAL_PROBE_DIR/scout-denied-$ADMIRAL_PROBE_NONCE
case "$PROBE_FAKE_MODE" in
pass) : >"$seen"; : >"$denied"; echo "The Write was blocked by a hook." ;;
skip-hook-says-denied) : >"$file"; echo "The Write was denied by the hook." ;;
no-tool) echo "I will not write that." ;;
different-file) : >"$seen"; : >"$ADMIRAL_SCOUT/other"; echo done ;;
ran-anyway) : >"$seen"; : >"$denied"; : >"$file"; echo done ;;
autopilot-markers-only) : >"$ADMIRAL_PROBE_DIR/seen-$ADMIRAL_PROBE_NONCE"; : >"$ADMIRAL_PROBE_DIR/denied-$ADMIRAL_PROBE_NONCE"; echo done ;;
error) echo "boom: not logged in" >&2; exit 3 ;;
esac
exit 0
`

func runScoutGuard(t *testing.T, mode string, fleetEnv map[string]string) guardRun {
	t.Helper()
	exe := filepath.Join(t.TempDir(), "claude")
	if err := os.WriteFile(exe, []byte(scoutHarness), 0o755); err != nil {
		t.Fatal(err)
	}
	g := guardRun{home: home(t), log: t.TempDir()}
	env := map[string]string{"PROBE_FAKE_LOG": g.log, "PROBE_FAKE_MODE": mode}
	for k, v := range fleetEnv {
		env[k] = v
	}
	fleet := config.Fleet{Key: "work", Harnesses: []config.Harness{{Name: "claude", Executable: exe, Found: true, Env: env}}}
	g.report, g.err = probes.New(g.home, probes.ClaudeScoutGuard()).Run(context.Background(), fleet, "claude")
	return g
}

func (g guardRun) scout(t *testing.T) probes.Line {
	t.Helper()
	if g.err != nil {
		t.Fatalf("Run: %v", g.err)
	}
	return line(t, g.report, "scout.guard_hook")
}

func TestScoutGuardProbeIsClaudeOnly(t *testing.T) {
	t.Parallel()
	if got := probes.ScoutGuardHarnesses(); !slices.Equal(got, []string{"claude"}) {
		t.Errorf("ScoutGuardHarnesses = %v, want [claude]: Codex and Copilot have no guard hook wiring", got)
	}
	if p := probes.ClaudeScoutGuard(); p.Harness != "claude" || p.Capability != "scout.guard_hook" {
		t.Errorf("probe = %s %s, want claude scout.guard_hook", p.Harness, p.Capability)
	}
}

func TestScoutGuardProbePassesOnlyWhenTheGuardSawTheWriteAndNoFileExists(t *testing.T) {
	t.Parallel()
	g := runScoutGuard(t, "pass", nil)
	l := g.scout(t)
	if l.Status != capabilities.Passed || l.Detail == "" {
		t.Fatalf("line = %+v, want passed with a detail", l)
	}
	if !capabilities.Open(g.home).Proven("work", "claude", "scout.guard_hook") {
		t.Error("the passed result is not in the store")
	}
}

func TestScoutGuardProbeFailuresEachSayWhy(t *testing.T) {
	t.Parallel()
	want := map[string]string{
		"skip-hook-says-denied":  "without running the guard hook",
		"no-tool":                "never called the Write tool",
		"different-file":         "different file",
		"ran-anyway":             "after the guard hook denied it",
		"autopilot-markers-only": "never called the Write tool",
		"error":                  "claude failed: boom: not logged in",
	}
	details := map[string]string{}
	for mode := range want {
		t.Run(mode, func(t *testing.T) {
			g := runScoutGuard(t, mode, nil)
			l := g.scout(t)
			if l.Status != capabilities.Failed {
				t.Fatalf("status = %v, want failed (the model's words and exit code are never evidence)", l.Status)
			}
			if l.Detail == "" || strings.Contains(l.Detail, "\n") || !strings.Contains(l.Detail, want[mode]) {
				t.Errorf("detail = %q, want one line saying %q", l.Detail, want[mode])
			}
			if capabilities.Open(g.home).Proven("work", "claude", "scout.guard_hook") {
				t.Error("a failed probe left the capability proven")
			}
			details[mode] = l.Detail
		})
	}
	seen := map[string]string{}
	for mode, d := range details {
		if other, dup := seen[d]; dup {
			t.Errorf("%s and %s fail with the same detail %q", mode, other, d)
		}
		seen[d] = mode
	}
}

func TestScoutGuardProbeRunsClaudeWithTheRealBundleAndACheckoutItOwns(t *testing.T) {
	t.Parallel()
	g := runScoutGuard(t, "pass", map[string]string{"ADMIRAL_SCOUT": "/somewhere/else", "ADMIRAL_AUTOPILOT": "0"})
	g.scout(t)
	argv := strings.Split(strings.TrimSuffix(g.read(t, "argv"), "\x00"), "\x00")
	at := slices.Index(argv, "--allowedTools")
	if at < 0 || at+1 >= len(argv) || argv[at+1] != "Write" {
		t.Errorf("argv = %q, want --allowedTools Write", argv)
	}
	if !slices.Contains(argv, "-p") || !slices.Contains(argv, "--settings") {
		t.Errorf("argv = %q, want a headless run with the bundle's settings", argv)
	}
	if !strings.Contains(g.read(t, "settings.json"), "admiral guard pretooluse") {
		t.Error("the run's settings do not run the guard")
	}
	env := map[string]string{}
	for _, kv := range strings.Split(g.read(t, "env"), "\n") {
		if k, v, ok := strings.Cut(kv, "="); ok {
			env[k] = v
		}
	}
	checkout, dir := env["ADMIRAL_SCOUT"], env[probes.DirEnv]
	if !filepath.IsAbs(checkout) || checkout == "/somewhere/else" || !strings.HasPrefix(checkout, dir+string(filepath.Separator)) {
		t.Errorf("ADMIRAL_SCOUT = %q, want a directory inside the probe's run dir %q, whatever the fleet's env says", checkout, dir)
	}
	if env["ADMIRAL_AUTOPILOT"] != "1" {
		t.Errorf("ADMIRAL_AUTOPILOT = %q, want 1: a scout runs in autopilot", env["ADMIRAL_AUTOPILOT"])
	}
	if !probes.ValidEnv(dir, env[probes.NonceEnv]) {
		t.Errorf("nonce %q and dir %q are not what the guard accepts", env[probes.NonceEnv], dir)
	}
	want := filepath.Join(checkout, "scout-"+env[probes.NonceEnv])
	if prompt := g.read(t, "prompt"); !strings.Contains(prompt, "Write") || !strings.Contains(prompt, want) {
		t.Errorf("prompt = %q, want it to ask for the Write tool and the file %s", prompt, want)
	}
	if cwd := g.read(t, "cwd"); strings.TrimSpace(cwd) != dir && strings.TrimSpace(cwd) != checkout {
		t.Errorf("cwd = %q, want the run dir or the checkout", cwd)
	}
}

func TestScoutGuardProbeLeavesNothingBehind(t *testing.T) {
	t.Parallel()
	g := runScoutGuard(t, "pass", nil)
	g.scout(t)
	dir := strings.TrimSpace(strings.SplitN(strings.SplitN(g.read(t, "env"), probes.DirEnv+"=", 2)[1], "\n", 2)[0])
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Errorf("the probe's run dir %s survived the run: %v", dir, err)
	}
}
