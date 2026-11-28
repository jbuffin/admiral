package sources

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type obj = map[string]any

func instant(t *testing.T, v any) time.Time {
	t.Helper()
	s, ok := v.(string)
	if !ok {
		t.Fatalf("want an RFC 3339 string, got %#v", v)
	}
	at, err := time.Parse(time.RFC3339, s)
	if err != nil {
		t.Fatalf("%q is not RFC 3339: %v", s, err)
	}
	return at
}

// asJSON is v as the API sends it.
func asJSON(t *testing.T, v any) obj {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	var m obj
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatal(err)
	}
	return m
}

func absent(t *testing.T, m obj, what string, keys ...string) {
	t.Helper()
	for _, k := range keys {
		if v, ok := m[k]; ok {
			t.Errorf("%s carries %s = %v, want it left out", what, k, v)
		}
	}
}

func searchNode(kind string, n int, extra obj) obj {
	node := obj{"number": n, "title": "T", "url": "https://github.com/acme/widgets/" + kind + "/" + string(rune('0'+n%10)) + "",
		"repository": obj{"nameWithOwner": "acme/widgets", "url": "https://github.com/acme/widgets"}, "labels": obj{"nodes": []any{}}}
	if kind == "pull" {
		for k, v := range (obj{"state": "OPEN", "reviewDecision": nil, "mergeable": "MERGEABLE", "mergeStateStatus": "CLEAN", "statusCheckRollup": nil, "headRefOid": "head1"}) {
			node[k] = v
		}
	}
	for k, v := range extra {
		node[k] = v
	}
	return node
}

func writeSearch(t *testing.T, g *fakeGH, key, match string, nodes ...obj) {
	t.Helper()
	b, err := json.Marshal(obj{"data": obj{"viewer": obj{"login": "fake-viewer"}, "search": obj{"nodes": nodes}}})
	if err != nil {
		t.Fatal(err)
	}
	g.write("search/"+key+".match", match)
	g.write("search/"+key+".json", string(b))
	g.unfail(key)
}

func servedOrders(t *testing.T, s SourceView, view string) map[string]obj {
	t.Helper()
	out := map[string]obj{}
	for _, o := range s.view(view).Orders {
		out[o.ID] = asJSON(t, o)
	}
	return out
}

func TestGitHubSourceServesTheOrderFieldsAndLeavesOutWhatIsUnknown(t *testing.T) {
	g := newFakeGH(t)
	g.token("", "fake-token-default-1")
	writeSearch(t, g, "bugs", "label:bug",
		searchNode("pull", 20, obj{
			"author": obj{"login": "alice"}, "createdAt": "2026-11-10T09:30:00Z", "bodyText": "Fixes the login race.",
			"statusCheckRollup": obj{"state": "FAILURE", "contexts": obj{
				"checkRunCountsByState":      []obj{{"state": "FAILURE", "count": 2}, {"state": "CANCELLED", "count": 1}},
				"statusContextCountsByState": []obj{}}},
			"viewerLatestReview": obj{"state": "APPROVED", "commit": obj{"oid": "head1"}},
			"timelineItems": obj{"nodes": []obj{{"__typename": "ReviewRequestedEvent", "createdAt": "2026-11-10T10:00:00Z",
				"actor": obj{"login": "bob"}, "requestedReviewer": obj{"login": "fake-viewer"}}}},
		}),
		searchNode("pull", 21, obj{
			"author": obj{"login": "bob"}, "createdAt": "2026-11-11T09:30:00Z", "bodyText": " \n ",
			"viewerLatestReview": obj{"state": "APPROVED", "commit": obj{"oid": "older"}},
			"timelineItems": obj{"nodes": []obj{{"__typename": "ReviewRequestedEvent", "createdAt": "2026-11-11T10:00:00Z",
				"actor": obj{"login": "bob"}, "requestedReviewer": obj{}}}},
		}),
		searchNode("issues", 13, obj{"author": obj{"login": "erin"}, "createdAt": "2026-10-01T12:00:00Z", "bodyText": "Crash on start.", "subIssuesSummary": obj{"total": 0}}))
	f := newServiceFixture(t, ghConfig(campaignFleet, campaignSource))
	f.startSources()
	src := f.waitView("gha", "the bugs", func(s SourceView) bool { return len(s.orders("bugs")) == 3 })
	orders := servedOrders(t, src, "bugs")

	pr20, pr21, issue := orders["acme/widgets#20"], orders["acme/widgets#21"], orders["acme/widgets#13"]
	if pr20 == nil || pr21 == nil || issue == nil {
		t.Fatalf("bugs orders = %v, want 20, 21 and 13", src.orders("bugs"))
	}

	if pr20["author"] != "alice" || !instant(t, pr20["opened_at"]).Equal(instant(t, "2026-11-10T09:30:00Z")) || pr20["excerpt"] != "Fixes the login race." {
		t.Errorf("pull request 20 = %v, want author alice, opened_at 2026-11-10T09:30:00Z and its excerpt", pr20)
	}
	if !instant(t, pr20["review_requested_at"]).Equal(instant(t, "2026-11-10T10:00:00Z")) || pr20["reviewed_since_push"] != true {
		t.Errorf("pull request 20 = %v, want review_requested_at 2026-11-10T10:00:00Z and reviewed_since_push true", pr20)
	}
	if pr, _ := pr20["pr"].(obj); pr["failing_check_count"] != float64(2) {
		t.Errorf("pull request 20 pr = %v, want failing_check_count 2", pr20["pr"])
	}

	absent(t, pr21, "pull request 21 (empty body, a team's request, no failing check)", "excerpt", "review_requested_at")
	if pr, _ := pr21["pr"].(obj); pr == nil {
		t.Errorf("pull request 21 has no pr object: %v", pr21)
	} else {
		absent(t, pr, "pull request 21's pr", "failing_check_count")
	}
	if v, ok := pr21["reviewed_since_push"]; !ok || v != false {
		t.Errorf("pull request 21 reviewed_since_push = %v (present %v), want false: the review is on an older commit", v, ok)
	}

	if issue["author"] != "erin" || !instant(t, issue["opened_at"]).Equal(instant(t, "2026-10-01T12:00:00Z")) || issue["excerpt"] != "Crash on start." {
		t.Errorf("issue 13 = %v, want author erin, opened_at 2026-10-01T12:00:00Z and its excerpt", issue)
	}
	absent(t, issue, "the issue", "pr", "review_requested_at", "reviewed_since_push")
}

// holdSearches makes every search wait until the returned release runs, and release returns when it did.
func (g *fakeGH) holdSearches() (release func() time.Time) {
	g.t.Helper()
	g.write("search.hold", "")
	return func() time.Time {
		g.t.Helper()
		at := time.Now()
		if err := os.Remove(filepath.Join(g.dir, "search.hold")); err != nil {
			g.t.Fatal(err)
		}
		return at
	}
}

// waitSearchBegun waits for a gh graphql call beyond the first n, which a held search is then stuck in.
func (g *fakeGH) waitSearchBegun(n int) {
	g.t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for g.searches() <= n {
		if time.Now().After(deadline) {
			g.t.Fatal("the search never began")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// restartUntilReady restarts the source and waits for the new process to list its panels, pull its orders and settle.
func (f *serviceFixture) restartUntilReady(key string) {
	f.t.Helper()
	r := f.Svc.Sup.runner(key)
	sid := func() (int, bool) {
		r.mu.Lock()
		defer r.mu.Unlock()
		return r.sid, r.ready
	}
	before, _ := sid()
	if !f.Svc.Sup.Restart(key) {
		f.t.Fatal("Restart found no runner")
	}
	deadline := time.Now().Add(15 * time.Second)
	for {
		if n, ready := sid(); n != before && ready {
			return
		}
		if time.Now().After(deadline) {
			f.t.Fatal("the source did not restart")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// refreshedAt is when the view was last answered, and false when its JSON has no refreshed_at.
func refreshedAt(t *testing.T, p Panel) (time.Time, bool) {
	t.Helper()
	v, ok := asJSON(t, p)["refreshed_at"]
	if !ok {
		return time.Time{}, false
	}
	return instant(t, v), true
}

func TestGitHubViewRefreshedAtMarksTheLastSuccessfulSearch(t *testing.T) {
	g := newFakeGH(t)
	g.token("", "fake-token-default-1")
	g.write("search/bugs.match", "label:bug")
	g.write("search/bugs.json", "{}")
	g.fail("bugs")
	f := newServiceFixture(t, ghConfig(campaignFleet, campaignSource))
	f.startSources()
	refresh := func() {
		t.Helper()
		if code, body := f.do(http.MethodPost, "/gha/refresh", map[string]any{}); code != http.StatusOK {
			t.Fatalf("POST refresh = %d %s", code, body)
		}
	}
	bugs := func(s SourceView) Panel { return s.view("bugs") }

	failed := f.waitView("gha", "the failed search", func(s SourceView) bool { return bugs(s).Error != "" })
	if at, ok := refreshedAt(t, bugs(failed)); ok {
		t.Fatalf("a view that has had no successful search has refreshed_at %v, want none", at)
	}

	writeSearch(t, g, "bugs", "label:bug", searchNode("issues", 13, obj{"subIssuesSummary": obj{"total": 0}}))
	refresh()
	first := f.waitView("gha", "its first successful search", func(s SourceView) bool { _, ok := refreshedAt(t, bugs(s)); return ok })
	t1, _ := refreshedAt(t, bugs(first))
	if len(first.orders("bugs")) != 1 {
		t.Fatalf("bugs = %v, want one order", first.orders("bugs"))
	}

	refresh()
	second := f.waitView("gha", "a later search that returns the same orders", func(s SourceView) bool {
		at, ok := refreshedAt(t, bugs(s))
		return ok && at.After(t1)
	})
	t2, _ := refreshedAt(t, bugs(second))
	if len(second.orders("bugs")) != 1 {
		t.Fatalf("bugs = %v, want the same single order", second.orders("bugs"))
	}

	g.fail("bugs")
	refresh()
	broken := f.waitView("gha", "the failed search", func(s SourceView) bool { return bugs(s).Error != "" })
	if at, ok := refreshedAt(t, bugs(broken)); !ok || !at.Equal(t2) {
		t.Errorf("after a failed search refreshed_at = %v (present %v), want it unchanged at %v", at, ok, t2)
	}

	// Refresh now answers at once, but only the finished search may stamp the time.
	g.unfail("bugs")
	release := g.holdSearches()
	before := g.searches()
	refresh()
	g.waitSearchBegun(before)
	if at, ok := refreshedAt(t, bugs(f.Svc.viewOf(t, "gha"))); !ok || !at.Equal(t2) {
		t.Errorf("while the search is held refreshed_at = %v (present %v), want it unchanged at %v until the search has finished", at, ok, t2)
	}
	released := release()
	done := f.waitView("gha", "the held search to finish", func(s SourceView) bool {
		at, ok := refreshedAt(t, bugs(s))
		return ok && at.After(t2)
	})
	if t3, _ := refreshedAt(t, bugs(done)); t3.Before(released) {
		t.Errorf("refreshed_at = %v is before the search was released at %v: it was stamped before the search finished", t3, released)
	}
}

func (s *Service) viewOf(t *testing.T, key string) SourceView {
	t.Helper()
	for _, v := range s.View() {
		if v.Key == key {
			return v
		}
	}
	t.Fatalf("no source %q", key)
	return SourceView{}
}

// pullConn answers orders.list with res, or with err when set.
type pullConn struct {
	res json.RawMessage
	err error
}

func (c *pullConn) call(_ context.Context, method string, _ any, _ time.Duration) (json.RawMessage, error) {
	if method != "orders.list" {
		return nil, errors.New("unexpected " + method)
	}
	return c.res, c.err
}
func (c *pullConn) stop()                   {}
func (c *pullConn) kill()                   {}
func (c *pullConn) exited() <-chan struct{} { return nil }
func (c *pullConn) exitReason() string      { return "" }

func TestAPluginViewGetsRefreshedAtWhenItsOrdersArePulledAndKeepsItWhenAPullFails(t *testing.T) {
	r := &runner{key: "k", notify: func() {}, panels: []Panel{{ID: "p", Title: "P", Orders: []Order{}}}}
	panel := func() Panel { r.mu.Lock(); defer r.mu.Unlock(); return r.panels[0] }
	good := &pullConn{res: json.RawMessage(`[{"id":"a","title":"A"}]`)}

	if at, ok := refreshedAt(t, panel()); ok {
		t.Fatalf("a view never pulled has refreshed_at %v, want none", at)
	}
	r.pullOrders(t.Context(), good, "p")
	t1, ok := refreshedAt(t, panel())
	if !ok {
		t.Fatal("a pulled view has no refreshed_at")
	}
	time.Sleep(20 * time.Millisecond)
	r.pullOrders(t.Context(), good, "p")
	t2, _ := refreshedAt(t, panel())
	if !t2.After(t1) {
		t.Errorf("refreshed_at = %v after a second pull, want later than %v", t2, t1)
	}

	for name, bad := range map[string]*pullConn{
		"the plugin reports an error": {err: &rpcError{Code: codeSourceError, Message: "upstream down"}},
		"the call fails":              {err: errors.New("broken pipe")},
		"the answer is not a list":    {res: json.RawMessage(`{"not":"a list"}`)},
	} {
		time.Sleep(5 * time.Millisecond)
		r.pullOrders(t.Context(), bad, "p")
		if at, ok := refreshedAt(t, panel()); !ok || !at.Equal(t2) {
			t.Errorf("when %s refreshed_at = %v (present %v), want it unchanged at %v", name, at, ok, t2)
		}
	}

	r.mu.Lock()
	r.mergePanels([]Panel{{ID: "p", Title: "Renamed", Orders: []Order{}}, {ID: "q", Title: "New", Orders: []Order{}}})
	kept, fresh := r.panels[0], r.panels[1]
	r.mu.Unlock()
	if at, ok := refreshedAt(t, kept); !ok || !at.Equal(t2) {
		t.Errorf("after a panels re-list refreshed_at = %v (present %v), want it carried at %v", at, ok, t2)
	}
	if at, ok := refreshedAt(t, fresh); ok {
		t.Errorf("a view new to the re-list has refreshed_at %v, want none", at)
	}
}

func TestAPluginCannotSetAViewsRefreshedAt(t *testing.T) {
	panels, err := parsePanels(json.RawMessage(`[{"id":"p","title":"P","refreshed_at":"2026-01-01T00:00:00Z"}]`))
	if err != nil || len(panels) != 1 {
		t.Fatalf("parsePanels = %v, %v", panels, err)
	}
	if at, ok := refreshedAt(t, panels[0]); ok {
		t.Errorf("a plugin's own refreshed_at %v reached the view, want none", at)
	}
}

func TestParseOrdersReadsTheNewFieldsAndDropsABadOneKeepingTheOrder(t *testing.T) {
	hundred := strings.Repeat("é", 100)
	raw, err := json.Marshal([]obj{
		{"id": "good", "title": "G", "author": "alice", "opened_at": "2026-11-10T09:30:00Z", "review_requested_at": "2026-11-10T12:00:00+02:00",
			"reviewed_since_push": true, "excerpt": "a\nb", "pr": obj{"state": "OPEN", "failing_check_count": 2}},
		{"id": "bad", "title": "B", "author": "al\nice", "opened_at": "yesterday", "review_requested_at": "2026-11-10",
			"reviewed_since_push": "yes", "excerpt": "  \n ", "pr": obj{"state": "OPEN", "failing_check_count": -1}},
		{"id": "wrongtypes", "title": "W", "author": strings.Repeat("x", 101), "reviewed_since_push": 1, "excerpt": strings.Repeat("x", 300),
			"pr": obj{"state": "OPEN", "failing_check_count": "two"}},
		{"id": "hundred", "title": "H", "author": hundred},
	})
	if err != nil {
		t.Fatal(err)
	}
	got, err := parseOrders(raw)
	if err != nil || len(got) != 4 {
		t.Fatalf("parseOrders = %d orders, %v; a bad field must not fail the list or drop the order", len(got), err)
	}
	good, bad, wrong, kept := asJSON(t, got[0]), asJSON(t, got[1]), asJSON(t, got[2]), asJSON(t, got[3])

	if good["author"] != "alice" || !instant(t, good["opened_at"]).Equal(instant(t, "2026-11-10T09:30:00Z")) ||
		!instant(t, good["review_requested_at"]).Equal(instant(t, "2026-11-10T10:00:00Z")) || good["reviewed_since_push"] != true || good["excerpt"] != "a b" {
		t.Errorf("the good order = %v, want its fields kept (excerpt read as a b)", good)
	}
	if pr, _ := good["pr"].(obj); pr["failing_check_count"] != float64(2) {
		t.Errorf("the good order's pr = %v, want failing_check_count 2", good["pr"])
	}

	absent(t, bad, "the order with bad fields", "author", "opened_at", "review_requested_at", "reviewed_since_push", "excerpt")
	if pr, _ := bad["pr"].(obj); pr == nil || pr["state"] != "OPEN" {
		t.Errorf("the order with a negative failing_check_count lost its pr: %v", bad["pr"])
	} else {
		absent(t, pr, "its pr", "failing_check_count")
	}

	absent(t, wrong, "the order with wrong types", "author", "reviewed_since_push")
	if wrong["excerpt"] != strings.Repeat("x", 280)+"…" {
		t.Errorf("a 300-rune excerpt = %v, want the first 280 runes and …", wrong["excerpt"])
	}
	if pr, _ := wrong["pr"].(obj); pr == nil || pr["state"] != "OPEN" {
		t.Errorf("the order with a non-integer failing_check_count lost its pr: %v", wrong["pr"])
	} else {
		absent(t, pr, "its pr", "failing_check_count")
	}

	if kept["author"] != hundred {
		t.Errorf("a 100-rune author = %v, want it kept", kept["author"])
	}
}

func TestParseOrdersDropsAWrongTypedFieldAndKeepsTheOrderAndItsNeighbours(t *testing.T) {
	for _, field := range []string{"author", "opened_at", "excerpt", "review_requested_at"} {
		for name, bad := range map[string]any{"a number": 7, "an object": obj{"x": 1}, "a list": []any{"a"}, "a boolean": true} {
			t.Run(field+" as "+name, func(t *testing.T) {
				raw, err := json.Marshal([]obj{{"id": "first", "title": "F", "author": "alice"}, {"id": "bad", "title": "B", field: bad}})
				if err != nil {
					t.Fatal(err)
				}
				got, err := parseOrders(raw)
				if err != nil || len(got) != 2 {
					t.Fatalf("parseOrders = %d orders, %v; a wrong type must drop only the field", len(got), err)
				}
				if m := asJSON(t, got[1]); m["id"] != "bad" {
					t.Errorf("order = %v, want it kept", m)
				} else {
					absent(t, m, "the order", field)
				}
				if got[0].Author != "alice" {
					t.Errorf("the neighbour = %+v, want its author kept", got[0])
				}
			})
		}
	}
}

func TestParseOrdersDropsAnAuthorWithInvisibleOrDirectionalCharacters(t *testing.T) {
	for name, author := range map[string]string{"a right-to-left override": "al\u202eice", "a zero-width space": "al\u200bice", "a bidi isolate": "al\u2066ice",
		"a word joiner": "al\u2060ice", "a byte order mark": "\ufeffalice", "a C1 control": "al\u0085ice"} {
		raw, _ := json.Marshal([]obj{{"id": "a", "title": "A", "author": author}})
		got, err := parseOrders(raw)
		if err != nil || len(got) != 1 {
			t.Fatalf("%s: parseOrders = %v, %v", name, got, err)
		}
		if got[0].Author != "" {
			t.Errorf("%s: author = %q, want it dropped", name, got[0].Author)
		}
	}
}

func TestParseOrdersReadsTheFailingCountOfAPrWhateverTheKeysCase(t *testing.T) {
	raw := json.RawMessage(`[{"id":"a","title":"A","pr":{"state":"OPEN","Failing_Check_Count":"x"}},{"id":"b","title":"B","pr":{"state":"OPEN","Failing_Check_Count":3}}]`)
	got, err := parseOrders(raw)
	if err != nil || len(got) != 2 {
		t.Fatalf("parseOrders = %v, %v", got, err)
	}
	if got[0].PR == nil || got[0].PR.State != "OPEN" || got[0].PR.FailingCheckCount != 0 {
		t.Errorf("a bad count in another case: pr = %+v, want the pr kept with no count", got[0].PR)
	}
	if got[1].PR == nil || got[1].PR.FailingCheckCount != 3 {
		t.Errorf("a good count in another case: pr = %+v, want 3, as encoding/json matches keys without case", got[1].PR)
	}
}

func TestGitHubViewKeepsItsOrdersAndTimeAcrossARestartUntilTheNewSearchAnswers(t *testing.T) {
	g := newFakeGH(t)
	g.token("", "fake-token-default-1")
	writeSearch(t, g, "bugs", "label:bug", searchNode("issues", 13, obj{"subIssuesSummary": obj{"total": 0}}))
	f := newServiceFixture(t, ghConfig(campaignFleet, campaignSource))
	f.startSources()
	bugs := func(s SourceView) Panel { return s.view("bugs") }
	first := f.waitView("gha", "the first search", func(s SourceView) bool { _, ok := refreshedAt(t, bugs(s)); return ok && len(s.orders("bugs")) == 1 })
	t1, _ := refreshedAt(t, bugs(first))

	writeSearch(t, g, "bugs", "label:bug", searchNode("issues", 14, obj{"subIssuesSummary": obj{"total": 0}}))
	release := g.holdSearches()
	before := g.searches()
	f.restartUntilReady("gha")
	g.waitSearchBegun(before)
	mid := f.Svc.viewOf(t, "gha")
	if at, ok := refreshedAt(t, bugs(mid)); !ok || !at.Equal(t1) || len(mid.orders("bugs")) != 1 || bugs(mid).Orders[0].ID != "acme/widgets#13" {
		t.Errorf("before the new search: orders %v refreshed_at %v (present %v), want the old order 13 and %v", mid.orders("bugs"), at, ok, t1)
	}
	release()
	after := f.waitView("gha", "the new search", func(s SourceView) bool {
		at, ok := refreshedAt(t, bugs(s))
		return ok && at.After(t1)
	})
	if o := bugs(after).Orders; len(o) != 1 || o[0].ID != "acme/widgets#14" {
		t.Errorf("after the new search orders = %v, want order 14", o)
	}
}

func TestAPluginsOrdersNeverCarryAChain(t *testing.T) {
	r := &runner{key: "k", notify: func() {}, panels: []Panel{{ID: "p", Title: "P", Orders: []Order{}}}}
	chain := obj{"id": "https://github.com/a/b/pull/1", "position": 1, "size": 2, "head": "h", "base": "main", "mine": true}
	raw, err := json.Marshal([]obj{{"id": "a", "title": "A", "pr": obj{"state": "OPEN"}, "chain": chain}})
	if err != nil {
		t.Fatal(err)
	}
	r.pullOrders(t.Context(), &pullConn{res: raw}, "p")
	r.mu.Lock()
	got := asJSON(t, r.panels[0].Orders[0])
	r.mu.Unlock()
	absent(t, got, "a plugin's PR", "chain")
}
