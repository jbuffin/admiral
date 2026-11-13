package e2e

import (
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

// The fleet's cue records the terminal's raw input and parks until `go`, then anchors.
var inputCue = []string{"record", "bracketedpaste", "hook working", "wait go", "exit 1"}

func shipInputWorld(t *testing.T) *world {
	t.Helper()
	w := newWorld(t)
	w.cueFleets(map[string][]string{"f": inputCue})
	w.start()
	return w
}

func (w *world) shipURL(session, tail string) string {
	return w.in.LocalURL + "/api/ships/" + url.PathEscape(session) + tail
}

func (w *world) inputOf(session, crew string) string {
	d := filepath.Join(w.rec, session)
	if crew != "" {
		d = filepath.Join(d, "crew", crew)
	}
	b, _ := os.ReadFile(filepath.Join(d, "stdin"))
	return string(b)
}

func (w *world) waitStdinOf(session, crew, want string) {
	w.t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	got := ""
	for {
		if got = w.inputOf(session, crew); got == want {
			return
		}
		if time.Now().After(deadline) {
			w.t.Fatalf("within 5s the terminal input of %s %s never became %q; it is %q", session, crew, want, got)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func (w *world) holdStdin(session, crew, want string) {
	w.t.Helper()
	holdThat(w.t, "the terminal input of "+session+" "+crew+" changed from "+want, func() bool {
		return w.inputOf(session, crew) == want
	})
}

const pasted = "\x1b[200~hello\nworld[31m\t!\x1b[201~\r"

func TestShipInput_SendAndInterruptReachTheShipAndItsBoat(t *testing.T) {
	t.Parallel()
	w := shipInputWorld(t)
	ship, shipPane := w.crewShip("f", "alpha")
	scout := w.crewBoat(ship.Session, "scout", "claude")
	w.waitShip(ship.Session, "underway with a boat", func(s apiShip) bool { return s.Status == "underway" && len(s.Crew) == 1 })

	// CRLF becomes LF, C0 controls and ESC go, the edges are trimmed.
	dirty := "  hello\r\nwor\x07ld\x1b[31m\t!  \n"

	t.Run("a message pastes into the ship's pane and clears needs you", func(t *testing.T) {
		w := w.at(t)
		w.hookAt(shipPane, "PermissionRequest", hookPerm)
		w.waitShip(ship.Session, "needs_you", func(s apiShip) bool { return s.Status == "needs_you" })
		code, body := send(t, http.MethodPost, w.shipURL(ship.Session, "/messages"), map[string]any{"text": dirty})
		if code != http.StatusOK {
			t.Fatalf("send to the ship = %d, want 200: %s", code, body)
		}
		w.waitStdinOf(ship.Session, "", pasted)
		w.waitShip(ship.Session, "underway after the send", func(s apiShip) bool { return s.Status == "underway" })
		if st := w.opt(shipPane, "@admiral-state"); st != "working" {
			t.Errorf("after the send @admiral-state = %q, want working", st)
		}
	})

	t.Run("a refused message reaches no pane", func(t *testing.T) {
		w := w.at(t)
		for name, text := range map[string]string{"empty": "", "blank": " \n\t ", "controls only": "\x07\x1b\x00", "over 16 KiB": strings.Repeat("x", 17<<10)} {
			code, _ := send(t, http.MethodPost, w.shipURL(ship.Session, "/messages"), map[string]any{"text": text})
			if code != http.StatusBadRequest && !(name == "over 16 KiB" && code == http.StatusRequestEntityTooLarge) {
				t.Errorf("send of %s text = %d, want 400", name, code)
			}
		}
		if code, _ := send(t, http.MethodPost, w.shipURL(ship.Session, "/messages"), map[string]any{"text": strings.Repeat("x", 2<<20)}); code < 400 {
			t.Errorf("send of a 2 MiB body = %d, want a refusal", code)
		}
		w.holdStdin(ship.Session, "", pasted)
	})

	t.Run("an unknown ship or boat is 404", func(t *testing.T) {
		w := w.at(t)
		for _, tail := range []string{"/crew/ghost/messages", "/crew/ghost/interrupt"} {
			body := map[string]any{"text": "hi"}
			if code, _ := send(t, http.MethodPost, w.shipURL(ship.Session, tail), body); code != http.StatusNotFound {
				t.Errorf("POST %s on a live ship = %d, want 404", tail, code)
			}
		}
		for _, tail := range []string{"/messages", "/interrupt", "/stop", "/crew/scout/messages", "/crew/scout/interrupt"} {
			if code, _ := send(t, http.MethodPost, w.shipURL("no-such-ship", tail), map[string]any{"text": "hi"}); code != http.StatusNotFound {
				t.Errorf("POST %s on an unknown ship = %d, want 404", tail, code)
			}
		}
		w.holdStdin(ship.Session, "", pasted)
	})

	t.Run("interrupt sends Escape and leaves needs you", func(t *testing.T) {
		w := w.at(t)
		w.hookAt(shipPane, "PermissionRequest", hookPerm)
		w.waitShip(ship.Session, "needs_you", func(s apiShip) bool { return s.Status == "needs_you" })
		if code, body := send(t, http.MethodPost, w.shipURL(ship.Session, "/interrupt"), nil); code != http.StatusOK {
			t.Fatalf("interrupt the ship = %d, want 200: %s", code, body)
		}
		w.waitStdinOf(ship.Session, "", pasted+"\x1b")
		if st := w.opt(shipPane, "@admiral-state"); st != "needs_you" {
			t.Errorf("after interrupt @admiral-state = %q, want needs_you", st)
		}
	})

	t.Run("a message pastes into a crew boat's pane and clears its needs you", func(t *testing.T) {
		w := w.at(t)
		w.hookAt(scout.pane, "PermissionRequest", hookPerm)
		w.waitShip(ship.Session, "scout needs_you", func(s apiShip) bool {
			c, _ := crewOf(s, "scout")
			return c.Status == "needs_you"
		})
		code, body := send(t, http.MethodPost, w.shipURL(ship.Session, "/crew/scout/messages"), map[string]any{"text": dirty})
		if code != http.StatusOK {
			t.Fatalf("send to the boat = %d, want 200: %s", code, body)
		}
		w.waitStdinOf(ship.Session, "scout", pasted)
		w.waitShip(ship.Session, "scout underway after the send", func(s apiShip) bool {
			c, _ := crewOf(s, "scout")
			return c.Status == "underway"
		})
		if st := w.opt(scout.pane, "@admiral-state"); st != "working" {
			t.Errorf("after the send the boat's @admiral-state = %q, want working", st)
		}
		w.holdStdin(ship.Session, "", pasted+"\x1b") // the ship's pane got nothing
	})

	t.Run("a refused message to a boat reaches no pane", func(t *testing.T) {
		w := w.at(t)
		for name, text := range map[string]string{"empty": "", "over 16 KiB": strings.Repeat("x", 17<<10)} {
			code, _ := send(t, http.MethodPost, w.shipURL(ship.Session, "/crew/scout/messages"), map[string]any{"text": text})
			if code != http.StatusBadRequest && !(name == "over 16 KiB" && code == http.StatusRequestEntityTooLarge) {
				t.Errorf("send of %s text to the boat = %d, want 400", name, code)
			}
		}
		w.holdStdin(ship.Session, "scout", pasted)
	})

	t.Run("interrupt sends Escape to a crew boat only", func(t *testing.T) {
		w := w.at(t)
		if code, body := send(t, http.MethodPost, w.shipURL(ship.Session, "/crew/scout/interrupt"), nil); code != http.StatusOK {
			t.Fatalf("interrupt the boat = %d, want 200: %s", code, body)
		}
		w.waitStdinOf(ship.Session, "scout", pasted+"\x1b")
		w.holdStdin(ship.Session, "", pasted+"\x1b")
	})
}

func TestShipInput_AnchoredShipAndBoatRefuseWith409(t *testing.T) {
	t.Parallel()
	w := shipInputWorld(t)
	ship, _ := w.crewShip("f", "alpha")
	w.crewBoat(ship.Session, "cook", "claude")
	w.releaseGo(ship.Session, "cook")
	w.waitShip(ship.Session, "cook anchored", func(s apiShip) bool {
		c, _ := crewOf(s, "cook")
		return c.Status == "anchored"
	})
	for _, tail := range []string{"/crew/cook/messages", "/crew/cook/interrupt"} {
		if code, _ := send(t, http.MethodPost, w.shipURL(ship.Session, tail), map[string]any{"text": "hi"}); code != http.StatusConflict {
			t.Errorf("POST %s on an anchored boat = %d, want 409", tail, code)
		}
	}
	w.holdStdin(ship.Session, "cook", "")

	w.releaseGo(ship.Session, "")
	w.waitShip(ship.Session, "anchored", func(s apiShip) bool { return s.Status == "anchored" })
	for _, tail := range []string{"/messages", "/interrupt"} {
		if code, _ := send(t, http.MethodPost, w.shipURL(ship.Session, tail), map[string]any{"text": "hi"}); code != http.StatusConflict {
			t.Errorf("POST %s on an anchored ship = %d, want 409", tail, code)
		}
	}
	w.holdStdin(ship.Session, "", "")
}

func TestShipStop_EndsALiveShipAndItsCrewAndRecordsStopped(t *testing.T) {
	t.Parallel()
	w := shipInputWorld(t)
	ship, _ := w.crewShip("f", "alpha")
	w.crewBoat(ship.Session, "scout", "claude")
	rec := w.record(ship.Session)
	report, inbox := w.seedShipFiles(rec, `{"status":"done"}`)

	code, body := send(t, http.MethodPost, w.shipURL(ship.Session, "/stop"), nil)
	if code != http.StatusOK {
		t.Fatalf("stop = %d, want 200: %s", code, body)
	}

	log := w.waitEvent(ship.Session, "stopped", 1)
	if got, want := eventsOf(log), []string{"launched", "ended", "stopped"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("records = %v, want %v (ended before stopped)", got, want)
	}
	if e := lastOf(log, "ended"); e["signal"] != "KILL" || !isNull(e, "exit_code") {
		t.Errorf("ended = %v, want signal KILL and exit_code null", e)
	}
	holdFor(t, "the tmux session to end", func() bool { return !w.hasSession(ship.Session) })
	waitGone(t, 5*time.Second, report, inbox)
	w.waitGone(ship.Session, "the stop")
	if code, _ := send(t, http.MethodPost, w.shipURL(ship.Session, "/stop"), nil); code != http.StatusNotFound {
		t.Errorf("stopping the same ship again = %d, want 404", code)
	}
}

func TestShipStop_AnchoredShipRecordsStoppedAndLeavesTheList(t *testing.T) {
	t.Parallel()
	w := shipInputWorld(t)
	ship, _ := w.crewShip("f", "alpha")
	w.releaseGo(ship.Session, "")
	w.waitShip(ship.Session, "anchored", func(s apiShip) bool { return s.Status == "anchored" })

	if code, body := send(t, http.MethodPost, w.shipURL(ship.Session, "/stop"), nil); code != http.StatusOK {
		t.Fatalf("stop of an anchored ship = %d, want 200: %s", code, body)
	}
	log := w.waitEvent(ship.Session, "stopped", 1)
	if got, want := eventsOf(log), []string{"launched", "ended", "stopped"}; !reflect.DeepEqual(got, want) {
		t.Errorf("records = %v, want %v", got, want)
	}
	if e := lastOf(log, "ended"); !isNull(e, "signal") || num(t, e, "exit_code") != 1 {
		t.Errorf("ended = %v, want the harness's own exit 1, not a kill", e)
	}
	w.waitGone(ship.Session, "the stop")
}

func TestShipStop_WithMoorMoorsAShipWithOpenPRsAndEndsItsCrew(t *testing.T) {
	t.Parallel()
	w, _ := liveMooring(t)
	ship := w.launchLive()
	w.waitPaneWorking(strings.TrimSpace(w.mustTmux("display-message", "-p", "-t", "="+ship.Session+":0", "#{pane_id}")))
	w.crewBoat(ship.Session, "scout", "claude")
	w.writeReportFor(ship, reportWith("partial", mooredPR))

	code, body := send(t, http.MethodPost, w.shipURL(ship.Session, "/stop"), map[string]any{"moor": true})
	if code != http.StatusOK || strings.TrimSpace(body) != `{"moored":"`+ship.Session+`"}` {
		t.Fatalf("stop with moor = %d %s, want 200 {\"moored\":%q}", code, body, ship.Session)
	}
	w.wantMoored(ship)
	for _, s := range w.sessions() {
		if s == ship.Session {
			t.Errorf("the session %s and its crew boat are still up", s)
		}
	}
}
