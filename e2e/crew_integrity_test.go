package e2e

import (
	"net/http"
	"strings"
	"testing"
	"time"
)

// What a crew boat can make of its own option text (#152 u4 security round 1).
// A boat writes its own window's @admiral-crew-name, and tmux prints option
// values raw, so the names Admiral reads back are not the names it launched.
// The seam is the built binary on a private tmux socket with the fake harness.

// forgedRow is a list-panes row (33 fields, see sessionhost.format) that names
// session and the crew boat evil on pane, as an agent would plant it in its own
// option text.
func forgedRow(session, pane string) string {
	f := make([]string, 33)
	f[0], f[1] = session, "0"
	f[28], f[29], f[30] = "evil", pane, "@99"
	return strings.Join(f, "\x1f")
}

// noForgedBoat requires that the forged boat evil is on no list and has no bridge.
func (w *world) noForgedBoat(t *testing.T, victim string) {
	t.Helper()
	for _, s := range fetchShips(t, w.in) {
		if _, ok := crewOf(s, "evil"); ok {
			t.Fatalf("the forged crew boat evil is listed on %s: %s", s.Session, s.rawAll)
		}
	}
	if r := w.admiral("ships"); strings.Contains(r.stdout, "evil") {
		t.Fatalf("admiral ships lists the forged boat:\n%s", r.stdout)
	}
	expectBridgeRefused(t, w.in.LocalURL, victim+"/crew/evil", "", nil, "", http.StatusNotFound)
}

func TestCrewNamesAreNotForged(t *testing.T) {
	w := newWorld(t)
	w.cueFleets(map[string][]string{"one": {"hook working", "wait go", "exit 1"}})
	w.start()
	ship, shipPane := w.crewShip("one", "alpha")
	victim, _ := w.crewShip("one", "bravo") // the ship the forged row names
	scout := w.crewBoat(ship.Session, "scout", "claude")
	cook := w.crewBoat(ship.Session, "cook", "claude")
	mallory := w.crewBoat(ship.Session, "mallory", "claude")
	w.waitShip(ship.Session, "three crew boats", func(s apiShip) bool { return len(s.Crew) == 3 })

	t.Run("a newline in a boat's name does not forge a crew boat on another pane", func(t *testing.T) {
		w := w.at(t)
		name := "junk\n" + forgedRow(victim.Session, shipPane) + "\n"
		w.mustTmux("set-option", "-w", "-t", w.winOf(mallory.pane), "@admiral-crew-name", name)
		s := w.waitShip(ship.Session, "mallory gone", func(s apiShip) bool { _, ok := crewOf(s, "mallory"); return !ok })
		if _, ok := crewOf(s, "evil"); ok {
			t.Errorf("the forged crew boat evil is listed: %s", s.rawAll)
		}
		w.noForgedBoat(t, victim.Session)
		if len(s.Crew) != 2 {
			t.Errorf("the other two boats should stay, got %s", s.rawAll)
		}
		w.mustTmux("set-option", "-w", "-t", w.winOf(mallory.pane), "@admiral-crew-name", "mallory")
		w.waitShip(ship.Session, "mallory back", crewIs("mallory", "underway"))
	})

	t.Run("an invalid UTF-8 byte before the newline does not forge a crew boat either", func(t *testing.T) {
		w := w.at(t)
		// tmux's substitution of control characters gives up on a value that is not
		// valid UTF-8 and prints it raw, so the reader must not rely on it.
		name := "\xff\n" + forgedRow(victim.Session, shipPane) + "\n"
		// -u: without it a client in a plain locale rewrites the invalid byte.
		w.mustTmux("-u", "set-option", "-w", "-t", w.winOf(mallory.pane), "@admiral-crew-name", name)
		deadline := time.Now().Add(2 * time.Second) // four polls
		for time.Now().Before(deadline) {
			w.noForgedBoat(t, victim.Session)
			time.Sleep(200 * time.Millisecond)
		}
		w.mustTmux("set-option", "-w", "-t", w.winOf(mallory.pane), "@admiral-crew-name", "mallory")
		time.Sleep(1200 * time.Millisecond) // the ship reads again
		w.waitShip(ship.Session, "mallory back", crewIs("mallory", "underway"))
	})

	t.Run("a boat that takes another boat's name is read for neither", func(t *testing.T) {
		w := w.at(t)
		// scout's window comes first; cook renames itself to scout.
		w.mustTmux("set-option", "-w", "-t", w.winOf(cook.pane), "@admiral-crew-name", "scout")
		s := w.waitShip(ship.Session, "scout and cook unlisted", func(s apiShip) bool {
			_, a := crewOf(s, "scout")
			_, b := crewOf(s, "cook")
			return !a && !b
		})
		if len(s.Crew) != 1 {
			t.Errorf("mallory alone should stay, got %s", s.rawAll)
		}
		expectBridgeRefused(t, w.in.LocalURL, ship.Session+"/crew/scout", "", nil, "", http.StatusNotFound)
		dir, rec := w.installFakeTmux()
		r := w.admiralWith(cliOpts{path: []string{dir}, env: []string{"FAKE_TMUX_RECORD=" + rec}}, "attach", ship.Session, "scout")
		w.expectExit(r, 1, "attach <ship> scout, claimed by two windows")
		if n := len(attachCalls(w, rec)); n != 0 {
			t.Errorf("attach reached tmux for a name two windows claim: %d calls", n)
		}
		_ = scout
		w.mustTmux("set-option", "-w", "-t", w.winOf(cook.pane), "@admiral-crew-name", "cook")
		w.waitShip(ship.Session, "scout back", crewIs("scout", "underway"))
	})
}
