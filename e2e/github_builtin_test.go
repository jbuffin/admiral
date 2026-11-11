package e2e

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"
)

// #260 u3: GitHub is a built-in order source per fleet (no Python plugin), with
// views, routing of every item to exactly one fleet, and the fleet's env. The
// fake gh answers the searches.

// ghViewJSON, ghSrcJSON read /api/v1/sources with the renamed `views` and the
// new `warnings`, apart from sourceJSON, whose field is still `panels`.
type ghViewJSON struct {
	ID     string      `json:"id"`
	Title  string      `json:"title"`
	Orders []orderJSON `json:"orders"`
	Error  *string     `json:"error"`
}

type ghSrcJSON struct {
	Key          string          `json:"key"`
	Name         string          `json:"name"`
	Plugin       string          `json:"plugin"`
	IconURL      string          `json:"icon_url"`
	Capabilities []string        `json:"capabilities"`
	State        string          `json:"state"`
	Error        *string         `json:"error"`
	Views        []ghViewJSON    `json:"views"`
	Warnings     json.RawMessage `json:"warnings"`
}

func (s ghSrcJSON) view(id string) (ghViewJSON, bool) {
	for _, v := range s.Views {
		if v.ID == id {
			return v, true
		}
	}
	return ghViewJSON{}, false
}

func (s ghSrcJSON) viewOrders(id string) []string {
	v, _ := s.view(id)
	ids := []string{}
	for _, o := range v.Orders {
		ids = append(ids, o.ID)
	}
	return ids
}

func (s ghSrcJSON) warnings() []string {
	var out []string
	_ = json.Unmarshal(s.Warnings, &out)
	return out
}

func (w *world) ghSources() []ghSrcJSON {
	w.t.Helper()
	code, body := send(w.t, http.MethodGet, w.in.LocalURL+"/api/v1/sources", nil)
	if code != http.StatusOK {
		w.t.Fatalf("GET /api/v1/sources = %d, want 200: %s", code, body)
	}
	var list []ghSrcJSON
	if err := json.Unmarshal([]byte(body), &list); err == nil {
		return list
	}
	var wrap struct {
		Sources []ghSrcJSON `json:"sources"`
	}
	if err := json.Unmarshal([]byte(body), &wrap); err != nil {
		w.t.Fatalf("GET /api/v1/sources is not JSON: %v\n%s", err, body)
	}
	return wrap.Sources
}

// waitGH polls /api/v1/sources until pred holds for the source.
func (w *world) waitGH(key string, d time.Duration, what string, pred func(ghSrcJSON) bool) ghSrcJSON {
	w.t.Helper()
	deadline := time.Now().Add(d)
	for {
		list := w.ghSources()
		for _, s := range list {
			if s.Key == key && pred(s) {
				return s
			}
		}
		if time.Now().After(deadline) {
			w.t.Fatalf("within %s source %q never showed %s; last list: %s", d, key, what, dump(list))
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// ghFleet is one fleet of the config as raw JSON.
func (w *world) ghFleet(name string, roots []string, owners []string, env map[string]string) string {
	f := map[string]any{"name": name, "color": "#336699", "search_roots": roots,
		"claude": map[string]any{"executable": w.fakeExe("claude"), "env": w.harnessEnv()}}
	if owners != nil {
		f["owners"] = owners
	}
	if env != nil {
		f["env"] = env
	}
	b, err := json.Marshal(f)
	if err != nil {
		w.t.Fatal(err)
	}
	return string(b)
}

// ghConfig is a config with the fleets, given as key then raw JSON pairs in
// file order, and the sources.
func ghConfig(fleets []string, sources string) string {
	var parts []string
	for i := 0; i < len(fleets); i += 2 {
		k, _ := json.Marshal(fleets[i])
		parts = append(parts, string(k)+":"+fleets[i+1])
	}
	return `{"version":1,"fleets":{` + strings.Join(parts, ",") + `},"sources":` + sources + `}`
}

// checkoutOf makes a git checkout of a repo of github.com under root.
func (w *world) checkoutOf(root, dir, repo string) {
	w.t.Helper()
	d := filepath.Join(root, dir)
	if err := os.MkdirAll(d, 0o755); err != nil {
		w.t.Fatal(err)
	}
	w.git(d, "init", "-q")
	w.git(d, "remote", "add", "origin", "https://github.com/"+repo+".git")
}

func inFleetDef(d srcDef, fleet string) srcDef { d.Fleet = fleet; return d }

func TestGitHubBuiltin(t *testing.T) {
	const (
		defaultToken = "fake-token-default-1"
		workToken    = "fake-token-work-2"
	)
	w := newWorld(t)
	g := w.newFakeGH()
	g.token("", defaultToken)
	g.token("work", workToken)

	// One repo, acme/widgets, is checked out in both fleets: a conflict. Fleet
	// bravo also lists the owner globex. alpha is the first fleet, the default.
	rootA, rootB := filepath.Join(w.root, "ra"), filepath.Join(w.root, "rb")
	w.checkoutOf(rootA, "widgets", "acme/widgets")
	w.checkoutOf(rootB, "widgets-fork", "acme/widgets")

	g.search("assigned", "assignee:@me",
		ghNode("github.com", "acme/widgets", 12, false, "Flaky login test"),
		ghNode("github.com", "globex/app", 5, false, "Globex bug"),
		ghNode("github.com", "other/thing", 9, false, "Somebody else's"))
	g.search("review", "review-requested:@me",
		ghNode("github.com", "acme/widgets", 7, true, "Bump deps"),
		ghNode("github.com", "globex/app", 6, true, "Globex PR"))
	g.search("mine", "author:@me", ghNode("github.com", "other/thing", 10, true, "My PR"))
	g.search("bugs", "label:bug",
		ghNode("github.com", "acme/widgets", 13, false, "Any bug"),
		ghNode("github.com", "globex/app", 8, false, "Globex any bug"))

	// The plugin folder is a poison: if Admiral ran the Python plugin it would
	// leave marker. It carries an old stamp so the start leaves it alone.
	marker := filepath.Join(w.root, "plugin-ran")
	dir := filepath.Join(w.home, "plugins", "github")
	w.writeRaw(filepath.Join(dir, "plugin.json"), `{"name":"github","description":"poison","command":"./plugin.py","args":[]}`, 0o644)
	w.writeRaw(filepath.Join(dir, "plugin.py"), "#!/bin/sh\ntouch "+marker+"\nexit 1\n", 0o755)
	w.writeRaw(filepath.Join(dir, ".admiral-stamp"), stampOf(map[string]string{"plugin.py": "an older copy"}), 0o644)

	fleets := []string{
		"alpha", w.ghFleet("Alpha", []string{rootA}, nil, nil),
		"bravo", w.ghFleet("Bravo", []string{rootB}, []string{"globex"}, nil),
		"charlie", w.ghFleet("Charlie", []string{}, nil, nil),
	}
	writeConfig(t, w.home, ghConfig(fleets, rawSources(
		srcDef{Key: "gha", Plugin: "github", Name: "GitHub", Fleet: "alpha", Settings: map[string]any{
			"views": []map[string]any{{"title": "Bugs", "query": "is:open label:bug"}}}},
		srcDef{Key: "ghb", Plugin: "github", Name: "GitHub", Fleet: "bravo", Settings: map[string]any{
			"account": "work", "views": []any{}}},
	)))
	w.start()

	titles := func(s ghSrcJSON) []string {
		out := []string{}
		for _, v := range s.Views {
			out = append(out, v.Title)
		}
		return out
	}
	ready := func(s ghSrcJSON) bool {
		return s.State == "ok" && len(s.Views) >= 3 && len(s.Views[0].Orders) > 0
	}
	a := w.waitGH("gha", 20*time.Second, "views with orders", ready)
	b := w.waitGH("ghb", 20*time.Second, "views with orders", ready)

	// AC1: the three default views, then the custom one.
	if got, want := titles(a), []string{"Assigned to me", "Review requested", "My PRs", "Bugs"}; !reflect.DeepEqual(got, want) {
		t.Errorf("alpha views = %v, want %v", got, want)
	}
	if got, want := titles(b), []string{"Assigned to me", "Review requested", "My PRs"}; !reflect.DeepEqual(got, want) {
		t.Errorf("bravo views = %v, want %v", got, want)
	}
	for i, id := range []string{"assigned-to-me", "review-requested", "my-prs"} {
		if a.Views[i].ID != id || b.Views[i].ID != id {
			t.Errorf("view %d ids = %q and %q, want %q (the title slug)", i, a.Views[i].ID, b.Views[i].ID, id)
		}
	}
	if a.Name != "GitHub" || !reflect.DeepEqual(a.Capabilities, []string{"refresh"}) {
		t.Errorf("alpha source name %q, capabilities %v, want GitHub with only refresh", a.Name, a.Capabilities)
	}

	// AC2: every item shows in exactly one fleet. acme/widgets is checked out in
	// both, the first fleet takes it; globex/app is bravo's owner; other/thing
	// goes to the first fleet, the default.
	for _, c := range []struct {
		src  ghSrcJSON
		view string
		want []string
	}{
		{a, "assigned-to-me", []string{"acme/widgets#12", "other/thing#9"}},
		{a, "review-requested", []string{"acme/widgets#7"}},
		{a, "my-prs", []string{"other/thing#10"}},
		{a, "bugs", []string{"acme/widgets#13"}},
		{b, "assigned-to-me", []string{"globex/app#5"}},
		{b, "review-requested", []string{"globex/app#6"}},
		{b, "my-prs", []string{}},
	} {
		if got := c.src.viewOrders(c.view); !reflect.DeepEqual(got, c.want) {
			t.Errorf("%s view %s = %v, want %v", c.src.Key, c.view, got, c.want)
		}
	}

	// Both fleets that check acme/widgets out carry the conflict warning.
	for _, s := range []ghSrcJSON{a, b} {
		if ws := s.warnings(); len(ws) == 0 || !strings.Contains(strings.Join(ws, "\n"), "acme/widgets") {
			t.Errorf("source %s warnings = %s, want one naming acme/widgets", s.Key, s.Warnings)
		}
	}

	// The calls: a token for the host (with the account for bravo), then
	// searches that carry it in GH_TOKEN, never in argv.
	calls := g.calls()
	if !slices.ContainsFunc(calls, func(c ghCall) bool { return slices.Equal(c.Argv, []string{"auth", "token", "-h", "github.com"}) }) {
		t.Errorf("no `gh auth token -h github.com` call; calls: %+v", calls)
	}
	if !slices.ContainsFunc(calls, func(c ghCall) bool {
		return slices.Equal(c.Argv, []string{"auth", "token", "-h", "github.com", "-u", "work"})
	}) {
		t.Errorf("no `gh auth token -h github.com -u work` call; calls: %+v", calls)
	}
	if len(g.searchesWith(defaultToken)) == 0 || len(g.searchesWith(workToken)) == 0 {
		t.Errorf("searches with the default token %d, with the work token %d, want some of each", len(g.searchesWith(defaultToken)), len(g.searchesWith(workToken)))
	}
	for _, c := range calls {
		if c.mentions(defaultToken) || c.mentions(workToken) {
			t.Errorf("a token is in argv: %q", c.Argv)
		}
	}

	// No Python plugin ran.
	if _, err := os.Stat(marker); err == nil {
		t.Errorf("the github plugin in plugins/github ran: the source must run in Go")
	}

	// And none is needed: with the folder gone the source still has its icon, the
	// add picker still offers GitHub, and an add through the API works and writes
	// `views: []`, on a fleet that nothing routes to.
	if err := os.RemoveAll(dir); err != nil {
		t.Fatal(err)
	}
	resp, icon := get(t, w.in.LocalURL+a.IconURL, nil)
	if a.IconURL == "" || resp.StatusCode != http.StatusOK || !strings.Contains(icon, "<svg") {
		t.Errorf("GET %q = %d %.60q, want the GitHub icon with no plugin folder", a.IconURL, resp.StatusCode, icon)
	}
	code, body := send(t, http.MethodGet, w.in.LocalURL+"/api/plugins", nil)
	if code != http.StatusOK || !strings.Contains(body, `"github"`) {
		t.Errorf("GET /api/plugins = %d %s, want github listed with no plugin folder", code, body)
	}
	code, body = w.srcReq(http.MethodPost, "", map[string]any{"plugin": "github", "name": "GitHub Three", "fleet": "charlie"})
	w.mustOK(code, body, "POST /api/sources for github with no plugin folder")
	var added struct {
		Key string `json:"key"`
	}
	if err := json.Unmarshal([]byte(body), &added); err != nil || added.Key == "" {
		t.Fatalf("POST answered %s, want the new source's key", body)
	}
	settings, _ := w.configSources()[added.Key]["settings"].(map[string]any)
	if views, ok := settings["views"].([]any); !ok || len(views) != 0 {
		t.Errorf("config.json settings of the added source = %v, want views: []", settings)
	}
	c := w.waitGH(added.Key, 20*time.Second, "three views", func(s ghSrcJSON) bool { return s.State == "ok" && len(s.Views) == 3 })
	if string(c.Warnings) != "[]" {
		t.Errorf("warnings of a fleet with no conflict = %s, want []", c.Warnings)
	}
	if _, err := os.Stat(marker); err == nil {
		t.Errorf("a plugin ran for the added source")
	}
}

func TestFleetEnv(t *testing.T) {
	w := newWorld(t)
	g := w.newFakeGH()
	g.token("", "fake-token-default-1")
	w.installPlugin("testplug", "testplug")
	pa, pb := w.newTP("pa"), w.newTP("pb")

	config := func(mark string) string {
		return ghConfig([]string{
			"alpha", w.ghFleet("Alpha", []string{w.work}, nil, map[string]string{"FLEET_MARK": mark, "ADMIRAL_OVER": "fleet-over"}),
			"bravo", w.ghFleet("Bravo", []string{w.work}, nil, nil),
		}, rawSources(
			inFleetDef(tpSource("pa", pa, nil), "alpha"),
			inFleetDef(tpSource("pb", pb, nil), "bravo"),
			srcDef{Key: "gha", Plugin: "github", Name: "GitHub", Fleet: "alpha", Settings: map[string]any{"views": []any{}}},
			srcDef{Key: "ghb", Plugin: "github", Name: "GitHub", Fleet: "bravo", Settings: map[string]any{"account": "work", "views": []any{}}},
		))
	}
	writeConfig(t, w.home, config("~/mark"))
	w.start()
	w.waitSource("pa", 15*time.Second, "state ok", isOK)
	w.waitSource("pb", 15*time.Second, "state ok", isOK)

	// A plugin source starts with Admiral's environment overlaid with its
	// fleet's env (~ expanded); a fleet with no env changes nothing.
	waitEnv := func(p *tplug, want string) {
		t.Helper()
		deadline := time.Now().Add(15 * time.Second)
		for p.read("env") != want {
			if time.Now().After(deadline) {
				t.Fatalf("within 15s the plugin saw env %q, want %q", p.read("env"), want)
			}
			time.Sleep(50 * time.Millisecond)
		}
	}
	mark := filepath.Join(w.userHom, "mark")
	waitEnv(pa, "FLEET_MARK="+mark+"\nADMIRAL_BASE=base\nADMIRAL_OVER=fleet-over")
	waitEnv(pb, "ADMIRAL_BASE=base\nADMIRAL_OVER=base")

	// The GitHub source's gh calls get the same.
	var alpha, bravo ghCall
	deadline := time.Now().Add(15 * time.Second)
	for {
		for _, c := range g.calls() {
			if slices.Equal(c.Argv, []string{"auth", "token", "-h", "github.com"}) {
				alpha = c
			}
			if slices.Equal(c.Argv, []string{"auth", "token", "-h", "github.com", "-u", "work"}) {
				bravo = c
			}
		}
		if alpha.Argv != nil && bravo.Argv != nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("within 15s gh was not asked for both fleets' tokens; calls: %+v", g.calls())
		}
		time.Sleep(50 * time.Millisecond)
	}
	if alpha.Env["FLEET_MARK"] != mark || alpha.Env["ADMIRAL_OVER"] != "fleet-over" || alpha.Env["ADMIRAL_BASE"] != "base" {
		t.Errorf("alpha's gh env = %v, want FLEET_MARK=%s, ADMIRAL_OVER=fleet-over, ADMIRAL_BASE=base", alpha.Env, mark)
	}
	if _, ok := bravo.Env["FLEET_MARK"]; ok || bravo.Env["ADMIRAL_OVER"] != "base" || bravo.Env["ADMIRAL_BASE"] != "base" {
		t.Errorf("bravo's gh env = %v, want no FLEET_MARK and Admiral's own ADMIRAL_OVER=base", bravo.Env)
	}

	// A change to a fleet's env restarts that fleet's plugin sources only.
	pa.waitStarts(5*time.Second, 1)
	replaceConfig(t, w.home, config("changed"))
	pa.waitStarts(15*time.Second, 2)
	waitEnv(pa, "FLEET_MARK=changed\nADMIRAL_BASE=base\nADMIRAL_OVER=fleet-over")
	if n := len(pb.starts()); n != 1 {
		t.Errorf("bravo's plugin started %d times, want 1: its fleet's env did not change", n)
	}
}
