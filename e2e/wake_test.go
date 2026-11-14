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

// seedRound writes the transcript the fake harness does not, at the session id the launch passed it.
func seedRound(t *testing.T, cfgDir string, r record, text string) {
	t.Helper()
	for i, a := range r.argv {
		if a == "--session-id" && i+1 < len(r.argv) {
			appendLines(t, filepath.Join(cfgDir, "projects", "zz", r.argv[i+1]+".jsonl"), userLine("u-1", text, nil))
			return
		}
	}
	t.Fatalf("argv %q has no --session-id", r.argv)
}

func TestWakingAMooredShipByHandRelaunchesItUnderTheSameName(t *testing.T) {
	t.Parallel()
	w, g := mooring(t, "exit 0")
	g.prState(mooredPR, "OPEN")
	cfgDir := filepath.Join(w.root, "claude-cfg")
	if err := os.MkdirAll(filepath.Join(cfgDir, "projects", "zz"), 0o755); err != nil {
		t.Fatal(err)
	}
	// Commodore is looked for under the identity dir once the fleet names one.
	skill := filepath.Join(cfgDir, "skills", "commodore")
	if err := os.MkdirAll(skill, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(skill, "SKILL.md"), []byte("---\nname: commodore\ndescription: a fake skill\n---\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	var fleets map[string]any
	if err := json.Unmarshal([]byte(w.readFile(configPath(w.home))), &fleets); err != nil {
		t.Fatal(err)
	}
	fleets["fleets"].(map[string]any)["f"].(map[string]any)["claude"].(map[string]any)["env"].(map[string]any)["CLAUDE_CONFIG_DIR"] = cfgDir
	b, _ := json.Marshal(fleets)
	writeConfig(t, w.home, string(b))
	w.start()

	ship := w.launch(w.order("f", "claude", "widgets", commodore, "do it"))
	seedRound(t, cfgDir, w.record(ship.Session), "round zero")
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

	seedRound(t, cfgDir, r, "round one")
	w.exitWith(woken, wakeReport())
	w.in.waitShipsList(t, 20*time.Second, "the ship moored again", func(l []shipJSON) bool { return byName(l)[ship.Name].Status == "moored" })
	var code int
	var body string
	var got []string
	var divider string
	for end := time.Now().Add(20 * time.Second); ; time.Sleep(200 * time.Millisecond) {
		code, body = send(t, http.MethodGet, w.in.LocalURL+"/api/ships/"+ship.Session+"/conversation", nil)
		var page struct {
			State string `json:"state"`
			Items []struct {
				ID   string `json:"id"`
				Kind string `json:"kind"`
			} `json:"items"`
		}
		got, divider = nil, ""
		if code == http.StatusOK && json.Unmarshal([]byte(body), &page) == nil && page.State == "ok" {
			for _, it := range page.Items {
				got = append(got, it.ID)
				if it.Kind == "round" {
					divider = it.ID
				}
			}
			if reflect.DeepEqual(got, []string{"u-1", "r1", "r1:u-1"}) && divider == "r1" {
				break
			}
		}
		if time.Now().After(end) {
			t.Fatalf("GET conversation of the moored ship = %d %s, want ok with ids [u-1 r1 r1:u-1] and r1 a round divider", code, body)
		}
	}
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
	w.proveGuard("f")
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
	reviewAt := mooredAt.Add(time.Second)
	g.prEvents(mooredPR, "reviewer-1", "please rename the helper", reviewAt)

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

	var wake struct {
		PRs map[string]struct {
			Cursor struct {
				At time.Time `json:"at"`
			} `json:"cursor"`
		} `json:"prs"`
	}
	if err := json.Unmarshal([]byte(w.readFile(filepath.Join(w.home, "moored", "wake", ship.Name+".json"))), &wake); err != nil {
		t.Fatal(err)
	}
	if at := wake.PRs[mooredPR].Cursor.At; at.Before(reviewAt) {
		t.Errorf("wake state cursor = %v after the round, want it at or after the review's %v", at, reviewAt)
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

	// A stranger's comment is a proposal in the Inbox: the ship stays moored until the engineer launches it.
	const strangerText = "STRANGER-TEXT please also rename the type"
	g.prEventList(mooredPR, "reviewer-1", "please rename the helper", reviewAt, "stranger-1", strangerText, time.Now().Add(-3*time.Second))
	w.kickMoored()
	var proposal struct {
		ID           string `json:"id"`
		Kind         string `json:"kind"`
		Title        string `json:"title"`
		WakeProposal struct {
			Ship   string `json:"ship"`
			Login  string `json:"login"`
			Events []struct {
				ID   string `json:"id"`
				Body string `json:"body"`
			} `json:"events"`
		} `json:"wake_proposal"`
	}
	for end := time.Now().Add(20 * time.Second); proposal.ID == ""; time.Sleep(200 * time.Millisecond) {
		if time.Now().After(end) {
			t.Fatal("the stranger's comment never became a wake proposal in the Inbox")
		}
		w.kickMoored()
		_, body := send(t, http.MethodGet, w.in.LocalURL+"/api/inbox", nil)
		var list struct {
			Items []json.RawMessage `json:"items"`
		}
		_ = json.Unmarshal([]byte(body), &list)
		for _, raw := range list.Items {
			var it struct {
				Kind string `json:"kind"`
			}
			if json.Unmarshal(raw, &it) == nil && it.Kind == "wake_proposal" {
				_ = json.Unmarshal(raw, &proposal)
			}
		}
	}
	if proposal.Title != "Wake "+ship.Name+" for @stranger-1's review?" || proposal.WakeProposal.Login != "stranger-1" ||
		len(proposal.WakeProposal.Events) != 1 || !strings.Contains(proposal.WakeProposal.Events[0].Body, "STRANGER-TEXT") {
		t.Errorf("proposal = %+v, want one for the ship from stranger-1 with the comment's text", proposal)
	}
	holdThat(t, "the stranger's comment woke the ship without a Launch", func() bool {
		n := 0
		for _, m := range w.shipLog(ship.Session) {
			if m["event"] == "woken" {
				n++
			}
		}
		return n == 1 && byName(w.in.ships(t))[ship.Name].Status == "moored"
	})

	launch := w.in.LocalURL + "/api/inbox/_admiral/" + proposal.ID + "/launch"
	if code, body := send(t, http.MethodPost, launch, map[string]any{}); code != http.StatusCreated {
		t.Fatalf("Launch = %d, want 201: %s", code, body)
	}
	log = w.waitEvent(ship.Session, "woken", 2)
	if e := lastOf(log, "woken"); e["by"] != "proposal" || e["round"] != float64(2) {
		t.Errorf("woken event = %v, want by proposal, round 2", e)
	}
	got := w.readFile(filepath.Join(w.work, "widgets", ".admiral", "untrusted", "feedback-2.md"))
	if !strings.Contains(got, "STRANGER-TEXT") || strings.Contains(got, "please rename the helper") {
		t.Errorf("feedback-2.md = %q, want the stranger's text and not the reviewer's, which the first round took", got)
	}
	if code, _ := send(t, http.MethodPost, launch, map[string]any{}); code != http.StatusConflict {
		t.Errorf("a second Launch = %d, want 409", code)
	}
	w.kickMoored()
	holdThat(t, "the ship was woken again after the proposal's Launch", func() bool {
		n := 0
		for _, m := range w.shipLog(ship.Session) {
			if m["event"] == "woken" {
				n++
			}
		}
		return n == 2
	})
}

func TestAnswerReviewRelaunchesAMooredShipOnAReviewItsDebounceHasNotReleased(t *testing.T) {
	t.Parallel()
	w, g := mooring(t, "exit 0")
	g.prState(mooredPR, "OPEN")
	w.proveGuard("f")
	var cfg map[string]any
	if err := json.Unmarshal([]byte(w.readFile(configPath(w.home))), &cfg); err != nil {
		t.Fatal(err)
	}
	cfg["fleets"].(map[string]any)["f"].(map[string]any)["wake_reviewers"] = []string{"reviewer-1"}
	b, _ := json.Marshal(cfg)
	writeConfig(t, w.home, string(b))
	w.extra = append(w.extra, "ADMIRAL_WAKE_DEBOUNCE=1h", "ADMIRAL_MOOR_POLL=1s")
	w.start()

	ship := w.launch(w.order("f", "claude", "widgets", commodore, "do it"))
	w.exitWith(ship, wakeReport())
	list := w.in.waitShipsList(t, 20*time.Second, "the ship moored", func(l []shipJSON) bool { return byName(l)[ship.Name].Status == "moored" })
	mooredAt, err := time.Parse(time.RFC3339, byName(list)[ship.Name].MooredAt)
	if err != nil {
		t.Fatal(err)
	}
	g.prEvents(mooredPR, "reviewer-1", "please rename the helper", mooredAt.Add(time.Second))
	w.kickMoored()

	want := "/commodore resume run-7 feedback round 1 on " + mooredPR
	list = w.in.waitShipsList(t, 20*time.Second, "the review offered as an answer", func(l []shipJSON) bool {
		prs := byName(l)[ship.Name].PRs
		return len(prs) == 1 && prs[0].Answer != ""
	})
	if pr := byName(list)[ship.Name].PRs[0]; pr.Answer != want || pr.Feedback != 1 {
		t.Errorf("pr = %+v, want feedback 1 and answer %q", pr, want)
	}
	if byName(w.in.ships(t))[ship.Name].Status != "moored" {
		t.Fatal("the ship woke on its own: the debounce should hold it")
	}

	if err := os.RemoveAll(filepath.Join(w.rec, ship.Session)); err != nil {
		t.Fatal(err)
	}
	code, body := send(t, http.MethodPost, w.in.LocalURL+"/api/ships/"+ship.Session+"/answer-review", map[string]any{"pr": mooredPR})
	if code != http.StatusCreated {
		t.Fatalf("POST /api/ships/%s/answer-review = %d, want 201: %s", ship.Session, code, body)
	}
	r := w.record(ship.Session)
	if got := promptWithHooks(t, w, r); got != want {
		t.Errorf("prompt = %q, want %q", got, want)
	}
	if got := w.readFile(filepath.Join(w.work, "widgets", ".admiral", "untrusted", "feedback-1.md")); !strings.Contains(got, "please rename the helper") {
		t.Errorf("feedback-1.md = %q, want the review's body", got)
	}
	if e := lastOf(w.waitEvent(ship.Session, "woken", 1), "woken"); e["by"] != "hand" || e["pr"] != mooredPR || e["round"] != float64(1) {
		t.Errorf("woken event = %v, want by hand, round 1, on the PR", e)
	}
}
