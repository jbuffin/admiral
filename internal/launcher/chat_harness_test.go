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

// A chat whose harness is offered but not Claude is refused at launch until that harness has its own launch.

func codexChatConfig(t *testing.T) (*config.Config, string) {
	t.Helper()
	root := t.TempDir()
	exe := filepath.Join(root, "codex")
	if err := os.WriteFile(exe, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	raw := `{"version":1,"fleets":{"work":{"name":"Work","search_roots":["` + root + `"],
		"claude":{},"codex":{"executable":"` + exe + `"},"chat":{"harness":"codex","model":"gpt-6.1-sol"}}}}`
	cfg, err := config.Parse([]byte(raw))
	if err != nil {
		t.Fatal(err)
	}
	cfg.ChatProbe = func(fleet, harness, capability string) (string, string) { return "passed", "" }
	return cfg, root
}

func TestChatDockAcceptsAnOfferedHarnessButStartChatRefusesAnyButClaude(t *testing.T) {
	cfg, root := codexChatConfig(t)
	if _, dock, err := ChatDock(cfg, "work"); err != nil || dock.Name != "codex" {
		t.Fatalf("ChatDock with both codex probes passed = %+v, %v; want the codex dock", dock, err)
	}

	// Tmux is only checked for being set: the refusal comes first.
	l := &Launcher{Tmux: "/usr/bin/false", Home: filepath.Join(root, "home"), Config: func() *config.Config { return cfg }}
	err := l.StartChat("work")
	var rerr *Error
	if !errors.As(err, &rerr) {
		t.Fatalf("StartChat = %v, want a refusal", err)
	}
	if rerr.Status != http.StatusNotImplemented || !strings.Contains(rerr.Msg, "Codex") {
		t.Errorf("StartChat refusal = %d %q, want 501 naming Codex", rerr.Status, rerr.Msg)
	}
	if _, statErr := os.Stat(ChatDir(l.Home, "work")); !os.IsNotExist(statErr) {
		t.Errorf("a refused start created the chat directory: %v", statErr)
	}
}
