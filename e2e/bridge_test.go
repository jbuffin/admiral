package e2e

import (
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"
)

// The bridge (u11, seam 1): the real binary, fake harnesses with cues, a
// browser played by a WebSocket client, and tmux for what the pane really did.
//
// The wire shapes below are what these tests assume. The brief leaves them to
// the implementer, so follow them or change them here and in the UI together.
//
//	GET /ws/bridge/<the ship's tmux session name>   (WebSocket; u1's auth and Origin checks apply)
//	  A session that is not a current ship (a gone ship, a strict prefix of a
//	  live one, "_admiral", anything unknown) is refused at the handshake with 404.
//
//	browser -> server, JSON text frames
//	  {"type":"join","cols":C,"rows":R}   first message; the browser's size; it becomes the active browser
//	  {"type":"resize","cols":C,"rows":R} a new size; makes the browser active (ignored while it is hidden)
//	  {"type":"active"}                   the browser was opened, tapped, clicked or typed in; makes it active
//	                                      at the size it last reported (ignored while hidden)
//	  {"type":"visible","visible":bool}   false: stop streaming to it; true: it rejoins like a joining browser
//	  {"type":"input","data":"..."}       bytes for the pane, exactly as given (control bytes as JSON escapes)
//
//	server -> browser
//	  JSON text frame {"type":"join", "screen":S, "scrollback":B, "cursor":{"x","y","visible"},
//	      "modes":{"alternate","application_cursor","bracketed_paste","mouse"}, "size":{"cols","rows"},
//	      "ship":{"status","exit_code","exit_signal"}}
//	    S is the visible screen and B the history above it (at most 2,000 lines), both from
//	    `capture-pane -p -e` (SGR colour codes kept, lines joined by "\n"). Sent once after
//	    `join`, and again whenever the browser reports visible after being hidden.
//	  binary frame: the pane's output bytes, decoded from %output (octal escapes undone), streamed after the join
//	  JSON text frame {"type":"size","cols":C,"rows":R}   the pane's size changed
//	  JSON text frame {"type":"ship","status":"underway|needs_you|anchored|sailing_off",
//	      "exit_code":N|null,"exit_signal":"KILL"|null}   the ship's state changed
//	  JSON text frame {"type":"refused","reason":"anchored"}   input to a ship that is not live
//
// The linger before the shared control client detaches is ADMIRAL_BRIDGE_IDLE
// (a Go duration, default 30s), set by the tests that watch it.

// liveFleet is one fleet whose fake reports working and then waits for input.
func liveFleet(t *testing.T, cues ...string) *world {
	t.Helper()
	w := newWorld(t)
	w.cueFleets(map[string][]string{"f": append([]string{"hook working"}, cues...)})
	return w
}

// S1: a joining browser gets the screen and the last 2,000 lines with colours,
// then the live stream.
func TestBridgeS1_JoinGetsScreenAndScrollbackThenStreams(t *testing.T) {
	w := liveFleet(t, "lines 3000")
	w.start()
	s := w.launchIn("f", "claude", "b1")
	w.waitFile(s.Session, "cues-done")
	w.in.waitStatusList(t, 3*time.Second, s.Session, "underway")

	// The fake did print coloured lines; a failure below is the bridge.
	pane := w.mustTmux("capture-pane", "-p", "-e", "-S", "-2000", "-t", w.target(s.Session))
	if !sgr.MatchString(pane) || !strings.Contains(stripSGR(pane), "line 3000") {
		t.Fatalf("setup: the fake's pane has no coloured lines up to line 3000:\n%.300s", pane)
	}

	b, j := w.joinBridge(t, s.Session, 100, 30)
	if !sgr.MatchString(j.Screen) || !sgr.MatchString(j.Scrollback) {
		t.Errorf("join carries no SGR colour codes (screen %.80q, scrollback %.80q)", j.Screen, j.Scrollback)
	}
	if !strings.Contains(stripSGR(j.Screen), "line 3000") {
		t.Errorf("the joined screen lacks the last printed line, 'line 3000': %.200q", stripSGR(j.Screen))
	}
	hist := strings.Split(strings.TrimRight(stripSGR(j.Scrollback), "\n"), "\n")
	if len(hist) != 2000 {
		t.Errorf("join scrollback has %d lines, want exactly 2000 (3000 lines were printed)", len(hist))
	}
	all := stripSGR(j.Scrollback) + stripSGR(j.Screen)
	if !strings.Contains(all, "line 2000") || strings.Contains(all, "line 0900") || strings.Contains(all, "line 0001") {
		t.Errorf("join should hold lines ~1000-3000 and nothing older (has 'line 2000': %v, 'line 0900': %v, 'line 0001': %v)",
			strings.Contains(all, "line 2000"), strings.Contains(all, "line 0900"), strings.Contains(all, "line 0001"))
	}
	if j.Modes.Alternate || j.Modes.ApplicationCursor || j.Modes.BracketedPaste || j.Modes.Mouse {
		t.Errorf("a plain shell-like pane joined with modes %+v, want none set", j.Modes)
	}
	if j.Size.Cols != 100 || j.Size.Rows != 30 {
		t.Errorf("join size = %dx%d, want the joining browser's 100x30", j.Size.Cols, j.Size.Rows)
	}
	if j.Ship.Status != "underway" {
		t.Errorf("join ship status = %q, want underway", j.Ship.Status)
	}

	// New output streams live, undecoded bytes intact: an escape, a backslash
	// (tmux sends both as octal) and two multibyte characters.
	mark := b.mark()
	marker := "\x1b[35mmagenta\x1b[0m back\\slash é✓ live-1\r\n"
	start := time.Now()
	w.paneWrite(s.Session, marker)
	b.waitOutput(2*time.Second, mark, marker)
	if lag := time.Since(start); lag > 500*time.Millisecond {
		t.Errorf("live output took %s to reach the browser on loopback, want no visible lag", lag)
	}
}

// S2: two browsers share one control client; the latest active one sizes the
// pane; input from either reaches the pane and clears needs-you; a hidden
// browser stops receiving and catches up on return.
func TestBridgeS2_TwoBrowsers(t *testing.T) {
	w := liveFleet(t, "hook needs_you")
	w.start()
	s := w.launchIn("f", "claude", "b2")
	w.waitFile(s.Session, "cues-done")
	w.in.waitStatusList(t, 3*time.Second, s.Session, "needs_you")

	a, _ := w.joinBridge(t, s.Session, 100, 30)
	w.waitPaneSize(t, s.Session, "100x30")
	b, jb := w.joinBridge(t, s.Session, 80, 24)
	w.waitPaneSize(t, s.Session, "80x24")
	if jb.Size.Cols != 80 || jb.Size.Rows != 24 {
		t.Errorf("the second browser's join size = %dx%d, want 80x24", jb.Size.Cols, jb.Size.Rows)
	}
	a.waitSize(3*time.Second, 0, 80, 24)
	if got := w.controlClients(s.Session); len(got) != 1 {
		t.Errorf("two browsers on one ship use %d tmux control clients (%v), want exactly 1 shared", len(got), got)
	}

	// The browser that last sent `active` or `resize` sets the size.
	ma, mb := a.mark(), b.mark()
	a.active()
	w.waitPaneSize(t, s.Session, "100x30")
	b.waitSize(3*time.Second, mb, 100, 30)
	b.active()
	w.waitPaneSize(t, s.Session, "80x24")
	a.resize(120, 40)
	w.waitPaneSize(t, s.Session, "120x40")
	a.waitSize(3*time.Second, ma, 120, 40)
	b.waitSize(3*time.Second, mb, 120, 40)

	// Input from either browser reaches the pane as given, with no lock, and
	// clears needs-you at once.
	b.input("y")
	w.waitStdin(t, s.Session, "y")
	if got := statusOf(w.in.ships(t), s.Session); got != "underway" {
		t.Errorf("right after browser B typed, GET status = %q, want underway", got)
	}
	w.setPaneState(s.Session, "needs_you")
	w.in.waitStatusList(t, 3*time.Second, s.Session, "needs_you")
	typed := "\x03\x1b[A\r é \t"
	a.input(typed)
	w.waitStdin(t, s.Session, "y"+typed)
	if got := statusOf(w.in.ships(t), s.Session); got != "underway" {
		t.Errorf("right after browser A typed, GET status = %q, want underway", got)
	}

	// A hidden browser stops receiving the stream and cannot become active.
	b.visible(false)
	time.Sleep(200 * time.Millisecond) // the server has taken the message
	mb = b.mark()
	ma = a.mark()
	b.resize(50, 20)
	b.active()
	w.paneWrite(s.Session, "while-hidden\r\n")
	a.waitOutput(2*time.Second, ma, "while-hidden") // A got it, so B's copy has had its chance to arrive
	time.Sleep(300 * time.Millisecond)
	if got := b.output(mb); got != "" {
		t.Errorf("a hidden browser still received output %s", quote(got))
	}
	if got := w.paneSize(s.Session); got != "120x40" {
		t.Errorf("a hidden browser changed the pane to %s, want it left at 120x40", got)
	}

	// Visible again, it rejoins like a joining browser: the whole screen again.
	b.visible(true)
	rj := b.waitJoin(3*time.Second, 2)
	if !strings.Contains(stripSGR(rj.Screen+rj.Scrollback), "while-hidden") {
		t.Errorf("the rejoin payload lacks what was printed while the browser was hidden: %.200q", stripSGR(rj.Screen))
	}
}

// S3: the control client lingers and detaches; a stale or foreign session name
// is refused; access follows u1's rules; a pane in the alternate screen joins
// with its modes.
func TestBridgeS3_ControlClientDetachesAfterTheLastBrowserLeaves(t *testing.T) {
	w := liveFleet(t)
	w.extra = append(w.extra, "ADMIRAL_BRIDGE_IDLE=2s")
	w.start()
	s := w.launchIn("f", "claude", "b3idle")
	w.waitFile(s.Session, "cues-done")

	if got := w.controlClients(s.Session); len(got) != 0 {
		t.Fatalf("before anyone opens the bridge %d control clients are attached, want 0", len(got))
	}
	a, _ := w.joinBridge(t, s.Session, 100, 30)
	w.waitPaneSize(t, s.Session, "100x30")
	first := w.controlClients(s.Session)
	if len(first) != 1 {
		t.Fatalf("with a browser on the bridge %d control clients are attached (%v), want 1", len(first), first)
	}

	a.close()
	time.Sleep(700 * time.Millisecond)
	if got := w.controlClients(s.Session); len(got) != 1 {
		t.Errorf("0.7s after the last browser left (idle linger 2s) %d control clients are attached, want it still attached", len(got))
	}
	// Coming back inside the linger reuses the same client.
	b, _ := w.joinBridge(t, s.Session, 100, 30)
	if got := w.controlClients(s.Session); len(got) != 1 || got[0] != first[0] {
		t.Errorf("rejoining inside the linger gave clients %v, want the same one (%v)", got, first)
	}
	b.close()

	deadline := time.Now().Add(6 * time.Second)
	for len(w.controlClients(s.Session)) != 0 {
		if time.Now().After(deadline) {
			t.Fatalf("the control client was still attached 6s after the last browser left (idle linger 2s)")
		}
		time.Sleep(100 * time.Millisecond)
	}
	if got := w.paneSize(s.Session); got != "100x30" {
		t.Errorf("after the detach the pane is %s, want it to keep its last size 100x30", got)
	}
}

func TestBridgeS3_OnlyCurrentShipsAreWatchable(t *testing.T) {
	w := liveFleet(t)
	w.start()
	s := w.launchIn("f", "claude", "b3stale")
	w.waitFile(s.Session, "cues-done")

	// tmux resolves a bare -t name to any session it is a prefix of. A name
	// that is only a prefix of a live ship must not reach it.
	prefix := s.Session[:len(s.Session)-1]
	expectBridgeRefused(t, w.in.LocalURL, prefix, "", nil, "", http.StatusNotFound)
	expectBridgeRefused(t, w.in.LocalURL, "_admiral", "", nil, "", http.StatusNotFound)
	expectBridgeRefused(t, w.in.LocalURL, "no-such-ship", "", nil, "", http.StatusNotFound)
	if got := w.controlClients(s.Session); len(got) != 0 {
		t.Errorf("refused bridges left control clients on the live ship: %v", got)
	}
	if got := w.stdinOf(s.Session); got != "" {
		t.Errorf("the live ship received %s from refused bridges", quote(got))
	}

	// The case in the brief: the session is gone and a longer name exists.
	// The impostor records what is typed into it.
	impostor := s.Session + "-ii"
	rec := w.rec + "/" + impostor + ".stdin"
	w.mustTmux("kill-session", "-t", w.target(s.Session))
	w.mustTmux("new-session", "-d", "-s", impostor, "stty raw -echo; exec cat -u >"+rec)
	w.in.waitShipsList(t, 5*time.Second, "the killed ship gone", func(l []shipJSON) bool { return statusOf(l, s.Session) == "" })
	expectBridgeRefused(t, w.in.LocalURL, s.Session, "", nil, "", http.StatusNotFound)
	if got := w.controlClients(impostor); len(got) != 0 {
		t.Errorf("a refused bridge attached to %s: %v", impostor, got)
	}
	time.Sleep(200 * time.Millisecond)
	if data, _ := readFile(rec); data != "" {
		t.Errorf("%s received %s from a bridge for the gone session", impostor, quote(data))
	}
}

func TestBridgeS3_AccessFollowsTheToken(t *testing.T) {
	w := liveFleet(t)
	in := w.startLAN(t)
	s := w.launchIn("f", "claude", "b3auth")
	w.waitFile(s.Session, "cues-done")

	t.Run("non-local without the token is refused", func(t *testing.T) {
		expectBridgeRefused(t, in.lanURL(), s.Session, "", nil, "", http.StatusUnauthorized)
		expectBridgeRefused(t, in.lanURL(), s.Session, "token=nope", nil, "", http.StatusUnauthorized)
	})
	connects := func(t *testing.T, query string, hdr http.Header) {
		t.Helper()
		b, resp, err := bridgeDial(t, in.lanURL(), s.Session, query, hdr, "")
		if err != nil {
			t.Fatalf("a non-local peer with the token should connect, got %v (err %v)", resp, err)
		}
		b.send(map[string]any{"type": "join", "cols": 80, "rows": 24})
		b.waitJoin(5*time.Second, 1)
	}
	t.Run("the cookie works", func(t *testing.T) {
		connects(t, "", http.Header{"Cookie": {in.cookieName() + "=" + in.Token}})
	})
	t.Run("?token= works", func(t *testing.T) {
		connects(t, "token="+url.QueryEscape(in.Token), nil)
	})
	t.Run("a local peer needs no token", func(t *testing.T) {
		b, resp, err := bridgeDial(t, in.LocalURL, s.Session, "", nil, "")
		if err != nil {
			t.Fatalf("a local peer should connect, got %v (err %v)", resp, err)
		}
		b.send(map[string]any{"type": "join", "cols": 80, "rows": 24})
		b.waitJoin(5*time.Second, 1)
	})
	t.Run("a foreign Origin is refused even from a local peer", func(t *testing.T) {
		expectBridgeRefused(t, in.LocalURL, s.Session, "", http.Header{"Origin": {"http://evil.example"}}, "", http.StatusForbidden)
	})
}

func TestBridgeS3_AlternateScreenJoinRestoresCursorAndModes(t *testing.T) {
	w := liveFleet(t, "lines 50", "altscreen")
	w.start()
	s := w.launchIn("f", "claude", "b3alt")
	w.waitFile(s.Session, "cues-done")

	// The fake did switch: tmux reports the alternate screen and the keypad flag.
	flags := w.mustTmux("display-message", "-p", "-t", w.target(s.Session), "#{alternate_on} #{keypad_cursor_flag} #{bracket_paste_flag}")
	if flags != "1 1 1" {
		t.Fatalf("setup: tmux reports alternate_on/keypad_cursor_flag/bracket_paste_flag = %q, want 1 1 1", flags)
	}
	_, j := w.joinBridge(t, s.Session, 100, 30)
	if !j.Modes.Alternate || !j.Modes.ApplicationCursor || !j.Modes.BracketedPaste || j.Modes.Mouse {
		t.Errorf("join modes = %+v, want alternate, application_cursor and bracketed_paste set and mouse not", j.Modes)
	}
	if j.Cursor.X != 10 || j.Cursor.Y != 0 {
		t.Errorf("join cursor = (%d,%d), want (10,0) after printing 'ALT SCREEN' at the top left", j.Cursor.X, j.Cursor.Y)
	}
	if !strings.Contains(stripSGR(j.Screen), "ALT SCREEN") {
		t.Errorf("the joined screen lacks 'ALT SCREEN': %.200q", stripSGR(j.Screen))
	}
	// Whether the join also carries the normal screen's history is an open
	// question (tmux's capture-pane -S -2000 returns it with the alternate
	// screen), so it is not pinned here.
}

// S4: the ship's exit reaches an open bridge; an anchored ship is read-only.
func TestBridgeS4_ExitReachesTheBridge(t *testing.T) {
	w := newWorld(t)
	w.cueFleets(map[string][]string{
		"zero":  {"hook working", "wait go", "exit 0"},
		"three": {"hook working", "lines 3", "wait go", "exit 3"},
	})
	w.start()

	z := w.launchIn("zero", "claude", "b4zero")
	w.waitFile(z.Session, "recorded")
	w.in.waitStatusList(t, 3*time.Second, z.Session, "underway")
	zb, zj := w.joinBridge(t, z.Session, 100, 30)
	if zj.Ship.Status != "underway" {
		t.Errorf("join ship status = %q, want underway", zj.Ship.Status)
	}
	mark := zb.mark()
	w.release(z.Session, "go")
	zb.waitShip(6*time.Second, mark, "status sailing_off after exit 0", func(s shipState) bool { return s.Status == "sailing_off" })

	a := w.launchIn("three", "claude", "b4three")
	w.waitFile(a.Session, "recorded")
	w.in.waitStatusList(t, 3*time.Second, a.Session, "underway")
	ab, _ := w.joinBridge(t, a.Session, 100, 30)
	mark = ab.mark()
	w.release(a.Session, "go")
	got := ab.waitShip(6*time.Second, mark, "status anchored after exit 3", func(s shipState) bool { return s.Status == "anchored" })
	if got.ExitCode == nil || *got.ExitCode != 3 || got.ExitSignal != nil {
		t.Errorf("anchored ship state = %+v, want exit_code 3 and exit_signal null", got)
	}
	mark = ab.mark()
	ab.input("x")
	ab.waitFor(3*time.Second, `a {"type":"refused","reason":"anchored"} message for input to an anchored ship`, func() bool {
		for _, raw := range ab.texts(mark, "refused") {
			if strings.Contains(string(raw), `"anchored"`) {
				return true
			}
		}
		return false
	})

	// Joining an anchored ship gives its frozen last screen and refuses input.
	late, lj := w.joinBridge(t, a.Session, 100, 30)
	if lj.Ship.Status != "anchored" || lj.Ship.ExitCode == nil || *lj.Ship.ExitCode != 3 {
		t.Errorf("join of an anchored ship has ship %+v, want anchored with exit_code 3", lj.Ship)
	}
	if !strings.Contains(stripSGR(lj.Screen+lj.Scrollback), "line 0003") {
		t.Errorf("the anchored ship's frozen screen lacks its last output 'line 0003': %.200q", stripSGR(lj.Screen))
	}
	mark = late.mark()
	late.input("x")
	late.waitFor(3*time.Second, "a refusal of input to the anchored ship", func() bool { return len(late.texts(mark, "refused")) > 0 })
}

// titleOf reads `title` of one ship from GET /api/ships; the bool is false when
// the ship has no such field.
func (in *fleetInst) titleOf(t *testing.T, session string) (string, bool) {
	t.Helper()
	code, body := send(t, http.MethodGet, in.LocalURL+"/api/ships", nil)
	if code != 200 {
		t.Fatalf("GET /api/ships = %d: %s", code, body)
	}
	var wrap struct {
		Ships []struct {
			Session string  `json:"session"`
			Title   *string `json:"title"`
		} `json:"ships"`
	}
	if err := json.Unmarshal([]byte(body), &wrap); err != nil {
		t.Fatalf("ship list is not JSON: %v\n%s", err, body)
	}
	for _, s := range wrap.Ships {
		if s.Session == session {
			if s.Title == nil {
				return "", false
			}
			return *s.Title, true
		}
	}
	t.Fatalf("ship %s is not listed: %s", session, body)
	return "", false
}

// The order's first line rides the ship as `title` (the eleventh session
// option @admiral-title), survives a restart, and is empty without a source.
func TestBridgeTitleIsTheOrdersFirstLineAndSurvivesARestart(t *testing.T) {
	w := liveFleet(t)
	w.startFresh()
	w.waitSource("orders", 10*time.Second, "state ok", isOK)
	panel := w.panelID("orders")
	code, body := w.srcReq(http.MethodPost, "/orders/orders", map[string]any{"panel": panel, "text": "Fix the flaky test\n\nsee the CI log"})
	w.mustOK(code, body, "add order")
	s := w.waitSource("orders", 5*time.Second, "the order", func(s sourceJSON) bool { return len(s.orderIDs()) == 1 })
	ship := w.mustLaunchFrom("f", "orders", s.orderIDs()[0])
	w.waitFile(ship.Session, "cues-done")

	if got, ok := w.in.titleOf(t, ship.Session); !ok || got != "Fix the flaky test" {
		t.Errorf("GET /api/ships title = %q (present %v), want the order's first line", got, ok)
	}
	if got := w.mustTmux("show-options", "-v", "-t", w.target(ship.Session), "@admiral-title"); got != "Fix the flaky test" {
		t.Errorf("session option @admiral-title = %q, want the order's first line", got)
	}
	plain := w.launchIn("f", "claude", "b-notitle")
	if got, ok := w.in.titleOf(t, plain.Session); !ok || got != "" {
		t.Errorf("a launch with no source has title %q (present %v), want an empty string", got, ok)
	}

	w.restart()
	w.in.waitShipsList(t, 5*time.Second, "both ships after the restart", func(l []shipJSON) bool { return len(l) == 2 })
	if got, ok := w.in.titleOf(t, ship.Session); !ok || got != "Fix the flaky test" {
		t.Errorf("after a restart the title is %q (present %v), want it rebuilt from the session option", got, ok)
	}
}
