package sources

import (
	"encoding/json"
	"fmt"
	"net/http"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

// listed reads a source's entry from GET /api/sources as a plain map.
func (f *serviceFixture) listed(key string) map[string]any {
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
			return s
		}
	}
	f.t.Fatalf("no source %q in %s", key, body)
	return nil
}

func tabsOf(t *testing.T, s map[string]any) [][2]string {
	t.Helper()
	raw, ok := s["tabs"].([]any)
	if !ok {
		t.Fatalf("source %v has no tabs list", s["key"])
	}
	var out [][2]string
	for _, e := range raw {
		m := e.(map[string]any)
		out = append(out, [2]string{m["id"].(string), m["title"].(string)})
	}
	return out
}

func startedGH(t *testing.T, views string) *serviceFixture {
	t.Helper()
	newFakeGH(t)
	f := newServiceFixture(t, viewsConfig(views))
	f.startSources()
	f.waitView("gh", "its runner", func(SourceView) bool { return true })
	return f
}

func TestTheSourcesListGivesTheVisibleTabsInStoredOrderWithTheirTitlesAndAPluginSourceNone(t *testing.T) {
	newFakeGH(t)
	f := newServiceFixture(t, ghConfig(`"alpha":{"name":"Alpha","claude":{}}`,
		`"gh":{"plugin":"github","fleet":"alpha","settings":{"views":`+mixedViews+`}},"pw":{"plugin":"notinstalled","fleet":"alpha"}`))
	f.startSources()
	f.waitView("gh", "its runner", func(SourceView) bool { return true })
	if _, has := f.listed("pw")["tabs"]; has {
		t.Error("a plugin source lists tabs")
	}
	got := tabsOf(t, f.listed("gh"))
	want := [][2]string{{"my-prs", "Mine"}, {"bugs", "Bugs"}, {"review-requested", "Review requested"}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("tabs = %v, want %v: hidden entries (For you, Assigned to me, Docs) are left out", got, want)
	}
}

func TestTheTabsPlaceAndRenameForYou(t *testing.T) {
	f := startedGH(t, `[{"id":"assigned-to-me","hidden":false},{"id":"for-you","hidden":false,"title":"My desk"},`+
		`{"id":"review-requested","hidden":true},{"id":"my-prs","hidden":false}]`)
	got := tabsOf(t, f.listed("gh"))
	want := [][2]string{{"assigned-to-me", "Assigned to me"}, {"for-you", "My desk"}, {"my-prs", "My PRs"}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("tabs = %v, want %v", got, want)
	}
}

func TestSettingsGiveEachViewEntryItsEffectiveID(t *testing.T) {
	f := newServiceFixture(t, viewsConfig(`[`+builtinViewEntries+`,{"title":"Bugs","query":"is:open label:bug"},`+
		`{"title":"bugs","query":"is:open label:bug is:pr"},{"title":"Review requested","query":"is:open"},`+
		`{"id":"stale-id","title":"Docs","query":"is:open label:docs"},{"title":"No query"}]`))
	code, body := f.do(http.MethodGet, "/gh/settings", nil)
	if code != http.StatusOK {
		t.Fatalf("GET settings = %d %s", code, body)
	}
	var got struct {
		Settings struct {
			Views []map[string]any `json:"views"`
		} `json:"settings"`
	}
	if err := json.Unmarshal([]byte(body), &got); err != nil {
		t.Fatal(err)
	}
	var ids []any
	for _, v := range got.Settings.Views {
		ids = append(ids, v["id"])
	}
	want := []any{"for-you", "assigned-to-me", "review-requested", "my-prs", "bugs", "bugs-2", "review-requested-2", "docs", nil}
	if !reflect.DeepEqual(ids, want) {
		t.Errorf("ids = %v, want %v: a custom view's slug takes -2 after a built-in's or an earlier id, a stored id is ignored, and an entry that is no view has none", ids, want)
	}
	sameJSON(t, "stored settings", f.stored("gh"), `{"views":[`+builtinViewEntries+`,{"title":"Bugs","query":"is:open label:bug"},{"title":"bugs","query":"is:open label:bug is:pr"},{"title":"Review requested","query":"is:open"},{"id":"stale-id","title":"Docs","query":"is:open label:docs"},{"title":"No query"}]}`)
}

func TestTheReposRouteListsWhatRoutesToTheSourcesFleet(t *testing.T) {
	newFakeGH(t)
	rootA, rootB := t.TempDir(), t.TempDir()
	mkCheckout(t, filepath.Join(rootA, "widgets"), "https://github.com/acme/widgets.git")
	mkCheckout(t, filepath.Join(rootA, "tools"), "git@ghe.example.com:corp/tools.git")
	mkCheckout(t, filepath.Join(rootB, "widgets-fork"), "https://github.com/acme/widgets.git")
	mkCheckout(t, filepath.Join(rootB, "app"), "https://github.com/Bravo/App.git")
	fleets := fmt.Sprintf(`"alpha":{"name":"Alpha","claude":{},"search_roots":[%q],"owners":["Globex"]},"bravo":{"name":"Bravo","claude":{},"search_roots":[%q]},"charlie":{"name":"Charlie","claude":{}}`, rootA, rootB)
	f := newServiceFixture(t, ghConfig(fleets,
		`"gha":{"plugin":"github","fleet":"alpha"},`+
			`"ghe":{"plugin":"github","fleet":"alpha","settings":{"host":"ghe.example.com"}},`+
			`"ghb":{"plugin":"github","fleet":"bravo"},`+
			`"ghc":{"plugin":"github","fleet":"charlie"}`))
	f.startSources()
	f.waitView("ghb", "its runner", func(SourceView) bool { return true })

	readAll := func(key string) map[string]any {
		code, body := f.do(http.MethodGet, "/"+key+"/repos", nil)
		if code != http.StatusOK {
			t.Fatalf("GET %s/repos = %d %s", key, code, body)
		}
		var out map[string]any
		if err := json.Unmarshal([]byte(body), &out); err != nil {
			t.Fatal(err)
		}
		return out
	}
	// The other keys are checked below; the warnings have their own checks.
	read := func(key string) map[string]any {
		out := readAll(key)
		delete(out, "warnings")
		return out
	}
	if got := read("gha"); !reflect.DeepEqual(got, map[string]any{"repos": []any{"acme/widgets"}, "owners": []any{"globex"}, "default": true}) {
		t.Errorf("alpha's repos = %v, want acme/widgets (checked out in both fleets, so the first has it), owner globex, default", got)
	}
	if got := read("ghe")["repos"]; !reflect.DeepEqual(got, []any{"corp/tools"}) {
		t.Errorf("a source on ghe.example.com lists %v, want only that host's repos, [corp/tools]", got)
	}
	if got := read("ghb"); !reflect.DeepEqual(got, map[string]any{"repos": []any{"bravo/app"}, "owners": []any{}, "default": false}) {
		t.Errorf("bravo's repos = %v, want bravo/app, no owners as [], not the default", got)
	}
	for _, key := range []string{"gha", "ghb"} {
		w, _ := readAll(key)["warnings"].([]any)
		if len(w) != 1 || !strings.Contains(fmt.Sprint(w[0]), "acme/widgets is checked out in fleets") {
			t.Errorf("%s warnings = %v, want the one routing warning for acme/widgets, checked out in alpha and bravo", key, w)
		}
	}
	if got := readAll("ghc")["warnings"]; !reflect.DeepEqual(got, []any{}) {
		t.Errorf("a fleet with no conflict has warnings %v, want [] and not null or absent", got)
	}
}

func TestTheReposRouteRefusals(t *testing.T) {
	newFakeGH(t)
	f := newServiceFixture(t, moveConfig)
	f.startSources()
	f.waitView("pw", "its runner", func(SourceView) bool { return true })
	if code, body := f.do(http.MethodGet, "/nosuch/repos", nil); code != http.StatusNotFound || !strings.Contains(body, `"error"`) {
		t.Errorf("unknown source = %d %s, want 404 with an error body", code, body)
	}
	if code, _ := f.do(http.MethodGet, "/pw/repos", nil); code != http.StatusBadRequest {
		t.Errorf("a source that is not GitHub = %d, want 400", code)
	}
}

func TestACheckBeforeTheFirstRefreshSettlesAnswersCheckingNotAnEmptyState(t *testing.T) {
	g := newFakeGH(t)
	g.token("", "fake-token-default-1")
	g.write("viewer.fail", "gh: Something odd (HTTP 500)\n")
	f := newServiceFixture(t, ghConfig(campaignFleet, connSource))
	f.startSources()
	f.waitView("gh", "its runner", func(SourceView) bool { return true })

	var got map[string]any
	deadline := time.Now().Add(15 * time.Second)
	for {
		code, body := f.do(http.MethodPost, "/gh/connection/check", map[string]any{})
		if code == http.StatusOK {
			got = nil
			_ = json.Unmarshal([]byte(body), &got)
			break
		}
		if code != http.StatusConflict || time.Now().After(deadline) {
			t.Fatalf("POST connection/check = %d %s", code, body)
		}
		time.Sleep(10 * time.Millisecond)
	}
	if got["state"] != "checking" {
		t.Errorf("state = %v, want checking: nothing has found the connection yet", got["state"])
	}
	if at, has := got["checked_at"]; has && at != "" {
		t.Errorf("checked_at = %v, want none: nothing has checked", at)
	}
}

func TestACountRefusedOnTheTokenAnswersTheConnectionItJustFoundEvenIfTheSessionWasReplaced(t *testing.T) {
	f, g := liveGH(t, bugsView)
	g.countFail("all", "q=", badCredentials)
	// A restart that replaced the session while the count ran: its late result no longer reaches the runner's snapshot.
	rn := f.Svc.Sup.runner("gh")
	rn.mu.Lock()
	rn.sid++
	rn.mu.Unlock()
	code, body := f.do(http.MethodPost, "/gh/search-count", map[string]any{"query": "is:open"})
	var got struct {
		Connection map[string]any `json:"connection"`
	}
	_ = json.Unmarshal([]byte(body), &got)
	if code != http.StatusConflict || got.Connection["state"] != "signed_out" || got.Connection["message"] == nil {
		t.Errorf("count = %d %s, want 409 whose connection is the signed_out result the count settled on, with its message", code, body)
	}
}
