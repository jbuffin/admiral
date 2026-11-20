package harnessmodels_test

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/jbuffin/admiral/internal/config"
	"github.com/jbuffin/admiral/internal/harnessmodels"
)

// Listing one dock's models by running its executable and speaking the harness's own protocol.

func dock(t *testing.T, harness string, env map[string]string) (config.Harness, string) {
	t.Helper()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	rec := t.TempDir()
	e := map[string]string{"ADMIRAL_FAKE_LISTER": "1", "FAKE_RECORD_DIR": rec}
	for k, v := range env {
		e[k] = v
	}
	return config.Harness{Name: harness, Executable: exe, Found: true, Env: e}, rec
}

func read(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("the fake harness never started: %v", err)
	}
	return string(b)
}

func gone(t *testing.T, rec string) {
	t.Helper()
	pid, err := strconv.Atoi(read(t, filepath.Join(rec, "pid")))
	if err != nil {
		t.Fatal(err)
	}
	if err := syscall.Kill(pid, 0); err == nil {
		t.Errorf("the harness process %d is still running after the listing returned", pid)
		_ = syscall.Kill(pid, syscall.SIGKILL)
	}
}

func TestListClaudeAsksForItsModelsAndDropsTheDefaultEntry(t *testing.T) {
	h, rec := dock(t, "claude", map[string]string{"FAKE_MODELS": "opus,fable,sonnet,haiku,claude-fable-5-1"})
	start := time.Now()
	got, err := harnessmodels.List(context.Background(), h, 20*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"opus", "fable", "sonnet", "haiku", "claude-fable-5-1"}; !slices.Equal(got, want) {
		t.Errorf("models = %v, want %v (the harness's order, no default entry)", got, want)
	}
	if took := time.Since(start); took > 8*time.Second {
		t.Errorf("List took %s: it waited for the harness to exit instead of killing it once answered", took)
	}
	if argv := read(t, filepath.Join(rec, "argv")); argv != "-p --input-format stream-json --output-format stream-json --verbose" {
		t.Errorf("claude ran with %q, want the headless stream-json flags only", argv)
	}
	gone(t, rec)
}

func TestListCodexFollowsNextCursorAndLeavesOutHiddenModels(t *testing.T) {
	h, rec := dock(t, "codex", map[string]string{"FAKE_MODELS": "gpt-6.2-sol,gpt-6.1-sol,gpt-6-luna"})
	got, err := harnessmodels.List(context.Background(), h, 20*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"gpt-6.2-sol", "gpt-6.1-sol", "gpt-6-luna"}; !slices.Equal(got, want) {
		t.Errorf("models = %v, want both pages and no hidden entry %v", got, want)
	}
	if argv := read(t, filepath.Join(rec, "argv")); argv != "app-server" {
		t.Errorf("codex ran with %q, want app-server alone", argv)
	}
	gone(t, rec)
}

func TestListCopilotAsksSessionNewAndKeepsAutoOnce(t *testing.T) {
	h, rec := dock(t, "copilot", map[string]string{"FAKE_MODELS": "claude-opus-5.5,gpt-6.2-sol,gemini-4-carbon,grok-4.7"})
	got, err := harnessmodels.List(context.Background(), h, 20*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"auto", "claude-opus-5.5", "gpt-6.2-sol", "gemini-4-carbon", "grok-4.7"}; !slices.Equal(got, want) {
		t.Errorf("models = %v, want %v (auto listed twice by the harness, once here)", got, want)
	}
	if argv := read(t, filepath.Join(rec, "argv")); argv != "--acp" {
		t.Errorf("copilot ran with %q, want --acp alone", argv)
	}
	gone(t, rec)
}

func TestListDropsNamesThatFailTheModelNamePattern(t *testing.T) {
	h, _ := dock(t, "claude", map[string]string{"FAKE_MODELS": "sonnet,-x,a b,opus[1m],opus[a-b],$(x),opus[1m]x," + strings.Repeat("a", 129)})
	got, err := harnessmodels.List(context.Background(), h, 20*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"sonnet", "opus[1m]"}; !slices.Equal(got, want) {
		t.Errorf("models = %v, want %v", got, want)
	}
}

func TestListRunsTheExecutableWithTheDocksEnvAndNoneOfItsArgs(t *testing.T) {
	h, rec := dock(t, "claude", map[string]string{"FAKE_MODELS": "sonnet"})
	h.Args = []string{"--dangerously-skip-permissions", "--add-dir", "/x"}
	if _, err := harnessmodels.List(context.Background(), h, 20*time.Second); err != nil {
		t.Fatal(err)
	}
	if argv := read(t, filepath.Join(rec, "argv")); strings.Contains(argv, "dangerously") || strings.Contains(argv, "--add-dir") {
		t.Errorf("argv %q carries the dock's launch args, which can break app-server and --acp", argv)
	}
}

func TestListRunsInAnEmptyDirectoryOfItsOwnWithoutTheShipVariables(t *testing.T) {
	t.Setenv("ADMIRAL_PORT", "1234")
	here, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for _, harness := range []string{"claude", "codex", "copilot"} {
		h, rec := dock(t, harness, map[string]string{"FAKE_MODELS": "sonnet", "ADMIRAL_SHIP": "work-1", "CLAUDECODE": "1"})
		if _, err := harnessmodels.List(context.Background(), h, 20*time.Second); err != nil {
			t.Fatal(err)
		}
		cwd := read(t, filepath.Join(rec, "cwd"))
		if cwd == here || cwd == "" {
			t.Errorf("%s ran in %q, want a directory of its own, not the server's", harness, cwd)
		}
		if _, err := os.Stat(cwd); err == nil {
			t.Errorf("%s's directory %s is still there after the listing", harness, cwd)
		}
		for _, line := range strings.Split(read(t, filepath.Join(rec, "env")), "\n") {
			for _, v := range []string{"ADMIRAL_SHIP=", "ADMIRAL_PORT=", "CLAUDECODE="} {
				if strings.HasPrefix(line, v) {
					t.Errorf("%s saw %s, which ties the listing to a ship or breaks claude's start", harness, line)
				}
			}
		}
	}
}

func TestListFailsWhenTheHarnessFailsHangsOrPrintsGarbage(t *testing.T) {
	for _, c := range []struct {
		mode    string
		timeout time.Duration
	}{{"fail", 20 * time.Second}, {"hang", 400 * time.Millisecond}, {"garbage", 400 * time.Millisecond}} {
		for _, harness := range []string{"claude", "codex", "copilot"} {
			t.Run(c.mode+"/"+harness, func(t *testing.T) {
				h, rec := dock(t, harness, map[string]string{"FAKE_MODELS": "a,b", "FAKE_MODE": c.mode})
				start := time.Now()
				got, err := harnessmodels.List(context.Background(), h, c.timeout)
				if err == nil {
					t.Errorf("List = %v, want an error", got)
				}
				if len(got) != 0 {
					t.Errorf("models = %v on an error, want none", got)
				}
				if took := time.Since(start); took > 10*time.Second {
					t.Errorf("List took %s with a %s timeout", took, c.timeout)
				}
				if c.mode == "fail" && err != nil && !strings.Contains(err.Error(), "boom") {
					t.Errorf("err = %v, want the harness's stderr in it", err)
				}
				gone(t, rec)
			})
		}
	}
}

func TestListWithAMissingExecutableIsAnError(t *testing.T) {
	h := config.Harness{Name: "claude", Executable: filepath.Join(t.TempDir(), "nope"), Found: false}
	if got, err := harnessmodels.List(context.Background(), h, time.Second); err == nil {
		t.Errorf("List = %v, want an error for an executable that is not there", got)
	}
}
