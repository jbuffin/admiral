package capabilities

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/jbuffin/admiral/internal/config"
)

func fakeHarness(t *testing.T, script string, env map[string]string) config.Fleet {
	t.Helper()
	path := filepath.Join(t.TempDir(), "claude")
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"+script+"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	return config.Fleet{Key: "work", Harnesses: []config.Harness{
		{Name: "claude", Executable: path, Found: true, Env: env},
	}}
}

func TestDetectVersionReturnsTheTrimmedOutput(t *testing.T) {
	f := fakeHarness(t, `[ "$1" = "--version" ] && echo "  2.1.300 (Claude Code)  "`, nil)
	got, err := DetectVersion(context.Background(), f, "claude", 5*time.Second)
	if err != nil || got != "2.1.300 (Claude Code)" {
		t.Errorf("DetectVersion = %q, %v", got, err)
	}
}

func TestDetectVersionRunsUnderTheHarnessEnv(t *testing.T) {
	f := fakeHarness(t, `echo "$FAKE_CLAUDE_VERSION"`, map[string]string{"FAKE_CLAUDE_VERSION": "9.9.9"})
	got, err := DetectVersion(context.Background(), f, "claude", 5*time.Second)
	if err != nil || got != "9.9.9" {
		t.Errorf("DetectVersion = %q, %v", got, err)
	}
}

func TestDetectVersionNonzeroExitIsAnError(t *testing.T) {
	f := fakeHarness(t, `echo 2.1.300; exit 3`, nil)
	if got, err := DetectVersion(context.Background(), f, "claude", 5*time.Second); err == nil {
		t.Errorf("DetectVersion = %q, nil; want an error", got)
	}
}

func TestDetectVersionEmptyOutputIsAnError(t *testing.T) {
	f := fakeHarness(t, `true`, nil)
	if got, err := DetectVersion(context.Background(), f, "claude", 5*time.Second); err == nil {
		t.Errorf("DetectVersion = %q, nil; want an error, not an empty version", got)
	}
}

func TestDetectVersionMissingExecutableIsAnError(t *testing.T) {
	f := config.Fleet{Key: "work", Harnesses: []config.Harness{
		{Name: "claude", Executable: filepath.Join(t.TempDir(), "gone"), Found: true},
	}}
	if got, err := DetectVersion(context.Background(), f, "claude", 5*time.Second); err == nil {
		t.Errorf("DetectVersion = %q, nil; want an error", got)
	}
}

func TestDetectVersionHarnessNotInFleetIsAnError(t *testing.T) {
	f := fakeHarness(t, `echo 1.0`, nil)
	if got, err := DetectVersion(context.Background(), f, "codex", 5*time.Second); err == nil {
		t.Errorf("DetectVersion = %q, nil; want an error", got)
	}
}

func TestDetectVersionTimeoutIsAnError(t *testing.T) {
	f := fakeHarness(t, `exec sleep 30`, nil)
	start := time.Now()
	got, err := DetectVersion(context.Background(), f, "claude", 200*time.Millisecond)
	if err == nil {
		t.Errorf("DetectVersion = %q, nil; want a timeout error", got)
	}
	if d := time.Since(start); d > 5*time.Second {
		t.Errorf("took %v; the timeout did not stop the process", d)
	}
}

func TestDetectVersionTimeoutKillsDescendants(t *testing.T) {
	pidFile := filepath.Join(t.TempDir(), "child.pid")
	f := fakeHarness(t, `sleep 30 &
echo $! > `+pidFile+`
wait`, nil)
	if _, err := DetectVersion(context.Background(), f, "claude", 500*time.Millisecond); err == nil {
		t.Fatal("want a timeout error")
	}
	raw, err := os.ReadFile(pidFile)
	if err != nil {
		t.Fatal(err)
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(raw)))
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(3 * time.Second)
	for syscall.Kill(pid, 0) == nil {
		if time.Now().After(deadline) {
			_ = syscall.Kill(pid, syscall.SIGKILL)
			t.Fatalf("child %d outlived the timeout", pid)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func TestDetectVersionUsesTheFirstLine(t *testing.T) {
	f := fakeHarness(t, `printf '\n2.1.300 (Claude Code)\nUpdate available: 2.2.0\n'`, nil)
	got, err := DetectVersion(context.Background(), f, "claude", 5*time.Second)
	if err != nil || got != "2.1.300 (Claude Code)" {
		t.Errorf("DetectVersion = %q, %v", got, err)
	}
}

func TestDetectVersionErrorCarriesStderr(t *testing.T) {
	f := fakeHarness(t, `echo "boom: not logged in" >&2; exit 2`, nil)
	_, err := DetectVersion(context.Background(), f, "claude", 5*time.Second)
	if err == nil || !strings.Contains(err.Error(), "boom: not logged in") {
		t.Errorf("err = %v, want the stderr line", err)
	}
}
