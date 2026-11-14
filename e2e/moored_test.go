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

const mooredPR = "https://github.com/acme/widgets/pull/7"

// mooring is a world with one fleet f that has a built-in GitHub source and a checkout whose remote routes to it.
func mooring(t *testing.T, exit string) (*world, *fakeGH) {
	t.Helper()
	w := newWorld(t)
	g := w.newFakeGH()
	g.token("", "fake-token-f")
	w.checkoutOf(w.work, "widgets", "acme/widgets")
	w.outFleets(map[string][]string{"f": {"hook working", "wait go", "sleep 1", exit}}, srcDef{Key: "gh", Plugin: "github", Name: "GitHub", Fleet: "f", Settings: map[string]any{"views": []any{}}})
	return w, g
}

func mooredReport(prs ...string) string { return reportWith("done", prs...) }

func reportWith(result string, prs ...string) string {
	list := []map[string]any{}
	for _, u := range prs {
		list = append(list, map[string]any{"url": u, "closes": []string{}})
	}
	b, _ := json.Marshal(map[string]any{"version": 2, "result": result, "why": "PR opened", "prs": list,
		"resume": "/commodore resume run-7"})
	return string(b)
}

// exitWith lets the fake harness exit once the report is in place.
func (w *world) exitWith(ship shipJSON, report string) {
	w.t.Helper()
	rec := w.record(ship.Session)
	w.seedShipFiles(rec, report)
	w.touch(ship.Session, "go")
}

func (w *world) mooredFile(name string) string { return filepath.Join(w.home, "moored", name+".json") }

func TestMooredShipSurvivesRestart(t *testing.T) {
	t.Parallel()
	w, g := mooring(t, "exit 0")
	g.prState(mooredPR, "OPEN")
	w.start()

	req := w.order("f", "claude", "widgets", commodore, "do it")
	req["model"] = "opus"
	ship := w.launch(req)
	w.exitWith(ship, mooredReport(mooredPR))

	list := w.in.waitShipsList(t, 20*time.Second, "the ship moored", func(l []shipJSON) bool {
		return byName(l)[ship.Name].Status == "moored"
	})
	got := byName(list)[ship.Name]
	if len(got.PRs) != 1 || got.PRs[0].URL != mooredPR || got.PRs[0].State != "OPEN" || got.MooredAt == "" {
		t.Errorf("moored ship = %+v, want its PR %s and a moored time", got, mooredPR)
	}
	if _, err := time.Parse(time.RFC3339, got.MooredAt); err != nil {
		t.Errorf("moored_at = %q, want RFC 3339: %v", got.MooredAt, err)
	}

	var rec map[string]any
	if err := json.Unmarshal([]byte(w.readFile(w.mooredFile(ship.Name))), &rec); err != nil {
		t.Fatalf("the record under moored/<name>.json is not JSON: %v", err)
	}
	skill, _ := rec["skill"].(map[string]any)
	prs, _ := rec["prs"].([]any)
	if rec["name"] != ship.Name || rec["session"] != ship.Session || rec["fleet"] != "f" || rec["harness"] != "claude" ||
		rec["model"] != "opus" || rec["checkout"] != ship.Dir || rec["resume"] != "/commodore resume run-7" ||
		rec["round"] != float64(0) || skill["kind"] != "commodore" || len(prs) != 1 || prs[0] != mooredPR || rec["moored_at"] == "" {
		t.Errorf("record = %v, want name, session, fleet f, claude, model opus, checkout %s, the PR, resume, round 0, skill commodore", rec, ship.Dir)
	}

	waitCond(t, 10*time.Second, "the tmux session is gone", func() bool { return len(w.sessions()) == 0 })
	log := w.waitEvent(ship.Session, "moored", 1)
	if lastOf(log, "ended")["report"] == nil {
		t.Errorf("the ended record has no report: %v", log)
	}

	w.restart()
	list = w.in.waitShipsList(t, 20*time.Second, "the ship still moored after a restart", func(l []shipJSON) bool {
		return byName(l)[ship.Name].Status == "moored"
	})
	if again := byName(list)[ship.Name]; len(again.PRs) != 1 || again.MooredAt != got.MooredAt {
		t.Errorf("after the restart = %+v, want the same PR and moored time %s", again, got.MooredAt)
	}
	if len(w.sessions()) != 0 {
		t.Errorf("sessions after the restart = %v, want none", w.sessions())
	}
}

func TestCleanExitFoundAtStartupIsChecked(t *testing.T) {
	t.Parallel()
	w, g := mooring(t, "exit 0")
	g.prState(mooredPR, "OPEN")
	w.start()
	ship := w.launch(w.order("f", "claude", "widgets", commodore, "do it"))
	rec := w.record(ship.Session)

	w.in.Stop()
	w.seedShipFiles(rec, mooredReport(mooredPR))
	w.touch(ship.Session, "go")
	waitCond(t, 15*time.Second, "the pane has exited", func() bool {
		out, _ := w.tmux("list-panes", "-a", "-F", "#{pane_dead}")
		return strings.Contains(out, "1")
	})
	w.in = launchAdmiral(t, w.home, "127.0.0.1:"+w.in.Port, w.baseEnv())

	w.in.waitShipsList(t, 20*time.Second, "the exit-0 ship found at startup moored", func(l []shipJSON) bool {
		return byName(l)[ship.Name].Status == "moored"
	})
	if !exists(w.mooredFile(ship.Name)) {
		t.Errorf("no record at %s", w.mooredFile(ship.Name))
	}
}

func TestCleanExitWithoutAnOpenPRSailsOff(t *testing.T) {
	t.Parallel()
	w, g := mooring(t, "exit 0")
	g.prState(mooredPR, "CLOSED")
	w.start()
	ship := w.launch(w.order("f", "claude", "widgets", commodore, "do it"))
	w.exitWith(ship, mooredReport(mooredPR))

	waitCond(t, 20*time.Second, "Admiral asked gh about the PR", func() bool { return g.asked(mooredPR) })
	w.waitEvent(ship.Session, "ended", 1)
	w.in.waitShipsList(t, 20*time.Second, "the ship sailed off", func(l []shipJSON) bool { _, ok := byName(l)[ship.Name]; return !ok })
	if exists(w.mooredFile(ship.Name)) {
		t.Errorf("a closed PR left a record")
	}
	for _, m := range w.outcomeLog() {
		if m["event"] == "moored" {
			t.Errorf("a moored record was written for a closed PR: %v", m)
		}
	}
}

func TestNonZeroExitAnchorsWhateverTheReportSays(t *testing.T) {
	t.Parallel()
	w, g := mooring(t, "exit 3")
	g.prState(mooredPR, "OPEN")
	w.start()
	ship := w.launch(w.order("f", "claude", "widgets", commodore, "do it"))
	w.exitWith(ship, mooredReport(mooredPR))

	w.in.waitShipsList(t, 20*time.Second, "the ship anchored", func(l []shipJSON) bool { return byName(l)[ship.Name].Status == "anchored" })
	holdThat(t, "an anchored ship moored or got a record", func() bool {
		return byName(w.in.ships(t))[ship.Name].Status == "anchored" && !exists(w.mooredFile(ship.Name))
	})
}

func waitCond(t *testing.T, d time.Duration, what string, ok func() bool) {
	t.Helper()
	for end := time.Now().Add(d); !ok(); time.Sleep(100 * time.Millisecond) {
		if time.Now().After(end) {
			t.Fatalf("timed out after %s waiting for: %s", d, what)
		}
	}
}

// asked reports whether any gh call named the pull request.
func (g *fakeGH) asked(url string) bool {
	for _, c := range g.calls() {
		if c.mentions(url) {
			return true
		}
	}
	return false
}

// prChips steers the chips the batched poll reads for a PR: any of them may be "" for none.
func (g *fakeGH) prChips(url, review, checks, mergeable string) {
	g.t.Helper()
	key := strings.NewReplacer("/", "_", ":", "_").Replace(url)
	for dir, v := range map[string]string{"pr_review": review, "pr_checks": checks, "pr_mergeable": mergeable} {
		if err := os.MkdirAll(filepath.Join(g.dir, dir), 0o755); err != nil {
			g.t.Fatal(err)
		}
		if v != "" {
			g.write(dir+"/"+key, v+"\n")
		}
	}
}

// prEvents steers the events the poll reads for a PR: one COMMENTED review by author, submitted at the given time, with body.
func (g *fakeGH) prEvents(url, author, body string, at time.Time) {
	g.t.Helper()
	key := strings.NewReplacer("/", "_", ":", "_").Replace(url)
	if err := os.MkdirAll(filepath.Join(g.dir, "pr_events"), 0o755); err != nil {
		g.t.Fatal(err)
	}
	ev, err := json.Marshal(map[string]any{"reviews": map[string]any{"nodes": []map[string]any{{
		"id": "R1", "author": map[string]any{"__typename": "User", "login": author}, "state": "COMMENTED",
		"submittedAt": at.UTC().Format(time.RFC3339), "body": body, "url": url + "#pullrequestreview-1",
		"comments": map[string]any{"totalCount": 0}}}}})
	if err != nil {
		g.t.Fatal(err)
	}
	g.write("pr_events/"+key, string(ev)+"\n")
}

// prEventList is prEvents with a plain PR comment from stranger added, id C1.
func (g *fakeGH) prEventList(url, reviewer, reviewBody string, reviewAt time.Time, stranger, strangerBody string, strangerAt time.Time) {
	g.t.Helper()
	key := strings.NewReplacer("/", "_", ":", "_").Replace(url)
	nodes := []map[string]any{{"id": "C1", "author": map[string]any{"__typename": "User", "login": stranger},
		"createdAt": strangerAt.UTC().Format(time.RFC3339), "body": strangerBody, "url": url + "#issuecomment-1"}}
	ev, err := json.Marshal(map[string]any{
		"reviews": map[string]any{"nodes": []map[string]any{{
			"id": "R1", "author": map[string]any{"__typename": "User", "login": reviewer}, "state": "COMMENTED",
			"submittedAt": reviewAt.UTC().Format(time.RFC3339), "body": reviewBody, "url": url + "#pullrequestreview-1",
			"comments": map[string]any{"totalCount": 0}}}},
		"comments": map[string]any{"nodes": nodes},
	})
	if err != nil {
		g.t.Fatal(err)
	}
	g.write("pr_events/"+key, string(ev)+"\n")
}

func (w *world) kickMoored() {
	w.t.Helper()
	if code, body := send(w.t, http.MethodPost, w.in.LocalURL+"/api/moored/refresh", nil); code != http.StatusAccepted {
		w.t.Fatalf("POST /api/moored/refresh = %d, want 202: %s", code, body)
	}
}

func TestMooredShipReturnsToPortWhenItsPRMerges(t *testing.T) {
	t.Parallel()
	w, g := mooring(t, "exit 0")
	g.prState(mooredPR, "OPEN")
	g.prChips(mooredPR, "APPROVED", "PENDING", "MERGEABLE")
	w.start()
	ship := w.launch(w.order("f", "claude", "widgets", commodore, "do it"))
	w.exitWith(ship, mooredReport(mooredPR))
	w.in.waitShipsList(t, 20*time.Second, "the ship moored", func(l []shipJSON) bool { return byName(l)[ship.Name].Status == "moored" })

	w.kickMoored()
	list := w.in.waitShipsList(t, 20*time.Second, "the poll's chips on the moored ship", func(l []shipJSON) bool {
		prs := byName(l)[ship.Name].PRs
		return len(prs) == 1 && prs[0].Review == "APPROVED"
	})
	got := byName(list)[ship.Name]
	if pr := got.PRs[0]; pr.State != "OPEN" || pr.Checks != "PENDING" || pr.Mergeable != "MERGEABLE" || pr.Conflicts {
		t.Errorf("chips = %+v, want OPEN, approved, checks PENDING, MERGEABLE, no conflicts", pr)
	}
	if _, err := time.Parse(time.RFC3339, got.PolledAt); err != nil || got.Stale {
		t.Errorf("polled_at = %q, stale = %v; want the poll's time and not stale", got.PolledAt, got.Stale)
	}

	g.prState(mooredPR, "MERGED")
	w.kickMoored()
	woken := lastOf(w.waitEvent(ship.Session, "woken", 1), "woken")
	if woken["by"] != "return" {
		t.Errorf("woken = %v, want by return", woken)
	}
	w.in.waitShipsList(t, 20*time.Second, "the ship gone after its clean-up wake", func(l []shipJSON) bool {
		_, ok := byName(l)[ship.Name]
		return !ok
	})
	if exists(w.mooredFile(ship.Name)) {
		t.Errorf("the record %s is still there, so the name is still reserved", w.mooredFile(ship.Name))
	}
	log := w.waitEvent(ship.Session, "unmoored", 1)
	n := 0
	for _, m := range log {
		if m["event"] == "unmoored" {
			n++
		}
	}
	ev := lastOf(log, "unmoored")
	prs, _ := ev["prs"].([]any)
	first, _ := prs[0].(map[string]any)
	if n != 1 || ev["reason"] != "merged" || len(prs) != 1 || first["url"] != mooredPR || first["state"] != "MERGED" {
		t.Errorf("unmoored events = %d, last = %v; want one with reason merged and the PR MERGED", n, ev)
	}
}

// liveMooring is a world whose ship writes nothing and stays up after its turn ends, so the server decides what happens.
func liveMooring(t *testing.T) (*world, *fakeGH) {
	t.Helper()
	w := newWorld(t)
	g := w.newFakeGH()
	g.token("", "fake-token-f")
	g.prState(mooredPR, "OPEN")
	w.checkoutOf(w.work, "widgets", "acme/widgets")
	w.outFleets(map[string][]string{"f": {"record", "bracketedpaste", "hook working", "wait go", `event Stop {"background_tasks":[]}`}},
		srcDef{Key: "gh", Plugin: "github", Name: "GitHub", Fleet: "f", Settings: map[string]any{"views": []any{}}})
	w.start()
	return w, g
}

func (w *world) launchLive() shipJSON {
	w.t.Helper()
	ship := w.launch(w.order("f", "claude", "widgets", commodore, "do it"))
	w.waitFile(ship.Session, "recorded")
	return ship
}

func (w *world) writeReportFor(ship shipJSON, report string) {
	w.t.Helper()
	path, _ := shipPaths(w.record(ship.Session))
	if err := os.WriteFile(path, []byte(report), 0o600); err != nil {
		w.t.Fatal(err)
	}
}

func (w *world) wantMoored(ship shipJSON) {
	w.t.Helper()
	list := w.in.waitShipsList(w.t, 20*time.Second, "the ship moored", func(l []shipJSON) bool {
		return byName(l)[ship.Name].Status == "moored"
	})
	if got := byName(list)[ship.Name]; len(got.PRs) != 1 || got.PRs[0].URL != mooredPR {
		w.t.Errorf("moored ship = %+v, want its PR %s", got, mooredPR)
	}
	waitCond(w.t, 10*time.Second, "the tmux session is gone", func() bool { return !w.hasSession(ship.Session) })
	log := w.waitEvent(ship.Session, "ended", 1)
	var got []string
	for _, e := range eventsOf(log) {
		if e == "moored" || e == "ended" || e == "stopped" || e == "returning" {
			got = append(got, e)
		}
	}
	if want := []string{"moored", "ended"}; !reflect.DeepEqual(got, want) {
		w.t.Errorf("records = %v, want %v: moored before ended, no stopped or returning", got, want)
	}
	if e := lastOf(log, "ended"); e["signal"] != "KILL" {
		w.t.Errorf("ended = %v, want signal KILL", e)
	}
	if !exists(w.mooredFile(ship.Name)) {
		w.t.Errorf("no moored record for %s", ship.Name)
	}
}

func TestAShipMoorsWhenCommodoreIsDoneAndItsTurnEnds(t *testing.T) {
	t.Parallel()
	w, g := liveMooring(t)
	ship := w.launchLive()

	w.writeReportFor(ship, mooredReport(mooredPR))
	w.in.waitShipsList(t, 20*time.Second, "open_prs to list the report's open PR", func(l []shipJSON) bool {
		return reflect.DeepEqual(byName(l)[ship.Name].OpenPRs, []string{mooredPR})
	})
	holdThat(t, "the ship moored while its turn was still running", func() bool {
		return byName(w.in.ships(t))[ship.Name].Status != "moored" && w.hasSession(ship.Session)
	})
	if !g.asked(mooredPR) {
		t.Error("GitHub was never asked about the report's PR")
	}

	w.touch(ship.Session, "go")
	w.wantMoored(ship)
}

func TestAShipWithAPartialReportStaysLiveWhenItsTurnEnds(t *testing.T) {
	t.Parallel()
	w, _ := liveMooring(t)
	ship := w.launchLive()
	w.writeReportFor(ship, reportWith("partial", mooredPR))
	w.in.waitShipsList(t, 20*time.Second, "open_prs to list the report's open PR", func(l []shipJSON) bool {
		return reflect.DeepEqual(byName(l)[ship.Name].OpenPRs, []string{mooredPR})
	})
	w.touch(ship.Session, "go")
	w.waitFile(ship.Session, "cues-done")
	w.in.waitShipsList(t, 20*time.Second, "the ship to need you at the end of its turn", func(l []shipJSON) bool {
		return byName(l)[ship.Name].Status == "needs_you"
	})
	holdThat(t, "a ship whose report is partial left the live list", func() bool {
		s := byName(w.in.ships(t))[ship.Name]
		return s.Status != "moored" && w.hasSession(ship.Session) && !exists(w.mooredFile(ship.Name))
	})
}

func TestReturnToPortWithMoorMoorsTheShipWithoutTheCleanUpPrompt(t *testing.T) {
	t.Parallel()
	w, _ := liveMooring(t)
	ship := w.launchLive()
	w.writeReportFor(ship, reportWith("partial", mooredPR))
	w.touch(ship.Session, "go")
	w.waitFile(ship.Session, "cues-done")

	code, body := send(t, http.MethodPost, w.shipURL(ship.Session, "/return"), map[string]any{"moor": true})
	if code != http.StatusOK || strings.TrimSpace(body) != `{"moored":"`+ship.Session+`"}` {
		t.Fatalf("return with moor = %d %s, want 200 {\"moored\":%q}", code, body, ship.Session)
	}
	w.wantMoored(ship)
	if got := w.inputOf(ship.Session, ""); strings.Contains(got, "Return to port") {
		t.Errorf("the ship's terminal input = %q, want no clean-up prompt", got)
	}

	if code, _ := send(t, http.MethodPost, w.shipURL(ship.Session, "/return"), map[string]any{"moor": true}); code != http.StatusConflict {
		t.Errorf("return with moor on the moored ship = %d, want today's 409", code)
	}
}

const (
	reviewedPR  = "https://github.com/acme/widgets/pull/7"
	stackLayer1 = "https://github.com/acme/widgets/pull/21"
	stackLayer2 = "https://github.com/acme/widgets/pull/23"
	reviewStack = "https://github.com/acme/widgets/stack/5"
	otherAuthor = "dana"
)

// prByAuthor steers the author and the review events the poll reads for a PR in one write, since prAuthored and prEvents each replace the whole file.
func (g *fakeGH) prByAuthor(url, author string, createdAt time.Time, reviewer, body string, reviewAt time.Time) {
	g.t.Helper()
	key := strings.NewReplacer("/", "_", ":", "_").Replace(url)
	if err := os.MkdirAll(filepath.Join(g.dir, "pr_events"), 0o755); err != nil {
		g.t.Fatal(err)
	}
	ev := map[string]any{"author": map[string]any{"__typename": "User", "login": author}, "createdAt": createdAt.UTC().Format(time.RFC3339)}
	if reviewer != "" {
		ev["reviews"] = map[string]any{"nodes": []map[string]any{{
			"id": "R1", "author": map[string]any{"__typename": "User", "login": reviewer}, "state": "COMMENTED",
			"submittedAt": reviewAt.UTC().Format(time.RFC3339), "body": body, "url": url + "#pullrequestreview-1",
			"comments": map[string]any{"totalCount": 0}}}}
	}
	b, err := json.Marshal(ev)
	if err != nil {
		g.t.Fatal(err)
	}
	g.write("pr_events/"+key, string(b)+"\n")
}

// prReviewedBy is prByAuthor for a PR the viewer reviewed on commit reviewed while its head is head: the viewer's review is the one submitted at reviewAt.
func (g *fakeGH) prReviewedBy(url, author string, createdAt time.Time, viewer, reviewed, head string, reviewAt time.Time) {
	g.t.Helper()
	key := strings.NewReplacer("/", "_", ":", "_").Replace(url)
	if err := os.MkdirAll(filepath.Join(g.dir, "pr_events"), 0o755); err != nil {
		g.t.Fatal(err)
	}
	ev := map[string]any{"author": map[string]any{"__typename": "User", "login": author}, "createdAt": createdAt.UTC().Format(time.RFC3339),
		"headRefOid": head,
		"reviews": map[string]any{"nodes": []map[string]any{{
			"id": "R1", "author": map[string]any{"__typename": "User", "login": viewer}, "state": "COMMENTED",
			"commit": map[string]any{"oid": reviewed}, "submittedAt": reviewAt.UTC().Format(time.RFC3339), "body": "looks fine", "url": url + "#pullrequestreview-1",
			"comments": map[string]any{"totalCount": 0}}}}}
	b, err := json.Marshal(ev)
	if err != nil {
		g.t.Fatal(err)
	}
	g.write("pr_events/"+key, string(b)+"\n")
}

// reviewingWorld is liveMooring with an order source whose one order, o1, carries url, and a checkout whose remote routes to the GitHub source.
func reviewingWorld(t *testing.T, url string) (*world, *fakeGH) {
	t.Helper()
	w := newWorld(t)
	g := w.newFakeGH()
	g.token("", "fake-token-f")
	w.checkoutOf(w.work, "widgets", "acme/widgets")
	order := map[string]any{"id": "o1", "title": "Review it", "text": "review " + url}
	if url != "" {
		order["url"] = url
	}
	p := w.newTP("rv")
	p.mainOrders(order)
	w.installPlugin("testplug", "testplug")
	w.outFleets(map[string][]string{"f": {"record", "bracketedpaste", "hook working", "wait go", `event Stop {"background_tasks":[]}`}},
		srcDef{Key: "gh", Plugin: "github", Name: "GitHub", Fleet: "f", Settings: map[string]any{"views": []any{}}},
		tpSource("rv", p, nil))
	w.start()
	w.waitSource("rv", 10*time.Second, "the order", func(s sourceJSON) bool { return isOK(s) && len(s.orderIDs()) == 1 })
	return w, g
}

// launchReview launches order o1 of reviewingWorld's order source in the checkout of acme/widgets.
func (w *world) launchReview() shipJSON {
	w.t.Helper()
	ship := w.launch(map[string]any{"fleet": "f", "harness": "claude", "dir": filepath.Join(w.work, "widgets"), "skill": commodore,
		"source": "rv", "order_id": "o1"})
	w.waitFile(ship.Session, "recorded")
	return ship
}

// wantMooredWith is wantMoored for a ship that carries several PRs: they are listed in this order, each reviewing when reviewing says so.
func (w *world) wantMooredWith(ship shipJSON, reviewing bool, urls ...string) shipJSON {
	w.t.Helper()
	list := w.in.waitShipsList(w.t, 20*time.Second, "the ship moored", func(l []shipJSON) bool {
		return byName(l)[ship.Name].Status == "moored"
	})
	got := byName(list)[ship.Name]
	var have []string
	for _, p := range got.PRs {
		have = append(have, p.URL)
		if p.Reviewing != reviewing || p.Author != otherAuthor {
			w.t.Errorf("moored PR %+v, want reviewing %v by %s", p, reviewing, otherAuthor)
		}
	}
	if !reflect.DeepEqual(have, urls) {
		w.t.Errorf("moored PRs = %v, want %v in this order", have, urls)
	}
	waitCond(w.t, 10*time.Second, "the tmux session is gone", func() bool { return !w.hasSession(ship.Session) })
	if !exists(w.mooredFile(ship.Name)) {
		w.t.Errorf("no moored record for %s", ship.Name)
	}
	return got
}

func (w *world) finishTurn(ship shipJSON, report string) {
	w.t.Helper()
	w.writeReportFor(ship, report)
	w.touch(ship.Session, "go")
}

func TestAShipLaunchedOnAnotherAuthorsPRMoorsAndIsListedAsReviewing(t *testing.T) {
	t.Parallel()
	w, g := reviewingWorld(t, reviewedPR)
	g.prState(reviewedPR, "OPEN")
	g.prByAuthor(reviewedPR, otherAuthor, time.Now().Add(-time.Hour), "", "", time.Time{})
	ship := w.launchReview()

	w.finishTurn(ship, mooredReport())
	got := w.wantMooredWith(ship, true, reviewedPR)
	if !exists(w.mooredFile(ship.Name)) || !strings.Contains(w.readFile(w.mooredFile(ship.Name)), reviewedPR) {
		t.Errorf("record of %s = %s, want it to list the reviewed PR", got.Name, w.readFile(w.mooredFile(ship.Name)))
	}
	if code, body := w.postMergeWith(t, ship, map[string]any{}, w.pageMergeToken(t)); code != http.StatusConflict {
		t.Errorf("merge on a reviewing ship = %d %s, want 409", code, body)
	}
}

func TestAShipLaunchedOnAStackMoorsCarryingEveryOpenLayerBottomFirst(t *testing.T) {
	t.Parallel()
	w, g := reviewingWorld(t, reviewStack)
	for _, u := range []string{stackLayer1, stackLayer2} {
		g.prState(u, "OPEN")
		g.prByAuthor(u, otherAuthor, time.Now().Add(-time.Hour), "", "", time.Time{})
	}
	g.rest("stack", "GET", "repos/acme/widgets/stacks/5", 200, map[string]any{"pull_requests": []map[string]any{
		{"number": 21, "state": "open"}, {"number": 22, "state": "closed"}, {"number": 23, "state": "open"}}}, "")
	ship := w.launchReview()

	w.finishTurn(ship, mooredReport())
	w.wantMooredWith(ship, true, stackLayer1, stackLayer2)
}

func TestAFreeTextShipWithADoneReportThatListsNoPRsDoesNotMoor(t *testing.T) {
	t.Parallel()
	w, _ := reviewingWorld(t, "")
	ship := w.launchReview()

	w.finishTurn(ship, mooredReport())
	w.waitFile(ship.Session, "cues-done")
	holdThat(t, "a ship with no PR moored", func() bool {
		return byName(w.in.ships(t))[ship.Name].Status != "moored" && w.hasSession(ship.Session) && !exists(w.mooredFile(ship.Name))
	})
}

func TestAReviewedPRTheAuthorPushedToShowsRespondedWithoutWakingAndReReviewWakesTheShipOnThatURL(t *testing.T) {
	t.Parallel()
	w, g := reviewingWorld(t, reviewedPR)
	g.prState(reviewedPR, "OPEN")
	created := time.Now().Add(-time.Hour)
	g.prReviewedBy(reviewedPR, otherAuthor, created, "fake-viewer", "aaa111", "aaa111", time.Now().Add(-20*time.Minute))
	ship := w.launchReview()
	w.finishTurn(ship, mooredReport())
	w.wantMooredWith(ship, true, reviewedPR)

	w.kickMoored()
	g.prReviewedBy(reviewedPR, otherAuthor, created, "fake-viewer", "aaa111", "bbb222", time.Now().Add(-20*time.Minute)) // the author pushed
	w.kickMoored()
	list := w.in.waitShipsList(t, 20*time.Second, "the author's push shows as responded", func(l []shipJSON) bool {
		prs := byName(l)[ship.Name].PRs
		return len(prs) == 1 && prs[0].Responded
	})
	if got := byName(list)[ship.Name]; got.Status != "moored" || !exists(w.mooredFile(ship.Name)) {
		t.Errorf("ship %+v, want it still moored: a response never wakes a reviewing ship", got)
	}

	if code, body := send(t, http.MethodPost, w.in.LocalURL+"/api/ships/"+ship.Session+"/rereview", map[string]any{"url": reviewedPR + "/"}); code != http.StatusBadRequest {
		t.Errorf("rereview on a URL the ship does not carry = %d %s, want 400", code, body)
	}
	if err := os.RemoveAll(filepath.Join(w.rec, ship.Session)); err != nil { // the fake harness reuses a session's recording directory
		t.Fatal(err)
	}
	code, body := send(t, http.MethodPost, w.in.LocalURL+"/api/ships/"+ship.Session+"/rereview", map[string]any{"url": reviewedPR})
	if code != http.StatusCreated {
		t.Fatalf("POST /api/ships/%s/rereview = %d, want 201: %s", ship.Session, code, body)
	}
	if woken := decodeShip(t, body); woken.Session != ship.Session || woken.Name != ship.Name {
		t.Errorf("woken ship = %s (%s), want %s (%s)", woken.Name, woken.Session, ship.Name, ship.Session)
	}
	if got := promptWithHooks(t, w, w.record(ship.Session)); got != "/commodore "+reviewedPR {
		t.Errorf("prompt = %q, want %q", got, "/commodore "+reviewedPR)
	}
	log := w.waitEvent(ship.Session, "woken", 1)
	if e := lastOf(log, "woken"); e["by"] != "hand" || e["pr"] != reviewedPR {
		t.Errorf("woken event = %v, want by hand on %s", e, reviewedPR)
	}
}
