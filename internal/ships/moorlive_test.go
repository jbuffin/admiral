package ships

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jbuffin/admiral/internal/actor"
	"github.com/jbuffin/admiral/internal/launcher"
	"github.com/jbuffin/admiral/internal/moor"
)

const (
	livePR7 = "https://github.com/acme/widgets/pull/7"
	livePR8 = "https://github.com/acme/widgets/pull/8"
	livePR9 = "https://github.com/acme/widgets/pull/9"
)

// openGH answers which PRs are open and counts how often it was asked.
type openGH struct {
	pollGitHub
	om   sync.Mutex
	open map[string]bool
	err  error
	asks int
}

func (g *openGH) OpenPRs(_ context.Context, urls []string) (map[string]bool, error) {
	g.om.Lock()
	defer g.om.Unlock()
	g.asks++
	if g.err != nil {
		return nil, g.err
	}
	out := map[string]bool{}
	for _, u := range urls {
		out[u] = g.open[u]
	}
	return out, nil
}

func (g *openGH) asked() int { g.om.Lock(); defer g.om.Unlock(); return g.asks }

func (g *openGH) set(open map[string]bool, err error) {
	g.om.Lock()
	defer g.om.Unlock()
	g.open, g.err = open, err
}

// liveRig is a live ship, saltpaw, in fleet work whose checkout routes acme/widgets to that fleet.
type liveRig struct {
	*nextRig
	gh    *openGH
	stamp time.Time
}

func newLiveRig(t *testing.T, woken bool) *liveRig {
	t.Helper()
	var n *nextRig
	if woken {
		r := newWakeRig(t, 0)
		r.moored(t, 0)
		if _, lerr := r.s.Wake(context.Background(), "saltpaw", ""); lerr != nil {
			t.Fatalf("Wake = %v", lerr)
		}
		r.started(t, 1)
		mux := http.NewServeMux()
		r.s.Routes(mux)
		n = &nextRig{wakeRig: r, mux: mux}
	} else {
		n = newNextRig(t)
	}
	cfg := "[remote \"origin\"]\n\turl = https://github.com/acme/widgets.git\n"
	if err := os.WriteFile(filepath.Join(n.checkout, ".git", "config"), []byte(cfg), 0o644); err != nil {
		t.Fatal(err)
	}
	g := &openGH{open: map[string]bool{livePR7: true, livePR9: true}}
	n.s.GitHub = func(string) moor.GitHub { return g }
	n.s.baselineAll()
	return &liveRig{nextRig: n, gh: g, stamp: time.Now()}
}

// writeReport puts a version 2 report where the ship's ADMIRAL_REPORT points, with a new mtime each time.
func (r *liveRig) writeReport(t *testing.T, result string, prs ...string) {
	t.Helper()
	path, _, _ := launcher.ShipFiles(r.home, "saltpaw")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	list := []map[string]any{}
	for _, u := range prs {
		list = append(list, map[string]any{"url": u, "closes": []string{}})
	}
	rep := map[string]any{"version": 2, "why": "x", "prs": list, "resume": "/commodore resume run-7"}
	if result != "" {
		rep["result"] = result
	}
	b, _ := json.Marshal(rep)
	if err := os.WriteFile(path, b, 0o600); err != nil {
		t.Fatal(err)
	}
	r.stamp = r.stamp.Add(time.Minute)
	if err := os.Chtimes(path, r.stamp, r.stamp); err != nil {
		t.Fatal(err)
	}
}

type wireShip struct {
	Session string
	Status  string
	OpenPRs []string `json:"open_prs"`
	PRs     []struct{ URL string }
}

func (r *liveRig) wire(t *testing.T) (ship wireShip, raw map[string]json.RawMessage, ok bool) {
	t.Helper()
	rec := httptest.NewRecorder()
	r.mux.ServeHTTP(rec, httptest.NewRequest("GET", "/api/ships", nil))
	var got struct{ Ships []json.RawMessage }
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil || rec.Code != 200 {
		t.Fatalf("GET /api/ships = %d %s", rec.Code, rec.Body)
	}
	for _, b := range got.Ships {
		var s wireShip
		if json.Unmarshal(b, &s) == nil && s.Session == "saltpaw" {
			_ = json.Unmarshal(b, &raw)
			return s, raw, true
		}
	}
	return wireShip{}, nil, false
}

func (r *liveRig) post(path, body string, ctxs ...context.Context) *httptest.ResponseRecorder {
	var req *http.Request
	if body == "" {
		req = httptest.NewRequest("POST", "/api/ships/saltpaw/"+path, nil)
	} else {
		req = httptest.NewRequest("POST", "/api/ships/saltpaw/"+path, strings.NewReader(body))
	}
	for _, c := range ctxs {
		req = req.WithContext(c)
	}
	rec := httptest.NewRecorder()
	r.mux.ServeHTTP(rec, req)
	return rec
}

func (r *liveRig) gone(t *testing.T, what string) {
	t.Helper()
	waitUntil(t, what, func() bool { _ = r.s.Host.Sync(context.Background()); return !r.afloat() })
}

func (r *liveRig) live(t *testing.T) bool {
	t.Helper()
	_ = r.s.Host.Sync(context.Background())
	return r.afloat()
}

func (r *liveRig) mooredFile() string { return filepath.Join(r.home, "moored", "saltpaw.json") }

// events is every outcome line of the ship, in the order written.
func (r *liveRig) events(t *testing.T) []map[string]any {
	t.Helper()
	files, _ := filepath.Glob(filepath.Join(r.home, "outcomes", "*.jsonl"))
	var out []map[string]any
	for _, p := range files {
		b, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		for _, line := range strings.Split(string(b), "\n") {
			var rec map[string]any
			if json.Unmarshal([]byte(line), &rec) == nil && rec["ship"] == "saltpaw" {
				out = append(out, rec)
			}
		}
	}
	return out
}

func (r *liveRig) eventNames(t *testing.T) []string {
	var names []string
	for _, e := range r.events(t) {
		names = append(names, e["event"].(string))
	}
	return names
}

// holdsFor fails when check turns false within d; it is how a test shows that nothing happens.
func holdsFor(t *testing.T, d time.Duration, msg string, check func() bool) {
	t.Helper()
	for end := time.Now().Add(d); time.Now().Before(end); time.Sleep(50 * time.Millisecond) {
		if !check() {
			t.Fatal(msg)
		}
	}
}

func (r *liveRig) waitMoored(t *testing.T) {
	t.Helper()
	waitUntil(t, "the ship to show as moored", func() bool {
		s, _, ok := r.wire(t)
		return ok && s.Status == "moored"
	})
	r.gone(t, "the session to end")
	waitUntil(t, "the ended record", func() bool { return len(outcomeLines(t, r.home, "saltpaw", "ended")) == 1 })
}

// wantMooredLog checks the outcome log of a ship that moored: moored, then ended with KILL, and no stopped.
func (r *liveRig) wantMooredLog(t *testing.T) {
	t.Helper()
	var names []string
	for _, n := range r.eventNames(t) {
		if n == "moored" || n == "ended" || n == "stopped" || n == "returning" {
			names = append(names, n)
		}
	}
	if want := []string{"moored", "ended"}; !reflect.DeepEqual(names, want) {
		t.Errorf("records = %v, want %v: moored before ended, no stopped or returning", names, want)
	}
	if e := outcomeLines(t, r.home, "saltpaw", "ended"); len(e) == 1 && e[0]["signal"] != "KILL" {
		t.Errorf("ended = %v, want signal KILL", e[0])
	}
	s, _, _ := r.wire(t)
	if s.Status != "moored" || len(s.PRs) != 1 || s.PRs[0].URL != livePR7 {
		t.Errorf("ship = %+v, want moored with its PR %s", s, livePR7)
	}
	if _, err := os.Stat(r.mooredFile()); err != nil {
		t.Errorf("no moored record: %v", err)
	}
}

func TestOpenPRsListsTheReportPRsThatGitHubConfirmsOpenAndAsksOnlyWhenTheReportChanges(t *testing.T) {
	r := newLiveRig(t, false)
	go r.s.Run(r.ctx)
	if s, raw, ok := r.wire(t); !ok || s.OpenPRs != nil || raw["open_prs"] != nil {
		t.Fatalf("ship before any report = %+v, want no open_prs", raw)
	}

	r.writeReport(t, "partial", livePR7, livePR8)
	waitUntil(t, "open_prs to list the open PR only", func() bool {
		s, _, _ := r.wire(t)
		return reflect.DeepEqual(s.OpenPRs, []string{livePR7})
	})
	asked := r.gh.asked()
	holdsFor(t, 1500*time.Millisecond, "GitHub was asked again while the report was unchanged", func() bool {
		r.wire(t)
		return r.gh.asked() == asked
	})

	r.writeReport(t, "partial", livePR9, livePR7)
	waitUntil(t, "open_prs to follow the changed report, in report order", func() bool {
		s, _, _ := r.wire(t)
		return reflect.DeepEqual(s.OpenPRs, []string{livePR9, livePR7})
	})

	r.writeReport(t, "partial", livePR8)
	waitUntil(t, "open_prs to be omitted when no report PR is open", func() bool {
		_, raw, _ := r.wire(t)
		return raw["open_prs"] == nil
	})
}

func TestListingShipsNeverAsksGitHubForTheOpenPRs(t *testing.T) {
	r := newLiveRig(t, false) // no Run: only the request could ask
	r.writeReport(t, "done", livePR7)
	for range 3 {
		r.wire(t)
	}
	if n := r.gh.asked(); n != 0 {
		t.Errorf("GET /api/ships asked GitHub %d times, want none", n)
	}
}

func TestAShipMoorsWhenItsTurnEndsWithADoneReportAndAnOpenPR(t *testing.T) {
	r := newLiveRig(t, false)
	go r.s.Run(r.ctx)
	r.turn(t, "working", "")
	r.writeReport(t, "done", livePR7)
	waitUntil(t, "GitHub to confirm the PR", func() bool { s, _, _ := r.wire(t); return len(s.OpenPRs) == 1 })

	holdsFor(t, 1500*time.Millisecond, "the ship moored while its turn was still running", func() bool {
		s, _, _ := r.wire(t)
		_, err := os.Stat(r.mooredFile())
		return s.Status != "moored" && err != nil && r.live(t)
	})

	r.turn(t, "needs_you", "stop")
	r.waitMoored(t)
	r.wantMooredLog(t)
}

func TestAShipThatIsNotDoneWithOpenPRsStaysLive(t *testing.T) {
	for _, c := range []struct {
		name, result string
		prs          []string
		open         map[string]bool
		wantOpen     bool
	}{
		{"partial", "partial", []string{livePR7}, map[string]bool{livePR7: true}, true},
		{"failed", "failed", []string{livePR7}, map[string]bool{livePR7: true}, true},
		{"no result", "", []string{livePR7}, map[string]bool{livePR7: true}, true},
		{"done with only a closed PR", "done", []string{livePR8}, map[string]bool{}, false},
	} {
		t.Run(c.name, func(t *testing.T) {
			r := newLiveRig(t, false)
			r.gh.set(c.open, nil)
			go r.s.Run(r.ctx)
			r.writeReport(t, c.result, c.prs...)
			if c.wantOpen {
				waitUntil(t, "GitHub to confirm the PR", func() bool { s, _, _ := r.wire(t); return len(s.OpenPRs) == 1 })
			} else {
				waitUntil(t, "GitHub to be asked", func() bool { return r.gh.asked() > 0 })
			}
			r.turn(t, "needs_you", "stop")
			holdsFor(t, 1500*time.Millisecond, "the ship left the live list or got a record", func() bool {
				s, _, ok := r.wire(t)
				_, err := os.Stat(r.mooredFile())
				return ok && s.Status == "needs_you" && err != nil && !contains(r.eventNames(t), "moored")
			})
		})
	}
}

func contains(l []string, s string) bool {
	for _, x := range l {
		if x == s {
			return true
		}
	}
	return false
}

func TestAReturningShipIsStoppedNotMooredWhenItsCleanUpTurnEnds(t *testing.T) {
	r := newLiveRig(t, false)
	go r.s.Run(r.ctx)
	r.turn(t, "working", "")
	r.writeReport(t, "done", livePR7)
	waitUntil(t, "GitHub to confirm the PR", func() bool { s, _, _ := r.wire(t); return len(s.OpenPRs) == 1 })
	if rec := r.post("return", ""); rec.Code != http.StatusOK {
		t.Fatalf("return = %d %s", rec.Code, rec.Body)
	}
	r.turn(t, "needs_you", "stop")
	waitUntil(t, "the clean-up prompt at the ship's prompt", func() bool { return strings.Contains(r.pane(t), "Return to port") })
	r.turn(t, "working", "")
	r.turn(t, "needs_you", "stop")
	r.gone(t, "the ship to be stopped")
	waitUntil(t, "the stopped record", func() bool { return len(outcomeLines(t, r.home, "saltpaw", "stopped")) == 1 })
	if contains(r.eventNames(t), "moored") {
		t.Errorf("records = %v, want a returning ship never moored", r.eventNames(t))
	}
	if _, err := os.Stat(r.mooredFile()); err == nil {
		t.Error("a returning ship left a moored record")
	}
}

func TestAWokenShipThatMoorsLogsItsRoundMooredNotAnchored(t *testing.T) {
	for _, c := range []struct {
		name  string
		moors func(t *testing.T, r *liveRig)
	}{
		{"a done report at the end of its turn", func(t *testing.T, r *liveRig) {
			go r.s.Run(r.ctx)
			r.writeReport(t, "done", livePR7)
			r.turn(t, "needs_you", "stop")
		}},
		{"the Moor choice on Stop", func(t *testing.T, r *liveRig) {
			r.writeReport(t, "partial", livePR7)
			if rec := r.post("stop", `{"moor":true}`); rec.Code != http.StatusOK {
				t.Fatalf("stop = %d %s", rec.Code, rec.Body)
			}
		}},
	} {
		t.Run(c.name, func(t *testing.T) {
			r := newLiveRig(t, true)
			c.moors(t, r)
			r.waitMoored(t)
			waitUntil(t, "the round record", func() bool { return len(outcomeLines(t, r.home, "saltpaw", "round")) >= 1 })
			rounds := outcomeLines(t, r.home, "saltpaw", "round")
			if len(rounds) != 1 || rounds[0]["round"] != float64(1) || rounds[0]["end"] != "moored" {
				t.Errorf("round records = %v, want one for round 1 ended moored", rounds)
			}
		})
	}
}

func TestStopWithMoorMoorsAShipWhosePRsGitHubConfirmsAtThatMoment(t *testing.T) {
	r := newLiveRig(t, false) // no Run, so no cached list: the answer comes from a fresh check
	r.writeReport(t, "partial", livePR7, livePR8)

	rec := r.post("stop", `{"moor":true}`)
	if rec.Code != http.StatusOK || strings.TrimSpace(rec.Body.String()) != `{"moored":"saltpaw"}` {
		t.Fatalf("stop with moor = %d %s, want 200 {\"moored\":\"saltpaw\"}", rec.Code, rec.Body)
	}
	r.waitMoored(t)
	r.wantMooredLog(t)
}

func TestStopWithMoorFromAdmiralBotStampsTheMooredRecordVia(t *testing.T) {
	r := newLiveRig(t, false)
	r.writeReport(t, "partial", livePR7)
	ctx := actor.WithFleet(context.Background(), "work")
	if rec := r.post("stop", `{"moor":true}`, ctx); rec.Code != http.StatusOK {
		t.Fatalf("stop = %d %s", rec.Code, rec.Body)
	}
	waitUntil(t, "the moored record", func() bool { return len(outcomeLines(t, r.home, "saltpaw", "moored")) == 1 })
	if got := outcomeLines(t, r.home, "saltpaw", "moored"); got[0]["via"] != "admiralbot" {
		t.Errorf("moored record = %v, want via admiralbot", got[0])
	}
}

func TestReturnWithMoorMoorsWithoutLoggingReturningOrTypingTheCleanUpPrompt(t *testing.T) {
	r := newLiveRig(t, false)
	r.writeReport(t, "partial", livePR7)
	r.turn(t, "needs_you", "stop") // a plain Return to port would type its prompt now

	rec := r.post("return", `{"moor":true}`)
	if rec.Code != http.StatusOK || strings.TrimSpace(rec.Body.String()) != `{"moored":"saltpaw"}` {
		t.Fatalf("return with moor = %d %s, want 200 {\"moored\":\"saltpaw\"}", rec.Code, rec.Body)
	}
	r.waitMoored(t)
	r.wantMooredLog(t)
}

func TestMoorOnAShipWithoutAnOpenPRIsRefusedAndLeavesTheShipAlone(t *testing.T) {
	for _, c := range []struct {
		name   string
		woken  bool
		report func(t *testing.T, r *liveRig)
	}{
		{"no report", false, func(*testing.T, *liveRig) {}},
		{"only a closed PR", false, func(t *testing.T, r *liveRig) { r.writeReport(t, "done", livePR8) }},
		{"GitHub failing past the retry window", false, func(t *testing.T, r *liveRig) {
			r.gh.set(nil, errors.New("gh is down"))
			r.writeReport(t, "partial", livePR7)
			old := moor.RetryWindow
			moor.RetryWindow = 300 * time.Millisecond
			t.Cleanup(func() { moor.RetryWindow = old })
		}},
		{"a woken ship whose new report is not written yet", true, func(*testing.T, *liveRig) {}},
	} {
		for _, route := range []string{"stop", "return"} {
			t.Run(c.name+" on "+route, func(t *testing.T) {
				r := newLiveRig(t, c.woken)
				c.report(t, r)
				rec := r.post(route, `{"moor":true}`)
				if rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), `"error"`) {
					t.Fatalf("%s with moor = %d %s, want 409 with a message", route, rec.Code, rec.Body)
				}
				holdsFor(t, 500*time.Millisecond, "the refused ship was touched", func() bool {
					_, err := os.Stat(r.mooredFile())
					names := r.eventNames(t)
					return r.live(t) && err != nil && !contains(names, "moored") && !contains(names, "stopped") &&
						!contains(names, "ended") && !contains(names, "returning")
				})
			})
		}
	}
}

func TestMoorWhoseRecordCannotBeWrittenKeepsTheShipLive(t *testing.T) {
	for _, route := range []string{"stop", "return"} {
		t.Run(route, func(t *testing.T) {
			r := newLiveRig(t, false)
			r.writeReport(t, "partial", livePR7)
			if err := os.WriteFile(filepath.Join(r.home, "moored"), []byte("not a directory"), 0o600); err != nil {
				t.Fatal(err)
			}
			rec := r.post(route, `{"moor":true}`)
			if rec.Code != http.StatusInternalServerError {
				t.Fatalf("%s with moor = %d %s, want 500", route, rec.Code, rec.Body)
			}
			holdsFor(t, 500*time.Millisecond, "the ship was ended or marked moored", func() bool {
				names := r.eventNames(t)
				return r.live(t) && !contains(names, "moored") && !contains(names, "ended") && !contains(names, "stopped")
			})
		})
	}
}

func TestADoneShipWhoseRecordCannotBeWrittenStaysLive(t *testing.T) {
	r := newLiveRig(t, false)
	if err := os.WriteFile(filepath.Join(r.home, "moored"), []byte("not a directory"), 0o600); err != nil {
		t.Fatal(err)
	}
	go r.s.Run(r.ctx)
	r.writeReport(t, "done", livePR7)
	waitUntil(t, "GitHub to confirm the PR", func() bool { s, _, _ := r.wire(t); return len(s.OpenPRs) == 1 })
	r.turn(t, "needs_you", "stop")
	holdsFor(t, 1500*time.Millisecond, "the ship left the live list", func() bool {
		s, _, ok := r.wire(t)
		return ok && s.Status == "needs_you" && !contains(r.eventNames(t), "ended")
	})
}

func TestMoorFalseOrNoBodyKeepsTodaysStopAndReturnToPort(t *testing.T) {
	r := newLiveRig(t, false)
	r.writeReport(t, "done", livePR7)
	r.turn(t, "working", "")
	if rec := r.post("return", `{"moor":false}`); rec.Code != http.StatusOK || strings.TrimSpace(rec.Body.String()) != `{"returning":true}` {
		t.Fatalf("return with moor false = %d %s, want today's {\"returning\":true}", rec.Code, rec.Body)
	}
	if rec := r.post("stop", `{"moor":false}`); rec.Code != http.StatusOK || strings.TrimSpace(rec.Body.String()) != `{"stopped":"saltpaw"}` {
		t.Fatalf("stop with moor false = %d %s, want today's {\"stopped\":\"saltpaw\"}", rec.Code, rec.Body)
	}
	waitUntil(t, "the stopped record", func() bool { return len(outcomeLines(t, r.home, "saltpaw", "stopped")) == 1 })
	if contains(r.eventNames(t), "moored") {
		t.Errorf("records = %v, want no moored", r.eventNames(t))
	}
}

func TestReturnWithMoorOnAMooredShipKeepsTodays409(t *testing.T) {
	r := newWakeRig(t, 0)
	r.moored(t, 0)
	mux := http.NewServeMux()
	r.s.Routes(mux)
	n := &nextRig{wakeRig: r, mux: mux}
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest("POST", "/api/ships/saltpaw/return", strings.NewReader(`{"moor":true}`)))
	wantError(t, rec, http.StatusConflict, "a moored ship")
	_ = n
}

func TestADoneShipWhoseGitHubCheckFailedMoorsOnceGitHubAnswersWithTheReportUnchanged(t *testing.T) {
	oldWin, oldFirst := moor.RetryWindow, openRetryFirst
	moor.RetryWindow, openRetryFirst = 200*time.Millisecond, 100*time.Millisecond
	t.Cleanup(func() { moor.RetryWindow, openRetryFirst = oldWin, oldFirst })
	r := newLiveRig(t, false)
	r.gh.set(nil, errors.New("gh is down"))
	go r.s.Run(r.ctx)
	r.writeReport(t, "done", livePR7)
	waitUntil(t, "GitHub to be asked", func() bool { return r.gh.asked() > 0 })
	r.turn(t, "needs_you", "stop")
	holdsFor(t, 600*time.Millisecond, "the ship moored or listed a PR while GitHub failed", func() bool {
		s, _, ok := r.wire(t)
		return ok && s.Status == "needs_you" && len(s.OpenPRs) == 0 && !contains(r.eventNames(t), "moored")
	})

	r.gh.set(map[string]bool{livePR7: true}, nil)
	r.waitMoored(t)
	r.wantMooredLog(t)
}

func TestMoorAnswers409WhenGitHubDoesNotAnswer(t *testing.T) {
	old := moor.RetryWindow
	moor.RetryWindow = 200 * time.Millisecond
	t.Cleanup(func() { moor.RetryWindow = old })
	r := newLiveRig(t, false)
	r.writeReport(t, "partial", livePR7)
	r.gh.set(nil, errors.New("gh is down"))
	if rec := r.post("stop", `{"moor":true}`); rec.Code != http.StatusConflict {
		t.Fatalf("stop with moor while gh fails = %d %s, want 409", rec.Code, rec.Body)
	}
	if !r.live(t) || contains(r.eventNames(t), "moored") {
		t.Errorf("the ship was ended or moored by a Moor that GitHub did not confirm")
	}
}

func TestMoorFindsNothingToMoorWhenAStopEndedTheShipFirst(t *testing.T) {
	r := newLiveRig(t, false)
	r.writeReport(t, "partial", livePR7)
	unlock := r.s.lockShip("saltpaw") // the Stop in progress
	done := make(chan *httptest.ResponseRecorder, 1)
	go func() { done <- r.post("stop", `{"moor":true}`) }()
	time.Sleep(300 * time.Millisecond) // the request has checked GitHub and waits for the lock
	if err := r.s.killShip(context.Background(), "saltpaw", false); err != nil {
		t.Fatal(err)
	}
	_ = r.s.Host.Sync(context.Background())
	unlock()
	if rec := <-done; rec.Code != http.StatusInternalServerError {
		t.Errorf("Moor after the ship ended = %d %s, want 500", rec.Code, rec.Body)
	}
	if _, err := os.Stat(r.mooredFile()); err == nil || contains(r.eventNames(t), "moored") {
		t.Errorf("a ship that was already ended got a moored record")
	}
}
