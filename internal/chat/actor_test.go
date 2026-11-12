package chat

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/jbuffin/admiral/internal/actor"
	"github.com/jbuffin/admiral/internal/config"
	"github.com/jbuffin/admiral/internal/launcher"
	"github.com/jbuffin/admiral/internal/sessionhost"
)

func chatRig(t *testing.T) (*launcher.Launcher, string, string) {
	t.Helper()
	if testing.Short() {
		t.Skip("needs tmux: skipped under -short")
	}
	tmux, err := exec.LookPath("tmux")
	if err != nil {
		t.Fatal("tmux is required:", err)
	}
	root, err := os.MkdirTemp("/tmp", "adm-chat")
	if err != nil {
		t.Fatal(err)
	}
	home := filepath.Join(root, "home")
	if err := os.MkdirAll(home, 0o700); err != nil {
		t.Fatal(err)
	}
	exe := filepath.Join(root, "claude")
	if err := os.WriteFile(exe, []byte("#!/bin/sh\nexec sleep 60\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(map[string]any{"version": 1, "fleets": map[string]any{"work": map[string]any{
		"name": "Work", "search_roots": []string{root},
		"claude": map[string]any{"executable": exe},
		"chat":   map[string]any{"model": "opus", "effort": "high"}}}})
	cfg, err := config.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	sock := filepath.Join(root, "t.sock")
	t.Cleanup(func() {
		_ = exec.Command(tmux, "-S", sock, "kill-server").Run()
		_ = os.RemoveAll(root)
	})
	return &launcher.Launcher{Tmux: tmux, Socket: sock, Home: home, Config: func() *config.Config { return cfg }}, home, sock
}

func serviceFor(t *testing.T, l *launcher.Launcher, home string) *Service {
	t.Helper()
	m, err := actor.NewMinter(home)
	if err != nil {
		t.Fatal(err)
	}
	return &Service{Actor: m, Tmux: l.Tmux, Socket: l.Socket, Home: home}
}

func TestTheChatsActorTokenSurvivesARestartAndChangesWithANewChatSession(t *testing.T) {
	l, home, sock := chatRig(t)
	if err := l.StartChat("work"); err != nil {
		t.Fatal(err)
	}
	first := serviceFor(t, l, home).ActorToken("work")
	if first == "" {
		t.Fatal("a running chat has no token")
	}
	if again := serviceFor(t, l, home).ActorToken("work"); again != first {
		t.Errorf("after an Admiral restart the token is %q, want the same as before", again)
	}
	if got := serviceFor(t, l, home).ActorToken("other"); got != "" {
		t.Errorf("a fleet with no chat has token %q, want none", got)
	}
	srv := sessionhost.NewServer(l.Tmux, sock)
	if err := srv.KillSession(context.Background(), sessionhost.ChatSession("work")); err != nil {
		t.Fatal(err)
	}
	if gone := serviceFor(t, l, home).ActorToken("work"); gone != "" {
		t.Errorf("an ended chat still has token %q", gone)
	}
	if err := l.StartChat("work"); err != nil {
		t.Fatal(err)
	}
	if next := serviceFor(t, l, home).ActorToken("work"); next == "" || next == first {
		t.Errorf("a new chat session has token %q, want a new one unlike %q", next, first)
	}
}
