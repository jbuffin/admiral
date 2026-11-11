package e2e

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// probeWorld is a setup world whose fake claude reports a version from a file and acts out $FAKE_PROBE.
type probeWorld struct {
	*world
	versionFile string
	file        string
}

func newProbeWorld(t *testing.T) *probeWorld {
	t.Helper()
	w := setupWorld(t)
	pw := &probeWorld{world: w, versionFile: filepath.Join(w.root, "version")}
	w.writeRaw(pw.versionFile, "2.1.300 (fake)", 0o644)
	pw.file = w.answers(t, `"work":{"name":"Work","search_roots":["~/code"],"claude":{"executable":`+quote(w.fakeExe2("claude"))+`}}`)
	return pw
}

func (pw *probeWorld) env(extra ...string) []string {
	return append([]string{"FAKE_RECORD_DIR=" + pw.rec, "FAKE_VERSION_FILE=" + pw.versionFile}, extra...)
}

func (pw *probeWorld) setup(extra ...string) cliResult {
	pw.t.Helper()
	return pw.admiralWith(cliOpts{env: pw.env(extra...)}, "setup", "--answers", pw.file, "--yes")
}

func (pw *probeWorld) store() map[string]any {
	pw.t.Helper()
	b, err := os.ReadFile(filepath.Join(pw.home, "capabilities.json"))
	if err != nil {
		pw.t.Fatalf("capabilities.json: %v", err)
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		pw.t.Fatal(err)
	}
	return m
}

func (pw *probeWorld) storeText() string {
	b, _ := os.ReadFile(filepath.Join(pw.home, "capabilities.json"))
	return string(b)
}

func (pw *probeWorld) headlessRuns() int {
	b, _ := os.ReadFile(filepath.Join(pw.rec, "headless-runs"))
	n := 0
	for _, l := range strings.Split(string(b), "\n") {
		if strings.TrimSpace(l) != "" {
			n++
		}
	}
	return n
}

func lineFor(out, label string) string {
	for _, l := range strings.Split(out, "\n") {
		if strings.Contains(l, label) {
			return l
		}
	}
	return ""
}

func TestProbe_SetupProvesTheGuardOnAFreshHome(t *testing.T) {
	t.Parallel()
	pw := newProbeWorld(t)
	r := pw.setup()
	if r.code != 0 {
		t.Fatalf("setup exited %d\n%s%s", r.code, r.stdout, r.stderr)
	}
	if l := lineFor(r.stdout, "Pre-tool guard hook"); !strings.Contains(l, "passed") {
		t.Errorf("setup output has no passed line for the guard hook (got %q):\n%s", l, r.stdout)
	}
	if l := lineFor(r.stdout, "Advisor"); !strings.Contains(strings.ToLower(l), "not checked") {
		t.Errorf("the advisor has no probe and should read not checked (got %q)", l)
	}
	if s := pw.storeText(); !strings.Contains(s, "2.1.300 (fake)") || !strings.Contains(s, "autopilot.guard_hook") {
		t.Errorf("capabilities.json lacks the version or the guard result:\n%s", s)
	}
	if pw.headlessRuns() != 1 {
		t.Errorf("the fake claude ran headless %d times, want 1", pw.headlessRuns())
	}
	for _, name := range []string{"outcomes", "tmux.sock"} {
		if _, err := os.Stat(filepath.Join(pw.home, name)); err == nil {
			t.Errorf("setup's probe left %s in the home", name)
		}
	}
}

func TestProbe_EachWayTheGuardCanFailPrintsFailedWithItsOwnReason(t *testing.T) {
	t.Parallel()
	var mu sync.Mutex
	seen := map[string]string{}
	for _, mode := range []string{"skip-hook", "run-anyway", "no-tool", "different-command"} {
		t.Run(mode, func(t *testing.T) {
			t.Parallel()
			pw := newProbeWorld(t)
			r := pw.setup("FAKE_PROBE=" + mode)
			if r.code != 0 {
				t.Fatalf("setup exited %d: a failed probe is a result, not an error\n%s%s", r.code, r.stdout, r.stderr)
			}
			l := lineFor(r.stdout, "Pre-tool guard hook")
			if !strings.Contains(l, "failed") {
				t.Fatalf("guard line = %q, want failed\n%s", l, r.stdout)
			}
			res := pw.store()
			raw, _ := json.Marshal(res)
			if !strings.Contains(string(raw), `"passed":false`) && !strings.Contains(string(raw), `"Passed":false`) {
				t.Errorf("the store holds no failed result: %s", raw)
			}
			detail := strings.TrimSpace(strings.TrimPrefix(l[strings.Index(l, "failed")+len("failed"):], ":"))
			if detail == "" {
				t.Errorf("the failed line gives no reason: %q", l)
			}
			mu.Lock()
			seen[mode] = detail
			mu.Unlock()
		})
	}
	t.Cleanup(func() {
		by := map[string]string{}
		for m, d := range seen {
			if o, dup := by[d]; dup {
				t.Errorf("%s and %s print the same reason %q", m, o, d)
			}
			by[d] = m
		}
	})
}

func TestProbe_RunningSetupAgainWithNothingToChangeStillReprobes(t *testing.T) {
	t.Parallel()
	pw := newProbeWorld(t)
	if r := pw.setup(); !strings.Contains(lineFor(r.stdout, "Pre-tool guard hook"), "passed") {
		t.Fatalf("first run did not pass:\n%s", r.stdout)
	}
	r := pw.setup("FAKE_PROBE=run-anyway")
	if !strings.Contains(r.stdout, "No changes") {
		t.Fatalf("second run changed something:\n%s", r.stdout)
	}
	if l := lineFor(r.stdout, "Pre-tool guard hook"); !strings.Contains(l, "failed") {
		t.Errorf("a rerun with nothing to change reused the old result (line %q): Check again must probe", l)
	}
	if pw.headlessRuns() != 2 {
		t.Errorf("headless runs = %d, want 2", pw.headlessRuns())
	}
}

func TestProbe_DryRunAndDecliningRunNoProbe(t *testing.T) {
	t.Parallel()
	pw := newProbeWorld(t)
	pw.admiralWith(cliOpts{env: pw.env()}, "setup", "--answers", pw.file, "--dry-run")
	pw.admiralWith(cliOpts{env: pw.env(), stdin: "n\n"}, "setup", "--answers", pw.file)
	if pw.headlessRuns() != 0 {
		t.Errorf("a probe ran %d times on a dry run or a declined setup", pw.headlessRuns())
	}
	if _, err := os.Stat(filepath.Join(pw.home, "capabilities.json")); err == nil {
		t.Error("capabilities.json was written without the engineer's go-ahead")
	}
	// Once the config holds the answers, a dry run finds nothing to change and still probes nothing.
	pw.setup()
	before := pw.headlessRuns()
	r := pw.admiralWith(cliOpts{env: pw.env()}, "setup", "--answers", pw.file, "--dry-run")
	if !strings.Contains(r.stdout, "No changes") {
		t.Fatalf("the dry run changed something:\n%s", r.stdout)
	}
	if got := pw.headlessRuns(); got != before {
		t.Errorf("a dry run with nothing to change ran %d probes", got-before)
	}
}

func TestProbe_RestartKeepsTheStatusAndSpendsNoModelCall(t *testing.T) {
	t.Parallel()
	pw := newProbeWorld(t)
	pw.setup()
	if pw.headlessRuns() != 1 {
		t.Fatalf("setup did not probe: headless runs = %d, want 1", pw.headlessRuns())
	}
	pw.extra = pw.env()
	pw.start()
	holdThat(t, "starting the server re-probed an unchanged harness", func() bool { return pw.headlessRuns() == 1 })
	if s := pw.storeText(); !strings.Contains(s, `"version": "2.1.300 (fake)"`) || !strings.Contains(s, "autopilot.guard_hook") {
		t.Errorf("the status did not survive the restart:\n%s", s)
	}
}

func TestProbe_ANewHarnessVersionReprobesAtServerStartInTheBackground(t *testing.T) {
	t.Parallel()
	pw := newProbeWorld(t)
	pw.setup()
	if pw.headlessRuns() != 1 {
		t.Fatalf("setup did not probe: headless runs = %d, want 1", pw.headlessRuns())
	}
	pw.writeRaw(pw.versionFile, "2.1.400 (fake)", 0o644)
	pw.extra = pw.env()
	pw.start()
	end := time.Now().Add(15 * time.Second)
	for pw.headlessRuns() < 2 && time.Now().Before(end) {
		time.Sleep(100 * time.Millisecond)
	}
	if pw.headlessRuns() != 2 {
		t.Fatalf("headless runs = %d after the version moved, want a second probe", pw.headlessRuns())
	}
	for end := time.Now().Add(10 * time.Second); time.Now().Before(end) && !strings.Contains(pw.storeText(), "2.1.400"); {
		time.Sleep(100 * time.Millisecond)
	}
	if s := pw.storeText(); !strings.Contains(s, `"2.1.400 (fake)": {`) {
		t.Errorf("the new result is not stored under 2.1.400:\n%s", s)
	}
}

func TestProbe_ServerStartRecordsTheVersionOfAHarnessNeverProbedWithoutRunningIt(t *testing.T) {
	t.Parallel()
	pw := newProbeWorld(t)
	pw.extra = pw.env()
	pw.start()
	end := time.Now().Add(10 * time.Second)
	for time.Now().Before(end) && !strings.Contains(pw.storeText(), "2.1.300") {
		time.Sleep(100 * time.Millisecond)
	}
	if !strings.Contains(pw.storeText(), "2.1.300") {
		t.Fatalf("server start did not record the version:\n%s", pw.storeText())
	}
	holdThat(t, "a harness never probed was probed at server start", func() bool { return pw.headlessRuns() == 0 })
	if strings.Contains(pw.storeText(), "autopilot.guard_hook") {
		t.Errorf("a guard result appeared without a probe:\n%s", pw.storeText())
	}
}

func TestProbe_AFailedVersionCheckAtServerStartClearsTheRecordedVersion(t *testing.T) {
	t.Parallel()
	pw := newProbeWorld(t)
	pw.writeRaw(filepath.Join(pw.home, "capabilities.json"), `{"fleets":{"work":{"claude":{"version":"2.1.300 (fake)","versions":{"2.1.300 (fake)":{"autopilot.guard_hook":{"passed":true}}}}}}}`, 0o600)
	pw.writeRaw(pw.versionFile, "FAIL", 0o644)
	pw.extra = pw.env()
	pw.start()
	end := time.Now().Add(10 * time.Second)
	has := func() bool {
		return strings.Contains(strings.ReplaceAll(pw.storeText(), `": "`, `":"`), `"version":"2.1.300`)
	}
	for time.Now().Before(end) && has() {
		time.Sleep(100 * time.Millisecond)
	}
	if s := pw.storeText(); has() {
		t.Errorf("the recorded version survived a failed --version, so the old result still counts:\n%s", s)
	}
}

func TestProbe_TheProbeCannotTouchAShipOrThePaneItRunsIn(t *testing.T) {
	t.Parallel()
	pw := newProbeWorld(t)
	other := filepath.Join(pw.root, "other.sock")
	if out, err := execTmux(other, "new-session", "-d", "-s", "x", "-n", "y", "sleep 600"); err != nil {
		t.Skipf("no private tmux: %v %s", err, out)
	}
	t.Cleanup(func() { _, _ = execTmux(other, "kill-server") })
	pane, _ := execTmux(other, "display", "-p", "-t", "x", "#{pane_id}")
	before, _ := execTmux(other, "display", "-p", "-t", pane, "#{@admiral-state}")
	r := pw.admiralWith(cliOpts{env: pw.env("ADMIRAL_SHIP=ghost", "ADMIRAL_REPORT="+filepath.Join(pw.root, "ghost.json"),
		"ADMIRAL_INBOX="+filepath.Join(pw.root, "inbox"), "TMUX="+other+",1,0", "TMUX_PANE="+pane)}, "setup", "--answers", pw.file, "--yes")
	if !strings.Contains(lineFor(r.stdout, "Pre-tool guard hook"), "passed") {
		t.Fatalf("the probe did not pass from inside a ship's env:\n%s", r.stdout)
	}
	if m, _ := filepath.Glob(filepath.Join(pw.home, "outcomes", "*")); len(m) != 0 {
		t.Errorf("the probe recorded outcomes %v", m)
	}
	if _, err := os.Stat(filepath.Join(pw.root, "ghost.json")); err == nil {
		t.Error("the probe wrote the ship's report")
	}
	if after, _ := execTmux(other, "display", "-p", "-t", pane, "#{@admiral-state}"); after != before {
		t.Errorf("the probe changed the pane's state from %q to %q", before, after)
	}
}

func execTmux(sock string, args ...string) (string, error) {
	out, err := exec.Command("tmux", append([]string{"-S", sock}, args...)...).CombinedOutput()
	return strings.TrimRight(string(out), "\n"), err
}
