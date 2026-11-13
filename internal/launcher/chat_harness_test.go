package launcher

import (
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jbuffin/admiral/internal/config"
)

// A chat on a harness that is offered but has no launch of its own is refused at launch, creating nothing.

const unlaunchableHarness = "gemini"

func TestStartChatRefusesAnOfferedHarnessWithNoLaunchOfItsOwn(t *testing.T) {
	root := t.TempDir()
	exe := filepath.Join(root, unlaunchableHarness)
	if err := os.WriteFile(exe, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	cfg := &config.Config{Fleets: []config.Fleet{{Key: "work", Name: "Work", SearchRoots: []string{root},
		Chat:      config.ChatSettings{Harness: unlaunchableHarness},
		Harnesses: []config.Harness{{Name: unlaunchableHarness, Executable: exe, Found: true}}}}}
	cfg.ChatProbe = func(fleet, harness, capability string) (string, string) { return "passed", "" }
	if _, dock, err := ChatDock(cfg, "work"); err != nil || dock.Name != unlaunchableHarness {
		t.Fatalf("ChatDock = %+v, %v; want the %s dock", dock, err, unlaunchableHarness)
	}

	// Tmux is only checked for being set: the refusal comes first.
	l := &Launcher{Tmux: "/usr/bin/false", Home: filepath.Join(root, "home"), Config: func() *config.Config { return cfg }}
	err := l.StartChat("work")
	var rerr *Error
	if !errors.As(err, &rerr) {
		t.Fatalf("StartChat = %v, want a refusal", err)
	}
	if rerr.Status != http.StatusNotImplemented || !strings.Contains(rerr.Msg, unlaunchableHarness) {
		t.Errorf("StartChat refusal = %d %q, want 501 naming %s", rerr.Status, rerr.Msg, unlaunchableHarness)
	}
	if _, statErr := os.Stat(ChatDir(l.Home, "work")); !os.IsNotExist(statErr) {
		t.Errorf("a refused start created the chat directory: %v", statErr)
	}
}
