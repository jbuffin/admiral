// Package codexpane runs a Codex ship's pane: a per-ship `codex app-server` on a private
// socket, with the TUI attached to it through --remote. The server lives and dies with the TUI.
package codexpane

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

type Options struct {
	Socket string
	// Exe is the dock's resolved Codex executable: a fleet wrapper may set CODEX_HOME, so the
	// server and the TUI must both be it, never a `codex` found on PATH.
	Exe  string
	Args []string // what follows the exe in the ship's command line
	// Owner is the uid the socket directory must belong to.
	Owner int
	Wait  time.Duration // how long to wait for the server's socket; 0 is 5 s
}

const defaultWait = 5 * time.Second

// SocketPath is a fresh socket path for one ship launch. /tmp is fixed, not $TMPDIR, to stay
// under macOS's 104-byte sun_path limit.
func SocketPath(uid int) (string, error) {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return fmt.Sprintf("/tmp/admiral-%d/%s.sock", uid, hex.EncodeToString(b[:])), nil
}

// EnsureSocketDir creates dir with mode 0700 if it is missing, and refuses one that is a
// symlink, belongs to someone else or is open to group or others.
func EnsureSocketDir(dir string, owner int) error {
	fi, err := os.Lstat(dir)
	if errors.Is(err, os.ErrNotExist) {
		if err = os.Mkdir(dir, 0o700); err != nil && !errors.Is(err, os.ErrExist) {
			return err
		}
		fi, err = os.Lstat(dir)
	}
	if err != nil {
		return err
	}
	if fi.Mode()&os.ModeSymlink != 0 || !fi.IsDir() {
		return fmt.Errorf("%s is not a plain directory", dir)
	}
	if st, ok := fi.Sys().(*syscall.Stat_t); !ok || int(st.Uid) != owner {
		return fmt.Errorf("%s is not owned by uid %d", dir, owner)
	}
	if fi.Mode().Perm()&0o077 != 0 {
		return fmt.Errorf("%s has mode %o, want 0700", dir, fi.Mode().Perm())
	}
	return nil
}

// serverHookFlags are the `-c hooks.*` pairs among the ship's args, before any "--": the server runs
// the hooks, and it takes none of the dock's other flags.
func serverHookFlags(args []string) []string {
	var out []string
	for i := 0; i+1 < len(args); i++ {
		if args[i] == "--" {
			break
		}
		if args[i] == "-c" && strings.HasPrefix(args[i+1], "hooks.") {
			out = append(out, "-c", args[i+1])
			i++
		}
	}
	return out
}

// Run starts the server, runs the TUI against it and returns the TUI's exit status. Any
// failure to bring the server up runs the plain TUI instead: a ship always launches.
func Run(o Options) int {
	wait := o.Wait
	if wait == 0 {
		wait = defaultWait
	}
	// Notify, not Ignore: an ignored signal would stay ignored in the TUI. The channel is
	// buffered and drained, so Ctrl-C and quit never end the ship or the server.
	sigs := make(chan os.Signal, 8)
	signal.Notify(sigs, syscall.SIGINT, syscall.SIGQUIT, syscall.SIGHUP, syscall.SIGTERM)
	defer signal.Stop(sigs)

	srv, ok, sig := startServer(o, wait, sigs)
	if sig != 0 {
		return 128 + int(sig)
	}
	if !ok {
		return runTUI(o, append([]string{"--no-daemon"}, o.Args...), sigs)
	}
	defer srv.stop(o)
	return runTUI(o, append([]string{"--remote", "unix://" + o.Socket}, o.Args...), sigs)
}

type server struct {
	cmd    *exec.Cmd
	exited chan struct{}
}

// stop ends the server and everything it started, and leaves no socket behind.
func (s *server) stop(o Options) {
	if s != nil {
		if s.cmd.Process != nil {
			pgid := s.cmd.Process.Pid
			_ = syscall.Kill(-pgid, syscall.SIGTERM)
			select {
			case <-s.exited:
			case <-time.After(time.Second):
			}
			_ = syscall.Kill(-pgid, syscall.SIGKILL)
			select {
			case <-s.exited:
			case <-time.After(time.Second):
			}
		}
	}
	removeSocket(o)
}

// daemonDir is where Codex puts the real socket behind the listen path; a var so tests can
// keep off the real one.
var daemonDir = func(uid int) string { return fmt.Sprintf("/tmp/codex-daemon-%d", uid) }

// removeSocket deletes the listen path. Codex makes it a symlink to a socket under
// /tmp/codex-daemon-<uid>/, which an unclean exit leaves behind.
func removeSocket(o Options) {
	if target, err := os.Readlink(o.Socket); err == nil {
		if !filepath.IsAbs(target) {
			target = filepath.Join(filepath.Dir(o.Socket), target)
		}
		target = filepath.Clean(target)
		root := daemonDir(o.Owner) + "/"
		if fi, err := os.Lstat(target); err == nil && strings.HasPrefix(target, root) && fi.Mode()&os.ModeSocket != 0 {
			if st, ok := fi.Sys().(*syscall.Stat_t); ok && int(st.Uid) == o.Owner {
				_ = os.Remove(target)
			}
		}
	}
	_ = os.Remove(o.Socket)
}

// startServer returns a server only once its socket accepts a connection. A signal that ends
// the wrapper comes back as sig, with the server already stopped.
func startServer(o Options, wait time.Duration, sigs <-chan os.Signal) (srv *server, ok bool, sig syscall.Signal) {
	if err := EnsureSocketDir(filepath.Dir(o.Socket), o.Owner); err != nil {
		return nil, false, 0
	}
	_ = os.Remove(o.Socket) // a stale file would make the listen fail
	args := append([]string{"app-server", "--listen", "unix://" + o.Socket}, serverHookFlags(o.Args)...)
	cmd := exec.Command(o.Exe, args...)
	// stdin and stdout are /dev/null (nil): the server must not draw on or read the TUI's terminal.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		return nil, false, 0
	}
	s := &server{cmd: cmd, exited: make(chan struct{})}
	go func() { _ = cmd.Wait(); close(s.exited) }()

	deadline := time.After(wait)
	tick := time.NewTicker(20 * time.Millisecond)
	defer tick.Stop()
	for {
		if c, err := net.DialTimeout("unix", o.Socket, 200*time.Millisecond); err == nil {
			_ = c.Close()
			return s, true, 0
		}
		select {
		case <-s.exited:
			s.stop(o)
			return nil, false, 0
		case <-deadline:
			s.stop(o)
			return nil, false, 0
		case got := <-sigs:
			if got == syscall.SIGHUP || got == syscall.SIGTERM {
				s.stop(o)
				return nil, false, got.(syscall.Signal)
			}
		case <-tick.C:
		}
	}
}

// runTUI runs the exe in the foreground with the terminal passed through and returns its exit
// status. SIGHUP and SIGTERM are passed on to it; the caller then stops the server.
func runTUI(o Options, args []string, sigs <-chan os.Signal) int {
	cmd := exec.Command(o.Exe, args...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	if err := cmd.Start(); err != nil {
		fmt.Fprintln(os.Stderr, "admiral:", err)
		return 127
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	for {
		select {
		case err := <-done:
			return exitStatus(err)
		case sig := <-sigs:
			if sig != syscall.SIGHUP && sig != syscall.SIGTERM {
				continue
			}
			_ = cmd.Process.Signal(sig)
			select {
			case <-done:
			case <-time.After(2 * time.Second):
				_ = cmd.Process.Kill()
				<-done
			}
			return 128 + int(sig.(syscall.Signal))
		}
	}
}

func exitStatus(err error) int {
	if err == nil {
		return 0
	}
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		if ws, ok := ee.Sys().(syscall.WaitStatus); ok && ws.Signaled() {
			return 128 + int(ws.Signal())
		}
		return ee.ExitCode()
	}
	return 1
}
