package e2e

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

// Lock holders stay alive until a "release" file appears, so no test races a
// timer. The resource has no check: a lock does not need one.

const lockExitBusy = 75

type lockRes struct {
	Name    string `json:"name"`
	InUse   int    `json:"in_use"`
	Holders []struct {
		Kind  string `json:"kind"`
		Label string `json:"label"`
	} `json:"holders"`
}

func (w *world) writeLockConfig(sources map[string]any) {
	w.t.Helper()
	var cfg map[string]any
	if err := json.Unmarshal([]byte(w.config()), &cfg); err != nil {
		w.t.Fatal(err)
	}
	cfg["resources"] = map[string]any{"phone": map[string]any{"capacity": 1}}
	if sources != nil {
		cfg["sources"] = sources
	}
	b, err := json.Marshal(cfg)
	if err != nil {
		w.t.Fatal(err)
	}
	writeConfig(w.t, w.home, string(b))
}

func (w *world) lockState() lockRes {
	w.t.Helper()
	code, body := send(w.t, http.MethodGet, w.in.LocalURL+"/api/resources", nil)
	var out struct {
		Resources []lockRes `json:"resources"`
	}
	if err := json.Unmarshal([]byte(body), &out); code != http.StatusOK || err != nil {
		w.t.Fatalf("GET /api/resources = %d %s", code, body)
	}
	for _, r := range out.Resources {
		if r.Name == "phone" {
			return r
		}
	}
	w.t.Fatalf("GET /api/resources lists no phone: %s", body)
	return lockRes{}
}

func (w *world) waitLock(what string, pred func(lockRes) bool) lockRes {
	w.t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		if r := w.lockState(); pred(r) {
			return r
		}
		if time.Now().After(deadline) {
			w.t.Fatalf("within 10s phone never showed %s; it shows %+v", what, w.lockState())
		}
		time.Sleep(50 * time.Millisecond)
	}
}

type lockProc struct {
	cmd         *exec.Cmd
	stdout, err *syncBuffer
	done        chan struct{}
	dir         string // holds "pid" and "started", and "release" ends the command
}

func (l *lockProc) exited() bool {
	select {
	case <-l.done:
		return true
	default:
		return false
	}
}

func (l *lockProc) release() { _ = os.WriteFile(filepath.Join(l.dir, "release"), nil, 0o600) }

func (w *world) waitExit(l *lockProc, what string) int {
	w.t.Helper()
	select {
	case <-l.done:
		return l.cmd.ProcessState.ExitCode()
	case <-time.After(10 * time.Second):
		w.t.Fatalf("%s: admiral lock did not exit within 10s; stderr %q", what, l.err.String())
		return 0
	}
}

// startLock runs `admiral lock <flags…> phone -- sh -c <script>`. The script runs
// until release; extra is the shell snippet to run first.
func (w *world) startLock(extra string, flags ...string) *lockProc {
	w.t.Helper()
	dir, err := os.MkdirTemp(w.root, "lk")
	if err != nil {
		w.t.Fatal(err)
	}
	if extra == "" {
		extra = "true"
	}
	script := extra + `; echo $$ > "$1/pid"; touch "$1/started"; while [ ! -f "$1/release" ]; do sleep 0.1; done`
	args := append([]string{"lock", "--port", w.in.Port}, flags...)
	args = append(args, "phone", "--", "sh", "-c", script, "sh", dir)
	cmd := exec.Command(binaryPath(w.t), args...)
	cmd.Dir, cmd.Env = w.userHom, w.baseEnv()
	// A killed lock leaves its command running with our pipes open; Wait must not wait for it.
	cmd.WaitDelay = 500 * time.Millisecond
	l := &lockProc{cmd: cmd, stdout: &syncBuffer{}, err: &syncBuffer{}, done: make(chan struct{}), dir: dir}
	cmd.Stdout, cmd.Stderr = l.stdout, l.err
	if err := cmd.Start(); err != nil {
		w.t.Fatal(err)
	}
	go func() { _ = cmd.Wait(); close(l.done) }()
	w.t.Cleanup(func() {
		l.release()
		if !l.exited() {
			_ = cmd.Process.Kill()
			<-l.done
		}
		if b, err := os.ReadFile(filepath.Join(dir, "pid")); err == nil { // the command outlives a killed lock
			var pid int
			if _, err := fmt.Sscan(string(b), &pid); err == nil {
				_ = syscall.Kill(pid, syscall.SIGKILL)
			}
		}
	})
	return l
}

func (w *world) lockCmd(args ...string) cliResult {
	w.t.Helper()
	return w.admiral(append([]string{"lock", "--port", w.in.Port}, args...)...)
}

func (w *world) holds(flags ...string) *lockProc {
	w.t.Helper()
	l := w.startLock("", flags...)
	w.waitHeld(l)
	return l
}

// waitHeld waits for the server to show phone held, failing at once, with what
// the lock printed, if `admiral lock` has already exited.
func (w *world) waitHeld(l *lockProc) lockRes {
	w.t.Helper()
	return w.waitLock("held by a lock", func(r lockRes) bool {
		if l.exited() {
			w.t.Fatalf("admiral lock exited %d before phone showed held\nstdout: %s\nstderr: %s",
				l.cmd.ProcessState.ExitCode(), l.stdout.String(), l.err.String())
		}
		return r.InUse == 1
	})
}

func (w *world) startPhone() {
	w.t.Helper()
	w.writeLockConfig(nil)
	w.start()
	if r := w.lockState(); r.InUse != 0 { // the resource is served, free
		w.t.Fatalf("phone starts in use: %+v", r)
	}
}

// A lock holds back a launch that names its resource (409, no ship); the same
// launch goes through once the lock ends.
func TestLockBlocksALaunchNamingItUntilItEnds(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	w.proveGuard()
	w.writeCue("wait go", "exit 0")
	w.startPhone()

	l := w.startLock("")
	held := w.waitHeld(l)
	if len(held.Holders) != 1 || held.Holders[0].Kind != "lock" ||
		held.Holders[0].Label != fmt.Sprintf("admiral lock (pid %d): sh", l.cmd.Process.Pid) {
		t.Errorf("phone holders = %+v, want one lock labelled `admiral lock (pid %d): sh`", held.Holders, l.cmd.Process.Pid)
	}

	req := w.autopilotLaunch("claude", "lockedlaunch", map[string]any{"resources": []string{"phone"}})
	if code, body := w.post(req); code != http.StatusConflict {
		t.Errorf("a launch naming a resource a lock holds = %d %s, want 409", code, body)
	}
	if n := len(w.in.ships(t)); n != 0 {
		t.Errorf("ships = %d after the refused launch, want none", n)
	}
	if l.exited() {
		t.Fatalf("the lock ended while the launch was refused: %q", l.err.String())
	}

	l.release()
	if code := w.waitExit(l, "after release"); code != 0 {
		t.Errorf("admiral lock exited %d, want its command's 0; stderr %q", code, l.err.String())
	}
	if strings.Contains(l.err.String(), "lost the lock") {
		t.Errorf("a command that ended first still got the lost-lock line: %q", l.err.String())
	}
	w.waitLock("free", func(r lockRes) bool { return r.InUse == 0 })
	w.launch(req)
	w.waitLock("held by the ship", func(r lockRes) bool {
		return r.InUse == 1 && len(r.Holders) == 1 && r.Holders[0].Kind == "ship"
	})
}

func TestLockBusyExits75AndWaitRunsAfterTheFirstEnds(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	w.startPhone()
	first := w.holds()

	r := w.lockCmd("phone", "--", "true")
	w.expectExit(r, lockExitBusy, "a second lock without --wait")
	if r.stdout != "" || strings.Count(strings.TrimSpace(r.stderr), "\n") != 0 || strings.TrimSpace(r.stderr) == "" {
		t.Errorf("busy lock printed stdout %q stderr %q, want a one-line message on stderr", r.stdout, r.stderr)
	}

	ran := filepath.Join(w.root, "second-ran")
	second := w.startLock(`touch `+ran+`; true`, "--wait")
	holdThat(t, "a --wait lock ran or exited while the first lock held the unit, or the first ended early", func() bool {
		return !second.exited() && !exists(ran) && !first.exited()
	})

	first.release()
	w.waitExit(first, "first")
	deadline := time.Now().Add(10 * time.Second)
	for _, err := os.Stat(ran); err != nil; _, err = os.Stat(ran) {
		if time.Now().After(deadline) {
			t.Fatalf("the --wait lock never ran its command within 10s of the unit freeing; stderr %q", second.err.String())
		}
		time.Sleep(50 * time.Millisecond)
	}
	second.release()
	if code := w.waitExit(second, "second"); code != 0 {
		t.Errorf("the --wait lock exited %d, want 0", code)
	}

	// The documented order: `admiral lock <resource> [--wait] -- <command…>`.
	r = w.admiral("lock", "phone", "--wait", "--port", w.in.Port, "--", "true")
	w.expectExit(r, 0, "flags after the resource")

	// Everything after `--` is the command's: the help scan stops there.
	r = w.lockCmd("phone", "--", "sh", "-c", `echo "$1"`, "sh", "--help")
	w.expectExit(r, 0, "a command given --help")
	if strings.TrimSpace(r.stdout) != "--help" {
		t.Errorf("stdout = %q, want the command's own --help argument echoed, not admiral's usage", r.stdout)
	}
}

// A --wait lock waits behind a ship that holds the unit and takes it once the
// ship's session is gone: main.go wakes the Set when a ship changes.
func TestLockWaitsBehindAShipUntilItEnds(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	w.proveGuard()
	w.writeCue("wait go", "exit 0")
	w.startPhone()
	ship := w.launch(w.autopilotLaunch("claude", "lockship", map[string]any{"resources": []string{"phone"}}))
	w.waitLock("held by the ship", func(r lockRes) bool {
		return r.InUse == 1 && len(r.Holders) == 1 && r.Holders[0].Kind == "ship"
	})

	l := w.startLock("", "--wait")
	holdThat(t, "a --wait lock ran while a ship held the only unit", func() bool {
		return !exists(filepath.Join(l.dir, "started")) && !l.exited()
	})
	w.mustTmux("kill-session", "-t", ship.Session)
	deadline := time.Now().Add(10 * time.Second)
	for _, err := os.Stat(filepath.Join(l.dir, "started")); err != nil; _, err = os.Stat(filepath.Join(l.dir, "started")) {
		if time.Now().After(deadline) {
			t.Fatalf("the --wait lock never ran within 10s of the ship's session ending; stderr %q", l.err.String())
		}
		time.Sleep(50 * time.Millisecond)
	}
	l.release()
	if code := w.waitExit(l, "after release"); code != 0 {
		t.Errorf("admiral lock exited %d, want 0", code)
	}
}

func TestLockKilledReleasesTheUnit(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	w.startPhone()
	l := w.holds()
	if err := l.cmd.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	w.waitExit(l, "after SIGKILL")
	w.waitLock("free", func(r lockRes) bool { return r.InUse == 0 })
	w.expectExit(w.lockCmd("phone", "--", "true"), 0, "a lock after the holder was killed")
}

func TestLockLostWhenTheServerStops(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	w.startPhone()
	l := w.holds()
	w.in.Stop()
	deadline := time.Now().Add(10 * time.Second)
	want := "lost the lock on phone (the server went away); sh is still running without it"
	for !strings.Contains(l.err.String(), want) {
		if time.Now().After(deadline) {
			t.Fatalf("no lost-lock line within 10s of the server stopping; stderr %q", l.err.String())
		}
		time.Sleep(50 * time.Millisecond)
	}
	if l.exited() {
		t.Fatalf("admiral lock exited when the server stopped; the command should keep running: %q", l.err.String())
	}
	l.release()
	if code := w.waitExit(l, "after release"); code != 0 {
		t.Errorf("admiral lock exited %d, want its command's 0", code)
	}
}

// Skips when the Mac's LAN address does not answer (docs/agents/testing.md).
func TestLockRouteRefusesANonLoopbackPeer(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	w.writeLockConfig(nil)
	in := startAdmiral(t, w.home, "0.0.0.0:"+freePort(t))
	requireLAN(t, in)
	cookie := map[string]string{"Cookie": in.cookieName() + "=" + in.Token}
	if resp, body := get(t, in.lanURL()+"/api/resources", cookie); resp.StatusCode != http.StatusOK || !strings.Contains(body, "phone") {
		t.Fatalf("GET /api/resources over the LAN with the token = %d %s, want 200 listing phone", resp.StatusCode, body)
	}

	req, err := http.NewRequest(http.MethodPost, in.lanURL()+"/api/resources/phone/lock", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Cookie", cookie["Cookie"])
	resp, err := client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("POST /api/resources/phone/lock over the LAN with the token = %d, want 403", resp.StatusCode)
	}
	if resp, body := get(t, in.LocalURL+"/api/resources", nil); resp.StatusCode != http.StatusOK || strings.Contains(body, `"kind":"lock"`) {
		t.Errorf("after the refused POST, GET /api/resources = %d %s, want no lock held", resp.StatusCode, body)
	}
}
