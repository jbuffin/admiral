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

// A chat whose harness is offered but has no launch of its own yet is refused at launch: Copilot, until it gets one.

func copilotChatConfig(t *testing.T) (*config.Config, string) {
	t.Helper()
	root := t.TempDir()
	exe := filepath.Join(root, "copilot")
	if err := os.WriteFile(exe, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	raw := `{"version":1,"fleets":{"work":{"name":"Work","search_roots":["` + root + `"],
		"claude":{},"copilot":{"executable":"` + exe + `"},"chat":{"harness":"copilot","model":"gpt-5.6-sol"}}}}`
	cfg, err := config.Parse([]byte(raw))
	if err != nil {
		t.Fatal(err)
	}
	cfg.ChatProbe = func(fleet, harness, capability string) (string, string) { return "passed", "" }
	return cfg, root
}

func TestChatDockAcceptsAnOfferedHarnessButStartChatRefusesCopilot(t *testing.T) {
	cfg, root := copilotChatConfig(t)
	if _, dock, err := ChatDock(cfg, "work"); err != nil || dock.Name != "copilot" {
		t.Fatalf("ChatDock with both copilot probes passed = %+v, %v; want the copilot dock", dock, err)
	}

	// Tmux is only checked for being set: the refusal comes first.
	l := &Launcher{Tmux: "/usr/bin/false", Home: filepath.Join(root, "home"), Config: func() *config.Config { return cfg }}
	err := l.StartChat("work")
	var rerr *Error
	if !errors.As(err, &rerr) {
		t.Fatalf("StartChat = %v, want a refusal", err)
	}
	if rerr.Status != http.StatusNotImplemented || !strings.Contains(rerr.Msg, "Copilot") {
		t.Errorf("StartChat refusal = %d %q, want 501 naming Copilot", rerr.Status, rerr.Msg)
	}
	if _, statErr := os.Stat(ChatDir(l.Home, "work")); !os.IsNotExist(statErr) {
		t.Errorf("a refused start created the chat directory: %v", statErr)
	}
}
