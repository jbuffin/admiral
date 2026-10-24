package e2e

import (
	"net/http"
	"os"
	"strings"
	"testing"
	"time"
)

// The bridge on a crew boat's pane (#152 "Bridge", spec #72 stories 15 and 18):
// GET /ws/bridge/<session>/crew/<name> speaks the ship bridge's protocol for the
// crew boat's pane and window, and the ship's bridge keeps to the ship's own
// pane and window once the session has crew windows. The seam is the built
// binary on a private tmux socket with the fake harness.
//
// Every ship and crew boat runs the one fleet cue (`hook working`, `wait go`,
// `exit 1`), so a test releases the ship alone or one crew boat alone with
// releaseGo, and a released one anchors with exit 1. A pane's own output comes
// from writing to its terminal (paneWriteID), and input shows up as the
// terminal's echo, since the fake is parked in a cue and not reading stdin.

// winOf is the window id (@N) of a pane.
func (w *world) winOf(pane string) string {
	w.t.Helper()
	return strings.TrimSpace(w.mustTmux("display-message", "-p", "-t", pane, "#{window_id}"))
}

// winSize is #{window_width}x#{window_height} of a window id.
func (w *world) winSize(win string) string {
	w.t.Helper()
	return strings.TrimSpace(w.mustTmux("display", "-p", "-t", win, "#{window_width}x#{window_height}"))
}

// waitWinSize waits for a window to reach a size.
func (w *world) waitWinSize(win, want string) {
	w.t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		got, err := w.tmux("display", "-p", "-t", win, "#{window_width}x#{window_height}")
		if err == nil && got == want {
			return
		}
		if time.Now().After(deadline) {
			w.t.Fatalf("within 5s window %s never became %s (it is %s)", win, want, got)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// expectWinSizes requires each window to hold its size, after a short settle so
// a resize that is still on its way would show.
func (w *world) expectWinSizes(why string, want map[string]string) {
	w.t.Helper()
	time.Sleep(400 * time.Millisecond)
	for win, size := range want {
		if got := w.winSize(win); got != size {
			w.t.Errorf("%s: window %s is %s, want it left at %s", why, win, got, size)
		}
	}
}

// paneWriteID writes to a pane's terminal by pane id.
func (w *world) paneWriteID(pane, data string) {
	w.t.Helper()
	tty := strings.TrimSpace(w.mustTmux("display-message", "-p", "-t", pane, "#{pane_tty}"))
	f, err := os.OpenFile(tty, os.O_WRONLY, 0)
	if err != nil {
		w.t.Fatalf("opening the terminal %s of pane %s: %v", tty, pane, err)
	}
	defer f.Close()
	if _, err := f.WriteString(data); err != nil {
		w.t.Fatal(err)
	}
}

// capture is a pane's visible text.
func (w *world) capture(pane string) string {
	out, _ := w.tmux("capture-pane", "-p", "-t", pane)
	return out
}

func (w *world) waitCapture(pane, want string) {
	w.t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !strings.Contains(w.capture(pane), want) {
		if time.Now().After(deadline) {
			w.t.Fatalf("within 5s pane %s never showed %q; it shows %q", pane, want, w.capture(pane))
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func (w *world) waitOpt(pane, name, want string) {
	w.t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for w.opt(pane, name) != want {
		if time.Now().After(deadline) {
			w.t.Fatalf("within 5s pane %s never got %s=%q (it is %q)", pane, name, want, w.opt(pane, name))
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// dialCrew joins the bridge on a crew boat, failing the test if it is refused.
func (w *world) dialCrew(t *testing.T, session, crew string, cols, rows int) (*bridgeClient, joinMsg) {
	t.Helper()
	return w.joinBridge(t, session+"/crew/"+crew, cols, rows)
}

func TestCrewBridge(t *testing.T) {
	w := newWorld(t)
	w.cueFleets(map[string][]string{"one": {"hook working", "wait go", "exit 1"}})
	w.start()
	ship, shipPane := w.crewShip("one", "alpha")
	a := w.crewBoat(ship.Session, "scout", "claude")
	b := w.crewBoat(ship.Session, "cook", "claude")
	shipWin, aWin, bWin := w.winOf(shipPane), w.winOf(a.pane), w.winOf(b.pane)

	t.Run("a new ship bridge follows the ship after the current window moves to a crew window", func(t *testing.T) {
		// First, while no bridge is open: a bridge already open keeps the pane it
		// resolved, which would hide the bug.
		w := w.at(t)
		w.mustTmux("select-window", "-t", "="+ship.Session+":"+aWin)
		w.paneWriteID(shipPane, "SHIP-SCREEN-MARK\r\n")
		sb, j := w.joinBridge(t, ship.Session, 120, 40)
		sb.active()
		if !strings.Contains(j.Screen, "SHIP-SCREEN-MARK") {
			t.Errorf("after select-window to a crew window the ship's bridge joined the wrong pane; screen %q", j.Screen)
		}
		m := sb.mark()
		w.paneWriteID(shipPane, "SHIP-LIVE-MARK\r\n")
		w.paneWriteID(a.pane, "CREW-LIVE-MARK\r\n")
		sb.waitOutput(5*time.Second, m, "SHIP-LIVE-MARK")
		if strings.Contains(sb.output(m), "CREW-LIVE-MARK") {
			t.Errorf("the ship's bridge streamed the crew pane after select-window")
		}
		w.waitWinSize(shipWin, "120x40")
		sb.input("ship-typed-after-select")
		w.waitCapture(shipPane, "ship-typed-after-select")
		if strings.Contains(w.capture(a.pane), "ship-typed-after-select") {
			t.Errorf("the ship's input reached the crew pane after select-window")
		}
		w.mustTmux("select-window", "-t", "="+ship.Session+":"+shipWin)
	})

	t.Run("a resize on the ship's bridge leaves the crew windows' sizes alone", func(t *testing.T) {
		// Broken today: refresh-client -C sizes the whole client, and every window
		// of the session follows it. This needs no crew bridge, so it stands alone.
		w := w.at(t)
		cur := map[string]string{aWin: w.winSize(aWin), bWin: w.winSize(bWin)}
		sb, _ := w.joinBridge(t, ship.Session, 110, 35)
		sb.active()
		w.waitWinSize(shipWin, "110x35")
		w.expectWinSizes("a join on the ship's bridge", cur)
		sb.resize(112, 36)
		w.waitWinSize(shipWin, "112x36")
		w.expectWinSizes("a resize on the ship's bridge", cur)
	})

	t.Run("join gets the crew pane's screen and only its output", func(t *testing.T) {
		w := w.at(t)
		w.paneWriteID(a.pane, "SCREEN-OF-SCOUT\r\n")
		scout, j := w.dialCrew(t, ship.Session, "scout", 80, 24)
		if !strings.Contains(j.Screen, "SCREEN-OF-SCOUT") {
			t.Errorf("the join screen of the crew boat should hold its own pane's text, got %q", j.Screen)
		}
		if j.Ship.Status != "underway" {
			t.Errorf("the join's ship object describes the crew boat: status %q, want underway", j.Ship.Status)
		}
		m := scout.mark()
		w.paneWriteID(shipPane, "OUT-SHIP\r\n")
		w.paneWriteID(b.pane, "OUT-COOK\r\n")
		w.paneWriteID(a.pane, "OUT-SCOUT\r\n")
		scout.waitOutput(5*time.Second, m, "OUT-SCOUT")
		time.Sleep(300 * time.Millisecond)
		if got := scout.output(m); strings.Contains(got, "OUT-SHIP") || strings.Contains(got, "OUT-COOK") {
			t.Errorf("the bridge on scout streamed another pane's output: %q", got)
		}
		// The ship's bridge, in turn, streams the ship's pane and not the crew boat's.
		sb, _ := w.joinBridge(t, ship.Session, 80, 24)
		m = sb.mark()
		w.paneWriteID(a.pane, "AGAIN-SCOUT\r\n")
		w.paneWriteID(shipPane, "AGAIN-SHIP\r\n")
		sb.waitOutput(5*time.Second, m, "AGAIN-SHIP")
		if got := sb.output(m); strings.Contains(got, "AGAIN-SCOUT") {
			t.Errorf("the ship's bridge streamed a crew boat's output: %q", got)
		}
	})

	t.Run("input reaches the crew pane and clears its needs-you, not the ship's", func(t *testing.T) {
		w := w.at(t)
		w.mustTmux("set", "-p", "-t", shipPane, "@admiral-state", "needs_you")
		w.mustTmux("set", "-p", "-t", a.pane, "@admiral-state", "needs_you")
		w.waitShip(ship.Session, "scout needing you", crewIs("scout", "needs_you"))
		scout, j := w.dialCrew(t, ship.Session, "scout", 80, 24)
		if j.Ship.Status != "needs_you" {
			t.Errorf("the join's ship object should carry the crew boat's status needs_you, got %q", j.Ship.Status)
		}
		scout.active()
		scout.input("typed-in-scout")
		w.waitCapture(a.pane, "typed-in-scout")
		w.waitOpt(a.pane, "@admiral-state", "working")
		if got := w.opt(shipPane, "@admiral-state"); got != "needs_you" {
			t.Errorf("input on the crew boat's bridge cleared the ship's needs-you (state %q)", got)
		}
		for _, p := range []string{shipPane, b.pane} {
			if strings.Contains(w.capture(p), "typed-in-scout") {
				t.Errorf("input to scout reached pane %s", p)
			}
		}
		scout.waitShip(5*time.Second, 0, "status underway", func(s shipState) bool { return s.Status == "underway" })

		// The ship's bridge keeps clearing only the ship's.
		w.mustTmux("set", "-p", "-t", b.pane, "@admiral-state", "needs_you")
		w.waitShip(ship.Session, "cook needing you", crewIs("cook", "needs_you"))
		sb, _ := w.joinBridge(t, ship.Session, 80, 24)
		sb.active()
		sb.input("typed-in-ship")
		w.waitCapture(shipPane, "typed-in-ship")
		w.waitOpt(shipPane, "@admiral-state", "working")
		if got := w.opt(b.pane, "@admiral-state"); got != "needs_you" {
			t.Errorf("input on the ship's bridge cleared a crew boat's needs-you (state %q)", got)
		}
		w.mustTmux("set", "-p", "-t", b.pane, "@admiral-state", "working")
	})

	t.Run("sizing is per window in both directions", func(t *testing.T) {
		w := w.at(t)
		cur := map[string]string{shipWin: w.winSize(shipWin), aWin: w.winSize(aWin), bWin: w.winSize(bWin)}
		// A crew bridge sizes only its crew window, and a crew-first attach doesn't
		// shrink the ship's window.
		scout, _ := w.dialCrew(t, ship.Session, "scout", 100, 30)
		scout.active()
		w.waitWinSize(aWin, "100x30")
		scout.waitSize(5*time.Second, 0, 100, 30)
		w.expectWinSizes("a resize on scout's bridge", map[string]string{shipWin: cur[shipWin], bWin: cur[bWin]})

		// The ship's bridge sizes only the ship's window: this is broken today,
		// where refresh-client -C sizes every window of the session.
		sb, _ := w.joinBridge(t, ship.Session, 114, 37)
		sb.active()
		w.waitWinSize(shipWin, "114x37")
		w.expectWinSizes("a resize on the ship's bridge", map[string]string{aWin: "100x30", bWin: cur[bWin]})

		cook, _ := w.dialCrew(t, ship.Session, "cook", 90, 20)
		cook.active()
		w.waitWinSize(bWin, "90x20")
		w.expectWinSizes("a resize on cook's bridge", map[string]string{shipWin: "114x37", aWin: "100x30"})
		scout.resize(101, 31)
		w.waitWinSize(aWin, "101x31")
		w.expectWinSizes("a resize on scout's bridge, later", map[string]string{shipWin: "114x37", bWin: "90x20"})
	})

	t.Run("404 for a name or session that is not a ship with that crew boat", func(t *testing.T) {
		w := w.at(t)
		// A good address connects first, so a missing route can't pass for a 404.
		ok, _ := w.dialCrew(t, ship.Session, "scout", 80, 24)
		ok.close()
		w.mustTmux("new-session", "-d", "-s", "_setup", "sleep 300")
		for _, p := range []string{
			ship.Session + "/crew/ghost",
			ship.Session + "/crew/scou",   // a strict prefix of a crew name
			ship.Session + "/crew/scouts", // longer than one
			ship.Session[:len(ship.Session)-1] + "/crew/scout",
			"nosuchship/crew/scout",
			"_admiral/crew/scout",
			"_setup/crew/scout",
		} {
			expectBridgeRefused(t, w.in.LocalURL, p, "", nil, "", http.StatusNotFound)
		}
	})

	t.Run("input to a crew boat that is anchored is refused with its status, and the ship's isn't", func(t *testing.T) {
		w := w.at(t)
		w.releaseGo(ship.Session, "cook")
		w.waitShip(ship.Session, "cook anchored", crewIs("cook", "anchored"))
		cook, j := w.dialCrew(t, ship.Session, "cook", 80, 24)
		cook.active()
		if j.Ship.Status != "anchored" || j.Ship.ExitCode == nil || *j.Ship.ExitCode != 1 {
			t.Errorf("the join of an anchored crew boat should say anchored with exit_code 1, got %+v", j.Ship)
		}
		cook.input("typed-in-anchored")
		cook.waitFor(5*time.Second, "a `refused` message with reason anchored", func() bool {
			for _, raw := range cook.texts(0, "refused") {
				if strings.Contains(string(raw), `"anchored"`) {
					return true
				}
			}
			return false
		})
		time.Sleep(200 * time.Millisecond)
		if strings.Contains(w.capture(b.pane), "typed-in-anchored") {
			t.Errorf("input reached an anchored crew boat")
		}
	})

	// Closing runs before the ship anchors: the ship's pane must still take output
	// to show its bridge stays open.
	t.Run("closing a crew boat closes its bridge and leaves the ship's open", func(t *testing.T) {
		w := w.at(t)
		cook, _ := w.dialCrew(t, ship.Session, "cook", 80, 24)
		sb, _ := w.joinBridge(t, ship.Session, 80, 24)
		url := w.in.LocalURL + "/api/ships/" + ship.Session + "/crew/cook/close"
		if code, body := send(t, http.MethodPost, url, nil); code/100 != 2 {
			t.Fatalf("close cook = %d: %s", code, body)
		}
		cook.waitFor(10*time.Second, "the bridge on cook to close", func() bool {
			cook.mu.Lock()
			defer cook.mu.Unlock()
			return cook.err != nil
		})
		expectBridgeRefused(t, w.in.LocalURL, ship.Session+"/crew/cook", "", nil, "", http.StatusNotFound)
		time.Sleep(300 * time.Millisecond)
		sb.mu.Lock()
		err := sb.err
		sb.mu.Unlock()
		if err != nil {
			t.Errorf("the ship's bridge closed with cook's: %v", err)
		}
		m := sb.mark()
		w.paneWriteID(shipPane, "SHIP-STILL-OPEN\r\n")
		sb.waitOutput(5*time.Second, m, "SHIP-STILL-OPEN")
	})

	t.Run("input to a live crew boat of an anchored ship is accepted", func(t *testing.T) {
		w := w.at(t)
		w.releaseGo(ship.Session, "")
		w.waitShip(ship.Session, "the ship anchored", func(s apiShip) bool { return s.Status == "anchored" })
		scout, j := w.dialCrew(t, ship.Session, "scout", 80, 24)
		scout.active()
		if j.Ship.Status == "anchored" {
			t.Errorf("a live crew boat of an anchored ship joins as %q", j.Ship.Status)
		}
		scout.input("typed-under-anchored-ship")
		w.waitCapture(a.pane, "typed-under-anchored-ship")
		for _, r := range scout.texts(0, "refused") {
			t.Errorf("input to a live crew boat was refused: %s", r)
		}
		scout.resize(95, 28)
		w.waitWinSize(aWin, "95x28")
	})
}

// A ship's bridge and the bridge on a crew boat share the session's one control
// client, which detaches after the last browser has left the ship and all its crew
// boats, and a crew-first attach leaves the ship's window at its size.
func TestCrewBridgeSharesOneControlClient(t *testing.T) {
	w := newWorld(t)
	w.extra = append(w.extra, "ADMIRAL_BRIDGE_IDLE=1s")
	w.cueFleets(map[string][]string{"one": {"hook working", "wait go", "exit 1"}})
	w.start()
	ship, shipPane := w.crewShip("one", "alpha")
	a := w.crewBoat(ship.Session, "scout", "claude")
	shipWin := w.winOf(shipPane)
	w.waitShip(ship.Session, "scout", crewIs("scout", "underway"))
	// A first client sizes the ship's window and detaches; the next client to
	// attach would set it to its own 80x24 if the windows were not pinned.
	first, _ := w.joinBridge(t, ship.Session, 133, 41)
	first.active()
	w.waitWinSize(shipWin, "133x41")
	first.close()
	for deadline := time.Now().Add(6 * time.Second); len(w.controlClients(ship.Session)) != 0; time.Sleep(100 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatalf("the first control client never detached")
		}
	}

	scout, _ := w.dialCrew(t, ship.Session, "scout", 90, 25)
	scout.active()
	w.waitWinSize(w.winOf(a.pane), "90x25")
	w.expectWinSizes("a crew bridge opened first", map[string]string{shipWin: "133x41"})
	sb, _ := w.joinBridge(t, ship.Session, 100, 30)
	if got := w.controlClients(ship.Session); len(got) != 1 {
		t.Errorf("a ship bridge and a crew bridge open: %d control clients (%v), want 1", len(got), got)
	}
	sb.close()
	time.Sleep(1500 * time.Millisecond)
	if got := w.controlClients(ship.Session); len(got) != 1 {
		t.Errorf("the crew bridge is still open: %d control clients, want the shared one", len(got))
	}
	scout.close()
	deadline := time.Now().Add(6 * time.Second)
	for len(w.controlClients(ship.Session)) != 0 {
		if time.Now().After(deadline) {
			t.Fatalf("the control client was still attached 6s after the last browser left the ship and its crew")
		}
		time.Sleep(100 * time.Millisecond)
	}
}
