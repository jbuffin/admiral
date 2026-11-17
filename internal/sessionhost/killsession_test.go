package sessionhost

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestKillSessionCountsASessionThatIsAlreadyGoneAsDone(t *testing.T) {
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

	if err := s.KillSession(ctx, "ghost"); err != nil {
		t.Fatalf("no server running: %v, want nil", err)
	}
	for _, name := range []string{"keep", "gone"} {
		if err := s.StartSession(SessionSpec{Name: name, Dir: tmp, Argv: []string{"sleep", "60"}}); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.KillSession(ctx, "gone"); err != nil {
		t.Fatal(err)
	}
	if err := s.KillSession(ctx, "gone"); err != nil {
		t.Fatalf("a session already killed: %v, want nil", err)
	}
	if !s.HasSession(ctx, "keep") {
		t.Fatal("killing a gone session must leave the others running")
	}
}
