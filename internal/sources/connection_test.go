package sources

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

const connSource = `"gh":{"plugin":"github","fleet":"alpha","settings":{"views":[{"title":"Bugs","query":"is:open label:bug"}]}}`

// count answers a count call whose arguments hold match with n; fail makes it exit with that stderr.
func (g *fakeGH) count(key, match string, n int) {
	g.t.Helper()
	if err := os.MkdirAll(filepath.Join(g.dir, "count"), 0o755); err != nil {
		g.t.Fatal(err)
	}
	g.write("count/"+key+".match", match)
	g.write("count/"+key+".json", `{"data":{"search":{"issueCount":`+itoa(n)+`}}}`)
}

func (g *fakeGH) countFail(key, match, stderr string) {
	g.t.Helper()
	if err := os.MkdirAll(filepath.Join(g.dir, "count"), 0o755); err != nil {
		g.t.Fatal(err)
	}
	g.write("count/"+key+".match", match)
	g.write("count/"+key+".fail", stderr)
}

func itoa(n int) string { b, _ := json.Marshal(n); return string(b) }

// connectionOf reads a source's connection object from GET /api/sources, nil when it has none.
func (f *serviceFixture) connectionOf(key string) map[string]any {
	f.t.Helper()
	code, body := f.do(http.MethodGet, "", nil)
	if code != http.StatusOK {
		f.t.Fatalf("GET /api/sources = %d %s", code, body)
	}
	var list []map[string]any
	if err := json.Unmarshal([]byte(body), &list); err != nil {
		f.t.Fatal(err)
	}
	for _, s := range list {
		if s["key"] == key {
			c, _ := s["connection"].(map[string]any)
			return c
		}
	}
	f.t.Fatalf("no source %q in %s", key, body)
	return nil
}

func (f *serviceFixture) waitConnection(key, what string, pred func(map[string]any) bool) map[string]any {
	f.t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for {
		c := f.connectionOf(key)
		if c != nil && pred(c) {
			return c
		}
		if time.Now().After(deadline) {
			f.t.Fatalf("within 15s source %q connection never showed %s; last: %v", key, what, c)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func fixOf(c map[string]any) []string {
	var out []string
	l, _ := c["fix"].([]any)
	for _, x := range l {
		out = append(out, x.(string))
	}
	return out
}

func (g *fakeGH) qArgs() []string {
	g.t.Helper()
	argvs, _ := filepath.Glob(filepath.Join(g.dir, "calls", "call.*", "argv"))
	var out []string
	for _, a := range argvs {
		b, _ := os.ReadFile(a)
		for _, arg := range strings.Split(string(b), "\x00") {
			if strings.HasPrefix(arg, "q=") {
				out = append(out, arg)
			}
		}
	}
	return out
}

// liveGH starts a signed-in GitHub source with the views and waits until it is connected.
func liveGH(t *testing.T, views string) (*serviceFixture, *fakeGH) {
	t.Helper()
	g := newFakeGH(t)
	g.token("", "fake-token-default-1")
	g.write("viewer", "octocat")
	g.search("bugs", "label:bug", [3]any{"acme/widgets", 13, "Any bug"})
	f := newServiceFixture(t, ghConfig(campaignFleet,
		`"gh":{"plugin":"github","fleet":"alpha","settings":{"views":`+views+`}}`))
	f.startSources()
	f.waitConnection("gh", "connected", func(c map[string]any) bool { return c["state"] == "connected" })
	return f, g
}

const bugsView = `[{"title":"Bugs","query":"is:open label:bug"}]`

func TestSignedOutSourceShowsTheFixAndCheckAgainConnectsIt(t *testing.T) {
	g := newFakeGH(t)
	f := newServiceFixture(t, ghConfig(campaignFleet, connSource))
	f.startSources()

	out := f.waitConnection("gh", "signed out", func(c map[string]any) bool { return c["state"] == "signed_out" })
	if !reflect.DeepEqual(fixOf(out), []string{"gh auth login -h github.com"}) || out["host"] != "github.com" || out["message"] == "" {
		t.Errorf("signed-out connection = %v, want the login command for github.com, the host and a message", out)
	}
	at1, err := time.Parse(time.RFC3339, out["checked_at"].(string))
	if err != nil {
		t.Fatalf("checked_at = %v, want RFC 3339: %v", out["checked_at"], err)
	}
	f.waitView("gh", "the sign-in card", func(s SourceView) bool { return s.State == "auth_required" })

	g.token("", "fake-token-default-1")
	g.write("viewer", "octocat")
	g.search("bugs", "label:bug", [3]any{"acme/widgets", 13, "Any bug"})
	code, body := f.do(http.MethodPost, "/gh/connection/check", map[string]any{})
	if code != http.StatusOK {
		t.Fatalf("POST connection/check = %d %s", code, body)
	}
	var got map[string]any
	_ = json.Unmarshal([]byte(body), &got)
	if got["state"] != "connected" || got["login"] != "octocat" || len(fixOf(got)) != 0 || got["message"] != nil && got["message"] != "" {
		t.Errorf("check answered %v, want connected as octocat with no fix or message", got)
	}
	at2, _ := time.Parse(time.RFC3339, got["checked_at"].(string))
	if !at2.After(at1) {
		t.Errorf("checked_at = %v, want later than %v", at2, at1)
	}
	f.waitView("gh", "the card gone and the search run", func(s SourceView) bool {
		return s.State == "ok" && s.Auth == nil && len(s.orders("bugs")) == 1
	})
	if c := f.connectionOf("gh"); c["state"] != "connected" {
		t.Errorf("GET /api/sources connection = %v, want connected", c)
	}
}

func TestGhMissingShowsBothCommands(t *testing.T) {
	newFakeGH(t)
	t.Setenv("PATH", t.TempDir())
	f := newServiceFixture(t, ghConfig(campaignFleet, connSource))
	f.startSources()
	c := f.waitConnection("gh", "gh missing", func(c map[string]any) bool { return c["state"] == "gh_missing" })
	if want := []string{"brew install gh", "gh auth login -h github.com"}; !reflect.DeepEqual(fixOf(c), want) {
		t.Errorf("fix = %v, want %v", fixOf(c), want)
	}
	f.waitView("gh", "auth_required", func(s SourceView) bool { return s.State == "auth_required" })
}

func TestConnectionNamesTheLoginTheAccountAndTheHost(t *testing.T) {
	g := newFakeGH(t)
	g.token("newbie", "fake-token-new-3")
	g.write("viewer", "octocat")
	g.search("bugs", "label:bug", [3]any{"acme/widgets", 13, "Any bug"})
	f := newServiceFixture(t, ghConfig(campaignFleet,
		`"gh":{"plugin":"github","fleet":"alpha","settings":{"account":"newbie","views":`+bugsView+`}}`))
	f.startSources()
	c := f.waitConnection("gh", "connected", func(c map[string]any) bool { return c["state"] == "connected" })
	if c["login"] != "octocat" || c["account"] != "newbie" || c["host"] != "github.com" {
		t.Errorf("connection = %v, want login octocat, account newbie, host github.com", c)
	}
}

func TestUnreachableIsTheConnectionsStateButARefusedSearchIsTheViewsError(t *testing.T) {
	g := newFakeGH(t)
	g.token("", "fake-token-default-1")
	g.search("bugs", "label:bug", [3]any{"acme/widgets", 13, "Any bug"})
	g.write("search/bugs.fail", "error connecting to api.github.com\n")
	f := newServiceFixture(t, ghConfig(campaignFleet, connSource))
	f.startSources()
	c := f.waitConnection("gh", "unreachable", func(c map[string]any) bool { return c["state"] == "unreachable" })
	if c["message"] != "Couldn't reach github.com" || len(fixOf(c)) != 0 {
		t.Errorf("connection = %v, want the Couldn't reach message and no fix", c)
	}

	g.write("search/bugs.fail", "boom: search failed\n")
	if code, body := f.do(http.MethodPost, "/gh/refresh", map[string]any{}); code != http.StatusOK {
		t.Fatalf("POST refresh = %d %s", code, body)
	}
	f.waitView("gh", "an error on Bugs", func(s SourceView) bool { return strings.Contains(s.view("bugs").Error, "boom") })
	holdFor(t, "a refused search changed the connection", func() bool {
		n := f.connectionOf("gh")
		return n["state"] == "unreachable" && n["checked_at"] == c["checked_at"]
	})
}

// holdFor polls check over a short hold, to show that something does not happen.
func holdFor(t *testing.T, msg string, check func() bool) {
	t.Helper()
	end := time.Now().Add(time.Second)
	for time.Now().Before(end) {
		if !check() {
			t.Fatal(msg)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func TestASourceWithNoVisibleSearchViewStillLearnsItsLogin(t *testing.T) {
	f, _ := liveGH(t, `[{"id":"for-you","hidden":false},{"id":"assigned-to-me","hidden":true},{"id":"review-requested","hidden":true},{"id":"my-prs","hidden":true}]`)
	if c := f.connectionOf("gh"); c["login"] != "octocat" {
		t.Errorf("connection = %v, want login octocat", c)
	}
}

func TestEveryViewOfAStartingGitHubSourceCarriesAConnection(t *testing.T) {
	newFakeGH(t)
	f := newServiceFixture(t, ghConfig(campaignFleet, connSource))
	f.startSources()
	deadline := time.Now().Add(15 * time.Second)
	seen := false
	for time.Now().Before(deadline) {
		b, _ := json.Marshal(f.Svc.View())
		var list []map[string]any
		_ = json.Unmarshal(b, &list)
		for _, s := range list {
			if s["key"] != "gh" {
				continue
			}
			seen = true
			c, _ := s["connection"].(map[string]any)
			if c == nil {
				t.Fatalf("a view of the starting source has no connection: %s", b)
			}
			if c["state"] != "checking" {
				return // it moved past checking without a gap or a panic
			}
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("source seen %v, but its connection never left checking", seen)
}

func TestConnectionSurvivesARestartOfTheSource(t *testing.T) {
	g := newFakeGH(t)
	g.token("", "fake-token-default-1")
	g.write("viewer", "octocat")
	slowSearch(t, g)
	g.search("bugs", "label:bug", [3]any{"acme/widgets", 13, "Any bug"})
	f := newServiceFixture(t, ghConfig(campaignFleet, connSource))
	f.startSources()
	f.waitConnection("gh", "connected", func(c map[string]any) bool { return c["state"] == "connected" && c["login"] == "octocat" })

	sid := func() int {
		r := f.Svc.Sup.runner("gh")
		r.mu.Lock()
		defer r.mu.Unlock()
		return r.sid
	}
	before := sid()
	g.write("slow", "2")
	if !f.Svc.Sup.Restart("gh") {
		t.Fatal("Restart found no runner")
	}
	deadline := time.Now().Add(10 * time.Second)
	for sid() == before {
		if time.Now().After(deadline) {
			t.Fatal("the source did not restart")
		}
		time.Sleep(10 * time.Millisecond)
	}
	holdFor(t, "the connection left the last one during the restart", func() bool {
		c := f.connectionOf("gh")
		return c["state"] == "connected" && c["login"] == "octocat"
	})
}

func TestCheckAgainRefusals(t *testing.T) {
	newFakeGH(t)
	f := newServiceFixture(t, moveConfig)
	f.startSources()
	f.waitView("pw", "its runner", func(SourceView) bool { return true })
	if code, _ := f.do(http.MethodPost, "/nosuch/connection/check", map[string]any{}); code != http.StatusNotFound {
		t.Errorf("unknown key = %d, want 404", code)
	}
	if code, _ := f.do(http.MethodPost, "/pw/connection/check", map[string]any{}); code != http.StatusBadRequest {
		t.Errorf("non-GitHub source = %d, want 400", code)
	}
	if code, body := f.do(http.MethodGet, "", nil); code != http.StatusOK || strings.Contains(body, `"connection"`) {
		t.Errorf("GET /api/sources = %d %s, want no connection on a plugin source", code, body)
	}
}

func TestSearchCountByQueryAsGitHubCountsIt(t *testing.T) {
	f, g := liveGH(t, bugsView)
	g.count("zz", "label:wontfix", 1234)
	code, body := f.do(http.MethodPost, "/gh/search-count", map[string]any{"query": "is:open label:wontfix"})
	if code != http.StatusOK {
		t.Fatalf("search-count = %d %s", code, body)
	}
	var got map[string]any
	_ = json.Unmarshal([]byte(body), &got)
	if got["count"] != float64(1234) {
		t.Errorf("answer = %s, want count 1234", body)
	}
	for _, q := range g.qArgs() {
		if strings.Contains(q, "label:wontfix") && strings.Contains(q, "sort:") {
			t.Errorf("count call carried a sort: %q", q)
		}
	}
}

func TestSearchCountByViewUsesTheViewsQueryHiddenOnesIncluded(t *testing.T) {
	f, g := liveGH(t, `[{"id":"for-you","hidden":false},{"id":"assigned-to-me","hidden":true},{"title":"Bugs","query":"is:open label:bug"}]`)
	g.count("custom", "label:bug", 7)
	g.count("hidden", "assignee:@me", 11)
	for view, want := range map[string]float64{"bugs": 7, "assigned-to-me": 11} {
		code, body := f.do(http.MethodPost, "/gh/search-count", map[string]any{"view": view})
		var got map[string]any
		_ = json.Unmarshal([]byte(body), &got)
		if code != http.StatusOK || got["count"] != want {
			t.Errorf("view %s = %d %s, want count %v", view, code, body, want)
		}
	}
}

func TestSearchCountAFailingSearchIs422WithGitHubsText(t *testing.T) {
	f, g := liveGH(t, bugsView)
	g.countFail("bad", "label:broken", "boom: search failed\n")
	code, body := f.do(http.MethodPost, "/gh/search-count", map[string]any{"query": "is:open label:broken"})
	var got map[string]any
	_ = json.Unmarshal([]byte(body), &got)
	if code != http.StatusUnprocessableEntity || got["error"] != "Search failed: boom: search failed" {
		t.Errorf("failing count = %d %s, want 422 with \"Search failed: boom: search failed\"", code, body)
	}
}

func TestSearchCountRefusals(t *testing.T) {
	f, g := liveGH(t, bugsView)
	before := len(g.qArgs())
	for name, c := range map[string]struct {
		path string
		body any
		want int
	}{
		"empty query":    {"/gh/search-count", map[string]any{"query": "  "}, 400},
		"no fields":      {"/gh/search-count", map[string]any{}, 400},
		"over 1024":      {"/gh/search-count", map[string]any{"query": strings.Repeat("a", 1025)}, 400},
		"for-you":        {"/gh/search-count", map[string]any{"view": "for-you"}, 400},
		"unknown view":   {"/gh/search-count", map[string]any{"view": "nosuch"}, 400},
		"unknown source": {"/nosuch/search-count", map[string]any{"query": "is:open"}, 404},
	} {
		if code, body := f.do(http.MethodPost, c.path, c.body); code != c.want {
			t.Errorf("%s = %d %s, want %d", name, code, body, c.want)
		}
	}
	if n := len(g.qArgs()); n != before {
		t.Errorf("%d gh searches ran for refused counts", n-before)
	}
	p := newServiceFixture(t, moveConfig)
	p.startSources()
	p.waitView("pw", "its runner", func(SourceView) bool { return true })
	if code, _ := p.do(http.MethodPost, "/pw/search-count", map[string]any{"query": "is:open"}); code != http.StatusBadRequest {
		t.Errorf("non-GitHub source = %d, want 400", code)
	}
}

func TestSearchCountOfASignedOutSourceIs409WithTheConnectionAndRunsNoGh(t *testing.T) {
	g := newFakeGH(t)
	f := newServiceFixture(t, ghConfig(campaignFleet, connSource))
	f.startSources()
	f.waitConnection("gh", "signed out", func(c map[string]any) bool { return c["state"] == "signed_out" })
	code, body := f.do(http.MethodPost, "/gh/search-count", map[string]any{"query": "is:open"})
	var got map[string]any
	_ = json.Unmarshal([]byte(body), &got)
	conn, _ := got["connection"].(map[string]any)
	if code != http.StatusConflict || conn["state"] != "signed_out" {
		t.Errorf("signed-out count = %d %s, want 409 with the signed_out connection", code, body)
	}
	if len(g.qArgs()) != 0 {
		t.Error("gh searched for a signed-out source")
	}
}

func TestGitHubSourceCannotBeDeletedOrRestartedThroughTheRoutes(t *testing.T) {
	f, _ := liveGH(t, bugsView)
	before := f.file()
	sid := func() int {
		r := f.Svc.Sup.runner("gh")
		r.mu.Lock()
		defer r.mu.Unlock()
		return r.sid
	}
	s0 := sid()
	if code, body := f.do(http.MethodDelete, "/gh", nil); code != http.StatusConflict || !strings.Contains(body, "error") {
		t.Errorf("DELETE = %d %s, want 409 with an error", code, body)
	}
	if code, body := f.do(http.MethodPost, "/gh/restart", nil); code != http.StatusConflict || !strings.Contains(body, "error") {
		t.Errorf("restart = %d %s, want 409 with an error", code, body)
	}
	holdFor(t, "a refused Remove or Restart changed the config or the runner", func() bool {
		return f.file() == before && f.Svc.Sup.runner("gh") != nil && sid() == s0
	})
}

func TestAPluginSourceKeepsRemoveAndRestart(t *testing.T) {
	f := newServiceFixture(t, moveConfig)
	f.startSources()
	f.waitView("pw", "its runner", func(SourceView) bool { return true })
	if code, body := f.do(http.MethodPost, "/pw/restart", nil); code != http.StatusOK {
		t.Errorf("restart of a plugin source = %d %s, want 200", code, body)
	}
	if code, body := f.do(http.MethodDelete, "/pw", nil); code != http.StatusOK {
		t.Errorf("DELETE of a plugin source = %d %s, want 200", code, body)
	}
}

const badCredentials = "gh: Bad credentials (HTTP 401)\n"

func TestARevokedTokenReadsAsSignedOutAndOtherRefusalsKeepTheLastState(t *testing.T) {
	f, g := liveGH(t, bugsView)
	was := f.connectionOf("gh")

	g.write("viewer.fail", "gh: Something odd (HTTP 500)\n")
	code, body := f.do(http.MethodPost, "/gh/connection/check", map[string]any{})
	var got map[string]any
	_ = json.Unmarshal([]byte(body), &got)
	if code != http.StatusOK || got["state"] != "connected" || got["checked_at"] != was["checked_at"] || !strings.Contains(got["message"].(string), "Something odd") {
		t.Errorf("check after another refusal = %d %v, want the last connected state with the refusal as its message", code, got)
	}

	g.write("viewer.fail", badCredentials)
	code, body = f.do(http.MethodPost, "/gh/connection/check", map[string]any{})
	got = nil
	_ = json.Unmarshal([]byte(body), &got)
	if code != http.StatusOK || got["state"] != "signed_out" || !strings.Contains(got["message"].(string), "Bad credentials") ||
		!reflect.DeepEqual(fixOf(got), []string{"gh auth login -h github.com"}) {
		t.Errorf("check after a revoked token = %d %v, want signed_out with the login command", code, got)
	}
}

func TestASearchTheTokenIsRefusedOnIsSignedOutButAnotherRefusalLeavesTheConnection(t *testing.T) {
	f, g := liveGH(t, bugsView)
	g.write("search/bugs.fail", "gh: Something odd (HTTP 500)\n")
	f.do(http.MethodPost, "/gh/refresh", map[string]any{})
	f.waitView("gh", "an error on Bugs", func(s SourceView) bool { return strings.Contains(s.view("bugs").Error, "Something odd") })
	if c := f.connectionOf("gh"); c["state"] != "connected" {
		t.Errorf("connection after a refused search = %v, want connected kept", c)
	}
	g.write("search/bugs.fail", badCredentials)
	f.do(http.MethodPost, "/gh/refresh", map[string]any{})
	f.waitConnection("gh", "signed out", func(c map[string]any) bool { return c["state"] == "signed_out" })
}

func TestACountTheTokenIsRefusedOnIsAConflictThatSignsTheSourceOut(t *testing.T) {
	f, g := liveGH(t, bugsView)
	g.countFail("all", "q=", badCredentials)
	code, body := f.do(http.MethodPost, "/gh/search-count", map[string]any{"query": "is:open"})
	if code != http.StatusConflict || !strings.Contains(body, "signed_out") {
		t.Errorf("count with a revoked token = %d %s, want 409 with a signed_out connection", code, body)
	}
}

func TestAGoodCountConnectsASignedOutSourceAndAnotherRefusalDoesNot(t *testing.T) {
	g := newFakeGH(t)
	g.count("all", "q=", 7)
	f := newServiceFixture(t, ghConfig(campaignFleet, connSource))
	f.startSources()
	f.waitConnection("gh", "signed out", func(c map[string]any) bool { return c["state"] == "signed_out" })
	g.token("", "fake-token-default-1")
	if code, body := f.do(http.MethodPost, "/gh/search-count", map[string]any{"query": "is:open"}); code != http.StatusOK {
		t.Fatalf("count = %d %s", code, body)
	}
	f.waitConnection("gh", "connected", func(c map[string]any) bool { return c["state"] == "connected" })
	g.countFail("all", "q=", "gh: Validation failed (HTTP 422)\n")
	if code, _ := f.do(http.MethodPost, "/gh/search-count", map[string]any{"query": "is:open"}); code != http.StatusUnprocessableEntity {
		t.Errorf("a refused count = %d, want 422", code)
	}
	if c := f.connectionOf("gh"); c["state"] != "connected" {
		t.Errorf("connection after a refused count = %v, want connected", c)
	}
}

func TestACancelledCheckRecordsNothing(t *testing.T) {
	f, _ := liveGH(t, bugsView)
	was := f.connectionOf("gh")
	g := f.Svc.Sup.runner("gh").p.(*githubConn)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, ok := g.check(ctx); ok {
		t.Error("a cancelled check reported a result")
	}
	if _, _, err := g.count(ctx, "is:open"); err == nil {
		t.Error("a cancelled count answered")
	}
	if now := f.connectionOf("gh"); now["state"] != "connected" || now["checked_at"] != was["checked_at"] {
		t.Errorf("connection after a cancelled check = %v, want %v", now, was)
	}
}
