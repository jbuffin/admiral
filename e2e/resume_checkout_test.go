package e2e

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

// Resume orders launch where, and as, the ship that wrote the report ran (#120
// u1). The real binary, the fake harness and two freeform sources:
//
//	builds   panel "orders" on autopilot, fleet buildf, skill Commodore, --origin-arg
//	orders   panel "orders" on autopilot, fleet resumef, no skill, --resume-arg;
//	         it is autopilot.resume_source
//
// A build ship writes a report with a resume text that holds no repository URL
// and one inbox item. When the item is answered, the resume order launches in
// the build ship's checkout, under the builds entry (its fleet, args and low
// stakes) but with the resume panel's skill, so /commodore is not doubled.

const rcResume = "/commodore resume r"

var rcRemote = remoteOf("widgets")

// rcWorld starts Admiral with the two sources above. originMax is the builds
// panel's max_ships. The checkout of acme/widgets is returned with the world.
func rcWorld(t *testing.T, originMax int) (*world, string) {
	t.Helper()
	w := newWorld(t)
	cues := map[string][]string{"buildf": {"hook working", sailCue, "exit 0"}, "resumef": {"hook working"}}
	var cfg map[string]any
	if err := json.Unmarshal([]byte(w.config()), &cfg); err != nil {
		t.Fatal(err)
	}
	fleets := cfg["fleets"].(map[string]any)
	for key, lines := range cues {
		path := w.root + "/cue-" + key
		if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		env := w.harnessEnv()
		env["FAKE_CUE"] = path
		fleets[key] = map[string]any{"name": key, "color": "#336699", "search_roots": []string{w.work},
			"claude": map[string]any{"executable": w.fakeExe("claude"), "env": env}}
	}
	build := apEntry(true, originMax, map[string]any{"fleet": "buildf", "skill": commodore, "args": []string{"--origin-arg"}})
	resume := apEntry(true, 2, map[string]any{"fleet": "resumef", "low_stakes": "advisor", "args": []string{"--resume-arg"}})
	cfg["sources"] = map[string]any{
		"builds": map[string]any{"plugin": "freeform", "name": "Builds", "autopilot": map[string]any{"orders": build}},
		"orders": map[string]any{"plugin": "freeform", "name": "Orders", "autopilot": map[string]any{"orders": resume}},
	}
	cfg["autopilot"] = map[string]any{"max_ships": 6, "resume_source": resumeTarget("orders", "orders")}
	b, err := json.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	writeConfig(t, w.home, string(b))
	checkout := w.repo("widgets", rcRemote)
	w.start()
	w.waitSource("builds", 10*time.Second, "state ok", isOK)
	w.waitSource("orders", 10*time.Second, "state ok", isOK)
	return w, checkout
}

func (w *world) rcAddOrder(source, text string) {
	w.t.Helper()
	code, body := send(w.t, http.MethodPost, w.in.LocalURL+"/api/sources/"+source+"/orders", map[string]any{"panel": "orders", "text": text})
	w.mustOK(code, body, "add an order to "+source)
}

// rcBuildShip adds a build order and returns the ship autopilot launches for
// it, after its report (resume text, one inbox item q-1) is written.
func (w *world) rcBuildShip(text string) (shipJSON, string) {
	w.t.Helper()
	w.rcAddOrder("builds", text)
	ship := w.rcShip("the build ship", func(s shipJSON) bool { return s.Source == "builds" && live(s) })
	return ship, w.rcReport(ship)
}

// rcReport writes the ship's report and the question item q-1, and returns
// the inbox directory.
func (w *world) rcReport(ship shipJSON) string {
	w.t.Helper()
	w.waitFile(ship.Session, "recorded")
	report := map[string]any{"version": 1, "result": "partial", "why": "waiting on the engineer", "inbox": []string{"q-1"}, "resume": rcResume}
	_, dir := w.seedShipFiles(w.record(ship.Session), string(mustJSON(w.t, report)))
	seedItem(w.t, dir, "q-1", questionItem("q-1", "2026-10-22T10:00:00Z"))
	w.in.waitInbox(w.t, "q-1")
	return dir
}

// rcResumeShip waits for the ship launched from the resume source.
func (w *world) rcResumeShip() shipJSON {
	w.t.Helper()
	return w.rcShip("the resume order's ship", func(s shipJSON) bool { return s.Source == "orders" && live(s) })
}

// rcShip waits for a ship that satisfies pred and returns it.
func (w *world) rcShip(what string, pred func(shipJSON) bool) shipJSON {
	w.t.Helper()
	for _, sh := range w.waitShipCount(15*time.Second, what, pred, 1) {
		if pred(sh) {
			return sh
		}
	}
	w.t.Fatalf("no ship for %s", what)
	return shipJSON{}
}

func (w *world) rcHoldNoResumeShip(msg string) {
	w.t.Helper()
	holdThat(w.t, msg, func() bool {
		return len(w.shipsWhere(func(s shipJSON) bool { return s.Source == "orders" })) == 0
	})
}

// rcExpectOrigin checks a resume ship launched as the build panel's entry says.
func rcExpectOrigin(t *testing.T, w *world, ship shipJSON, checkout string) {
	t.Helper()
	if ship.Dir != checkout {
		t.Errorf("resume ship dir = %q, want the build ship's checkout %q", ship.Dir, checkout)
	}
	if ship.Fleet != "buildf" {
		t.Errorf("resume ship fleet = %q, want buildf, the build panel's", ship.Fleet)
	}
	r := w.record(ship.Session)
	if r.cwd != checkout {
		t.Errorf("resume ship cwd = %q, want %q", r.cwd, checkout)
	}
	if !slices.Contains(r.argv, "--origin-arg") || slices.Contains(r.argv, "--resume-arg") {
		t.Errorf("argv = %q, want the build panel's --origin-arg and not the resume panel's --resume-arg", r.argv)
	}
	if r.env["ADMIRAL_LOW_STAKES"] != "never" || r.env["ADMIRAL_AUTOPILOT"] != "1" {
		t.Errorf("env ADMIRAL_LOW_STAKES=%q ADMIRAL_AUTOPILOT=%q, want never and 1 (the build panel's entry)", r.env["ADMIRAL_LOW_STAKES"], r.env["ADMIRAL_AUTOPILOT"])
	}
	// The resume text starts with the skill; the build panel's Commodore skill
	// must not be put in front of it again.
	prompts := 0
	for _, a := range r.argv {
		if strings.Contains(a, "resume r") {
			prompts++
			if a != rcResume {
				t.Errorf("prompt = %q, want the resume text once: %q", a, rcResume)
			}
		}
	}
	if prompts != 1 {
		t.Errorf("%d arguments hold the resume text in %q, want 1", prompts, r.argv)
	}
}

// C1: an autopilot build ship's resume order, whose text names no repository,
// launches in the ship's checkout under the build panel's entry, whichever
// state the ship is in when the last item is answered.
func TestResumeOrderLaunchesInTheBuildShipsCheckout(t *testing.T) {
	for _, mode := range []string{"live", "sailed off", "exits right after the answer"} {
		t.Run(mode, func(t *testing.T) {
			w, checkout := rcWorld(t, 2)
			ship, dir := w.rcBuildShip("Build " + rcRemote)
			switch mode {
			case "live":
				writeAnswerFile(t, dir, "q-1")
			case "sailed off":
				w.sailOff(ship.Session)
				w.answerQuestion(ship.Session, "q-1")
			default:
				writeAnswerFile(t, dir, "q-1")
				w.sailOff(ship.Session)
			}
			rcExpectOrigin(t, w, w.rcResumeShip(), checkout)
		})
	}
}

// A hand-launched ship has no build panel behind it: its resume order still
// launches in its checkout, under the resume panel's own entry.
func TestResumeOrderOfAHandLaunchedShipRunsUnderTheResumePanel(t *testing.T) {
	w, checkout := rcWorld(t, 2)
	ship := w.launch(map[string]any{"fleet": "buildf", "harness": "claude", "dir": checkout, "skill": noSkill, "order_id": "hand", "text": "x"})
	dir := w.rcReport(ship)
	writeAnswerFile(t, dir, "q-1")

	got := w.rcResumeShip()
	if got.Dir != checkout || got.Fleet != "resumef" {
		t.Errorf("resume ship dir %q fleet %q, want %q and resumef", got.Dir, got.Fleet, checkout)
	}
	r := w.record(got.Session)
	if !slices.Contains(r.argv, "--resume-arg") || slices.Contains(r.argv, "--origin-arg") || r.env["ADMIRAL_LOW_STAKES"] != "advisor" {
		t.Errorf("argv %q low stakes %q, want the resume panel's --resume-arg and advisor", r.argv, r.env["ADMIRAL_LOW_STAKES"])
	}
}

// The record outlives Admiral: a restart between the add and the launch (the
// resume panel's autopilot is paused) still launches in the right place.
func TestResumeOrderKeepsItsCheckoutAcrossARestart(t *testing.T) {
	w, checkout := rcWorld(t, 2)
	w.setPaused("orders", "orders", true)
	ship, _ := w.rcBuildShip("Build " + rcRemote)
	w.sailOff(ship.Session)
	w.answerQuestion(ship.Session, "q-1")
	w.waitSource("orders", 15*time.Second, "the resume order", func(s sourceJSON) bool { return len(s.orderIDs()) == 1 })
	// The record is written after the order shows in the listing.
	for deadline := time.Now().Add(10 * time.Second); !strings.Contains(w.readFile(filepath.Join(w.home, "autopilot.json")), `"resumes"`); {
		if time.Now().After(deadline) {
			t.Fatal("within 10s autopilot.json never held the resume order's record")
		}
		time.Sleep(50 * time.Millisecond)
	}
	w.restart()
	w.waitSource("orders", 10*time.Second, "the resume order after the restart", func(s sourceJSON) bool { return len(s.orderIDs()) == 1 })
	w.rcHoldNoResumeShip("a paused resume panel launched an order")

	w.setPaused("orders", "orders", false)
	rcExpectOrigin(t, w, w.rcResumeShip(), checkout)
}

// C2: the checkout the ship ran in is gone. The order is skipped with a
// reason that says so, nothing launches, and the order stays listed.
func TestResumeOrderWhoseCheckoutIsGoneIsSkipped(t *testing.T) {
	w, checkout := rcWorld(t, 2)
	ship, _ := w.rcBuildShip("Build " + rcRemote)
	w.sailOff(ship.Session)
	if err := os.RemoveAll(checkout); err != nil {
		t.Fatal(err)
	}
	w.answerQuestion(ship.Session, "q-1")

	rec := w.waitOutcome("skipped", 1)[0]
	if rec["source"] != "orders" || rec["panel"] != "orders" || rec["ship"] != "" {
		t.Errorf("skipped record = %v, want source orders, panel orders and no ship", rec)
	}
	if reason, _ := rec["reason"].(string); !strings.Contains(strings.ToLower(reason), "gone") {
		t.Errorf("skipped reason = %q, want it to say the ship's checkout is gone", rec["reason"])
	}
	w.rcHoldNoResumeShip("a ship launched for a resume order whose checkout is gone")
	if got := w.orderTexts("orders"); len(got) != 1 || got[0] != rcResume {
		t.Errorf("orders after the skip = %v, want the resume order still listed", got)
	}
}

// The resume order counts against the build panel's max_ships. While that
// panel is full the order waits, with no skipped record, and the resume
// panel's other orders still launch. It launches once the panel has room.
func TestResumeOrderWaitsWhileTheBuildPanelIsFull(t *testing.T) {
	w, checkout := rcWorld(t, 1)
	first, _ := w.rcBuildShip("Build " + rcRemote)
	w.rcAddOrder("builds", "Build again "+rcRemote) // waits: the panel's one slot is taken
	w.sailOff(first.Session)
	second := w.rcShip("the second build ship", func(s shipJSON) bool { return s.Source == "builds" && s.Session != first.Session && live(s) })

	w.answerQuestion(first.Session, "q-1")
	w.waitSource("orders", 15*time.Second, "the resume order", func(s sourceJSON) bool { return len(s.orderIDs()) == 1 })
	w.rcAddOrder("orders", "Review "+rcRemote) // behind the resume order in the same panel
	plain := w.rcResumeShip()
	if plain.Fleet != "resumef" || plain.Dir != checkout {
		t.Errorf("the plain order's ship fleet %q dir %q, want resumef and %q", plain.Fleet, plain.Dir, checkout)
	}
	holdThat(t, "the resume order launched, or was skipped, while the build panel was full", func() bool {
		return len(w.shipsWhere(func(s shipJSON) bool { return s.Source == "orders" })) == 1 && len(w.outcomesOf("skipped")) == 0
	})

	w.sail(second.Session)
	w.rcShip("the resume order's ship", func(s shipJSON) bool {
		return s.Source == "orders" && s.Session != plain.Session && live(s)
	})
}
