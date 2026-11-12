package e2e

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
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

func mooredReport(prs ...string) string {
	list := []map[string]any{}
	for _, u := range prs {
		list = append(list, map[string]any{"url": u, "closes": []string{}})
	}
	b, _ := json.Marshal(map[string]any{"version": 2, "result": "done", "why": "PR opened", "prs": list,
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
	holdThat(t, "an anchored ship moored, got a record or had its PR looked up", func() bool {
		return byName(w.in.ships(t))[ship.Name].Status == "anchored" && !exists(w.mooredFile(ship.Name)) && !g.asked(mooredPR)
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

func TestMooredShipSailsOffWhenItsPRMerges(t *testing.T) {
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
	w.in.waitShipsList(t, 20*time.Second, "the ship sailed off after the merge", func(l []shipJSON) bool {
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
