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

	"github.com/jbuffin/admiral/internal/config"
)

const builtinViewEntries = `{"id":"for-you","hidden":false},{"id":"assigned-to-me","hidden":false},{"id":"review-requested","hidden":false},{"id":"my-prs","hidden":false}`

// A mixed list: reordered, one built-in renamed, For you and a custom view hidden.
const mixedViews = `[{"id":"my-prs","hidden":false,"title":"Mine"},{"title":"Bugs","query":"is:open label:bug"},` +
	`{"id":"for-you","hidden":true},{"id":"assigned-to-me","hidden":true},{"id":"review-requested","hidden":false},` +
	`{"title":"Docs","query":"is:open label:docs","hidden":true}]`

func viewsConfig(views string) string {
	return ghConfig(`"alpha":{"name":"Alpha","claude":{}}`,
		`"gh":{"plugin":"github","fleet":"alpha","settings":{"views":`+views+`}}`)
}

func (g *fakeGH) ran(sub string) bool {
	g.t.Helper()
	argvs, _ := filepath.Glob(filepath.Join(g.dir, "calls", "call.*", "argv"))
	for _, a := range argvs {
		if b, _ := os.ReadFile(a); strings.Contains(string(b), sub) {
			return true
		}
	}
	return false
}

// sameJSON compares two JSON texts by value; a save re-marshals an object's keys in its own order.
func sameJSON(t *testing.T, what, got, want string) {
	t.Helper()
	var g, w any
	if err := json.Unmarshal([]byte(got), &g); err != nil {
		t.Fatalf("%s is not JSON: %s", what, got)
	}
	_ = json.Unmarshal([]byte(want), &w)
	if !reflect.DeepEqual(g, w) {
		t.Errorf("%s = %s, want %s", what, got, want)
	}
}

func viewIDs(s SourceView) (ids []string) {
	for _, v := range s.Views {
		ids = append(ids, v.ID)
	}
	return
}

func TestSourceViewsFollowTheViewsListOrderHiddenAndTitle(t *testing.T) {
	g := newFakeGH(t)
	g.token("", "fake-token-default-1")
	// A stale panel_order must not matter: for a GitHub source the views list is the only order.
	f := newServiceFixture(t, ghConfig(`"alpha":{"name":"Alpha","claude":{}}`,
		`"gh":{"plugin":"github","fleet":"alpha","panel_order":["review-requested","bugs"],"settings":{"views":`+mixedViews+`}}`))
	f.startSources()
	s := f.waitView("gh", "its views", func(s SourceView) bool { return len(s.Views) > 0 })
	ids := viewIDs(s)
	if want := []string{"my-prs", "bugs", "review-requested"}; !reflect.DeepEqual(ids, want) {
		t.Errorf("view ids = %v, want %v: the views list is the only order", ids, want)
	}
}

func TestAHiddenViewIsNotSearched(t *testing.T) {
	g := newFakeGH(t)
	g.token("", "fake-token-default-1")
	g.search("mine", "author:@me", [3]any{"acme/widgets", 10, "My PR"})
	g.search("bugs", "label:bug", [3]any{"acme/widgets", 13, "A bug"})
	g.search("docs", "label:docs", [3]any{"acme/widgets", 14, "A docs item"})
	f := newServiceFixture(t, viewsConfig(mixedViews))
	f.startSources()
	f.waitView("gh", "orders in the visible views", func(s SourceView) bool {
		return len(s.orders("my-prs")) > 0 && len(s.orders("bugs")) > 0
	})
	if !g.ran("label:bug") {
		t.Fatal("the visible Bugs view was never searched")
	}
	if g.ran("label:docs") {
		t.Error("the hidden Docs view was searched")
	}
}

// The runner's protocol: a hidden view answers as a view that isn't there, and For you is never a view.
func TestTheGitHubConnectionServesOnlyVisibleSearchViews(t *testing.T) {
	newFakeGH(t)
	cfg, err := config.Parse([]byte(`{"version":1,"fleets":{"alpha":{"name":"Alpha","claude":{}}}}`))
	if err != nil {
		t.Fatal(err)
	}
	g := openGitHub(openArgs{key: "gh", def: config.Source{Key: "gh", Plugin: "github", Fleet: "alpha"},
		cfg: func() *config.Config { return cfg }, onNote: func(string, json.RawMessage) {},
		logf: func(string, ...any) {}, setWarnings: func([]string) {}})
	t.Cleanup(g.stop)
	ctx := context.Background()
	if _, err := g.call(ctx, "initialize", map[string]any{"settings": json.RawMessage(`{"views":` + mixedViews + `}`)}, 0); err != nil {
		t.Fatal(err)
	}
	raw, err := g.call(ctx, "panels.list", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	var panels []map[string]string
	_ = json.Unmarshal(raw, &panels)
	wantPanels := []map[string]string{{"id": "my-prs", "title": "Mine"}, {"id": "bugs", "title": "Bugs"}, {"id": "review-requested", "title": "Review requested"}}
	if !reflect.DeepEqual(panels, wantPanels) {
		t.Errorf("panels.list = %v, want %v", panels, wantPanels)
	}
	for _, hidden := range []string{"for-you", "assigned-to-me", "docs"} {
		if _, err := g.call(ctx, "orders.list", map[string]any{"panel": hidden}, 0); err == nil {
			t.Errorf("orders.list of %q answered, want an error: it is hidden or not a search", hidden)
		}
	}
	if _, err := g.call(ctx, "orders.list", map[string]any{"panel": "bugs"}, 0); err != nil {
		t.Errorf("orders.list of the visible view bugs = %v, want an answer", err)
	}
}

func TestSettingsReturnTheViewsListUnmaskedAndASaveRoundTripsIt(t *testing.T) {
	f := newServiceFixture(t, viewsConfig(mixedViews))
	code, body := f.do(http.MethodGet, "/gh/settings", nil)
	if code != http.StatusOK || strings.Contains(body, unchangedMarker) {
		t.Fatalf("GET settings = %d %s, want 200 with nothing masked", code, body)
	}
	var got struct {
		Settings struct {
			Views []any `json:"views"`
		} `json:"settings"`
	}
	if err := json.Unmarshal([]byte(body), &got); err != nil {
		t.Fatal(err)
	}
	var want []any
	_ = json.Unmarshal([]byte(mixedViews), &want)
	if !reflect.DeepEqual(got.Settings.Views, want) {
		t.Fatalf("views = %v, want %v", got.Settings.Views, want)
	}
	var back map[string]any
	_ = json.Unmarshal([]byte(body), &back)
	if code, b := f.do(http.MethodPut, "/gh", map[string]any{"settings": back["settings"]}); code != http.StatusOK {
		t.Fatalf("PUT = %d %s", code, b)
	}
	sameJSON(t, "stored settings", f.stored("gh"), `{"views":`+mixedViews+`}`)
}

func TestASaveStoresTheBuiltInEntriesToo(t *testing.T) {
	f := newServiceFixture(t, viewsConfig(`[`+builtinViewEntries+`]`))
	settings := map[string]any{"views": []any{map[string]any{"title": "Bugs", "query": "is:open label:bug"}}}
	if code, b := f.do(http.MethodPut, "/gh", map[string]any{"settings": settings}); code != http.StatusOK {
		t.Fatalf("PUT = %d %s", code, b)
	}
	want := `{"views":[` + builtinViewEntries + `,{"title":"Bugs","query":"is:open label:bug"}]}`
	sameJSON(t, "stored settings", f.stored("gh"), want)
}

func TestAddingAGitHubSourceStoresTheBuiltInEntries(t *testing.T) {
	newFakeGH(t)
	f := newServiceFixture(t, ghConfig(`"alpha":{"name":"Alpha","claude":{}}`, ``))
	f.startSources()
	code, body := f.do(http.MethodPost, "", map[string]any{"plugin": "github", "name": "GitHub Two", "fleet": "alpha"})
	var added struct {
		Key string `json:"key"`
	}
	if err := json.Unmarshal([]byte(body), &added); code != http.StatusCreated || err != nil {
		t.Fatalf("POST = %d %s, want 201 with the key", code, body)
	}
	sameJSON(t, "stored settings", f.stored(added.Key), `{"views":[`+builtinViewEntries+`]}`)
}

func TestPanelOrderOnAGitHubSourceReordersItsViews(t *testing.T) {
	f := newServiceFixture(t, viewsConfig(`[`+builtinViewEntries+`,{"title":"Bugs","query":"is:open label:bug"}]`))
	code, b := f.do(http.MethodPut, "/gh/panel-order", map[string]any{"panels": []string{"bugs", "my-prs"}})
	if code != http.StatusOK {
		t.Fatalf("PUT panel-order = %d %s", code, b)
	}
	// The two ids fill, in the order given, the places the two views held: Bugs takes My PRs's, My PRs takes Bugs's.
	want := `{"views":[{"id":"for-you","hidden":false},{"id":"assigned-to-me","hidden":false},{"id":"review-requested","hidden":false},` +
		`{"title":"Bugs","query":"is:open label:bug"},{"id":"my-prs","hidden":false}]}`
	sameJSON(t, "stored settings", f.stored("gh"), want)
	if strings.Contains(f.file(), "panel_order") {
		t.Errorf("config.json holds a panel_order for the GitHub source:\n%s", f.file())
	}
}

func TestHidingRenamingAndReorderingViewsSurvivesARestart(t *testing.T) {
	f := newServiceFixture(t, viewsConfig(`[`+builtinViewEntries+`,{"title":"Bugs","query":"is:open label:bug"}]`))
	var settings map[string]any
	_ = json.Unmarshal([]byte(`{"views":`+mixedViews+`}`), &settings)
	if code, b := f.do(http.MethodPut, "/gh", map[string]any{"settings": settings}); code != http.StatusOK {
		t.Fatalf("PUT = %d %s", code, b)
	}
	// Reopening from the saved file is what a restart does.
	code, body := newServiceFixture(t, f.file()).do(http.MethodGet, "/gh/settings", nil)
	var got struct {
		Settings struct {
			Views json.RawMessage `json:"views"`
		} `json:"settings"`
	}
	if err := json.Unmarshal([]byte(body), &got); code != http.StatusOK || err != nil {
		t.Fatalf("GET settings after the restart = %d %s", code, body)
	}
	sameJSON(t, "views after the restart", string(got.Settings.Views), mixedViews)
}
