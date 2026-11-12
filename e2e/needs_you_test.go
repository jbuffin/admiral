package e2e

import (
	"bytes"
	"encoding/json"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/coder/websocket"
)

// FAKE_CUE is one file per fleet, so a test that needs different cues for
// different ships gives each its own fleet (cueFleets).

// The raw fields keep "null" apart from "missing".
type u5Ship struct {
	shipJSON
	ExitCode   json.RawMessage `json:"exit_code"`
	ExitSignal json.RawMessage `json:"exit_signal"`
}

func (in *fleetInst) u5Ships(t *testing.T) []u5Ship {
	t.Helper()
	code, body := send(t, http.MethodGet, in.LocalURL+"/api/ships", nil)
	if code != 200 {
		t.Fatalf("GET /api/ships = %d: %s", code, body)
	}
	var wrap struct {
		Ships []u5Ship `json:"ships"`
	}
	if err := json.Unmarshal([]byte(body), &wrap); err != nil {
		t.Fatalf("ship list is not JSON: %v\n%s", err, body)
	}
	return wrap.Ships
}

func (in *fleetInst) u5Ship(t *testing.T, session string) (u5Ship, bool) {
	t.Helper()
	for _, s := range in.u5Ships(t) {
		if s.Session == session {
			return s, true
		}
	}
	return u5Ship{}, false
}

// A nil value means the fleet has no cue file at all.
func (w *world) cueFleets(cues map[string][]string) {
	w.t.Helper()
	fleets := map[string]any{}
	for key, lines := range cues {
		path := filepath.Join(w.root, "cue-"+key)
		if lines != nil {
			if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
				w.t.Fatal(err)
			}
		}
		env := w.harnessEnv()
		env["FAKE_CUE"] = path
		dock := func(h string) map[string]any { return map[string]any{"executable": w.fakeExe(h), "env": env} }
		fleets[key] = map[string]any{"name": key, "color": "#336699", "search_roots": []string{w.work},
			"claude": dock("claude"), "codex": dock("codex"), "copilot": dock("copilot")}
	}
	b, err := json.Marshal(map[string]any{"version": 1, "fleets": fleets})
	if err != nil {
		w.t.Fatal(err)
	}
	writeConfig(w.t, w.home, string(b))
}

func statusOf(list []shipJSON, session string) string {
	for _, s := range list {
		if s.Session == session {
			return s.Status
		}
	}
	return ""
}

func waitStatus(t *testing.T, c *websocket.Conn, d time.Duration, session, status string) {
	t.Helper()
	waitShipsMsg(t, c, d, "ship "+session+" with status "+status, func(l []shipJSON) bool {
		return statusOf(l, session) == status
	})
}

func (in *fleetInst) waitStatusList(t *testing.T, d time.Duration, session, status string) {
	t.Helper()
	in.waitShipsList(t, d, "ship "+session+" with status "+status, func(l []shipJSON) bool {
		return statusOf(l, session) == status
	})
}

func (w *world) paneState(session string) string {
	w.t.Helper()
	out, err := w.tmux("show-options", "-t", session, "-p", "-v", "@admiral-state")
	if err != nil {
		w.t.Fatalf("reading @admiral-state of %s: %v\n%s", session, err, out)
	}
	return out
}

func (w *world) paneReason(session string) string {
	w.t.Helper()
	out, err := w.tmux("show-options", "-t", session, "-p", "-qv", "@admiral-reason")
	if err != nil {
		w.t.Fatalf("reading @admiral-reason of %s: %v\n%s", session, err, out)
	}
	return out
}

func (w *world) waitPaneReason(session, want string) {
	w.t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for w.paneReason(session) != want {
		if time.Now().After(deadline) {
			w.t.Fatalf("@admiral-reason of %s = %q after 5s, want %q", session, w.paneReason(session), want)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func (w *world) setPaneState(session, state string) {
	w.t.Helper()
	w.mustTmux("set-option", "-p", "-t", session, "@admiral-state", state)
}

func (w *world) inputSeen(session string) int {
	w.t.Helper()
	code, _ := send(w.t, http.MethodPost, w.in.LocalURL+"/api/ships/"+session+"/input-seen", nil)
	return code
}

func (w *world) dismiss(session string) int {
	w.t.Helper()
	code, _ := send(w.t, http.MethodPost, w.in.LocalURL+"/api/ships/"+session+"/dismiss", nil)
	return code
}

func (w *world) hasSession(session string) bool {
	for _, s := range w.sessions() {
		if s == session {
			return true
		}
	}
	return false
}

func (w *world) launchIn(fleet, harness, repo string) shipJSON {
	w.t.Helper()
	return w.launch(w.order(fleet, harness, repo, noSkill, "x"))
}

func TestHooksSetNeedsYouAndUnderway(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	const engineerLine = `printf '\033[32mshipshape\033[0m\nsecond line'`
	w.writeRaw(filepath.Join(w.userHom, ".claude", "settings.json"),
		`{"statusLine":{"type":"command","command":`+quote(engineerLine)+`}}`, 0o600)
	raw, err := os.ReadFile("../internal/cli/testdata/claude-statusline.json")
	if err != nil {
		t.Fatal(err)
	}
	var status bytes.Buffer
	if err := json.Compact(&status, raw); err != nil {
		t.Fatal(err)
	}
	w.cueFleets(map[string][]string{"f": {"hook needs_you", "sleep 3", "hook working"}, "sl": {"hook working", "statusline " + status.String(), "sleep 3600"}})
	w.start()
	ws := dialWS(t, w.in.instance)
	for _, h := range []string{"claude", "codex", "copilot"} {
		s := w.launchIn("f", h, "s1"+h)
		w.waitFile(s.Session, "recorded")
		waitStatus(t, ws, 2*time.Second, s.Session, "needs_you")
		waitStatus(t, ws, 5*time.Second, s.Session, "underway")
		if got := statusOf(w.in.ships(t), s.Session); got != "underway" {
			t.Errorf("%s ship after its working hook: GET status = %q, want underway", h, got)
		}
	}

	// The status line Claude runs is Admiral's wrapper, which passes the engineer's own line through
	// and keeps the usage fields on the pane.
	sl := w.launchIn("sl", "claude", "s1status")
	w.waitFile(sl.Session, "statusline-done")
	if e := w.readFile(filepath.Join(w.rec, sl.Session, "statusline-error")); e != "" {
		t.Fatalf("the fake could not run the status line: %s", e)
	}
	if got, want := w.readFile(filepath.Join(w.rec, sl.Session, "statusline-stdout")), "\x1b[32mshipshape\x1b[0m\nsecond line"; got != want {
		t.Errorf("status line output = %q, want the engineer's own line through Admiral's wrapper: %q", got, want)
	}
	deadline := time.Now().Add(5 * time.Second)
	var kept string
	for kept == "" && time.Now().Before(deadline) {
		kept, _ = w.tmux("show-options", "-t", sl.Session, "-p", "-qv", "@admiral-statusline")
		time.Sleep(50 * time.Millisecond)
	}
	var m struct {
		SessionID string `json:"session_id"`
		Cost      struct {
			Total float64 `json:"total_cost_usd"`
		} `json:"cost"`
		RateLimits struct {
			FiveHour struct {
				Used float64 `json:"used_percentage"`
			} `json:"five_hour"`
		} `json:"rate_limits"`
	}
	if err := json.Unmarshal([]byte(kept), &m); err != nil || m.SessionID != "5b1f0c2e-7a44-4a1e-9d0a-1c2d3e4f5a6b" || m.Cost.Total != 1.2345 || m.RateLimits.FiveHour.Used != 23.5 {
		t.Errorf("@admiral-statusline = %q (%v), want the sample's session, cost and five-hour quota", kept, err)
	}
	for _, banned := range []string{"session_name", "fake-prompt-text-xyzzy", "prompt_id"} {
		if strings.Contains(kept, banned) {
			t.Errorf("@admiral-statusline holds %q: %s", banned, kept)
		}
	}
}

// attachControlClient holds `tmux -C attach` open for the session until the
// test ends, the way the bridge does.
func (w *world) attachControlClient(session string) {
	w.t.Helper()
	cmd := exec.Command("tmux", "-S", w.sock(), "-C", "attach", "-t", session)
	cmd.Env = w.baseEnv()
	stdin, err := cmd.StdinPipe()
	if err != nil {
		w.t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		w.t.Fatalf("attach control client: %v", err)
	}
	w.t.Cleanup(func() { _ = stdin.Close(); _ = cmd.Process.Kill(); _ = cmd.Wait() })
}

func TestCodexBellNeedsYou(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	w.cueFleets(map[string][]string{
		"bell":    {"hook working", "sleep 1", "bell", "wait go", "hook working"},
		"attach":  {"hook working", "wait go", "bell", "sleep 3600"},
		"nocodex": {"hook working", "sleep 1", "bell", "hook needs_you"},
	})
	w.start()
	ws := dialWS(t, w.in.instance)

	c := w.launchIn("bell", "codex", "s2bell")
	waitStatus(t, ws, 4*time.Second, c.Session, "needs_you")
	if got := w.paneState(c.Session); got != "needs_you" {
		t.Errorf("after a Codex bell @admiral-state = %q, want needs_you", got)
	}
	w.waitPaneReason(c.Session, "bell")
	w.touch(c.Session, "go")
	waitStatus(t, ws, 5*time.Second, c.Session, "underway")
	if got := w.paneState(c.Session); got != "working" {
		t.Errorf("after the working hook @admiral-state = %q, want working", got)
	}

	a := w.launchIn("attach", "codex", "s2attach")
	w.waitFile(a.Session, "recorded")
	w.attachControlClient(a.Session)
	w.touch(a.Session, "go")
	waitStatus(t, ws, 5*time.Second, a.Session, "needs_you")

	// The Claude ship's needs_you hook comes after its bell, so once the ship
	// shows needs_you the bell has been read too: it must not have set reason bell.
	cl := w.launchIn("nocodex", "claude", "s2claude")
	waitStatus(t, ws, 5*time.Second, cl.Session, "needs_you")
	if got := w.paneReason(cl.Session); got == "bell" {
		t.Errorf("a Claude bell was taken as needs you: @admiral-reason = %q", got)
	}
}

// noHookWait stands in for NoHookWait (10 s in production).
const noHookWait = 4 * time.Second

func TestNoFirstHook(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	w.extra = append(w.extra, "ADMIRAL_NO_HOOK_WAIT="+noHookWait.String())
	w.cueFleets(map[string][]string{"none": nil, "late": {"sleep 1", "hook working", "sleep 3600"}})
	w.start()
	ws := dialWS(t, w.in.instance)

	t0 := time.Now()
	quiet := w.launchIn("none", "claude", "s3none")
	busy := w.launchIn("late", "claude", "s3late")
	waitStatus(t, ws, noHookWait+2*time.Second, quiet.Session, "needs_you")
	if el := time.Since(t0); el < noHookWait-time.Second {
		t.Errorf("a ship with no hook became needs_you after %s, want about %s", el, noHookWait)
	}
	if got := statusOf(w.in.ships(t), busy.Session); got != "underway" {
		t.Errorf("a ship that fired working at 1s has status %q past %s, want underway", got, noHookWait)
	}

	if code := w.inputSeen(quiet.Session); code/100 != 2 {
		t.Fatalf("input-seen = %d, want 2xx", code)
	}
	holdThat(t, "after input-seen the no-hook ship went back to needs_you", func() bool {
		return statusOf(w.in.ships(t), quiet.Session) == "underway"
	})
}

func TestNoFirstHookAfterRestart(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	w.extra = append(w.extra, "ADMIRAL_NO_HOOK_WAIT="+noHookWait.String())
	w.cueFleets(map[string][]string{"none": nil})
	w.start()
	s := w.launchIn("none", "claude", "s3restart")
	w.in.Stop()
	time.Sleep(noHookWait + 500*time.Millisecond)
	w.restart()
	w.in.waitStatusList(t, 15*time.Second, s.Session, "needs_you")
}

func TestInputSeenClearsNeedsYou(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	w.cueFleets(map[string][]string{"f": {"hook needs_you"}})
	w.start()
	s := w.launchIn("f", "claude", "s4")
	w.waitFile(s.Session, "cues-done")
	w.in.waitStatusList(t, 3*time.Second, s.Session, "needs_you")
	w.mustTmux("set-option", "-p", "-t", s.Session, "@admiral-reason", "stop")

	if code := w.inputSeen(s.Session); code/100 != 2 {
		t.Fatalf("input-seen = %d, want 2xx", code)
	}
	if got := w.paneReason(s.Session); got != "" {
		t.Errorf("after input-seen @admiral-reason = %q, want it cleared", got)
	}
	if got := statusOf(w.in.ships(t), s.Session); got != "underway" {
		t.Errorf("right after input-seen GET status = %q, want underway", got)
	}
	if got := w.paneState(s.Session); got != "working" {
		t.Errorf("after input-seen @admiral-state = %q, want working", got)
	}
	if code := w.inputSeen(s.Session); code/100 != 2 {
		t.Errorf("input-seen on an underway ship = %d, want 2xx", code)
	}
	if code := w.inputSeen("nosuchship"); code != http.StatusNotFound {
		t.Errorf("input-seen on an unknown ship = %d, want 404", code)
	}

	ws := dialWS(t, w.in.instance)
	w.setPaneState(s.Session, "needs_you")
	waitStatus(t, ws, 2*time.Second, s.Session, "needs_you")
}

func TestExitSailsOffOrAnchors(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	w.cueFleets(map[string][]string{
		"zero":   {"hook working", "wait go", "exit 0"},
		"three":  {"hook working", "exit 3"},
		"signal": {"hook working"},
		"waited": {"hook needs_you", "wait go", "exit 3"},
	})
	w.start()
	ws := dialWS(t, w.in.instance)

	// Exit 0.
	z := w.launchIn("zero", "claude", "s5zero")
	pos := map[string]float64{"x": 0.4, "y": 0.6}
	if code, body := send(t, http.MethodPut, w.in.LocalURL+"/api/ships/"+z.Session+"/position", pos); code/100 != 2 {
		t.Fatalf("PUT position = %d: %s", code, body)
	}
	statePath := filepath.Join(w.home, "state.json")
	waitStateFile(t, statePath, func(s string) bool { return strings.Contains(s, z.Session) })
	w.waitFile(z.Session, "recorded")
	w.touch(z.Session, "go")
	waitStatus(t, ws, 4*time.Second, z.Session, "sailing_off")
	seen := time.Now()
	if got := statusOf(w.in.ships(t), z.Session); got != "sailing_off" {
		t.Errorf("GET status of an exit-0 ship = %q, want sailing_off", got)
	}
	waitShipsMsg(t, ws, 6*time.Second, "the exit-0 ship gone", func(l []shipJSON) bool { return statusOf(l, z.Session) == "" })
	if el := time.Since(seen); el < sailOff/2 {
		t.Errorf("the ship sailed off after only %s, want it shown sailing_off for about %s", el, sailOff)
	}
	if w.hasSession(z.Session) {
		t.Errorf("session %s still exists after the ship sailed off", z.Session)
	}
	waitStateFile(t, statePath, func(s string) bool { return !strings.Contains(s, z.Session) })

	// Exit 3.
	a := w.launchIn("three", "claude", "s5three")
	waitStatus(t, ws, 4*time.Second, a.Session, "anchored")
	time.Sleep(sailOff + 500*time.Millisecond) // past the sail-off an exit 0 would get
	got, ok := w.in.u5Ship(t, a.Session)
	if !ok || got.Status != "anchored" || string(got.ExitCode) != "3" || string(got.ExitSignal) != "null" {
		t.Errorf("exit-3 ship = %+v (code %s, signal %s), want anchored, exit_code 3, exit_signal null", got.shipJSON, got.ExitCode, got.ExitSignal)
	}
	if !w.hasSession(a.Session) {
		t.Errorf("session of an anchored ship is gone")
	}

	// Killed by SIGKILL.
	k := w.launchIn("signal", "codex", "s5kill")
	w.waitFile(k.Session, "cues-done")
	pid := strings.TrimSpace(w.mustTmux("list-panes", "-t", k.Session, "-F", "#{pane_pid}"))
	var n int
	for _, c := range pid {
		n = n*10 + int(c-'0')
	}
	if err := syscall.Kill(n, syscall.SIGKILL); err != nil {
		t.Fatalf("kill pane process %s: %v", pid, err)
	}
	waitStatus(t, ws, 4*time.Second, k.Session, "anchored")
	got, _ = w.in.u5Ship(t, k.Session)
	if string(got.ExitCode) != "null" || string(got.ExitSignal) != `"KILL"` {
		t.Errorf("killed ship exit_code %s, exit_signal %s, want null and \"KILL\"", got.ExitCode, got.ExitSignal)
	}

	// A ship that was waiting when it exited is anchored, not needs_you.
	n3 := w.launchIn("waited", "claude", "s5wait")
	waitStatus(t, ws, 3*time.Second, n3.Session, "needs_you")
	w.waitFile(n3.Session, "recorded")
	w.touch(n3.Session, "go")
	waitStatus(t, ws, 4*time.Second, n3.Session, "anchored")
	if code := w.inputSeen(n3.Session); code != http.StatusConflict {
		t.Errorf("input-seen on an anchored ship = %d, want 409", code)
	}
	if got := w.paneState(n3.Session); got != "needs_you" {
		t.Errorf("input-seen on an anchored ship wrote @admiral-state %q, want it left at needs_you", got)
	}
}

func TestDismiss(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	w.cueFleets(map[string][]string{"dead": {"exit 3"}, "live": {"hook working"}})
	w.start()
	d := w.launchIn("dead", "claude", "s6dead")
	l := w.launchIn("live", "claude", "s6live")
	w.in.waitStatusList(t, 4*time.Second, d.Session, "anchored")
	send(t, http.MethodPut, w.in.LocalURL+"/api/ships/"+d.Session+"/position", map[string]float64{"x": 0.1, "y": 0.2})
	statePath := filepath.Join(w.home, "state.json")
	waitStateFile(t, statePath, func(s string) bool { return strings.Contains(s, d.Session) })

	if code := w.dismiss(l.Session); code/100 != 4 {
		t.Errorf("Dismiss on an underway ship = %d, want 4xx", code)
	}
	if !w.hasSession(l.Session) || statusOf(w.in.ships(t), l.Session) == "" {
		t.Errorf("Dismiss on an underway ship removed it")
	}
	if code := w.dismiss("nosuchship"); code != http.StatusNotFound {
		t.Errorf("Dismiss on an unknown ship = %d, want 404", code)
	}

	if code := w.dismiss(d.Session); code/100 != 2 {
		t.Fatalf("Dismiss on an anchored ship = %d, want 2xx", code)
	}
	if w.hasSession(d.Session) {
		t.Errorf("session %s still exists after Dismiss", d.Session)
	}
	if got := statusOf(w.in.ships(t), d.Session); got != "" {
		t.Errorf("the dismissed ship is still listed as %q", got)
	}
	waitStateFile(t, statePath, func(s string) bool { return !strings.Contains(s, d.Session) })
}

func TestRestartKeepsAnchoredAndNeedsYou(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	w.cueFleets(map[string][]string{
		"anchor": {"hook working", "exit 3"},
		"hook":   {"hook needs_you"},
		"bell":   {"hook working", "sleep 1", "bell", "sleep 3600"},
		"busy":   {"hook working"},
	})
	w.start()
	a := w.launchIn("anchor", "claude", "s7a")
	h := w.launchIn("hook", "claude", "s7h")
	b := w.launchIn("bell", "codex", "s7b")
	u := w.launchIn("busy", "claude", "s7u")
	want := map[string]string{a.Session: "anchored", h.Session: "needs_you", b.Session: "needs_you", u.Session: "underway"}
	// One poll for all four: a WebSocket wait per ship would hang on a slow
	// machine when an earlier wait had already read the message for a later ship.
	w.in.waitShipsList(t, 20*time.Second, "all four ships in their statuses", func(l []shipJSON) bool {
		for s, st := range want {
			if statusOf(l, s) != st {
				return false
			}
		}
		return true
	})
	w.in.Stop()
	w.restart()
	w.in.waitShipsList(t, 15*time.Second, "all four ships as before the restart", func(l []shipJSON) bool {
		for s, st := range want {
			if statusOf(l, s) != st {
				return false
			}
		}
		return true
	})
	got, _ := w.in.u5Ship(t, a.Session)
	if string(got.ExitCode) != "3" {
		t.Errorf("anchored ship after restart has exit_code %s, want 3", got.ExitCode)
	}
	if st := w.paneState(b.Session); st != "needs_you" {
		t.Errorf("the Codex bell ship's @admiral-state after restart = %q, want needs_you", st)
	}
}

func TestRestartKillsShipThatSailedOff(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	w.cueFleets(map[string][]string{"zero": {"hook working", "wait go", "exit 0"}})
	w.start()
	s := w.launchIn("zero", "claude", "s8zero")
	w.waitFile(s.Session, "recorded") // the harness is up and waiting
	w.in.Stop()
	// The ship exits only now, so Admiral's stop can't race its exit.
	w.touch(s.Session, "go")
	w.waitPaneDead(s.Session)
	if !w.hasSession(s.Session) {
		t.Fatalf("setup: the dead pane's session should survive Admiral being stopped")
	}
	w.restart()
	deadline := time.Now().Add(15 * time.Second)
	for w.hasSession(s.Session) {
		if time.Now().After(deadline) {
			t.Fatalf("session %s of an exit-0 ship still exists 15s after the restart", s.Session)
		}
		time.Sleep(100 * time.Millisecond)
	}
	w.in.waitShipsList(t, 15*time.Second, "no ships", func(l []shipJSON) bool { return len(l) == 0 })
}
