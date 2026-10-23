package e2e

import (
	"bufio"
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
	"unicode"
)

// `admiral lock` (#118, u2): the real binary, a capacity-1 resource called
// "phone", and lock holders that stay alive until a "release" file appears, so no
// test races a timer. The resource has no check: a lock does not need one.

const lockExitBusy = 75

type lockRes struct {
	Name    string `json:"name"`
	InUse   int    `json:"in_use"`
	Holders []struct {
		Kind  string `json:"kind"`
		Label string `json:"label"`
	} `json:"holders"`
}

// writeLockConfig writes the default fleets, the capacity-1 resource "phone" and
// the given sources.
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

// lockProc is a running `admiral lock`.
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

// holds starts a plain lock and waits until the server shows it holding phone.
func (w *world) holds(flags ...string) *lockProc {
	w.t.Helper()
	l := w.startLock("", flags...)
	w.waitLock("held by a lock", func(r lockRes) bool { return r.InUse == 1 })
	return l
}

func (w *world) startPhone() {
	w.t.Helper()
	w.writeLockConfig(nil)
	w.start()
	if r := w.lockState(); r.InUse != 0 { // u1: the resource is served, free
		w.t.Fatalf("phone starts in use: %+v", r)
	}
}

// AC 3 and 5: a lock holds back an autopilot launch, the panel says what it
// waits for and one resource_wait record is written; the launch follows soon
// after the lock ends.
func TestLockBlocksAnAutopilotLaunchUntilItEnds(t *testing.T) {
	w := newWorld(t)
	w.writeCue("wait go", "exit 0")
	w.repo("r1", remoteOf("r1"))
	p := w.newTP("a")
	p.mainOrders()
	w.installPlugin("testplug", "testplug")
	w.writeLockConfig(map[string]any{"a": tpAutopilot(p, "main", apEntry(true, 1, map[string]any{"resources": []string{"phone"}}))})
	w.start()
	w.waitSource("a", 10*time.Second, "state ok", isOK)
	if got := w.resourceList(); len(got) != 1 || got[0].Name != "phone" || got[0].InUse != 0 {
		t.Fatalf("GET /api/resources = %+v, want phone free", got)
	}

	l := w.startLock("")
	held := w.waitLock("held by a lock", func(r lockRes) bool { return r.InUse == 1 })
	if len(held.Holders) != 1 || held.Holders[0].Kind != "lock" ||
		held.Holders[0].Label != fmt.Sprintf("admiral lock (pid %d): sh", l.cmd.Process.Pid) {
		t.Errorf("phone holders = %+v, want one lock labelled `admiral lock (pid %d): sh`", held.Holders, l.cmd.Process.Pid)
	}

	p.mainOrders(apOrder("o1", remoteOf("r1")))
	p.do("orders-changed", "")
	w.waitSource("a", 5*time.Second, "o1", func(s sourceJSON) bool { return s.hasOrder("o1") })
	w.waitSource("a", 10*time.Second, "the panel waiting for phone", func(sourceJSON) bool {
		return strings.Join(w.waitingFor("a", "main"), ",") == "phone"
	})
	w.waitOutcome("resource_wait", 1)
	holdThat(t, "an autopilot ship launched while a lock held the only unit, or the lock ended", func() bool {
		return len(w.in.ships(t)) == 0 && !l.exited()
	})
	if n := len(w.outcomesOf("resource_wait")); n != 1 {
		t.Errorf("resource_wait records = %d, want one for the order", n)
	}

	l.release()
	if code := w.waitExit(l, "after release"); code != 0 {
		t.Errorf("admiral lock exited %d, want its command's 0; stderr %q", code, l.err.String())
	}
	w.waitShipCount(10*time.Second, "o1's ship after the lock ended", fromOrder("o1"), 1)
}

// Without --wait a second lock exits 75 at once with a one-line message; with
// --wait it runs its command once the first lock ends. Flags work before or
// after the resource.
func TestLockBusyExits75AndWaitRunsAfterTheFirstEnds(t *testing.T) {
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
	time.Sleep(time.Second)
	if second.exited() {
		t.Fatalf("a --wait lock exited while the unit was held: stderr %q", second.err.String())
	}
	if _, err := os.Stat(ran); err == nil {
		t.Fatalf("the --wait lock ran its command while the first lock held the unit")
	}
	if first.exited() {
		t.Fatalf("the first lock ended early: %q", first.err.String())
	}

	first.release()
	w.waitExit(first, "first")
	// The second command starts, and holds until released.
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

	// The issue's order: `admiral lock <resource> [--wait] -- <command…>`.
	r = w.admiral("lock", "phone", "--wait", "--port", w.in.Port, "--", "true")
	w.expectExit(r, 0, "flags after the resource")
}

// The exit codes, and that the command is run untouched, with its output passed
// through.
func TestLockExitCodesAndPassthrough(t *testing.T) {
	w := newWorld(t)
	w.startPhone()

	r := w.lockCmd("phone", "--", "sh", "-c", `echo "$1"; exit 3`, "sh", "--help")
	w.expectExit(r, 3, "the command's own status")
	if strings.TrimSpace(r.stdout) != "--help" {
		t.Errorf("stdout = %q, want the command's argument --help passed on, not taken as admiral's", r.stdout)
	}
	w.expectExit(w.lockCmd("phone", "--", "/nonexistent/tool"), 127, "a command that cannot start")
	if r := w.lockCmd("nosuch", "--", "true"); r.code != 1 || !strings.HasPrefix(r.stderr, "admiral: ") {
		t.Errorf("unknown resource: exit %d stderr %q, want 1 and an `admiral: …` line", r.code, r.stderr)
	}
	w.expectExit(w.lockCmd("--", "true"), 2, "no resource")
	w.expectExit(w.lockCmd("phone"), 2, "no command")
	if r := w.lockState(); r.InUse != 0 {
		t.Errorf("phone is still in use after the commands ended: %+v", r)
	}

	w.in.Stop()
	if r := w.lockCmd("phone", "--", "true"); r.code != 1 || !strings.HasPrefix(r.stderr, "admiral: ") {
		t.Errorf("no server: exit %d stderr %q, want 1 and an `admiral: …` line", r.code, r.stderr)
	}
}

// A killed `admiral lock` (SIGKILL, no chance to clean up) frees the unit: the
// dropped connection releases it.
func TestLockKilledReleasesTheUnit(t *testing.T) {
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

// The route itself: one JSON line, then the connection is the hold; a hostile
// label is cleaned (printable, 64 characters at most); closing releases.
func TestLockRouteHoldsForTheLifeOfTheConnection(t *testing.T) {
	w := newWorld(t)
	w.startPhone()

	label := "evil\x1b[31m\n" + strings.Repeat("x", 100)
	resp, err := http.Post(w.in.LocalURL+"/api/resources/phone/lock?label="+strings.NewReplacer("\x1b", "%1b", "\n", "%0a", "[", "%5b").Replace(label), "", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("POST /api/resources/phone/lock = %d, want 200", resp.StatusCode)
	}
	var first struct {
		Held bool `json:"held"`
	}
	line, err := bufio.NewReader(resp.Body).ReadString('\n')
	if err != nil || json.Unmarshal([]byte(line), &first) != nil || !first.Held {
		t.Fatalf("first line = %q (%v), want {\"held\": true}", line, err)
	}
	got := w.waitLock("held by the route", func(r lockRes) bool { return r.InUse == 1 })
	l := got.Holders[0].Label
	if got.Holders[0].Kind != "lock" || len(l) == 0 || len(l) > 64 || strings.IndexFunc(l, func(r rune) bool { return !unicode.IsPrint(r) }) >= 0 {
		t.Errorf("holder = %+v, want a lock with a printable label of 1 to 64 characters", got.Holders[0])
	}

	resp2, err := http.Post(w.in.LocalURL+"/api/resources/phone/lock", "", nil)
	if err != nil {
		t.Fatal(err)
	}
	resp2.Body.Close()
	if resp2.StatusCode != http.StatusConflict {
		t.Errorf("a second POST without wait = %d, want 409", resp2.StatusCode)
	}

	resp.Body.Close()
	w.waitLock("free after the connection closed", func(r lockRes) bool { return r.InUse == 0 })
}

// The route answers 403 to a peer that is not loopback, token or not. It skips
// when the Mac's LAN address does not answer (docs/agents/testing.md).
func TestLockRouteRefusesANonLoopbackPeer(t *testing.T) {
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
