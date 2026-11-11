package sources

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"
)

// fakeGH steers e2e/testdata/fakegh/gh through $FAKEGH_DIR.
type fakeGH struct {
	t   *testing.T
	dir string
}

func newFakeGH(t *testing.T) *fakeGH {
	t.Helper()
	bin, err := filepath.Abs("../../e2e/testdata/fakegh")
	if err != nil {
		t.Fatal(err)
	}
	g := &fakeGH{t: t, dir: t.TempDir()}
	if err := os.MkdirAll(filepath.Join(g.dir, "search"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("FAKEGH_DIR", g.dir)
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	return g
}

func (g *fakeGH) write(rel, content string) {
	g.t.Helper()
	if err := os.WriteFile(filepath.Join(g.dir, rel), []byte(content), 0o600); err != nil {
		g.t.Fatal(err)
	}
}

// token signs the account in; user "" is the host's active account.
func (g *fakeGH) token(user, token string) {
	g.t.Helper()
	if user == "" {
		g.write("token", token+"\n")
		return
	}
	g.write("token-"+user, token+"\n")
}

// search answers a search that has match in an argument with the given items,
// each {repo, number, title}.
func (g *fakeGH) search(key, match string, items ...[3]any) {
	g.t.Helper()
	nodes := []string{}
	for _, it := range items {
		repo, n, title := it[0], it[1], it[2]
		nodes = append(nodes, fmt.Sprintf(`{"number":%d,"title":%q,"url":"https://github.com/%s/issues/%d","repository":{"nameWithOwner":%q,"url":"https://github.com/%s"},"labels":{"nodes":[]}}`,
			n, title, repo, n, repo, repo))
	}
	g.write("search/"+key+".match", match)
	g.write("search/"+key+".json", `{"data":{"search":{"nodes":[`+strings.Join(nodes, ",")+`]}}}`)
	g.unfail(key)
}

func (g *fakeGH) fail(key string) { g.write("search/"+key+".fail", "boom: search failed\n") }

func (g *fakeGH) unfail(key string) {
	if err := os.Remove(filepath.Join(g.dir, "search", key+".fail")); err != nil && !os.IsNotExist(err) {
		g.t.Fatal(err)
	}
}

func (g *fakeGH) searches() int {
	g.t.Helper()
	dirs, _ := filepath.Glob(filepath.Join(g.dir, "calls", "call.*", "argv"))
	n := 0
	for _, d := range dirs {
		if b, _ := os.ReadFile(d); strings.Contains(string(b), "graphql") {
			n++
		}
	}
	return n
}

func mkCheckout(t *testing.T, dir, remote string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(dir, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	cfg := "[remote \"origin\"]\n\turl = " + remote + "\n"
	if err := os.WriteFile(filepath.Join(dir, ".git", "config"), []byte(cfg), 0o644); err != nil {
		t.Fatal(err)
	}
}

// startSources runs the fixture's supervisor until the test ends.
func (f *serviceFixture) startSources() {
	f.t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	f.Svc.Sup.Start(ctx)
	f.t.Cleanup(func() { cancel(); f.Svc.Sup.Wait() })
}

func (f *serviceFixture) waitView(key, what string, pred func(SourceView) bool) SourceView {
	f.t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for {
		list := f.Svc.View()
		for _, s := range list {
			if s.Key == key && pred(s) {
				return s
			}
		}
		if time.Now().After(deadline) {
			b, _ := json.Marshal(list)
			f.t.Fatalf("within 15s source %q never showed %s; sources: %s", key, what, b)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func (s SourceView) orders(view string) []string {
	ids := []string{}
	for _, v := range s.Views {
		if v.ID == view {
			for _, o := range v.Orders {
				ids = append(ids, o.ID)
			}
		}
	}
	return ids
}

func (s SourceView) view(id string) Panel {
	for _, v := range s.Views {
		if v.ID == id {
			return v
		}
	}
	return Panel{}
}

func ghConfig(fleets, sources string) string {
	return `{"version":1,"fleets":{` + fleets + `},"sources":{` + sources + `}}`
}

func TestGitHubSourceRoutesItemsToFleets(t *testing.T) {
	g := newFakeGH(t)
	g.token("", "fake-token-default-1")
	g.token("work", "fake-token-work-2")
	rootA, rootB := t.TempDir(), t.TempDir()
	mkCheckout(t, filepath.Join(rootA, "widgets"), "https://github.com/acme/widgets.git")
	mkCheckout(t, filepath.Join(rootB, "widgets-fork"), "https://github.com/acme/widgets.git")
	g.search("assigned", "assignee:@me",
		[3]any{"acme/widgets", 12, "Flaky login test"}, [3]any{"globex/app", 5, "Globex bug"}, [3]any{"other/thing", 9, "Somebody else's"})
	g.search("review", "review-requested:@me", [3]any{"acme/widgets", 7, "Bump deps"}, [3]any{"globex/app", 6, "Globex PR"})
	g.search("mine", "author:@me", [3]any{"other/thing", 10, "My PR"})
	g.search("bugs", "label:bug", [3]any{"acme/widgets", 13, "Any bug"}, [3]any{"globex/app", 8, "Globex any bug"})

	fleets := fmt.Sprintf(`"alpha":{"name":"Alpha","claude":{},"search_roots":[%q]},"bravo":{"name":"Bravo","claude":{},"search_roots":[%q],"owners":["globex"]}`, rootA, rootB)
	f := newServiceFixture(t, ghConfig(fleets,
		`"gha":{"plugin":"github","fleet":"alpha","settings":{"views":[{"title":"Bugs","query":"is:open label:bug"}]}},`+
			`"ghb":{"plugin":"github","fleet":"bravo","settings":{"account":"work","views":[]}}`))
	// A Python plugin left in the folder must never run: the source is built in.
	marker := filepath.Join(t.TempDir(), "plugin-ran")
	dir := filepath.Join(f.home, "plugins", "github")
	for name, body := range map[string]string{
		"plugin.json": `{"name":"github","description":"poison","command":"./plugin.py","args":[]}`,
		"plugin.py":   "#!/bin/sh\ntouch " + marker + "\nexit 1\n",
	} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	f.startSources()

	ready := func(s SourceView) bool { return s.State == "ok" && len(s.orders("assigned-to-me")) > 0 }
	a := f.waitView("gha", "orders", ready)
	b := f.waitView("ghb", "orders", func(s SourceView) bool { return ready(s) && len(s.orders("review-requested")) > 0 })

	for _, c := range []struct {
		src  SourceView
		view string
		want []string
	}{
		// acme/widgets is checked out in both fleets: the first fleet takes it.
		{a, "assigned-to-me", []string{"acme/widgets#12", "other/thing#9"}},
		{a, "review-requested", []string{"acme/widgets#7"}},
		{a, "my-prs", []string{"other/thing#10"}},
		{a, "bugs", []string{"acme/widgets#13"}},
		{b, "assigned-to-me", []string{"globex/app#5"}},
		{b, "review-requested", []string{"globex/app#6"}},
		{b, "my-prs", []string{}},
	} {
		if got := c.src.orders(c.view); !reflect.DeepEqual(got, c.want) {
			t.Errorf("%s view %s = %v, want %v", c.src.Key, c.view, got, c.want)
		}
	}
	for _, s := range []SourceView{a, b} {
		if len(s.Warnings) == 0 || !strings.Contains(strings.Join(s.Warnings, "\n"), "acme/widgets") {
			t.Errorf("source %s warnings = %v, want one naming acme/widgets", s.Key, s.Warnings)
		}
	}
	if _, err := os.Stat(marker); err == nil {
		t.Error("the plugin in plugins/github ran: the source must run in Go")
	}

	// The API names a source's lists `views` and its notes `warnings`.
	code, body := f.do(http.MethodGet, "", nil)
	var list []map[string]json.RawMessage
	if err := json.Unmarshal([]byte(body), &list); code != http.StatusOK || err != nil || len(list) != 2 {
		t.Fatalf("GET /api/sources = %d %s, want two sources", code, body)
	}
	if _, ok := list[0]["views"]; !ok {
		t.Errorf("source JSON keys = %v, want views", list[0])
	}
	if _, ok := list[0]["panels"]; ok {
		t.Errorf("source JSON still has panels: %v", list[0])
	}
}

func TestGitHubSourceSignInAndViewErrors(t *testing.T) {
	g := newFakeGH(t)
	g.search("bugs", "label:bug", [3]any{"acme/widgets", 13, "Any bug"})
	f := newServiceFixture(t, ghConfig(`"alpha":{"name":"Alpha","claude":{}}`,
		`"gh":{"plugin":"github","fleet":"alpha","settings":{"account":"newbie","views":[`+
			`{"title":"Bugs","query":"is:open label:bug"},{"title":"Other","query":"is:open label:other"}]}}`))
	f.startSources()
	refresh := func() {
		t.Helper()
		if code, body := f.do(http.MethodPost, "/gh/refresh", map[string]any{}); code != http.StatusOK {
			t.Fatalf("POST refresh = %d %s", code, body)
		}
	}

	// Signed out: the sign-in card shows and no search ran.
	f.waitView("gh", "the sign-in card", func(s SourceView) bool {
		return s.State == "auth_required" && s.Auth != nil && s.Auth.Message != ""
	})
	if n := g.searches(); n != 0 {
		t.Errorf("%d searches ran while signed out, want none", n)
	}

	// Signed in: the card goes and the items show.
	g.search("other", "label:other", [3]any{"acme/widgets", 21, "Other item"})
	g.token("newbie", "fake-token-new-3")
	refresh()
	f.waitView("gh", "the card gone and the items", func(s SourceView) bool {
		return s.State == "ok" && s.Auth == nil && len(s.orders("bugs")) > 0 && len(s.orders("other")) > 0
	})

	// One view's search fails: its error shows on it alone, with its last orders.
	g.fail("other")
	refresh()
	failed := f.waitView("gh", "an error on Other", func(s SourceView) bool { return s.view("other").Error != "" })
	if v := failed.view("bugs"); v.Error != "" || len(v.Orders) == 0 {
		t.Errorf("view bugs = %+v, want no error and its orders while Other fails", v)
	}
	if got := failed.orders("other"); !reflect.DeepEqual(got, []string{"acme/widgets#21"}) {
		t.Errorf("failed view other kept %v, want its last orders", got)
	}

	// The next good search clears it.
	g.unfail("other")
	refresh()
	f.waitView("gh", "the error cleared", func(s SourceView) bool {
		return s.view("other").Error == "" && len(s.orders("other")) == 1
	})

	_ = filepath.WalkDir(f.home, func(p string, d os.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			if b, _ := os.ReadFile(p); strings.Contains(string(b), "fake-token-new-3") {
				t.Errorf("%s holds the token", p)
			}
		}
		return nil
	})
}

func TestAddGitHubSourceNeedsNoPluginFolder(t *testing.T) {
	newFakeGH(t)
	f := newServiceFixture(t, ghConfig(`"alpha":{"name":"Alpha","claude":{}}`, ``))
	f.startSources()
	resp, err := http.Get(f.srv.URL + "/api/plugins")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var plugins []struct {
		Name string `json:"name"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&plugins); err != nil || len(plugins) == 0 || plugins[0].Name != "github" {
		t.Errorf("GET /api/plugins = %+v (%v), want github offered", plugins, err)
	}
	code, body := f.do(http.MethodPost, "", map[string]any{"plugin": "github", "name": "GitHub Two", "fleet": "alpha"})
	var added struct {
		Key string `json:"key"`
	}
	if err := json.Unmarshal([]byte(body), &added); code != http.StatusCreated || err != nil {
		t.Fatalf("POST = %d %s, want 201 with the key", code, body)
	}
	if got := f.stored(added.Key); got != `{"views":[]}` {
		t.Errorf("stored settings = %q, want {\"views\":[]}: a github source with views is the built-in one", got)
	}
}

func TestStaleSessionCannotSetWarnings(t *testing.T) {
	changed := 0
	r := &runner{sid: 2, notify: func() { changed++ }}
	r.setWarnings(1, []string{"from a stopped session"})
	if len(r.warnings) != 0 || changed != 0 {
		t.Errorf("a stale session set warnings %v (%d changes), want none", r.warnings, changed)
	}
	r.setWarnings(2, []string{"current"})
	if len(r.warnings) != 1 || changed != 1 {
		t.Errorf("the current session's warnings = %v (%d changes), want one", r.warnings, changed)
	}
}

// markers lists the FLEET_MARK each recorded `gh auth token` call saw ("" for none).
func (g *fakeGH) markers() []string {
	g.t.Helper()
	dirs, _ := filepath.Glob(filepath.Join(g.dir, "calls", "call.*"))
	var out []string
	for _, d := range dirs {
		argv, _ := os.ReadFile(filepath.Join(d, "argv"))
		if !strings.HasPrefix(string(argv), "auth\x00") {
			continue
		}
		env, _ := os.ReadFile(filepath.Join(d, "env"))
		mark := ""
		for _, line := range strings.Split(string(env), "\n") {
			if v, ok := strings.CutPrefix(line, "FLEET_MARK="); ok {
				mark = v
			}
		}
		out = append(out, mark)
	}
	return out
}

func TestFleetEnvChangeRestartsOnlyItsFleetsSources(t *testing.T) {
	g := newFakeGH(t)
	g.token("", "fake-token-default-1")
	fleets := func(mark string) string {
		return fmt.Sprintf(`"alpha":{"name":"Alpha","claude":{},"env":{"FLEET_MARK":%q}},"bravo":{"name":"Bravo","claude":{}}`, mark)
	}
	sources := `"gha":{"plugin":"github","fleet":"alpha","settings":{"views":[]}},"ghb":{"plugin":"github","fleet":"bravo","settings":{"views":[]}}`
	f := newServiceFixture(t, ghConfig(fleets("one"), sources))
	f.startSources()
	waitMarks := func(what string, pred func([]string) bool) {
		t.Helper()
		deadline := time.Now().Add(15 * time.Second)
		for !pred(g.markers()) {
			if time.Now().After(deadline) {
				t.Fatalf("within 15s no gh call showed %s; FLEET_MARKs seen: %q", what, g.markers())
			}
			time.Sleep(10 * time.Millisecond)
		}
	}
	waitMarks("both fleets' tokens", func(m []string) bool { return slices.Contains(m, "one") && slices.Contains(m, "") })
	sid := func(key string) int {
		r := f.Svc.Sup.runner(key)
		r.mu.Lock()
		defer r.mu.Unlock()
		return r.sid
	}
	alpha, bravo := sid("gha"), sid("ghb")

	if err := os.WriteFile(filepath.Join(f.home, "config.json"), []byte(ghConfig(fleets("two"), sources)), 0o600); err != nil {
		t.Fatal(err)
	}
	waitMarks("the changed FLEET_MARK", func(m []string) bool { return slices.Contains(m, "two") })
	if sid("gha") == alpha {
		t.Error("alpha's source kept its process after its fleet's env changed")
	}
	if sid("ghb") != bravo {
		t.Error("bravo's source restarted though its fleet's env did not change")
	}
}
