package codexpane

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
)

// The test binary plays three parts, chosen by CODEXPANE_ROLE: the wrapper under test, a fake
// codex (the shim script below sets the role) and a sleeper the fake server leaves behind.
func TestMain(m *testing.M) {
	switch os.Getenv("CODEXPANE_ROLE") {
	case "wrapper":
		owner, err := strconv.Atoi(os.Getenv("CODEXPANE_OWNER"))
		if err != nil {
			owner = os.Getuid()
		}
		ms, _ := strconv.Atoi(os.Getenv("CODEXPANE_WAIT_MS"))
		os.Exit(Run(Options{Socket: os.Getenv("CODEXPANE_SOCKET"), Exe: os.Getenv("CODEXPANE_EXE"),
			Args: os.Args[1:], Owner: owner, Wait: time.Duration(ms) * time.Millisecond}))
	case "fake":
		os.Exit(runFake())
	case "sleeper":
		time.Sleep(time.Minute)
		os.Exit(0)
	}
	os.Exit(m.Run())
}

func writeFile(dir, name, body string) {
	_ = os.WriteFile(filepath.Join(dir, name+".part"), []byte(body), 0o600)
	_ = os.Rename(filepath.Join(dir, name+".part"), filepath.Join(dir, name))
}

func nul(args []string) string {
	var b strings.Builder
	for _, a := range args {
		b.WriteString(a + "\x00")
	}
	return b.String()
}

// runFake is codex: `app-server --listen unix://PATH …` is the server, anything else the TUI.
// FAKE_SERVER is serve (default), never (never listens) or exit-early; FAKE_TUI is exit:<n>, hold
// (until the file tui-release exists) or echo-stdin.
func runFake() int {
	dir := os.Getenv("FAKE_DIR")
	args := os.Args[1:]
	if len(args) > 0 && args[0] == "app-server" {
		return fakeServer(dir, args)
	}
	return fakeTUI(dir, args)
}

func fakeServer(dir string, args []string) int {
	writeFile(dir, "server.argv", nul(args))
	writeFile(dir, "server.env", strings.Join(os.Environ(), "\n"))
	writeFile(dir, "server.pid", strconv.Itoa(os.Getpid()))
	mode := os.Getenv("FAKE_SERVER")
	if mode == "exit-early" {
		fmt.Fprintln(os.Stderr, "error: unrecognized subcommand 'app-server'")
		return 2
	}
	fmt.Println("SERVER-NOISE")
	fmt.Fprintln(os.Stderr, "SERVER-NOISE")
	sleeper := exec.Command(os.Args[0])
	sleeper.Env = append(os.Environ(), "CODEXPANE_ROLE=sleeper")
	if err := sleeper.Start(); err == nil {
		writeFile(dir, "server.child.pid", strconv.Itoa(sleeper.Process.Pid))
	}
	// A hook as the server would run it: a child that reads the pane's variables.
	_ = exec.Command("/bin/sh", "-c", `printf '%s %s' "$TMUX_PANE" "$ADMIRAL_SHIP" > "$FAKE_DIR/hook.out"`).Run()
	go func() {
		_, _ = io.Copy(io.Discard, os.Stdin)
		writeFile(dir, "server.stdin-eof", "")
	}()
	if mode == "never" {
		time.Sleep(time.Minute)
		return 0
	}
	var path string
	for i, a := range args {
		if a == "--listen" && i+1 < len(args) {
			path = strings.TrimPrefix(args[i+1], "unix://")
		}
	}
	l, err := net.Listen("unix", path)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	l.(*net.UnixListener).SetUnlinkOnClose(false) // a killed codex leaves its socket too
	writeFile(dir, "server.listening", "")
	for {
		c, err := l.Accept()
		if err != nil {
			return 1
		}
		_ = c.Close()
	}
}

func fakeTUI(dir string, args []string) int {
	writeFile(dir, "tui.argv", nul(args))
	writeFile(dir, "tui.env", strings.Join(os.Environ(), "\n"))
	writeFile(dir, "tui.pid", strconv.Itoa(os.Getpid()))
	if len(args) > 1 && args[0] == "--remote" {
		if c, err := net.Dial("unix", strings.TrimPrefix(args[1], "unix://")); err == nil {
			_ = c.Close()
			writeFile(dir, "tui.connected", "")
		}
	}
	writeFile(dir, "tui.started", "")
	mode := os.Getenv("FAKE_TUI")
	switch {
	case strings.HasPrefix(mode, "exit:"):
		n, _ := strconv.Atoi(strings.TrimPrefix(mode, "exit:"))
		return n
	case mode == "hold":
		waitForFile(filepath.Join(dir, "tui-release"), 30*time.Second)
	case mode == "echo-stdin":
		waitForFile(filepath.Join(dir, "server.stdin-eof"), 5*time.Second)
		fmt.Println("TUI-OUT")
		line, _ := bufio.NewReader(os.Stdin).ReadString('\n')
		writeFile(dir, "tui.stdin", strings.TrimSpace(line))
	}
	return 0
}

func waitForFile(path string, d time.Duration) {
	for end := time.Now().Add(d); time.Now().Before(end); time.Sleep(10 * time.Millisecond) {
		if _, err := os.Stat(path); err == nil {
			return
		}
	}
}

type syncBuf struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuf) Write(p []byte) (int, error) { s.mu.Lock(); defer s.mu.Unlock(); return s.b.Write(p) }
func (s *syncBuf) String() string              { s.mu.Lock(); defer s.mu.Unlock(); return s.b.String() }

var hookFlags = []string{
	"-c", `hooks.Stop=[{hooks=[{type="command",command="admiral hook Stop"}]}]`,
	"-c", `hooks.SessionStart=[{hooks=[{type="command",command="admiral hook SessionStart"}]}]`,
}

// shipArgs is what the launcher puts after the exe: the dock's and the model's flags,
// Admiral's hook flags and bell, then the prompt.
var shipArgs = append(append([]string{"-m", "gpt-test", "-c", `model_reasoning_effort="high"`}, hookFlags...),
	"-c", `tui.notification_method="bel"`, "--", "do it")

type setup struct {
	server, tui string
	wait        time.Duration
	socketDir   string // a name under the temp dir; "d" when empty
	stdin       bool   // give the wrapper a pipe for stdin, held open
}

type rig struct {
	t         *testing.T
	dir       string // what the fakes record into
	socket    string
	cmd       *exec.Cmd
	stdin     io.WriteCloser
	out, errb *syncBuf
	done      chan int
}

func shortTemp(t *testing.T) string {
	t.Helper()
	// A unix socket path is limited to 104 bytes on macOS.
	tmp, err := os.MkdirTemp("", "cp")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(tmp) })
	return tmp
}

func start(t *testing.T, s setup) *rig {
	t.Helper()
	tmp := shortTemp(t)
	r := &rig{t: t, dir: filepath.Join(tmp, "fake"), out: &syncBuf{}, errb: &syncBuf{}, done: make(chan int, 1)}
	if err := os.Mkdir(r.dir, 0o700); err != nil {
		t.Fatal(err)
	}
	name := s.socketDir
	if name == "" {
		name = "d"
	}
	r.socket = filepath.Join(tmp, name, "s.sock")
	shim := filepath.Join(tmp, "codex")
	if err := os.WriteFile(shim, []byte("#!/bin/sh\nCODEXPANE_ROLE=fake exec '"+os.Args[0]+"' \"$@\"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	// A codex on PATH is one the wrapper must never run: the dock's exe is given by path.
	decoy := filepath.Join(tmp, "path")
	if err := os.Mkdir(decoy, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(decoy, "codex"), []byte("#!/bin/sh\n: > '"+filepath.Join(r.dir, "decoy-ran")+"'\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	var env []string
	for _, kv := range os.Environ() {
		if !strings.HasPrefix(kv, "CODEXPANE_") && !strings.HasPrefix(kv, "FAKE_") && !strings.HasPrefix(kv, "PATH=") {
			env = append(env, kv)
		}
	}
	wait := s.wait
	if wait == 0 {
		wait = 5 * time.Second
	}
	env = append(env, "PATH="+decoy+":"+os.Getenv("PATH"), "TMUX_PANE=%7", "ADMIRAL_SHIP=dory",
		"CODEXPANE_ROLE=wrapper", "CODEXPANE_SOCKET="+r.socket, "CODEXPANE_EXE="+shim,
		"CODEXPANE_WAIT_MS="+strconv.Itoa(int(wait.Milliseconds())),
		"FAKE_DIR="+r.dir, "FAKE_SERVER="+s.server, "FAKE_TUI="+s.tui)
	r.cmd = exec.Command(os.Args[0], shipArgs...)
	r.cmd.Env = env
	r.cmd.Stdout, r.cmd.Stderr = r.out, r.errb
	r.cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	r.cmd.WaitDelay = 3 * time.Second
	if s.stdin {
		var err error
		if r.stdin, err = r.cmd.StdinPipe(); err != nil {
			t.Fatal(err)
		}
	}
	if err := r.cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = syscall.Kill(-r.cmd.Process.Pid, syscall.SIGKILL)
		for _, f := range []string{"server.pid", "server.child.pid", "tui.pid"} {
			if pid := r.pid(f); pid > 0 {
				_ = syscall.Kill(pid, syscall.SIGKILL)
			}
		}
	})
	go func() {
		err := r.cmd.Wait()
		code := 0
		if ee, ok := err.(*exec.ExitError); ok {
			code = ee.ExitCode()
		} else if err != nil {
			code = -2
		}
		r.done <- code
	}()
	return r
}

func (r *rig) read(name string) (string, bool) {
	b, err := os.ReadFile(filepath.Join(r.dir, name))
	return string(b), err == nil
}

func (r *rig) waitFile(name string) string {
	r.t.Helper()
	for end := time.Now().Add(5 * time.Second); time.Now().Before(end); time.Sleep(10 * time.Millisecond) {
		if s, ok := r.read(name); ok {
			return s
		}
	}
	r.t.Fatalf("%s never appeared; wrapper stderr: %q", name, r.errb.String())
	return ""
}

func (r *rig) pid(name string) int {
	s, _ := r.read(name)
	n, _ := strconv.Atoi(strings.TrimSpace(s))
	return n
}

func (r *rig) argv(name string) []string {
	s, ok := r.read(name)
	if !ok {
		return nil
	}
	return strings.Split(strings.TrimSuffix(s, "\x00"), "\x00")
}

func (r *rig) exit() int {
	r.t.Helper()
	select {
	case code := <-r.done:
		r.done <- code
		return code
	case <-time.After(8 * time.Second):
		r.t.Fatal("the wrapper never exited")
		return 0
	}
}

func (r *rig) exited() bool {
	select {
	case code := <-r.done:
		r.done <- code
		return true
	default:
		return false
	}
}

func alive(pid int) bool {
	out, _ := exec.Command("ps", "-o", "stat=", "-p", strconv.Itoa(pid)).Output()
	s := strings.TrimSpace(string(out))
	return s != "" && !strings.HasPrefix(s, "Z")
}

func (r *rig) expectGone(what string, pid int) {
	r.t.Helper()
	if pid <= 0 {
		r.t.Fatalf("no pid recorded for %s", what)
	}
	for end := time.Now().Add(5 * time.Second); time.Now().Before(end); time.Sleep(20 * time.Millisecond) {
		if !alive(pid) {
			return
		}
	}
	r.t.Errorf("%s (pid %d) is still running", what, pid)
}

func (r *rig) expectNoSocket() {
	r.t.Helper()
	for end := time.Now().Add(5 * time.Second); time.Now().Before(end); time.Sleep(20 * time.Millisecond) {
		if _, err := os.Lstat(r.socket); os.IsNotExist(err) {
			return
		}
	}
	r.t.Errorf("the socket %s is still there", r.socket)
}

// expectNothingLeft is the contract after any end: no server, none of its children, no socket.
func (r *rig) expectNothingLeft() {
	r.t.Helper()
	r.expectGone("the server", r.pid("server.pid"))
	r.expectGone("the server's child", r.pid("server.child.pid"))
	r.expectNoSocket()
}

func TestTUIRunsRemoteBehindTheServerThatCameUp(t *testing.T) {
	r := start(t, setup{tui: "exit:0"})
	r.exit()
	wantTUI := append([]string{"--remote", "unix://" + r.socket}, shipArgs...)
	if got := r.argv("tui.argv"); !reflect.DeepEqual(got, wantTUI) {
		t.Errorf("the TUI ran with %q, want --remote right after the exe, then the ship's args:\n%q", got, wantTUI)
	}
	if _, ok := r.read("tui.connected"); !ok {
		t.Error("the TUI started before the server accepted connections")
	}
	if _, ok := r.read("decoy-ran"); ok {
		t.Error("the wrapper ran codex from PATH, not the exe it was given")
	}
}

func TestServerGetsTheHookFlagsAndNoDockArgs(t *testing.T) {
	r := start(t, setup{tui: "exit:0"})
	r.exit()
	want := append([]string{"app-server", "--listen", "unix://" + r.socket}, hookFlags...)
	if got := r.argv("server.argv"); !reflect.DeepEqual(got, want) {
		t.Errorf("the server ran with %q, want %q", got, want)
	}
}

func TestTUIExitStopsTheServerAndPassesItsStatus(t *testing.T) {
	r := start(t, setup{tui: "exit:7"})
	if code := r.exit(); code != 7 {
		t.Errorf("the wrapper exited %d, want the TUI's 7", code)
	}
	r.expectNothingLeft()
}

func TestServerThatNeverComesUpFallsBackToPlainCodex(t *testing.T) {
	r := start(t, setup{server: "never", tui: "exit:3", wait: 400 * time.Millisecond})
	if code := r.exit(); code != 3 {
		t.Errorf("the wrapper exited %d, want the plain run's 3", code)
	}
	expectPlain(t, r)
	r.expectNothingLeft()
}

func TestServerThatExitsEarlyFallsBackAtOnce(t *testing.T) {
	started := time.Now()
	r := start(t, setup{server: "exit-early", tui: "exit:4", wait: 20 * time.Second})
	if code := r.exit(); code != 4 {
		t.Errorf("the wrapper exited %d, want the plain run's 4", code)
	}
	if took := time.Since(started); took > 6*time.Second {
		t.Errorf("fell back after %v: it waited out the timeout on a server that had already exited", took)
	}
	expectPlain(t, r)
	r.expectNoSocket()
}

// expectPlain: the exe ran with the ship's args and no --remote. A leading --no-daemon is allowed.
func expectPlain(t *testing.T, r *rig) {
	t.Helper()
	got := r.argv("tui.argv")
	if len(got) > 0 && got[0] == "--no-daemon" {
		got = got[1:]
	}
	if !reflect.DeepEqual(got, shipArgs) {
		t.Errorf("the fallback ran with %q, want the ship's args %q", got, shipArgs)
	}
}

func TestPaneKilledOrTerminatedStopsTheServer(t *testing.T) {
	for _, sig := range []syscall.Signal{syscall.SIGHUP, syscall.SIGTERM} {
		t.Run(sig.String(), func(t *testing.T) {
			r := start(t, setup{tui: "hold"})
			r.waitFile("tui.started")
			if err := syscall.Kill(r.cmd.Process.Pid, sig); err != nil {
				t.Fatal(err)
			}
			r.expectNothingLeft()
		})
	}
}

func TestCtrlCAndQuitDoNotEndTheShip(t *testing.T) {
	r := start(t, setup{tui: "hold"})
	r.waitFile("tui.started")
	for _, sig := range []syscall.Signal{syscall.SIGINT, syscall.SIGQUIT} {
		if err := syscall.Kill(r.cmd.Process.Pid, sig); err != nil {
			t.Fatal(err)
		}
	}
	time.Sleep(500 * time.Millisecond)
	if r.exited() {
		t.Fatal("the wrapper exited on SIGINT or SIGQUIT")
	}
	for _, f := range []string{"server.pid", "server.child.pid", "tui.pid"} {
		if !alive(r.pid(f)) {
			t.Errorf("%s (pid %d) died", f, r.pid(f))
		}
	}
	if _, err := os.Lstat(r.socket); err != nil {
		t.Errorf("the socket is gone: %v", err)
	}
	writeFile(r.dir, "tui-release", "")
	if code := r.exit(); code != 0 {
		t.Errorf("the wrapper exited %d, want the TUI's 0", code)
	}
	r.expectNothingLeft()
}

func TestServerStaysOffTheTerminal(t *testing.T) {
	r := start(t, setup{tui: "echo-stdin", stdin: true})
	r.waitFile("tui.started")
	if _, err := io.WriteString(r.stdin, "typed\n"); err != nil {
		t.Fatal(err)
	}
	r.exit()
	if got, _ := r.read("server.stdin-eof"); got != "" || r.pid("server.pid") == 0 {
		t.Fatal("the server's stdin did not end at once while the wrapper's stayed open")
	}
	if got, _ := r.read("tui.stdin"); got != "typed" {
		t.Errorf("the TUI read %q from the terminal, want the typed line", got)
	}
	if !strings.Contains(r.out.String(), "TUI-OUT") {
		t.Errorf("stdout = %q, want the TUI's output passed through", r.out.String())
	}
	for name, got := range map[string]string{"stdout": r.out.String(), "stderr": r.errb.String()} {
		if strings.Contains(got, "SERVER-NOISE") {
			t.Errorf("the server's output reached the terminal's %s: %q", name, got)
		}
	}
}

func TestServerSeesThePanesEnvironmentForItsHooks(t *testing.T) {
	r := start(t, setup{tui: "exit:0"})
	r.exit()
	if got, _ := r.read("hook.out"); got != "%7 dory" {
		t.Errorf("a hook run by the server saw TMUX_PANE and ADMIRAL_SHIP as %q, want %q", got, "%7 dory")
	}
}

func TestUnsafeSocketDirFallsBackToPlainCodex(t *testing.T) {
	tmp := shortTemp(t)
	real := filepath.Join(tmp, "real")
	if err := os.Mkdir(real, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(real, filepath.Join(tmp, "link")); err != nil {
		t.Fatal(err)
	}
	// start makes its own temp dir, so aim the socket at the planted link through a relative name.
	r := start(t, setup{tui: "exit:5", socketDir: "../" + filepath.Base(tmp) + "/link"})
	if code := r.exit(); code != 5 {
		t.Errorf("the wrapper exited %d, want the plain run's 5", code)
	}
	expectPlain(t, r)
	if _, ok := r.read("server.argv"); ok {
		t.Error("a server was started with its socket in a linked directory")
	}
	if entries, _ := os.ReadDir(real); len(entries) != 0 {
		t.Errorf("something was created behind the link: %v", entries)
	}
}

func TestSocketDirIsPrivateAndOurs(t *testing.T) {
	parent := shortTemp(t)
	me := os.Getuid()

	t.Run("a missing one is created with mode 0700", func(t *testing.T) {
		dir := filepath.Join(parent, "new")
		if err := EnsureSocketDir(dir, me); err != nil {
			t.Fatal(err)
		}
		if fi, err := os.Lstat(dir); err != nil || !fi.IsDir() || fi.Mode().Perm() != 0o700 {
			t.Errorf("Lstat = %v, %v; want a directory with mode 0700", fi, err)
		}
	})
	t.Run("one of ours with mode 0700 is used", func(t *testing.T) {
		dir := filepath.Join(parent, "ok")
		if err := os.Mkdir(dir, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := EnsureSocketDir(dir, me); err != nil {
			t.Errorf("EnsureSocketDir = %v, want it accepted", err)
		}
	})
	t.Run("a symlink is refused, even to a safe directory", func(t *testing.T) {
		real := filepath.Join(parent, "real")
		if err := os.Mkdir(real, 0o700); err != nil {
			t.Fatal(err)
		}
		link := filepath.Join(parent, "link")
		if err := os.Symlink(real, link); err != nil {
			t.Fatal(err)
		}
		if err := EnsureSocketDir(link, me); err == nil {
			t.Error("EnsureSocketDir accepted a symlink")
		}
	})
	t.Run("another owner is refused", func(t *testing.T) {
		dir := filepath.Join(parent, "theirs")
		if err := os.Mkdir(dir, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := EnsureSocketDir(dir, me+1); err == nil {
			t.Error("EnsureSocketDir accepted a directory owned by someone else")
		}
	})
	t.Run("a wider mode is refused", func(t *testing.T) {
		dir := filepath.Join(parent, "wide")
		if err := os.Mkdir(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := EnsureSocketDir(dir, me); err == nil {
			t.Error("EnsureSocketDir accepted a directory with mode 0755")
		}
	})
}

func TestSocketPathIsPerLaunchAndShortEnoughForAnyUID(t *testing.T) {
	const maxUID = 4294967294
	seen := map[string]bool{}
	for range 20 {
		p, err := SocketPath(maxUID)
		if err != nil {
			t.Fatal(err)
		}
		if n := len(p); n >= 104 {
			t.Fatalf("%q is %d bytes, over macOS's 104-byte limit once NUL-terminated", p, n)
		}
		if !strings.HasPrefix(p, "/tmp/admiral-4294967294/") || !strings.HasSuffix(p, ".sock") {
			t.Fatalf("path = %q, want /tmp/admiral-<uid>/<hash>.sock", p)
		}
		seen[p] = true
	}
	if len(seen) != 20 {
		t.Errorf("20 launches got %d distinct paths", len(seen))
	}
}
