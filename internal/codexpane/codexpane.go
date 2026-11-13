// Package codexpane runs a Codex ship's pane: a per-ship `codex app-server` on a private
// socket, with the TUI attached to it through --remote. The server lives and dies with the TUI.
package codexpane

import (
	"bytes"
	"context"
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
	return checkDir(dir, fi, owner)
}

func checkDir(dir string, fi os.FileInfo, owner int) error {
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

// serverFlags are the config and feature flags among the ship's args, before any "--": tools
// and the model run in the server, so it gets the dock's overrides and Admiral's hooks and
// effort as well as the TUI. The TUI-only `tui.*` overrides, `-m` and the rest stay on the TUI.
func serverFlags(args []string) []string {
	var out []string
	for i := 0; i < len(args); i++ {
		a := args[i]
		if a == "--" {
			break
		}
		flag, val, joined := a, "", false
		if k, v, ok := strings.Cut(a, "="); ok && strings.HasPrefix(a, "--") {
			flag, val, joined = k, v, true
		}
		switch {
		case flag == "-c" || flag == "--config" || flag == "--enable" || flag == "--disable":
			if !joined {
				if i+1 >= len(args) {
					return out
				}
				i++
				val = args[i]
			}
			if flag == "-c" || flag == "--config" {
				if strings.HasPrefix(strings.TrimSpace(val), "tui.") {
					continue
				}
			}
			out = append(out, flag, val)
		case strings.HasPrefix(a, "-c") && !strings.HasPrefix(a, "--") && len(a) > 2:
			val = strings.TrimPrefix(a[2:], "=")
			if !strings.HasPrefix(strings.TrimSpace(val), "tui.") {
				out = append(out, "-c", val)
			}
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

	srv, why, sig := startServer(o, wait, sigs)
	if sig != 0 {
		return 128 + int(sig)
	}
	if srv == nil {
		fmt.Fprintf(os.Stderr, "admiral: running Codex without its own app-server: %s\n", why)
		args := o.Args
		// A Codex with no app-server predates --no-daemon and would reject it.
		if HasNoDaemon(o.Exe) {
			args = append([]string{"--no-daemon"}, args...)
		}
		return runTUI(o, args, sigs)
	}
	defer srv.stop(o)
	return runTUI(o, append([]string{"--remote", "unix://" + o.Socket}, o.Args...), sigs)
}

type server struct {
	cmd    *exec.Cmd
	exited chan struct{}
}

// stop ends the server and everything it started, and leaves no socket behind. A server that
// has already exited was reaped, so its pid may be anyone's now: no group is signalled then.
func (s *server) stop(o Options) {
	if s != nil && s.cmd.Process != nil {
		pgid := s.cmd.Process.Pid
		if !closed(s.exited) {
			_ = syscall.Kill(-pgid, syscall.SIGTERM)
			select {
			case <-s.exited:
			case <-time.After(time.Second):
			}
			if !closed(s.exited) {
				_ = syscall.Kill(-pgid, syscall.SIGKILL)
				select {
				case <-s.exited:
				case <-time.After(time.Second):
				}
			}
		}
	}
	removeSocket(o)
}

func closed(c <-chan struct{}) bool {
	select {
	case <-c:
		return true
	default:
		return false
	}
}

// HasNoDaemon asks the exe's --help, briefly; any doubt reads as no.
func HasNoDaemon(exe string) bool {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	var out bytes.Buffer
	cmd := exec.CommandContext(ctx, exe, "--help")
	cmd.Stdout = &out
	cmd.WaitDelay = 500 * time.Millisecond
	if err := cmd.Run(); err != nil {
		return false
	}
	return bytes.Contains(out.Bytes(), []byte("--no-daemon"))
}

// daemonDir is where Codex puts the real socket behind the listen path; a var so tests can
// keep off the real one.
var daemonDir = func(uid int) string { return fmt.Sprintf("/tmp/codex-daemon-%d", uid) }

// DaemonDir is the directory Codex keeps its sockets in for this user.
func DaemonDir() string { return daemonDir(os.Getuid()) }

// removeSocket deletes the listen path. Codex makes it a symlink to a socket in its own
// private directory (/private/tmp/codex-daemon-<uid>/<hash> on macOS, next to a <hash>.lock),
// which an unclean exit leaves behind. Those go only when the target's directory is the daemon
// directory, which must be ours and private.
func removeSocket(o Options) {
	defer func() { _ = os.Remove(o.Socket) }()
	target, err := os.Readlink(o.Socket)
	if err != nil {
		return
	}
	if !filepath.IsAbs(target) {
		target = filepath.Join(filepath.Dir(o.Socket), target)
	}
	root := daemonDir(o.Owner)
	fi, err := os.Lstat(root)
	if err != nil || checkDir(root, fi, o.Owner) != nil {
		return
	}
	real, err := filepath.EvalSymlinks(root)
	if err != nil {
		return
	}
	dir, err := filepath.EvalSymlinks(filepath.Dir(filepath.Clean(target)))
	if err != nil || dir != real {
		return
	}
	name := filepath.Base(target)
	for _, p := range []struct {
		path string
		mode os.FileMode
	}{{filepath.Join(real, name), os.ModeSocket}, {filepath.Join(real, name+".lock"), 0}} {
		fi, err := os.Lstat(p.path)
		if err != nil || fi.Mode()&os.ModeType != p.mode {
			continue
		}
		if st, ok := fi.Sys().(*syscall.Stat_t); ok && int(st.Uid) == o.Owner {
			_ = os.Remove(p.path)
		}
	}
}

// startServer returns a server only once its socket accepts a connection. A signal that ends
// the wrapper comes back as sig, with the server already stopped.
func startServer(o Options, wait time.Duration, sigs <-chan os.Signal) (srv *server, why string, sig syscall.Signal) {
	if err := EnsureSocketDir(filepath.Dir(o.Socket), o.Owner); err != nil {
		return nil, err.Error(), 0
	}
	_ = os.Remove(o.Socket) // a stale file would make the listen fail
	args := append([]string{"app-server", "--listen", "unix://" + o.Socket}, serverFlags(o.Args)...)
	cmd := exec.Command(o.Exe, args...)
	// stdin and stdout are /dev/null (nil): the server must not draw on or read the TUI's terminal.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		return nil, err.Error(), 0
	}
	s := &server{cmd: cmd, exited: make(chan struct{})}
	go func() { _ = cmd.Wait(); close(s.exited) }()

	deadline := time.After(wait)
	tick := time.NewTicker(20 * time.Millisecond)
	defer tick.Stop()
	for {
		if c, err := net.DialTimeout("unix", o.Socket, 200*time.Millisecond); err == nil {
			_ = c.Close()
			return s, "", 0
		}
		select {
		case <-s.exited:
			s.stop(o)
			return nil, "app-server exited before it listened", 0
		case <-deadline:
			s.stop(o)
			return nil, fmt.Sprintf("app-server did not listen within %v", wait), 0
		case got := <-sigs:
			if got == syscall.SIGHUP || got == syscall.SIGTERM {
				s.stop(o)
				return nil, "", got.(syscall.Signal)
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
