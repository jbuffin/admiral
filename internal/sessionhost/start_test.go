package sessionhost

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
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

// tmux's server takes at most 1000 arguments in one message, and each variable is a command of its own.
func TestStartSessionCarriesAnEnvironmentOfManyShortVariables(t *testing.T) {
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
	env := map[string]string{}
	for i := range 400 {
		env[fmt.Sprintf("SHORT_%03d", i)] = "v" + strconv.Itoa(i)
	}
	out := filepath.Join(tmp, "out")
	script := `printf '%s %s\n' "$SHORT_000" "$SHORT_399" > "$0.tmp"; mv "$0.tmp" "$0"`
	if err := NewServer(tmux, sock).StartSession(SessionSpec{Name: "ship", Dir: tmp, Env: env, Argv: []string{"/bin/sh", "-c", script, out}, Anchor: true}); err != nil {
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
	if string(got) != "v0 v399\n" {
		t.Errorf("the harness saw %q, want the first and last variables", got)
	}
}

// macOS sometimes fails to open a pty for a new pane under load; a launch tries again rather than failing the ship.
func TestALaunchOutlastsTmuxFailingToOpenAPtyOnce(t *testing.T) {
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
	// The first respawn of each pane fails the way tmux reports macOS's ENXIO; every other call is the real tmux.
	flaky := filepath.Join(tmp, "flaky-tmux")
	script := `#!/bin/sh
case " $* " in
*" respawn-pane "*)
	mark="` + tmp + `/failed-$(printf '%s' "$*" | cksum | cut -d' ' -f1)"
	if [ ! -e "$mark" ]; then : > "$mark"; echo "respawn pane failed: fork failed: Device not configured" >&2; exit 1; fi;;
esac
exec ` + tmux + ` "$@"
`
	if err := os.WriteFile(flaky, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	s := NewServer(flaky, sock)
	ran := func(name string) string { return filepath.Join(tmp, name) }
	touch := func(name string) []string { return []string{"/bin/sh", "-c", `: > "$0"`, ran(name)} }
	if err := s.StartSession(SessionSpec{Name: "ship", Dir: tmp, Argv: touch("ship"), Anchor: true}); err != nil {
		t.Fatalf("StartSession: %v", err)
	}
	if err := s.StartCrew(CrewSpec{Session: "ship", Name: "scout", Harness: "claude", Dir: tmp, Command: touch("scout")}); err != nil {
		t.Fatalf("StartCrew: %v", err)
	}
	for _, name := range []string{"ship", "scout"} {
		deadline := time.Now().Add(10 * time.Second)
		for _, err := os.Stat(ran(name)); err != nil; _, err = os.Stat(ran(name)) {
			if time.Now().After(deadline) {
				t.Fatalf("the %s harness never ran", name)
			}
			time.Sleep(20 * time.Millisecond)
		}
	}
}

// tmuxRig is a real tmux on a private socket, reached through a wrapper script the test writes.
func tmuxRig(t *testing.T, wrapper func(tmux string) string) (Server, string) {
	t.Helper()
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
	bin := filepath.Join(tmp, "wrapped-tmux")
	if err := os.WriteFile(bin, []byte(wrapper(tmux)), 0o755); err != nil {
		t.Fatal(err)
	}
	return NewServer(bin, sock), sock
}

// The wrapper holds every call after new-session until the placeholder's pane is dead, which is the worst case of a placeholder that exits at once.
func TestASessionWhosePlaceholderExitsAtOnceIsStillLaunchable(t *testing.T) {
	s, _ := tmuxRig(t, func(tmux string) string {
		return `#!/bin/sh
case " $* " in
*" new-session "*) exec ` + tmux + ` "$@";;
esac
for i in $(seq 500); do
	dead=$(` + tmux + ` -S "$2" list-panes -t '=ship:' -F '#{pane_dead}' 2>/dev/null) || break
	[ "$dead" = 1 ] && break
	sleep 0.01
done
exec ` + tmux + ` "$@"
`
	})
	tmp := t.TempDir()
	if err := s.WithPlaceholder("true").StartSession(SessionSpec{Name: "ship", Dir: tmp, Argv: []string{"/bin/cat"}}); err != nil {
		t.Fatalf("StartSession after the placeholder exited: %v", err)
	}
	if rows, _ := s.SessionRows(); len(rows) != 1 {
		t.Errorf("sessions = %v, want the ship's", rows)
	}
	if got, err := s.run(call{args: []string{"show-options", "-pqv", "-t", "=ship:", "remain-on-exit"}}); err != nil || strings.TrimSpace(got) != "" {
		t.Errorf("remain-on-exit on a ship that is not anchored = %q, %v, want it unset again", got, err)
	}
}

func TestAFailedStartNamesWhatTmuxSawOfTheSession(t *testing.T) {
	s, _ := tmuxRig(t, func(tmux string) string {
		return `#!/bin/sh
case " $* " in
*" history-limit "*) echo "no such session: =ship:" >&2; exit 1;;
esac
exec ` + tmux + ` "$@"
`
	})
	err := s.StartSession(SessionSpec{Name: "ship", Dir: t.TempDir(), Argv: []string{"/bin/cat"}})
	if err == nil {
		t.Fatal("StartSession succeeded, want the batch's failure")
	}
	for _, want := range []string{"tmux could not start the ship:", "no such session: =ship:", "sessions: ship", "pane_dead=0"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q lacks %q", err, want)
		}
	}
}
