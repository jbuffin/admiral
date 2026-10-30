package e2e

import (
	"strings"
	"testing"
)

// `admiral ships` with crew boats and `admiral attach <ship> [<crew>]` (#152
// "Terminal", spec #72 stories 18 and 19). The seam is the built binary on a
// private tmux socket with the fake harness. attach runs through the recording
// fake tmux (installFakeTmux), which logs the argv of the attach call.
//
// The cue is the fleet's `hook working`, `wait go`, `exit 1`: releasing one crew
// boat anchors it with exit 1.

// shipLines are the lines of `admiral ships` after the header, split into
// fields, in the order printed. A crew line has a two-space indent and empty
// SESSION and FLEET.
func shipLines(t *testing.T, out string) [][]string {
	t.Helper()
	var rows [][]string
	headed := false
	for _, l := range strings.Split(out, "\n") {
		f := strings.Split(l, "\t")
		if len(f) != 6 {
			continue
		}
		if !headed {
			headed = true
			continue
		}
		rows = append(rows, f)
	}
	if !headed {
		t.Fatalf("no tab-separated table in:\n%s", out)
	}
	return rows
}

// crewLinesOf are the crew lines printed directly under the ship's line, as
// "name|harness|status|repo", with the SESSION and FLEET columns required empty.
func crewLinesOf(t *testing.T, out, session string) []string {
	t.Helper()
	rows := shipLines(t, out)
	var got []string
	in := false
	for _, f := range rows {
		isCrew := strings.HasPrefix(f[0], "  ")
		if !isCrew {
			in = f[1] == session
			continue
		}
		if !in {
			continue
		}
		if f[1] != "" || f[2] != "" {
			t.Errorf("a crew line leaves SESSION and FLEET empty, got %q", f)
		}
		got = append(got, strings.TrimPrefix(f[0], "  ")+"|"+f[3]+"|"+f[4]+"|"+f[5])
	}
	return got
}

func TestCrewTerminal(t *testing.T) {
	w := newWorld(t)
	w.cueFleets(map[string][]string{"one": {"hook working", "wait go", "exit 1"}})
	w.start()
	// A few ship names are one word, and the attach checks need a spaced one.
	ship, shipPane := w.crewShip("one", "alpha")
	for !strings.Contains(ship.Name, " ") {
		ship, shipPane = w.crewShip("one", "alpha")
	}
	bare, _ := w.crewShip("one", "bravo") // a ship with no crew
	scout := w.crewBoat(ship.Session, "scout", "claude")
	cook := w.crewBoat(ship.Session, "cook", "claude")
	w.releaseGo(ship.Session, "cook")
	w.waitShip(ship.Session, "cook anchored", func(s apiShip) bool { return crewIs("cook", "anchored")(s) })

	wantCrew := []string{
		"cook|claude|anchored (exit 1)|anywhere",
		"scout|claude|underway|anywhere",
	}

	t.Run("ships lists each ship's crew boats under it, with the server up and offline", func(t *testing.T) {
		w := w.sub(t)
		up := w.admiral("ships", "--port", w.in.Port)
		w.expectExit(up, 0, "admiral ships")
		if got := crewLinesOf(t, up.stdout, ship.Session); strings.Join(got, "\n") != strings.Join(wantCrew, "\n") {
			t.Errorf("crew lines with the server up = %q, want %q under the ship's line, sorted by name:\n%s", got, wantCrew, up.stdout)
		}
		if got := crewLinesOf(t, up.stdout, bare.Session); len(got) != 0 {
			t.Errorf("a ship with no crew boats has no crew lines, got %q", got)
		}
		if strings.Contains(up.stdout, "Explore") {
			t.Errorf("subagents are not listed:\n%s", up.stdout)
		}
		names := w.admiral("ships", "--names", "--port", w.in.Port)
		w.expectExit(names, 0, "ships --names")
		for _, l := range strings.Split(strings.TrimSpace(names.stdout), "\n") {
			if strings.HasPrefix(l, " ") || l == "scout" || l == "cook" {
				t.Errorf("--names prints ship display names only, got line %q", l)
			}
		}

	})

	t.Run("attach opens the ship's own window and a crew boat's window", func(t *testing.T) {
		w := w.sub(t)
		shipWin := w.winOf(shipPane)
		scoutWin, cookWin := w.winOf(scout.pane), w.winOf(cook.pane)
		// A spaced display name still attaches as a ship (the argument list is
		// tried as a ship first), and then crew names split off the end.
		words := strings.Fields(ship.Name)
		dir, rec := w.installFakeTmux()
		attach := func(args ...string) cliResult {
			return w.admiralWith(cliOpts{path: []string{dir}, env: []string{"FAKE_TMUX_RECORD=" + rec}}, append([]string{"attach"}, args...)...)
		}
		target := func(n int) string {
			calls := attachCalls(w, rec)
			if len(calls) != n {
				t.Fatalf("expected %d recorded attach calls, got %q", n, calls)
			}
			argv := calls[n-1]
			for i := 0; i+1 < len(argv); i++ {
				if argv[i] == "-t" {
					return argv[i+1]
				}
			}
			t.Fatalf("no -t in %q", argv)
			return ""
		}
		isShipWindow := func(tg string) bool {
			return tg == "="+ship.Session+":^" || tg == "="+ship.Session+":"+shipWin
		}

		w.expectExit(attach(ship.Session), 0, "attach <session>")
		if tg := target(1); !isShipWindow(tg) {
			t.Errorf("attach <ship> target = %q, want =%s:^ or the ship's window id %s, never the bare session", tg, ship.Session, shipWin)
		}
		w.expectExit(attach(words...), 0, "attach <display words>")
		if tg := target(2); !isShipWindow(tg) {
			t.Errorf("a spaced display name should attach as the ship, got target %q", tg)
		}
		w.expectExit(attach(ship.Name), 0, "attach <quoted display name>")
		if tg := target(3); !isShipWindow(tg) {
			t.Errorf("a quoted spaced display name should attach as the ship, got target %q", tg)
		}

		w.expectExit(attach(append(append([]string{}, words...), "scout")...), 0, "attach <display words> scout")
		if tg := target(4); tg != "="+ship.Session+":"+scoutWin {
			t.Errorf("attach <ship> scout target = %q, want =%s:%s", tg, ship.Session, scoutWin)
		}
		if argv := attachCalls(w, rec)[3]; has(argv, "-r") {
			t.Errorf("a live crew boat attaches read-write, got %q", argv)
		}
		w.expectExit(attach(ship.Session, "cook"), 0, "attach <session> cook")
		if tg := target(5); tg != "="+ship.Session+":"+cookWin {
			t.Errorf("attach <ship> cook target = %q, want =%s:%s", tg, ship.Session, cookWin)
		}
		if argv := attachCalls(w, rec)[4]; !has(argv, "-r") {
			t.Errorf("an anchored crew boat attaches read-only (-r), got %q", argv)
		}

		// Moving the session's current window onto a crew window doesn't move
		// where `attach <ship>` lands.
		w.mustTmux("select-window", "-t", "="+ship.Session+":"+cookWin)
		w.expectExit(attach(ship.Session), 0, "attach <session> after select-window")
		if tg := target(6); !isShipWindow(tg) {
			t.Errorf("with a crew window current, attach <ship> target = %q, want the ship's window", tg)
		}
		w.mustTmux("select-window", "-t", "="+ship.Session+":"+shipWin)

		// The ship's own status doesn't decide a crew boat's -r.
		w.releaseGo(ship.Session, "")
		w.waitShip(ship.Session, "the ship anchored", func(s apiShip) bool { return s.Status == "anchored" })
		w.expectExit(attach(ship.Session, "scout"), 0, "attach scout under an anchored ship")
		if argv := attachCalls(w, rec)[6]; has(argv, "-r") {
			t.Errorf("a live crew boat of an anchored ship attaches read-write, got %q", argv)
		}

		// An unknown crew boat exits 1, naming it, and tmux never runs.
		r := attach(ship.Session, "ghost")
		w.expectExit(r, 1, "attach <ship> ghost")
		if !strings.Contains(r.stderr, "ghost") {
			t.Errorf("the refusal should name the crew boat ghost, got stderr %q", r.stderr)
		}
		if n := len(attachCalls(w, rec)); n != 7 {
			t.Errorf("a refused attach still ran tmux attach: %d calls", n)
		}
	})

	t.Run("ships offline lists the same crew lines", func(t *testing.T) {
		w := w.sub(t)
		w.in.Stop()
		down := w.admiral("ships", "--port", w.in.Port)
		w.expectExit(down, 0, "admiral ships offline")
		if got := crewLinesOf(t, down.stdout, ship.Session); strings.Join(got, "\n") != strings.Join(wantCrew, "\n") {
			t.Errorf("crew lines offline = %q, want the same %q:\n%s", got, wantCrew, down.stdout)
		}
	})
}
