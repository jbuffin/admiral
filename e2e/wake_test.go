package e2e

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

// wakeReport is a done report whose resume text carries its own invocation, as the report schema requires.
func wakeReport() string {
	b, _ := json.Marshal(map[string]any{"version": 2, "result": "done", "why": "PR opened",
		"prs": []map[string]any{{"url": mooredPR, "closes": []string{}}}, "resume": "/commodore resume run-7"})
	return string(b)
}

// promptWithHooks is the prompt, after checking the Admiral-owned flags before it: the mooring fleet's claude dock has no args,
// so promptOf's check of them does not fit.
func promptWithHooks(t *testing.T, w *world, r record) string {
	t.Helper()
	var rest []string
	for i := 0; i < len(r.argv); i++ {
		if r.argv[i] == "--session-id" && i+1 < len(r.argv) {
			i++
			continue
		}
		rest = append(rest, r.argv[i])
	}
	want := []string{"--settings", filepath.Join(w.home, "harness", "claude-settings.json"), "--"}
	if len(rest) == 0 || !reflect.DeepEqual(rest[:len(rest)-1], want) {
		t.Fatalf("claude argv before the prompt = %q, want %q", rest, want)
	}
	return rest[len(rest)-1]
}

// wake posts a hand wake and returns the ship it answers with.
func (w *world) wake(session, text string) shipJSON {
	w.t.Helper()
	// The fake harness reuses its recording directory for a session name, so the last start's files would be read as this one's.
	if err := os.RemoveAll(filepath.Join(w.rec, session)); err != nil {
		w.t.Fatal(err)
	}
	code, body := send(w.t, http.MethodPost, w.in.LocalURL+"/api/ships/"+session+"/wake", map[string]any{"text": text})
	if code != http.StatusCreated {
		w.t.Fatalf("POST /api/ships/%s/wake = %d, want 201: %s", session, code, body)
	}
	return decodeShip(w.t, body)
}

func TestWakingAMooredShipByHandRelaunchesItUnderTheSameName(t *testing.T) {
	t.Parallel()
	w, g := mooring(t, "exit 0")
	g.prState(mooredPR, "OPEN")
	w.start()

	ship := w.launch(w.order("f", "claude", "widgets", commodore, "do it"))
	w.exitWith(ship, wakeReport())
	w.in.waitShipsList(t, 20*time.Second, "the ship moored", func(l []shipJSON) bool { return byName(l)[ship.Name].Status == "moored" })

	woken := w.wake(ship.Session, "also check the docs")
	if woken.Session != ship.Session || woken.Name != ship.Name {
		t.Errorf("woken ship = %s (%s), want %s (%s)", woken.Name, woken.Session, ship.Name, ship.Session)
	}
	r := w.record(ship.Session)
	if got := promptWithHooks(t, w, r); got != "/commodore resume run-7 also check the docs" {
		t.Errorf("prompt = %q, want the resume text verbatim and the engineer's text", got)
	}
	w.in.waitShipsList(t, 20*time.Second, "the ship underway under its own name", func(l []shipJSON) bool {
		s := byName(l)[ship.Name]
		return s.Session == ship.Session && s.Status != "moored"
	})
	if exists(w.mooredFile(ship.Name)) {
		t.Error("the moored record is still there while the ship is woken")
	}
	log := w.waitEvent(ship.Session, "woken", 1)
	if e := lastOf(log, "woken"); e["by"] != "hand" || e["round"] != float64(1) {
		t.Errorf("woken event = %v, want by hand, round 1", e)
	}

	w.exitWith(woken, wakeReport())
	w.in.waitShipsList(t, 20*time.Second, "the ship moored again", func(l []shipJSON) bool { return byName(l)[ship.Name].Status == "moored" })
	var rec map[string]any
	if err := json.Unmarshal([]byte(w.readFile(w.mooredFile(ship.Name))), &rec); err != nil {
		t.Fatal(err)
	}
	if rec["round"] != float64(1) {
		t.Errorf("record round = %v after the first round, want 1", rec["round"])
	}
	if e := lastOf(w.waitEvent(ship.Session, "round", 1), "round"); e["round"] != float64(1) || e["end"] != "moored" {
		t.Errorf("round event = %v, want round 1 ending moored", e)
	}

	w.wake(ship.Session, "")
	if got := promptWithHooks(t, w, w.record(ship.Session)); got != "/commodore resume run-7" {
		t.Errorf("second wake prompt = %q, want the resume text alone", got)
	}
	if e := lastOf(w.waitEvent(ship.Session, "woken", 2), "woken"); e["round"] != float64(2) {
		t.Errorf("second woken event = %v, want round 2", e)
	}
}

func TestAReviewWakesAMooredShipOnItsOwnOnceAndOnlyOnce(t *testing.T) {
	t.Parallel()
	w, g := mooring(t, "exit 0")
	g.prState(mooredPR, "OPEN")
	w.seedGuard("f", "claude", true)
	var cfg map[string]any
	if err := json.Unmarshal([]byte(w.readFile(configPath(w.home))), &cfg); err != nil {
		t.Fatal(err)
	}
	cfg["fleets"].(map[string]any)["f"].(map[string]any)["wake_reviewers"] = []string{"reviewer-1"}
	b, _ := json.Marshal(cfg)
	writeConfig(t, w.home, string(b))
	w.extra = append(w.extra, "ADMIRAL_WAKE_DEBOUNCE=1s", "ADMIRAL_MOOR_POLL=1s")
	w.start()

	ship := w.launch(w.order("f", "claude", "widgets", commodore, "do it"))
	w.exitWith(ship, wakeReport())
	list := w.in.waitShipsList(t, 20*time.Second, "the ship moored", func(l []shipJSON) bool { return byName(l)[ship.Name].Status == "moored" })
	mooredAt, err := time.Parse(time.RFC3339, byName(list)[ship.Name].MooredAt)
	if err != nil {
		t.Fatal(err)
	}
	// The fake harness reuses its recording directory for a session name, so the last start's files would be read as the relaunch's.
	if err := os.RemoveAll(filepath.Join(w.rec, ship.Session)); err != nil {
		t.Fatal(err)
	}
	g.prEvents(mooredPR, "reviewer-1", "please rename the helper", mooredAt.Add(time.Second))

	log := w.waitEvent(ship.Session, "woken", 1)
	if e := lastOf(log, "woken"); e["by"] != "feedback" || e["round"] != float64(1) {
		t.Errorf("woken event = %v, want by feedback, round 1", e)
	}
	w.in.waitShipsList(t, 20*time.Second, "the ship underway under its own name", func(l []shipJSON) bool {
		s := byName(l)[ship.Name]
		return s.Session == ship.Session && s.Status != "moored"
	})
	r := w.record(ship.Session)
	if got := r.argv[len(r.argv)-1]; got != "/commodore resume run-7 feedback round 1 on "+mooredPR {
		t.Errorf("prompt = %q, want the resume text and feedback round 1 on the PR", got)
	}
	if got := w.readFile(filepath.Join(w.work, "widgets", ".admiral", "untrusted", "feedback-1.md")); !strings.Contains(got, "please rename the helper") {
		t.Errorf("feedback-1.md = %q, want the review's body", got)
	}

	w.exitWith(byName(w.in.ships(t))[ship.Name], wakeReport())
	w.in.waitShipsList(t, 20*time.Second, "the ship moored again", func(l []shipJSON) bool { return byName(l)[ship.Name].Status == "moored" })
	w.kickMoored()
	holdThat(t, "the ship woke again on the review its first round took", func() bool {
		n := 0
		for _, m := range w.shipLog(ship.Session) {
			if m["event"] == "woken" {
				n++
			}
		}
		return n == 1 && byName(w.in.ships(t))[ship.Name].Status == "moored"
	})
}
