package sessionhost

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// A ship's directory and arguments reach the harness as written, though tmux
// format-expands -c and reads a trailing ; as a command separator.
func TestStartSessionPassesDirAndArgsLiterally(t *testing.T) {
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
	dir := filepath.Join(tmp, "fmt#{pane_id}x")
	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(tmp, "out")
	args := []string{`end;`, `end\;`, `;`}
	script := `pwd > "$0.tmp"; for a in "$@"; do printf '%s\n' "$a"; done >> "$0.tmp"; mv "$0.tmp" "$0"`
	s := NewServer(tmux, sock)
	if err := s.StartSession(SessionSpec{Name: "ship", Dir: dir, Argv: append([]string{"/bin/sh", "-c", script, out}, args...), Anchor: true}); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(10 * time.Second)
	var got []byte
	for got, err = os.ReadFile(out); err != nil; got, err = os.ReadFile(out) {
		if time.Now().After(deadline) {
			t.Fatal("the harness never ran")
		}
		time.Sleep(20 * time.Millisecond)
	}
	if want := strings.Join(append([]string{dir}, args...), "\n") + "\n"; string(got) != want {
		t.Errorf("the harness saw\n%s\nwant\n%s", got, want)
	}

	var limit *LimitError
	err = s.StartSession(SessionSpec{Name: "huge", Dir: dir, Env: map[string]string{"HUGE": strings.Repeat("x", MaxMessage)}, Argv: []string{"true"}})
	if !errors.As(err, &limit) {
		t.Errorf("an environment value over tmux's message size = %v, want a LimitError", err)
	}
	if rows, _ := s.SessionRows(); len(rows) != 1 {
		t.Errorf("sessions after the refusal = %v, want only the first ship", rows)
	}
}
