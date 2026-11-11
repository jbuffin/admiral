package launcher

import (
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/jbuffin/admiral/internal/config"
)

// recordingLauncher launches into a real tmux whose harness writes its argv, one argument per
// NUL-ended entry, to the returned function's result.
func recordingLauncher(t *testing.T, harness string) (*Launcher, string, func() []string) {
	t.Helper()
	if testing.Short() {
		t.Skip("needs tmux: skipped under -short")
	}
	tmux, err := exec.LookPath("tmux")
	if err != nil {
		t.Fatal("tmux is required:", err)
	}
	// A unix socket path is limited to about 100 bytes.
	tmp, err := os.MkdirTemp("", "ad")
	if err != nil {
		t.Fatal(err)
	}
	sock := filepath.Join(tmp, "t.sock")
	t.Cleanup(func() {
		exec.Command(tmux, "-S", sock, "kill-server").Run()
		os.RemoveAll(tmp)
	})
	out := filepath.Join(tmp, "argv")
	exe := filepath.Join(tmp, "harness")
	script := "#!/bin/sh\nprintf '%s\\0' \"$@\" > " + out + ".part\nmv " + out + ".part " + out + "\nsleep 30\n"
	if err := os.WriteFile(exe, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	work := t.TempDir()
	cfg := &config.Config{Fleets: []config.Fleet{{Key: "work", Name: "Work", SearchRoots: []string{work},
		Harnesses: []config.Harness{{Name: harness, Executable: exe, Found: true}}}}}
	l := &Launcher{Config: func() *config.Config { return cfg }, Home: t.TempDir(), Tmux: tmux, Socket: sock}
	return l, work, func() []string {
		t.Helper()
		deadline := time.Now().Add(10 * time.Second)
		for {
			if b, err := os.ReadFile(out); err == nil {
				return strings.Split(strings.TrimSuffix(string(b), "\x00"), "\x00")
			}
			if time.Now().After(deadline) {
				t.Fatal("the harness never started")
			}
			time.Sleep(50 * time.Millisecond)
		}
	}
}

func flagValue(argv []string, flag string) (string, bool) {
	i := slices.Index(argv, flag)
	if i < 0 || i+1 >= len(argv) {
		return "", false
	}
	return argv[i+1], true
}

func TestLaunchModelAndEffortReachTheHarnessFlags(t *testing.T) {
	l, work, argv := recordingLauncher(t, "claude")
	if _, err := l.Launch(Request{Fleet: "work", Harness: "claude", Dir: work, Skill: Skill{Kind: "none"}, Model: "opus", Effort: "high"}); err != nil {
		t.Fatal(err)
	}
	got := argv()
	if v, _ := flagValue(got, "--model"); v != "opus" {
		t.Errorf("--model = %q in %q, want opus", v, got)
	}
	if v, _ := flagValue(got, "--effort"); v != "high" {
		t.Errorf("--effort = %q in %q, want high", v, got)
	}
}

func TestLaunchWithNoModelOrEffortLeavesTheHarnessDefaults(t *testing.T) {
	l, work, argv := recordingLauncher(t, "claude")
	if _, err := l.Launch(Request{Fleet: "work", Harness: "claude", Dir: work, Skill: Skill{Kind: "none"}}); err != nil {
		t.Fatal(err)
	}
	got := argv()
	for _, f := range []string{"--model", "--effort"} {
		if slices.Contains(got, f) {
			t.Errorf("argv %q carries %s, want the harness's own default", got, f)
		}
	}
}

func TestLaunchCodexEffortIsAConfigOverride(t *testing.T) {
	l, work, argv := recordingLauncher(t, "codex")
	if _, err := l.Launch(Request{Fleet: "work", Harness: "codex", Dir: work, Skill: Skill{Kind: "none"}, Effort: "medium"}); err != nil {
		t.Fatal(err)
	}
	got := argv()
	found := false
	for i, a := range got {
		if a == "-c" && i+1 < len(got) && strings.HasPrefix(got[i+1], "model_reasoning_effort=") && strings.Contains(got[i+1], "medium") {
			found = true
		}
	}
	if !found {
		t.Errorf("argv %q has no -c model_reasoning_effort=medium", got)
	}
}

func TestLaunchRefusesAModelOrEffortTheHarnessDoesNotOffer(t *testing.T) {
	work := t.TempDir()
	cfg := &config.Config{Fleets: []config.Fleet{{Key: "work", Name: "Work", SearchRoots: []string{work}, Harnesses: []config.Harness{
		{Name: "claude", Executable: "/bin/sh", Found: true},
		{Name: "codex", Executable: "/bin/sh", Found: true},
		{Name: "copilot", Executable: "/bin/sh", Found: true},
	}}}}
	l := &Launcher{Config: func() *config.Config { return cfg }, Home: t.TempDir(), Tmux: "tmux", Socket: filepath.Join(t.TempDir(), "unused.sock")}
	for _, c := range []struct {
		name, harness, model, effort, msg string
	}{
		{"an unknown model", "claude", "not-a-model", "", "model"},
		{"an unknown effort", "claude", "", "extreme", "effort"},
		{"max on codex, which stops at high", "codex", "", "max", "effort"},
		{"any effort on copilot, which has none", "copilot", "", "low", "effort"},
		{"a model that would read as a flag", "claude", "--dangerously-skip-permissions", "", "model"},
	} {
		_, err := l.Launch(Request{Fleet: "work", Harness: c.harness, Dir: work, Skill: Skill{Kind: "none"}, Model: c.model, Effort: c.effort})
		if le, ok := err.(*Error); !ok || le.Status != http.StatusBadRequest || !strings.Contains(le.Msg, c.msg) {
			t.Errorf("%s: Launch = %v, want a 400 naming %q", c.name, err, c.msg)
		}
	}
}

func TestFreeformLaunchMayNameASkill(t *testing.T) {
	l, work, argv := recordingLauncher(t, "claude")
	if _, err := l.Launch(Request{Fleet: "work", Harness: "claude", Dir: work, Skill: Skill{Kind: "skill", Name: "/review"},
		Text: "  look at the parser \n", Note: "dropped"}); err != nil {
		t.Fatal(err)
	}
	got := argv()
	if n := len(got); n < 2 || got[n-2] != "--" || got[n-1] != "/review look at the parser" {
		t.Errorf("argv %q should end with -- and the prompt \"/review look at the parser\"", got)
	}
}
