package probes_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/jbuffin/admiral/internal/capabilities"
	"github.com/jbuffin/admiral/internal/config"
	"github.com/jbuffin/admiral/internal/probes"
)

// restrictedWrapper stands between the probe and e2e/fakeharness/claude: it logs what a restricted
// probe run got under $WRAP_LOG, then runs the fake. $WRAP_MODE is error (a Claude that fails before
// its init record) or hang (a restricted run that never ends); any other value passes through. Runs that are
// neither restricted nor judge calls end at once.
const restrictedWrapper = `#!/bin/sh
if [ "$1" = "--version" ]; then exec "$WRAP_FAKE" "$@"; fi
restricted=
tools=
mcp=
prev=
for a in "$@"; do
  [ "$a" = --tools ] && tools=1
  [ "$a" = --mcp-config ] && mcp=1
  [ "$prev" = --setting-sources ] && [ "$a" = project ] && restricted=1
  [ "$prev" = --settings ] && settings=$a
  [ "$prev" = -p ] && prompt=$a
  prev=$a
done
[ -n "$mcp" ] && restricted=
# the guard and fence probes of a default run are not under test here, so they end at once; judge calls reach the fake
[ -z "$restricted" ] && { [ -z "$tools" ] || [ -n "$mcp" ]; } && exit 0
if [ -n "$restricted" ]; then
  mkdir -p "$WRAP_LOG"
  printf '%s\0' "$@" >"$WRAP_LOG/argv"
  env >"$WRAP_LOG/env"
  pwd -P >"$WRAP_LOG/cwd"
  command -v admiral >"$WRAP_LOG/admiral" 2>&1
  [ -f .git/objects/info/alternates ] && : >"$WRAP_LOG/alternates"
  if [ -f "$settings" ]; then cp "$settings" "$WRAP_LOG/settings.json"; else printf '%s' "$settings" >"$WRAP_LOG/settings.json"; fi
  printf '%s' "$prompt" >"$WRAP_LOG/prompt"
  case "$WRAP_MODE" in
  error) echo "boom: not logged in" >&2; exit 3 ;;
  hang) sleep 60; exit 0 ;;
  esac
fi
"$WRAP_FAKE" "$@"
rc=$?
if [ -n "$restricted" ]; then
  printf '%s\n' "$prompt" | sed -n 's/^RUN: cat //p' | tr -d "'\"" | while IFS= read -r p; do cat "$p" 2>/dev/null; done >"$WRAP_LOG/secrets"
fi
exit $rc
`

type restrictedRun struct {
	home, log, rec string
	report         probes.Report
	err            error
}

func restrictedFleet(t *testing.T, mode, wrapMode, log, rec string) config.Fleet {
	t.Helper()
	exe := filepath.Join(t.TempDir(), "claude")
	if err := os.WriteFile(exe, []byte(restrictedWrapper), 0o755); err != nil {
		t.Fatal(err)
	}
	env := map[string]string{"WRAP_FAKE": realFake(t, "claude"), "WRAP_LOG": log, "WRAP_MODE": wrapMode, "FAKE_RECORD_DIR": rec,
		"FAKE_RESTRICTED_PROBE": mode, "GH_TOKEN": "fake-token-work-2", "GITHUB_TOKEN": "fake-token-work-3", "GH_ENTERPRISE_TOKEN": "fake-token-work-4"}
	return config.Fleet{Key: "work", Harnesses: []config.Harness{{Name: "claude", Executable: exe, Found: true, Env: env}}}
}

func runRestricted(t *testing.T, mode, wrapMode string, ps ...probes.Probe) restrictedRun {
	t.Helper()
	r := restrictedRun{home: home(t), log: t.TempDir(), rec: filepath.Join(t.TempDir(), "rec")}
	if len(ps) == 0 {
		ps = []probes.Probe{probes.ClaudeRestrictedSandbox(), probes.ClaudeRestrictedNetwork()}
	}
	runner := probes.New(r.home, ps...)
	runner.Timeout = 2 * time.Minute
	r.report, r.err = runner.Run(context.Background(), restrictedFleet(t, mode, wrapMode, r.log, r.rec), "claude")
	return r
}

func (r restrictedRun) line(t *testing.T, id capabilities.ID) probes.Line {
	t.Helper()
	if r.err != nil {
		t.Fatalf("Run: %v", r.err)
	}
	return line(t, r.report, id)
}

func (r restrictedRun) read(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(r.log, name))
	if err != nil {
		t.Fatalf("the restricted run never logged %s: the probe did not run Claude with --setting-sources project: %v", name, err)
	}
	return string(b)
}

func (r restrictedRun) wantFailed(t *testing.T, id capabilities.ID, parts ...string) {
	t.Helper()
	l := r.line(t, id)
	if l.Status != capabilities.Failed {
		t.Errorf("%s = %+v, want failed", id, l)
	}
	if strings.Contains(l.Detail, "\n") {
		t.Errorf("%s detail = %q, want one line", id, l.Detail)
	}
	for _, p := range parts {
		if !strings.Contains(strings.ToLower(l.Detail), strings.ToLower(p)) {
			t.Errorf("%s detail = %q, want it to say %q", id, l.Detail, p)
		}
	}
	if capabilities.Open(r.home).Proven("work", "claude", id) {
		t.Errorf("%s is stored as proven after a failed run", id)
	}
}

func TestBothRestrictedProbesPassOnAConfinedRunAndAreStored(t *testing.T) {
	r := runRestricted(t, "pass", "")
	for id, label := range map[capabilities.ID]string{capabilities.RestrictedSandbox: "Commands are confined", capabilities.RestrictedAllow: "Network allowlist"} {
		l := r.line(t, id)
		if l.Status != capabilities.Passed || l.Detail == "" || l.Label != label {
			t.Errorf("%s = %+v, want passed with a detail and the label %q", id, l, label)
		}
		if !capabilities.Open(r.home).Proven("work", "claude", id) {
			t.Errorf("%s is not stored as proven", id)
		}
	}
	if d := r.line(t, capabilities.RestrictedSandbox).Detail; !strings.Contains(d, "the sandbox refused the shell read and git -C push") {
		t.Errorf("sandbox detail = %q, want it to name which layer refused", d)
	}
	if d := r.line(t, capabilities.RestrictedSandbox).Detail; !strings.Contains(d, "git committed") {
		t.Errorf("sandbox detail = %q, want it to say git committed", d)
	}
	if _, err := os.Stat(filepath.Join(r.rec, "headless-runs")); err == nil {
		t.Error("a restricted probe run appended to headless-runs: the setup tests count only guard runs")
	}
}

func TestRestrictedProbeFailuresSayWhy(t *testing.T) {
	t.Run("no sandbox", func(t *testing.T) {
		r := runRestricted(t, "no-sandbox", "")
		r.wantFailed(t, capabilities.RestrictedSandbox, "outside")
		r.wantFailed(t, capabilities.RestrictedAllow, "example.com")
	})
	t.Run("a write into the shared Claude temp dir lands", func(t *testing.T) {
		r := runRestricted(t, "tmp-open", "")
		r.wantFailed(t, capabilities.RestrictedSandbox, "landed", "/tmp/claude-")
	})
	t.Run("a write to .git lands", func(t *testing.T) {
		r := runRestricted(t, "git-open", "")
		r.wantFailed(t, capabilities.RestrictedSandbox, ".git")
	})
	t.Run("a nested repo is let through", func(t *testing.T) {
		r := runRestricted(t, "nest-open", "")
		r.wantFailed(t, capabilities.RestrictedSandbox, "nested")
	})
	t.Run("git cannot commit", func(t *testing.T) {
		r := runRestricted(t, "no-commit", "")
		r.wantFailed(t, capabilities.RestrictedSandbox, "commit", "index.lock")
	})
	t.Run("no network", func(t *testing.T) {
		r := runRestricted(t, "no-network", "")
		r.wantFailed(t, capabilities.RestrictedAllow, "registry.npmjs.org", "no network")
		if l := r.line(t, capabilities.RestrictedSandbox); l.Status != capabilities.Passed {
			t.Errorf("sandbox = %+v, want passed: a missing network says nothing about confinement", l)
		}
	})
	t.Run("no tool calls", func(t *testing.T) {
		r := runRestricted(t, "no-tool", "")
		for _, id := range []capabilities.ID{capabilities.RestrictedSandbox, capabilities.RestrictedAllow} {
			l := r.line(t, id)
			if l.Status != capabilities.NotChecked || !strings.Contains(l.Detail, "inconclusive") || !strings.Contains(l.Detail, "never ran") {
				t.Errorf("%s = %+v, want not_checked and inconclusive: a model that made no tool call skipped every command", id, l)
			}
		}
	})
	t.Run("claude fails before its init record", func(t *testing.T) {
		r := runRestricted(t, "pass", "error")
		r.wantFailed(t, capabilities.RestrictedSandbox, "boom: not logged in")
		r.wantFailed(t, capabilities.RestrictedAllow, "boom: not logged in")
	})
}

func TestRestrictedProbeRunsClaudeHeadlessInASharedCloneUnderTheRestrictedProfile(t *testing.T) {
	r := runRestricted(t, "pass", "", probes.ClaudeRestrictedSandbox())
	if l := r.line(t, capabilities.RestrictedSandbox); l.Status != capabilities.Passed {
		t.Fatalf("line = %+v, want passed", l)
	}
	argv := strings.Split(strings.TrimSuffix(r.read(t, "argv"), "\x00"), "\x00")
	want := map[string]string{"--permission-mode": "dontAsk", "--setting-sources": "project", "--model": "haiku", "--output-format": "stream-json"}
	for flag, v := range want {
		if i := slices.Index(argv, flag); i < 0 || i+1 >= len(argv) || argv[i+1] != v {
			t.Errorf("argv = %q, want %s %s", argv, flag, v)
		}
	}
	for _, flag := range []string{"-p", "--verbose", "--settings"} {
		if !slices.Contains(argv, flag) {
			t.Errorf("argv = %q, want %s", argv, flag)
		}
	}
	if slices.Contains(argv, "--mcp-config") {
		t.Errorf("argv = %q: a restricted probe run carries no --mcp-config, or the fake takes it for the fence probe", argv)
	}
	r.read(t, "alternates") // the working directory is a git clone --shared
	cwd := strings.TrimSpace(r.read(t, "cwd"))
	if _, err := os.Stat(cwd); err == nil {
		t.Errorf("the probe's clone %s is still there", cwd)
	}
	if admiral := r.read(t, "admiral"); !strings.Contains(admiral, "admiral") || strings.Contains(admiral, "not found") {
		t.Errorf("command -v admiral = %q: the guard hook blocks every Bash call unless admiral is on PATH", admiral)
	}
}

func TestRestrictedProbeSettingsAreTheDraftFixProfileAndDenyReadCoversEveryOutsideTarget(t *testing.T) {
	r := runRestricted(t, "pass", "", probes.ClaudeRestrictedSandbox())
	if l := r.line(t, capabilities.RestrictedSandbox); l.Status != capabilities.Passed {
		t.Fatalf("line = %+v, want passed", l)
	}
	var s struct {
		Sandbox struct {
			Enabled                  bool  `json:"enabled"`
			FailIfUnavailable        bool  `json:"failIfUnavailable"`
			AllowUnsandboxedCommands *bool `json:"allowUnsandboxedCommands"`
			Filesystem               struct {
				DenyRead []string `json:"denyRead"`
			} `json:"filesystem"`
		} `json:"sandbox"`
		Permissions struct {
			Allow []string `json:"allow"`
			Deny  []string `json:"deny"`
		} `json:"permissions"`
		Hooks map[string]json.RawMessage `json:"hooks"`
	}
	if err := json.Unmarshal([]byte(r.read(t, "settings.json")), &s); err != nil {
		t.Fatal(err)
	}
	cwd := strings.TrimSpace(r.read(t, "cwd"))
	if !s.Sandbox.Enabled || !s.Sandbox.FailIfUnavailable || s.Sandbox.AllowUnsandboxedCommands == nil || *s.Sandbox.AllowUnsandboxedCommands {
		t.Errorf("sandbox = %+v, want enabled, failIfUnavailable and allowUnsandboxedCommands false", s.Sandbox)
	}
	if !slices.Contains(s.Permissions.Allow, "Edit(/"+cwd+"/**)") {
		t.Errorf("permissions.allow = %q, want the Draft fix rule for the clone %s", s.Permissions.Allow, cwd)
	}
	if !slices.Contains(s.Permissions.Deny, "Bash(git push *)") {
		t.Errorf("permissions.deny = %q, want Bash(git push *)", s.Permissions.Deny)
	}
	if _, ok := s.Hooks["PreToolUse"]; !ok {
		t.Errorf("hooks = %v, want the bundle's PreToolUse guard", s.Hooks)
	}

	prompt := r.read(t, "prompt")
	if !strings.Contains(prompt, "dangerouslyDisableSandbox") {
		t.Errorf("prompt = %q, want it to ask for a Bash call with dangerouslyDisableSandbox", prompt)
	}
	urls := regexp.MustCompile(`https?://\S+`)
	paths := regexp.MustCompile(`/[A-Za-z0-9_.@+=-]+(?:/[A-Za-z0-9_.@+=-]+)+`)
	var outside []string
	for _, ln := range strings.Split(prompt, "\n") {
		cmd, ok := strings.CutPrefix(ln, "RUN: ")
		if !ok {
			continue
		}
		for _, p := range paths.FindAllString(urls.ReplaceAllString(cmd, ""), -1) {
			if p != cwd && !strings.HasPrefix(p, cwd+"/") {
				outside = append(outside, p)
			}
		}
	}
	if len(outside) < 3 {
		t.Fatalf("outside targets in the RUN lines = %q, want at least the write, the escape hatch and the secret path", outside)
	}
	for _, p := range outside {
		covered := false
		for _, d := range s.Sandbox.Filesystem.DenyRead {
			covered = covered || (filepath.IsAbs(d) && d != "/" && (p == d || strings.HasPrefix(p, d+"/")))
		}
		if !covered {
			t.Errorf("%s is outside the clone and under no absolute denyRead entry %q", p, s.Sandbox.Filesystem.DenyRead)
		}
	}
}

func TestRestrictedProbeNeverPutsASecretInThePromptAndScrubsTheEnvironment(t *testing.T) {
	r := runRestricted(t, "pass", "", probes.ClaudeRestrictedSandbox())
	if l := r.line(t, capabilities.RestrictedSandbox); l.Status != capabilities.Passed {
		t.Fatalf("line = %+v, want passed", l)
	}
	prompt := r.read(t, "prompt")
	for _, ln := range strings.Split(prompt, "\n") {
		if strings.HasPrefix(ln, "RUN: ") {
			continue
		}
		if strings.HasPrefix(ln, "RUN:") {
			t.Errorf("line %q is not prefixed with %q", ln, "RUN: ")
		}
	}
	if secret := strings.TrimSpace(r.read(t, "secrets")); secret == "" {
		t.Error("the prompt has no `RUN: cat <path>` of a secret file holding a value")
	} else if strings.Contains(prompt, secret) {
		t.Error("the secret's value is in the prompt")
	}
	env := r.read(t, "env")
	for _, v := range []string{"GH_TOKEN", "GITHUB_TOKEN", "GH_ENTERPRISE_TOKEN", "ADMIRAL_SHIP", "TMUX"} {
		if regexp.MustCompile(`(?m)^` + v + `=`).MatchString(env) {
			t.Errorf("claude's env holds %s", v)
		}
	}
	if strings.Contains(env, "fake-token-work-") {
		t.Error("a token's value reached claude's env")
	}
}

func TestRestrictedProbesAreRegisteredAndNamedAsRestrictedHarnesses(t *testing.T) {
	if got := probes.RestrictedHarnesses(); !slices.Equal(got, []string{"claude"}) {
		t.Errorf("RestrictedHarnesses() = %q, want [claude]", got)
	}
	r := home(t)
	rep, err := probes.NewDefault(r).Run(context.Background(), restrictedFleet(t, "pass", "", t.TempDir(), filepath.Join(t.TempDir(), "rec")), "claude")
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []capabilities.ID{capabilities.RestrictedSandbox, capabilities.RestrictedAllow} {
		if l := line(t, rep, id); l.Status != capabilities.Passed {
			t.Errorf("%s = %+v, want passed: the default run does not include its probe", id, l)
		}
	}
}

func TestASlowRestrictedProbeCannotStarveTheJudgesAndADeadlineIsNotAPass(t *testing.T) {
	if d := probes.NewDefault(home(t)).Timeout; d < 12*time.Minute {
		t.Errorf("default run timeout = %v, want at least 12m: two more probes of up to 2 minutes each", d)
	}
	h := home(t)
	r := probes.NewDefault(h)
	r.Timeout = 12 * time.Second
	rep, err := r.Run(context.Background(), restrictedFleet(t, "pass", "hang", t.TempDir(), filepath.Join(t.TempDir(), "rec")), "claude")
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []capabilities.ID{capabilities.JudgeNoTools, capabilities.JudgeNoNetwork} {
		if l := line(t, rep, id); l.Status != capabilities.Passed {
			t.Errorf("%s = %+v, want passed: the restricted probes must run after the judges", id, l)
		}
	}
	for _, id := range []capabilities.ID{capabilities.RestrictedSandbox, capabilities.RestrictedAllow} {
		if l := line(t, rep, id); l.Status == capabilities.Passed {
			t.Errorf("%s = %+v, want not passed: a run that hit its deadline is not a pass", id, l)
		}
		if capabilities.Open(h).Proven("work", "claude", id) {
			t.Errorf("%s is stored as proven after a run that hit its deadline", id)
		}
	}
}

// runs is how many restricted runs the fake saw, retries included.
func (r restrictedRun) runs(t *testing.T) int {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(r.rec, "restricted-runs"))
	if err != nil {
		t.Fatalf("the fake logged no restricted runs: %v", err)
	}
	return strings.Count(string(b), "run\n")
}

func TestARunWhereTheModelSkippedACommandIsRetriedOnceAndThenPasses(t *testing.T) {
	for _, mode := range []string{"skip-once", "unread-once"} {
		t.Run(mode, func(t *testing.T) {
			r := runRestricted(t, mode, "", probes.ClaudeRestrictedSandbox())
			if l := r.line(t, capabilities.RestrictedSandbox); l.Status != capabilities.Passed {
				t.Errorf("sandbox = %+v, want passed on the retry", l)
			}
			if n := r.runs(t); n != 4 {
				t.Errorf("%d restricted runs, want 4: the three runs and one retry", n)
			}
		})
	}
}

func TestAFailureThatIsNotASkippedCommandIsNeverRetried(t *testing.T) {
	for mode, want := range map[string]string{"tmp-open": "landed", "git-open": ".git", "no-commit": "index.lock", "no-sandbox": "outside"} {
		t.Run(mode, func(t *testing.T) {
			r := runRestricted(t, mode, "", probes.ClaudeRestrictedSandbox())
			r.wantFailed(t, capabilities.RestrictedSandbox, want)
			if n := r.runs(t); n != 3 {
				t.Errorf("%d restricted runs, want 3: a failure that is not a skipped command is not retried", n)
			}
		})
	}
}

func TestARunThatLeakedIsNotRetriedEvenWhenTheModelAlsoSkippedACommand(t *testing.T) {
	r := runRestricted(t, "skip-leak", "", probes.ClaudeRestrictedSandbox())
	r.wantFailed(t, capabilities.RestrictedSandbox, "landed")
	if n := r.runs(t); n != 3 {
		t.Errorf("%d restricted runs, want 3: the leaking run must not be rerun into a pass", n)
	}
}

func TestTheSandboxResultSaysThatGitCommittedInTheClone(t *testing.T) {
	r := runRestricted(t, "pass", "", probes.ClaudeRestrictedSandbox())
	if d := r.line(t, capabilities.RestrictedSandbox).Detail; !strings.Contains(d, "git committed in the clone") {
		t.Errorf("detail = %q, want it to say git committed", d)
	}
}

func TestARunTheModelKeepsSkippingIsInconclusiveAndNeverFailsTheHarness(t *testing.T) {
	r := runRestricted(t, "skip-always", "")
	for _, id := range []capabilities.ID{capabilities.RestrictedSandbox, capabilities.RestrictedAllow} {
		l := r.line(t, id)
		if l.Status != capabilities.NotChecked {
			t.Errorf("%s = %+v, want not_checked: a skipped command proves nothing either way", id, l)
		}
		for _, want := range []string{"inconclusive", "never ran"} {
			if !strings.Contains(strings.ToLower(l.Detail), want) {
				t.Errorf("%s detail = %q, want it to say %q", id, l.Detail, want)
			}
		}
		if _, ok, _ := capabilities.Open(r.home).Result("work", "claude", id); ok {
			t.Errorf("%s has a stored result after an inconclusive probe", id)
		}
	}
}

// inHome runs the restricted probes against a store that earlier runs already filled.
func inHome(t *testing.T, h, mode, version string, ps ...probes.Probe) restrictedRun {
	t.Helper()
	r := restrictedRun{home: h, log: t.TempDir(), rec: filepath.Join(t.TempDir(), "rec")}
	if len(ps) == 0 {
		ps = []probes.Probe{probes.ClaudeRestrictedSandbox(), probes.ClaudeRestrictedNetwork()}
	}
	fleet := restrictedFleet(t, mode, "", r.log, r.rec)
	fleet.Harnesses[0].Env["FAKE_VERSION"] = version
	runner := probes.New(h, ps...)
	runner.Timeout = 2 * time.Minute
	r.report, r.err = runner.Run(context.Background(), fleet, "claude")
	return r
}

func TestAHarnessAlreadyProvenStaysProvenWhenALaterProbeIsInconclusive(t *testing.T) {
	h := home(t)
	first := inHome(t, h, "pass", "2.1.300")
	ids := []capabilities.ID{capabilities.RestrictedSandbox, capabilities.RestrictedAllow}
	before := map[capabilities.ID]capabilities.Result{}
	for _, id := range ids {
		if l := first.line(t, id); l.Status != capabilities.Passed {
			t.Fatalf("%s = %+v, want the first run to pass", id, l)
		}
		before[id], _, _ = capabilities.Open(h).Result("work", "claude", id)
	}
	again := inHome(t, h, "skip-always", "2.1.300")
	for _, id := range ids {
		l := again.line(t, id)
		if l.Status != capabilities.Passed || !strings.Contains(strings.ToLower(l.Detail), "inconclusive") {
			t.Errorf("%s = %+v, want passed, with a detail that reads as inconclusive", id, l)
		}
		if !capabilities.Open(h).Proven("work", "claude", id) {
			t.Errorf("%s is no longer proven after an inconclusive run", id)
		}
		if got, _, _ := capabilities.Open(h).Result("work", "claude", id); got != before[id] {
			t.Errorf("%s stored result = %+v, want it untouched: %+v", id, got, before[id])
		}
	}
}

func TestAPassForOneHarnessVersionNeverCarriesToANewOneThatIsInconclusive(t *testing.T) {
	h := home(t)
	inHome(t, h, "pass", "2.1.300").line(t, capabilities.RestrictedSandbox)
	next := inHome(t, h, "skip-always", "2.1.301")
	for _, id := range []capabilities.ID{capabilities.RestrictedSandbox, capabilities.RestrictedAllow} {
		if l := next.line(t, id); l.Status != capabilities.NotChecked {
			t.Errorf("%s = %+v, want not_checked on a version never proven", id, l)
		}
		if capabilities.Open(h).Proven("work", "claude", id) {
			t.Errorf("%s is proven on a version whose probe was inconclusive", id)
		}
	}
}

func TestAConfinementFailureOnTheCommandsThatRanFailsAtOnceEvenWhenAnotherWasSkipped(t *testing.T) {
	r := runRestricted(t, "skip-confine-fail", "", probes.ClaudeRestrictedSandbox())
	r.wantFailed(t, capabilities.RestrictedSandbox, "did not refuse", "shell read")
	if n := r.runs(t); n != 3 {
		t.Errorf("%d restricted runs, want 3: a confinement failure is not retried", n)
	}
}

func TestAConfinementFailureOverwritesAStoredPassEvenWhenACommandWasSkipped(t *testing.T) {
	h := home(t)
	inHome(t, h, "pass", "2.1.300", probes.ClaudeRestrictedSandbox()).line(t, capabilities.RestrictedSandbox)
	r := inHome(t, h, "skip-confine-fail", "2.1.300", probes.ClaudeRestrictedSandbox())
	if l := r.line(t, capabilities.RestrictedSandbox); l.Status != capabilities.Failed {
		t.Errorf("sandbox = %+v, want failed", l)
	}
}

func TestANetworkLeakOnTheCommandThatRanFailsAtOnceEvenWhenTheOtherWasSkipped(t *testing.T) {
	r := runRestricted(t, "skip-net-leak", "", probes.ClaudeRestrictedNetwork())
	r.wantFailed(t, capabilities.RestrictedAllow, "example.com")
	if n := r.runs(t); n != 1 {
		t.Errorf("%d restricted runs, want 1: a leak is not retried", n)
	}
}

func TestTheRestrictedProbesWorstCaseFitsTheDefaultRunBudget(t *testing.T) {
	r := runRestricted(t, "skip-always", "")
	need := 4*time.Minute + 6*time.Minute + time.Duration(r.runs(t))*defaultRestrictedRun
	if got := probes.NewDefault(home(t)).Timeout; got < need {
		t.Errorf("default run timeout = %v, want at least %v: the guard and fence, four judge calls and the %d restricted runs a stubborn model costs", got, need, r.runs(t))
	}
}

// defaultRestrictedRun is the time limit of one restricted run.
const defaultRestrictedRun = 2 * time.Minute

func TestAnInconclusiveCheckAfterAStoredFailureKeepsTheStoredFailureInItsDetail(t *testing.T) {
	h := home(t)
	inHome(t, h, "no-sandbox", "2.1.300", probes.ClaudeRestrictedSandbox()).line(t, capabilities.RestrictedSandbox)
	l := inHome(t, h, "skip-always", "2.1.300", probes.ClaudeRestrictedSandbox()).line(t, capabilities.RestrictedSandbox)
	if l.Status != capabilities.Failed || !strings.Contains(l.Detail, "inconclusive") || !strings.Contains(l.Detail, "outside") {
		t.Errorf("sandbox = %+v, want still failed, with both this check's inconclusive reason and the stored failure", l)
	}
}
