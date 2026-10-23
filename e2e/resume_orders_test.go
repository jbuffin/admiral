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

// Resume orders (#116 u5): once every item id in a ship's report `inbox` has an
// answer file, Admiral adds the report's `resume` text as an order to the panel
// that `autopilot.resume_source` names. The real binary, a freeform source
// "orders" (panel "orders") and ships that sit underway.
//
// Wire shape under test:
//
//	config   {"autopilot": {"resume_source": {"source": "orders", "panel": "orders"}}}
//	report   $ADMIRAL_REPORT = {"version":1,"result":"partial","why":"..","resume":"text","inbox":["q-1","q-2"]}
//	outcome  one `resumed` record per report, with ship set to the session
//
// The order shows in GET /api/sources like any other order.

const (
	resumeText = "Finish the review of https://github.com/acme/widgets/pull/12 now the questions are answered"
	sailCue    = "wait go"
)

var ordersSource = map[string]any{"orders": map[string]any{"plugin": "freeform", "name": "Orders"}}

func resumeTarget(source, panel string) map[string]any {
	return map[string]any{"source": source, "panel": panel}
}

// resumeConfig writes the config: one claude fleet per cue key, the sources
// and, unless resume is nil, autopilot.resume_source.
func (w *world) resumeConfig(cues map[string][]string, sources map[string]any, resume any) {
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
	cfg := map[string]any{"version": 1, "fleets": fleets, "sources": sources}
	if resume != nil {
		cfg["autopilot"] = map[string]any{"resume_source": resume}
	}
	b, err := json.Marshal(cfg)
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

// writeAnswerFile answers an item the way someone other than Admiral's API
// would: the file, whole, through a rename.
func writeAnswerFile(t *testing.T, dir, id string) {
	t.Helper()
	b := mustJSON(t, map[string]any{"version": 1, "id": id, "kind": "question",
		"answered": time.Now().UTC().Format(time.RFC3339), "answers": map[string]any{"1": map[string]any{"choice": "Alpha"}}})
	tmp := filepath.Join(dir, "."+id+".answer.json.tmp.1")
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(tmp, filepath.Join(dir, id+".answer.json")); err != nil {
		t.Fatal(err)
	}
}

// orderTexts is the text of every order in the source, in all its panels.
func (w *world) orderTexts(source string) []string {
	w.t.Helper()
	s, ok := find(w.sources(), source)
	if !ok {
		return nil
	}
	out := []string{}
	for _, p := range s.Panels {
		for _, o := range p.Orders {
			out = append(out, o.Text)
		}
	}
	return out
}

func (w *world) waitOrders(source string, want ...string) {
	w.t.Helper()
	w.waitSource(source, 15*time.Second, "orders "+strings.Join(want, " | "), func(s sourceJSON) bool {
		return reflect.DeepEqual(w.orderTexts(source), want)
	})
}

// holdOrders fails if the source's orders ever differ from want for the hold.
func (w *world) holdOrders(msg, source string, want ...string) {
	w.t.Helper()
	holdThat(w.t, msg, func() bool {
		got := w.orderTexts(source)
		return len(got) == len(want) && (len(want) == 0 || reflect.DeepEqual(got, want))
	})
}

// S1: two items in the report. Answering one adds nothing; answering the second
// adds one order with the resume text to the configured panel, and a `resumed`
// outcome is recorded against the ship.
func TestResumeOrderAddedWhenTheLastItemIsAnswered(t *testing.T) {
	w := newWorld(t)
	w.resumeConfig(map[string][]string{"f": {"hook working"}}, ordersSource, resumeTarget("orders", "orders"))
	w.start()
	w.waitSource("orders", 10*time.Second, "state ok", isOK)
	ship, _, _ := w.resumeShip("res1", resumeText, []string{"q-1", "q-2"}, "q-1", "q-2")

	w.answerQuestion(ship.Session, "q-1")
	w.holdOrders("an order was added with an item of the report still unanswered", "orders")
	if n := len(w.outcomesOf("resumed")); n != 0 {
		t.Fatalf("%d resumed records with an item unanswered, want none", n)
	}

	w.answerQuestion(ship.Session, "q-2")
	w.waitOrders("orders", resumeText)
	log := w.waitEvent(ship.Session, "resumed", 1)
	if n := countEvent(log, "resumed"); n != 1 {
		t.Errorf("%d resumed records for the ship, want 1", n)
	}
	// The record carries ship, so `admiral outcomes --ship` lists it.
	if r := lastOf(log, "resumed"); r["ship"] != ship.Session {
		t.Errorf("resumed = %v, want ship %s", r, ship.Session)
	}
}

// Answer files that someone else wrote count the same as ones the API wrote:
// the inbox scan, not only the answer route, starts the check.
func TestResumeOrderAddedForAnswerFilesWrittenDirectly(t *testing.T) {
	w := newWorld(t)
	w.resumeConfig(map[string][]string{"f": {"hook working"}}, ordersSource, resumeTarget("orders", "orders"))
	w.start()
	w.waitSource("orders", 10*time.Second, "state ok", isOK)
	ship, _, dir := w.resumeShip("res1b", resumeText, []string{"q-1", "q-2"}, "q-1", "q-2")

	writeAnswerFile(t, dir, "q-1")
	w.holdOrders("an order was added with an item of the report still unanswered", "orders")
	writeAnswerFile(t, dir, "q-2")
	w.waitOrders("orders", resumeText)
	w.waitEvent(ship.Session, "resumed", 1)
}

// S2: a restart, and an answer written again, add no second order.
func TestResumeOrderIsAddedOnce(t *testing.T) {
	w := newWorld(t)
	w.resumeConfig(map[string][]string{"f": {"hook working"}}, ordersSource, resumeTarget("orders", "orders"))
	w.start()
	w.waitSource("orders", 10*time.Second, "state ok", isOK)
	ship, _, dir := w.resumeShip("res2", resumeText, []string{"q-1", "q-2"}, "q-1", "q-2")
	w.answerQuestion(ship.Session, "q-1")
	w.answerQuestion(ship.Session, "q-2")
	w.waitOrders("orders", resumeText)
	w.waitEvent(ship.Session, "resumed", 1)

	w.restart()
	w.waitSource("orders", 10*time.Second, "state ok", isOK)
	w.holdOrders("a restart added the resume order again", "orders", resumeText)

	writeAnswerFile(t, dir, "q-2") // the same answer, written again
	w.holdOrders("an answer written again added a second resume order", "orders", resumeText)
	if n := len(w.outcomesOf("resumed")); n != 1 {
		t.Errorf("%d resumed records after a restart and a repeated answer, want 1", n)
	}
}

// S3: a report that must add nothing adds nothing, Admiral keeps running, and a
// report right after them still gets its order, so the silence is not a stalled
// check.
func TestResumeOrderAddsNothingForReportsThatCannotResume(t *testing.T) {
	w := newWorld(t)
	w.resumeConfig(map[string][]string{"f": {"hook working"}}, ordersSource, resumeTarget("orders", "orders"))
	w.start()
	w.waitSource("orders", 10*time.Second, "state ok", isOK)

	noText, _, _ := w.resumeShip("res3a", "", []string{"q-1"}, "q-1")
	w.answerQuestion(noText.Session, "q-1")

	emptyInbox, _, _ := w.resumeShip("res3b", resumeText, []string{}, "q-2")
	w.answerQuestion(emptyInbox.Session, "q-2")

	noFile, _, _ := w.resumeShip("res3c", resumeText, []string{"q-3", "ghost"}, "q-3")
	w.answerQuestion(noFile.Session, "q-3")

	// A resume the source refuses (a control character) is dropped, not retried.
	refusedText, _, _ := w.resumeShip("res3d", "bad\u0001text https://github.com/acme/widgets/pull/12", []string{"q-4"}, "q-4")
	w.answerQuestion(refusedText.Session, "q-4")

	w.holdOrders("an order was added for a report that cannot resume", "orders")
	if n := len(w.outcomesOf("resumed")); n != 0 {
		t.Errorf("%d resumed records, want none", n)
	}

	control, _, _ := w.resumeShip("res3e", resumeText, []string{"q-5"}, "q-5")
	w.answerQuestion(control.Session, "q-5")
	w.waitOrders("orders", resumeText)
	if got := w.shipLog(control.Session); countEvent(got, "resumed") != 1 {
		t.Errorf("control ship's records = %v, want one resumed", eventsOf(got))
	}
}

// S3, config side: with a panel or source that isn't there, nothing is added
// and Admiral does not crash. (Without resume_source nothing is added either,
// but that holds before u5 too, so it has no failing test of its own.)
func TestResumeOrderWithoutAUsableTarget(t *testing.T) {
	cases := []struct {
		name   string
		resume any
	}{
		{"a panel that does not exist", resumeTarget("orders", "no-such-panel")},
		{"a source that does not exist", resumeTarget("no-such-source", "orders")},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			w := newWorld(t)
			w.resumeConfig(map[string][]string{"f": {"hook working"}}, ordersSource, c.resume)
			w.start()
			w.waitSource("orders", 10*time.Second, "state ok", isOK)
			ship, _, _ := w.resumeShip("res3f", resumeText, []string{"q-1"}, "q-1")
			w.answerQuestion(ship.Session, "q-1")

			w.holdOrders("an order was added with "+c.name, "orders")
			if n := len(w.outcomesOf("resumed")); n != 0 {
				t.Errorf("%d resumed records with %s, want none", n, c.name)
			}
			if code, body := send(t, http.MethodGet, w.in.LocalURL+"/api/ships", nil); code != http.StatusOK {
				t.Errorf("GET /api/ships = %d after the answer, want 200: %s", code, body)
			}
		})
	}
}

// sailWorld is a world whose ships sit underway until `go` and then exit 0, so
// they sail off.
func sailWorld(t *testing.T, sources map[string]any, resume any) *world {
	t.Helper()
	w := newWorld(t)
	w.resumeConfig(map[string][]string{"f": {"hook working", sailCue, "exit 0"}}, sources, resume)
	w.start()
	w.waitSource("orders", 10*time.Second, "state ok", isOK)
	return w
}

// sailOff lets the ship exit and waits for its session to go.
func (w *world) sailOff(session string) {
	w.t.Helper()
	w.touch(session, "go")
	w.waitEvent(session, "ended", 1)
	deadline := time.Now().Add(15 * time.Second)
	for w.hasSession(session) {
		if time.Now().After(deadline) {
			w.t.Fatalf("the exit-0 ship's session never went")
		}
		time.Sleep(100 * time.Millisecond)
	}
}

// S4: a sailed-off ship keeps its report and inbox while an item is unanswered.
// The last answer adds the order, and then the files go.
func TestResumeOrderForASailedOffShipsKeptInbox(t *testing.T) {
	w := sailWorld(t, ordersSource, resumeTarget("orders", "orders"))
	ship, reportPath, dir := w.resumeShip("res4", resumeText, []string{"q-1", "q-2"}, "q-1", "q-2")
	w.answerQuestion(ship.Session, "q-1")
	w.sailOff(ship.Session)
	time.Sleep(2 * time.Second)
	if !exists(reportPath) || !exists(dir) {
		t.Fatalf("the report or inbox went with q-2 unanswered")
	}
	w.holdOrders("an order was added with q-2 unanswered", "orders")

	w.answerQuestion(ship.Session, "q-2")
	waitGone(t, 15*time.Second, reportPath, dir)
	// The files are gone, so the order must already be there.
	if got := w.orderTexts("orders"); !reflect.DeepEqual(got, []string{resumeText}) {
		t.Errorf("orders when the files went = %v, want the resume order added first", got)
	}
	w.waitEvent(ship.Session, "resumed", 1)
}

// The last item was answered while Admiral was down. Start-up adds the order
// before its sweep of fully answered kept inboxes deletes the report.
func TestResumeOrderForAnAnswerWrittenWhileAdmiralWasDown(t *testing.T) {
	w := sailWorld(t, ordersSource, resumeTarget("orders", "orders"))
	ship, reportPath, dir := w.resumeShip("res4b", resumeText, []string{"q-1", "q-2"}, "q-1", "q-2")
	w.answerQuestion(ship.Session, "q-1")
	w.sailOff(ship.Session)
	time.Sleep(2 * time.Second)

	w.in.Stop()
	writeAnswerFile(t, dir, "q-2")
	w.restart()
	w.waitSource("orders", 10*time.Second, "state ok", isOK)

	w.waitOrders("orders", resumeText)
	w.waitEvent(ship.Session, "resumed", 1)
	waitGone(t, 15*time.Second, reportPath, dir)
}

// A ship that answered everything and then exits: ended deletes its report, and
// the resume order is added all the same.
func TestResumeOrderWhenTheShipExitsRightAfterTheLastAnswer(t *testing.T) {
	w := sailWorld(t, ordersSource, resumeTarget("orders", "orders"))
	ship, _, dir := w.resumeShip("res4c", resumeText, []string{"q-1"}, "q-1")
	writeAnswerFile(t, dir, "q-1")
	w.sailOff(ship.Session)

	w.waitOrders("orders", resumeText)
	w.waitEvent(ship.Session, "resumed", 1)
}

// The retry: the engineer's last answer lands while the target is not there.
// Admiral keeps the text in its own state, through the report's deletion and a
// restart, and adds the order once, and only once, the target exists.
func TestResumeOrderRetriesUntilTheTargetExists(t *testing.T) {
	w := sailWorld(t, ordersSource, resumeTarget("later", "orders"))
	ship, reportPath, dir := w.resumeShip("res5", resumeText, []string{"q-1"}, "q-1")
	w.sailOff(ship.Session)
	w.answerQuestion(ship.Session, "q-1") // the last answer: the files go, the target is missing
	waitGone(t, 15*time.Second, reportPath, dir)
	w.holdOrders("an order was added to a source that is not there", "orders")
	if n := len(w.outcomesOf("resumed")); n != 0 {
		t.Fatalf("%d resumed records before the target existed, want none", n)
	}

	w.restart()
	w.waitSource("orders", 10*time.Second, "state ok", isOK)
	w.holdOrders("a restart added an order to a source that is not there", "orders")

	both := map[string]any{"orders": ordersSource["orders"], "later": map[string]any{"plugin": "freeform", "name": "Later"}}
	w.resumeConfig(map[string][]string{"f": {"hook working", sailCue, "exit 0"}}, both, resumeTarget("later", "orders"))
	w.waitOrders("later", resumeText)
	w.waitEvent(ship.Session, "resumed", 1)
	w.holdOrders("the resume order was added to the wrong source", "orders")

	w.restart()
	w.waitSource("later", 10*time.Second, "state ok", isOK)
	w.holdOrders("a restart added a second resume order", "later", resumeText)
	if n := len(w.outcomesOf("resumed")); n != 1 {
		t.Errorf("%d resumed records, want 1", n)
	}
}
