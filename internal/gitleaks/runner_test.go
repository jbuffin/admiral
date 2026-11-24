package gitleaks_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/jbuffin/admiral/internal/gitleaks"
)

const decoy = "decoy-secret-0a1b2c3d"

type rig struct {
	t      *testing.T
	state  string // the fake's steering folder
	runner gitleaks.Runner
}

// newRig puts the fake gitleaks first on the runner's PATH; GITLEAKS_CONFIG and GITLEAKS_CONFIG_TOML are set so a leak shows.
func newRig(t *testing.T) *rig {
	t.Helper()
	fake, err := filepath.Abs(filepath.Join("..", "..", "e2e", "testdata", "fakegitleaks"))
	if err != nil {
		t.Fatal(err)
	}
	state := t.TempDir()
	return &rig{t: t, state: state, runner: gitleaks.Runner{Env: []string{
		"PATH=" + fake + ":/usr/bin:/bin", "FAKEGITLEAKS_DIR=" + state,
		"GITLEAKS_CONFIG=/repo/.gitleaks.toml", "GITLEAKS_CONFIG_TOML=allow-everything", "KEEP_ME=1",
	}}}
}

func (r *rig) set(name, content string) {
	r.t.Helper()
	if err := os.WriteFile(filepath.Join(r.state, name), []byte(content), 0o644); err != nil {
		r.t.Fatal(err)
	}
}

func (r *rig) last(name string) string {
	r.t.Helper()
	b, err := os.ReadFile(filepath.Join(r.state, "last", name))
	if err != nil {
		r.t.Fatalf("the scan recorded no %s: %v", name, err)
	}
	return string(b)
}

func (r *rig) scan(data string) gitleaks.Result {
	r.t.Helper()
	return r.runner.Scan(context.Background(), []byte(data))
}

func TestACleanFileIsClean(t *testing.T) {
	r := newRig(t)
	got := r.scan("name: bosun\nnothing here\n")
	if got.State != gitleaks.Clean || len(got.Findings) != 0 {
		t.Errorf("result = %+v, want clean with no findings", got)
	}
}

func TestAHitNamesTheRuleAndLineAndNeverTheValue(t *testing.T) {
	r := newRig(t)
	got := r.scan("one\ntwo\ntoken: " + decoy + "\n")
	if got.State != gitleaks.Hit || len(got.Findings) != 1 || got.Findings[0].Line != 3 || got.Findings[0].Rule != "decoy-secret" {
		t.Fatalf("result = %+v, want one hit on line 3 by decoy-secret", got)
	}
	raw, _ := json.Marshal(got)
	if strings.Contains(string(raw), decoy) || strings.Contains(string(raw), "REDACTED") {
		t.Errorf("the result carries report content: %s", raw)
	}
}

func TestGitleaksGetsTheFixedArgumentsTheBytesAndAScrubbedEnvironmentInAnEmptyDirectory(t *testing.T) {
	r := newRig(t)
	here, _ := os.Getwd()
	data := "name: bosun\n\ttabbed é\n"
	r.scan(data)

	argv := strings.Split(strings.TrimSuffix(r.last("argv"), "\x00"), "\x00")
	want := []string{"stdin", "--no-banner", "--redact", "--log-level", "error", "--report-format", "json", "--report-path", "-", "--exit-code", "3"}
	if strings.Join(argv, " ") != strings.Join(want, " ") {
		t.Errorf("argv = %q, want %q", argv, want)
	}
	if got := r.last("stdin"); got != data {
		t.Errorf("stdin = %q, want the exact bytes", got)
	}
	if env := r.last("env"); env != "" {
		t.Errorf("gitleaks saw %q, want neither GITLEAKS_CONFIG nor GITLEAKS_CONFIG_TOML", env)
	}
	cwd := strings.TrimSpace(r.last("cwd"))
	if cwd == "" || cwd == here || strings.HasPrefix(cwd, here) {
		t.Errorf("cwd = %q, want a temp directory apart from %q", cwd, here)
	}
	if entries := r.last("cwd-entries"); entries != "" {
		t.Errorf("the cwd held %q at the start, want it empty", entries)
	}
}

func TestEachScanRunsInAFreshDirectoryThatIsGoneAfterwards(t *testing.T) {
	r := newRig(t)
	r.scan("a\n")
	first := strings.TrimSpace(r.last("cwd"))
	r.scan("b\n")
	second := strings.TrimSpace(r.last("cwd"))
	if first == second {
		t.Errorf("both scans ran in %s", first)
	}
	if _, err := os.Stat(first); err == nil {
		t.Errorf("%s is still there", first)
	}
}

func TestTheVersionDecidesWhetherAScanRuns(t *testing.T) {
	for version, want := range map[string]string{
		"v8.19.0\n": gitleaks.Clean, "8.19.0\n": gitleaks.Clean, "v8.21.2": gitleaks.Clean, "9.0.0\n": gitleaks.Clean,
		"8.18.4\n": gitleaks.TooOld, "v8.18.0\n": gitleaks.TooOld, "7.6.1\n": gitleaks.TooOld,
		"banana\n": gitleaks.Failed, "\n": gitleaks.Failed, "8.19\n": gitleaks.Failed,
	} {
		r := newRig(t)
		r.set("version", version)
		got := r.scan("a\n")
		if got.State != want {
			t.Errorf("version %q: state = %q, want %q (%+v)", version, got.State, want, got)
		}
		if want == gitleaks.TooOld && (got.Version != strings.TrimPrefix(strings.TrimSpace(version), "v") || !strings.Contains(got.Install, "brew upgrade gitleaks")) {
			t.Errorf("version %q: result = %+v, want the found version and the upgrade command", version, got)
		}
		if want == gitleaks.TooOld {
			if _, err := os.Stat(filepath.Join(r.state, "last")); err == nil {
				t.Errorf("version %q: a scan ran on a too-old gitleaks", version)
			}
		}
	}
}

func TestAMissingGitleaksIsMissingWithTheInstallHint(t *testing.T) {
	r := newRig(t)
	r.runner.Env = []string{"PATH=" + t.TempDir()}
	got := r.scan("a\n")
	if got.State != gitleaks.Missing || !strings.Contains(got.Install, "brew install gitleaks") {
		t.Errorf("result = %+v, want missing with the install command", got)
	}
}

func TestACrashAnErrorExitAndUnparsableOutputEachFailTheScan(t *testing.T) {
	for fail, wantError := range map[string]string{"crash": "", "error": "fake gitleaks: boom", "garbage": "", "empty": ""} {
		r := newRig(t)
		r.set("fail", fail)
		got := r.scan("token: " + decoy + "\n")
		if got.State != gitleaks.Failed || len(got.Findings) != 0 {
			t.Errorf("%s: result = %+v, want failed with no findings", fail, got)
			continue
		}
		if got.Error == "" || (wantError != "" && got.Error != wantError) {
			t.Errorf("%s: error = %q, want %q (its first stderr line)", fail, got.Error, wantError)
		}
	}
}

func TestAScanThatRunsOutOfTimeFailsAndIsStopped(t *testing.T) {
	r := newRig(t)
	r.set("fail", "hang")
	r.runner.Timeout = 300 * time.Millisecond
	start := time.Now()
	got := r.scan("a\n")
	if took := time.Since(start); took > 5*time.Second {
		t.Errorf("the scan took %v, want it stopped near the timeout", took)
	}
	if got.State != gitleaks.Failed || !strings.Contains(got.Error, "timed out") {
		t.Errorf("result = %+v, want failed with a timeout error", got)
	}
}

func TestATimeoutKillsTheProcessesAWrapperForked(t *testing.T) {
	bin, pidFile := t.TempDir(), filepath.Join(t.TempDir(), "pid")
	script := "#!/bin/sh\n[ \"$1\" = version ] && echo 8.21.2 && exit 0\nsleep 600 &\necho $! > \"$PIDFILE\"\nwait\n"
	if err := os.WriteFile(filepath.Join(bin, "gitleaks"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	runner := gitleaks.Runner{Env: []string{"PATH=" + bin + ":/usr/bin:/bin", "PIDFILE=" + pidFile}, Timeout: 300 * time.Millisecond}
	if got := runner.Scan(context.Background(), []byte("a\n")); got.State != gitleaks.Failed {
		t.Fatalf("result = %+v, want failed", got)
	}
	raw, err := os.ReadFile(pidFile)
	if err != nil {
		t.Fatal(err)
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(raw)))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = syscall.Kill(pid, syscall.SIGKILL) })
	for deadline := time.Now().Add(3 * time.Second); time.Now().Before(deadline); time.Sleep(50 * time.Millisecond) {
		if syscall.Kill(pid, 0) != nil {
			return
		}
	}
	t.Errorf("the forked sleep (pid %d) outlived the timeout", pid)
}

func TestARelativePathEntryIsNotSearched(t *testing.T) {
	bin := t.TempDir()
	if err := os.WriteFile(filepath.Join(bin, "gitleaks"), []byte("#!/bin/sh\necho 8.21.2\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Chdir(bin)
	got := gitleaks.Runner{Env: []string{"PATH=.:"}}.Scan(context.Background(), []byte("a\n"))
	if got.State != gitleaks.Missing {
		t.Errorf("result = %+v, want missing: a relative PATH entry isn't the fleet's", got)
	}
}

func TestACancelledContextFailsWithoutRunningGitleaks(t *testing.T) {
	r := newRig(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	got := r.runner.Scan(ctx, []byte("a\n"))
	if got.State != gitleaks.Failed {
		t.Errorf("result = %+v, want failed", got)
	}
	if _, err := os.Stat(filepath.Join(r.state, "last")); err == nil {
		t.Errorf("a scan ran although the context was already cancelled")
	}
}
