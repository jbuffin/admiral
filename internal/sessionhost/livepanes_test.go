package sessionhost

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func TestReadLivePanesFindsShipAndChatPanesButNotCrewBoats(t *testing.T) {
	tmux := needTmux(t)
	tmp, err := os.MkdirTemp("/tmp", "sh")
	if err != nil {
		t.Fatal(err)
	}
	sock := filepath.Join(tmp, "tmux.sock")
	t.Setenv("TMUX_TMPDIR", tmp)
	t.Cleanup(func() {
		_ = exec.Command(tmux, "-S", sock, "kill-server").Run()
		_ = os.RemoveAll(tmp)
	})
	s := NewServer(tmux, sock)
	ctx := context.Background()
	if got, err := s.ReadLivePanes(ctx); err != nil || len(got) != 0 {
		t.Fatalf("no server: got %v, %v, want nothing", got, err)
	}
	line := `{"time":"2026-11-12T10:00:00Z","session_id":"s1"}`
	for _, name := range []string{"a-1", ChatName, "quiet"} {
		spec := SessionSpec{Name: name, Dir: tmp, Argv: []string{"sleep", "60"}, Anchor: true}
		if name != "quiet" {
			spec.PaneOptions = [][2]string{{"statusline", line}, {"harness", "claude"}}
		}
		if err := s.StartSession(spec); err != nil {
			t.Fatal(err)
		}
	}
	// A crew boat's pane: same session as a ship, named window.
	if _, err := s.run(call{args: []string{"new-window", "-d", "-t", "=a-1:", "-n", "boat", "sleep", "60"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.run(call{args: []string{"set-option", "-p", "-t", "=a-1:boat", StatuslineOption, line}}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.run(call{args: []string{"set-option", "-w", "-t", "=a-1:boat", Prefix + "crew-name", "scout"}}); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		got, err := s.ReadLivePanes(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if len(got) == 2 {
			byName := map[string]LivePane{}
			for _, p := range got {
				byName[p.Session] = p
			}
			if byName["a-1"].Statusline != line || byName[ChatName].Statusline != line || byName["a-1"].Harness != "claude" {
				t.Fatalf("panes = %+v", got)
			}
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("panes = %+v, want the ship's and the chat's only", got)
		}
		time.Sleep(50 * time.Millisecond)
	}
}
