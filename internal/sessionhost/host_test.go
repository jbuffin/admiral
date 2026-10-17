package sessionhost

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

// The events u5 depends on, from a real tmux on a private socket: every bell
// (not just the first), a pane's exit status, and a pane killed by a signal.
func TestBellsAndPaneDeath(t *testing.T) {
	tmux, err := exec.LookPath("tmux")
	if err != nil {
		t.Fatal("tmux is required:", err)
	}
	dir, err := os.MkdirTemp("/tmp", "sh")
	if err != nil {
		t.Fatal(err)
	}
	sock := filepath.Join(dir, "tmux.sock")
	t.Cleanup(func() {
		_ = exec.Command(tmux, "-S", sock, "kill-server").Run()
		_ = os.RemoveAll(dir)
	})
	t.Setenv("TMUX_TMPDIR", dir)
	run := func(args ...string) {
		t.Helper()
		if out, err := exec.Command(tmux, append([]string{"-S", sock}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("tmux %v: %v\n%s", args, err, out)
		}
	}

	h := New(tmux, sock)
	events, cancel := h.Subscribe()
	defer cancel()
	ctx, stop := context.WithCancel(context.Background())
	defer stop()
	go h.Run(ctx)
	select {
	case <-h.Ready():
	case <-time.After(10 * time.Second):
		t.Fatal("the host never read the sessions")
	}

	run("new-session", "-d", "-s", "bell", "sh", "-c", `sleep 2; printf '\a'; sleep 1; printf '\a'; sleep 1; exit 3`)
	run("set-option", "-p", "-t", "bell:", "remain-on-exit", "on")
	run("new-session", "-d", "-s", "killed", "sh", "-c", "sleep 3; kill -KILL $$")
	run("set-option", "-p", "-t", "killed:", "remain-on-exit", "on")
	run("set-option", "-t", "bell:", "@admiral-name", "Bell")

	bells, died, signalled := 0, false, false
	deadline := time.After(10 * time.Second)
	for !(bells == 2 && died && signalled) {
		select {
		case ev := <-events:
			switch {
			case ev.Kind == Bell && ev.Session == "bell":
				bells++
			case ev.Kind == PaneDied && ev.Session == "bell" && ev.Status == 3 && ev.Signal == "":
				died = true
			case ev.Kind == PaneDied && ev.Session == "killed":
				signalled = ev.Signal == "kill" && ev.Status == 0
			}
		case <-deadline:
			t.Fatalf("within 10s: bells = %d (want 2), exit status seen = %v, signal seen = %v", bells, died, signalled)
		}
	}
}

// A command that gives up (here: a context already cancelled) must not leave
// its reply behind for the next command to read as its own.
func TestCancelledCommandDoesNotShiftReplies(t *testing.T) {
	tmux, err := exec.LookPath("tmux")
	if err != nil {
		t.Fatal("tmux is required:", err)
	}
	dir, err := os.MkdirTemp("/tmp", "sh")
	if err != nil {
		t.Fatal(err)
	}
	sock := filepath.Join(dir, "tmux.sock")
	t.Setenv("TMUX_TMPDIR", dir)
	t.Cleanup(func() {
		_ = exec.Command(tmux, "-S", sock, "kill-server").Run()
		_ = os.RemoveAll(dir)
	})
	h := New(tmux, sock)
	ctx, stop := context.WithCancel(context.Background())
	defer stop()
	go h.Run(ctx)
	select {
	case <-h.Ready():
	case <-time.After(10 * time.Second):
		t.Fatal("the host never read the sessions")
	}
	for i := 0; i < 5; i++ {
		dead, cancel := context.WithCancel(context.Background())
		cancel()
		if _, err := h.command(dead, "display-message -p first"); err == nil {
			t.Fatal("a cancelled command should report the cancellation")
		}
		got, err := h.command(ctx, "display-message -p second")
		if err != nil || len(got) != 1 || got[0] != "second" {
			t.Fatalf("round %d: the next command got %q (%v), want its own reply [second]", i, got, err)
		}
	}
}
