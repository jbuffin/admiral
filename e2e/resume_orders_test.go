package e2e

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Resume of a live ship (ADR 0016): once every item id in a ship's report
// `inbox` has an answer, Admiral types the report's `resume` text at the prompt
// of a ship still afloat. The real binary and a ship that sits underway.
// Admiral adds no order for it: resume orders and `autopilot.resume_source` are
// gone (#261).

// resumeConfig writes the config: one claude fleet per cue key, no sources.
func (w *world) resumeConfig(cues map[string][]string) {
	w.t.Helper()
	fleets := map[string]any{}
	for key, lines := range cues {
		path := filepath.Join(w.root, "cue-"+key)
		if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
			w.t.Fatal(err)
		}
		env := w.harnessEnv()
		env["FAKE_CUE"] = path
		fleets[key] = map[string]any{"name": key, "color": "#336699", "search_roots": []string{w.work},
			"claude": map[string]any{"executable": w.fakeExe("claude"), "env": env}}
	}
	b, err := json.Marshal(map[string]any{"version": 1, "fleets": fleets})
	if err != nil {
		w.t.Fatal(err)
	}
	writeConfig(w.t, w.home, string(b))
}

// resumeShip launches a ship of fleet f and writes its report with the given
// resume text and inbox ids, and one question item per id.
func (w *world) resumeShip(repo, resume string, inbox []string, items ...string) (shipJSON, string, string) {
	w.t.Helper()
	ship := w.launchIn("f", "claude", repo)
	w.waitFile(ship.Session, "recorded")
	report := map[string]any{"version": 1, "result": "partial", "why": "waiting on the engineer", "inbox": inbox}
	if resume != "" {
		report["resume"] = resume
	}
	reportPath, dir := w.seedShipFiles(w.record(ship.Session), string(mustJSON(w.t, report)))
	for i, id := range items {
		seedItem(w.t, dir, id, questionItem(id, "2026-10-22T10:0"+itoa(i)+":00Z"))
	}
	w.in.waitInbox(w.t, items...)
	return ship, reportPath, dir
}

func answerBody() map[string]any {
	return map[string]any{"answers": map[string]any{"1": map[string]any{"choice": "Alpha"}, "2": map[string]any{"choice": []string{"X"}}}}
}

func (w *world) answerQuestion(session, id string) {
	w.t.Helper()
	if code, resp := w.in.answer(w.t, session, id, answerBody()); !ok2xx(code) {
		w.t.Fatalf("answering %s = %d: %s", id, code, resp)
	}
}

// A ship still afloat reads its answers itself (ADR 0016): once the last item
// its report lists is answered, Admiral types the report's resume text at the
// ship's prompt when its turn has ended, never while it is busy, and adds no
// order, so no second ship launches in its checkout.
func TestResumeOfALiveShipIsTypedAtItsPromptOnceItStops(t *testing.T) {
	w := newWorld(t)
	w.resumeConfig(map[string][]string{"f": {"record", "hook working", "wait go", "event Stop {}"}})
	w.start()
	const text = "/commodore resume run-1"
	ship, _, _ := w.resumeShip("live1", text, []string{"q-1"}, "q-1")
	w.waitFile(ship.Session, "recording")

	w.answerQuestion(ship.Session, "q-1")
	holdThat(t, "the resume text was typed while the ship was underway", func() bool { return w.stdinOf(ship.Session) == "" })
	holdThat(t, "a second ship launched for a ship still afloat", func() bool { return len(w.in.ships(t)) == 1 })

	w.touch(ship.Session, "go") // the turn ends: Stop
	w.waitStdin(t, ship.Session, text+"\r")
	log := w.waitEvent(ship.Session, "resumed", 1)
	if r := lastOf(log, "resumed"); r["live"] != true || r["order"] != nil {
		t.Errorf("resumed = %v, want live true and no order", r)
	}
	holdThat(t, "a second ship launched after the text was typed", func() bool { return len(w.in.ships(t)) == 1 })
	if got := w.stdinOf(ship.Session); got != text+"\r" {
		t.Errorf("stdin = %q, want the text typed once", got)
	}
}
