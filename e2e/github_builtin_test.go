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

// ghSrcJSON is a source as /api/v1/sources lists it, with `views` and `warnings`.
type ghSrcJSON struct {
	Key      string    `json:"key"`
	State    string    `json:"state"`
	Auth     *authJSON `json:"auth"`
	Warnings []string  `json:"warnings"`
	Views    []struct {
		ID     string      `json:"id"`
		Title  string      `json:"title"`
		Orders []orderJSON `json:"orders"`
	} `json:"views"`
}

func (s ghSrcJSON) viewOrders(id string) []string {
	ids := []string{}
	for _, v := range s.Views {
		if v.ID == id {
			for _, o := range v.Orders {
				ids = append(ids, o.ID)
			}
		}
	}
	return ids
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

func ghConfig(fleets []string, sources string) string {
	var parts []string
	for i := 0; i < len(fleets); i += 2 {
		k, _ := json.Marshal(fleets[i])
		parts = append(parts, string(k)+":"+fleets[i+1])
	}
	return `{"version":1,"fleets":{` + strings.Join(parts, ",") + `},"sources":` + sources + `}`
}

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
	t.Parallel()
	w := newWorld(t)
	g := w.newFakeGH()
	g.token("", "fake-token-default-1")
	g.token("work", "fake-token-work-2")

	// acme/widgets is checked out in both fleets, so it conflicts; bravo also
	// claims the owner globex. alpha is the first fleet, the default.
	rootA, rootB := filepath.Join(w.root, "ra"), filepath.Join(w.root, "rb")
	w.checkoutOf(rootA, "widgets", "acme/widgets")
	w.checkoutOf(rootB, "widgets-fork", "acme/widgets")
	g.search("assigned", "assignee:@me",
		ghNode("github.com", "acme/widgets", 12, false, "Flaky login test"),
		ghNode("github.com", "globex/app", 5, false, "Globex bug"),
		ghNode("github.com", "other/thing", 9, false, "Somebody else's"))
	g.search("review", "review-requested:@me", ghNode("github.com", "globex/app", 6, true, "Globex PR"))

	writeConfig(t, w.home, ghConfig([]string{
		"alpha", w.ghFleet("Alpha", []string{rootA}, nil, nil),
		"bravo", w.ghFleet("Bravo", []string{rootB}, []string{"globex"}, nil),
	}, rawSources(
		srcDef{Key: "gha", Plugin: "github", Name: "GitHub", Fleet: "alpha", Settings: map[string]any{"views": []any{}}},
		srcDef{Key: "ghb", Plugin: "github", Name: "GitHub", Fleet: "bravo", Settings: map[string]any{"account": "work", "views": []any{}}},
		// Nobody has signed this account in yet.
		srcDef{Key: "ghc", Plugin: "github", Name: "GitHub", Fleet: "alpha", Settings: map[string]any{
			"account": "newbie", "views": []map[string]any{{"title": "Newbie", "query": "is:open label:newbie"}}}},
	)))
	w.start()

	ready := func(s ghSrcJSON) bool { return s.State == "ok" && len(s.viewOrders("assigned-to-me")) > 0 }
	a := w.waitGH("gha", 20*time.Second, "views with orders", ready)
	b := w.waitGH("ghb", 20*time.Second, "views with orders", ready)

	for _, c := range []struct {
		src  ghSrcJSON
		view string
		want []string
	}{
		{a, "assigned-to-me", []string{"acme/widgets#12", "other/thing#9"}},
		{b, "assigned-to-me", []string{"globex/app#5"}},
		{b, "review-requested", []string{"globex/app#6"}},
		{a, "review-requested", []string{}},
	} {
		if got := c.src.viewOrders(c.view); !reflect.DeepEqual(got, c.want) {
			t.Errorf("%s view %s = %v, want %v", c.src.Key, c.view, got, c.want)
		}
	}
	for _, s := range []ghSrcJSON{a, b} {
		if !strings.Contains(strings.Join(s.Warnings, "\n"), "acme/widgets") {
			t.Errorf("source %s warnings = %v, want one naming acme/widgets", s.Key, s.Warnings)
		}
	}

	// Signed out: the sign-in card shows and its search never runs. Then the
	// account signs in and its items come.
	w.waitGH("ghc", 20*time.Second, "the sign-in card", func(s ghSrcJSON) bool {
		return s.State == "auth_required" && s.Auth != nil && s.Auth.Message != ""
	})
	holdThat(t, "a search ran for the signed-out account", func() bool {
		return !slices.ContainsFunc(g.calls(), func(c ghCall) bool { return strings.Contains(strings.Join(c.Argv, " "), "label:newbie") })
	})
	g.search("newbie", "label:newbie", ghNode("github.com", "acme/widgets", 21, false, "Newbie item"))
	g.token("newbie", "fake-token-new-3")
	code, body := w.srcReq(http.MethodPost, "/ghc/refresh", map[string]any{})
	w.mustOK(code, body, "POST refresh")
	w.waitGH("ghc", 20*time.Second, "the card gone and its items", func(s ghSrcJSON) bool {
		return s.State == "ok" && s.Auth == nil && reflect.DeepEqual(s.viewOrders("newbie"), []string{"acme/widgets#21"})
	})
}

func TestFleetEnv(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	w.installPlugin("testplug", "testplug")
	pa, pb := w.newTP("pa"), w.newTP("pb")
	writeConfig(t, w.home, ghConfig([]string{
		"alpha", w.ghFleet("Alpha", []string{w.work}, nil, map[string]string{"FLEET_MARK": "~/mark", "ADMIRAL_OVER": "fleet-over"}),
		"bravo", w.ghFleet("Bravo", []string{w.work}, nil, nil),
	}, rawSources(inFleetDef(tpSource("pa", pa, nil), "alpha"), inFleetDef(tpSource("pb", pb, nil), "bravo"))))
	w.start()
	w.waitSource("pa", 15*time.Second, "state ok", isOK)
	w.waitSource("pb", 15*time.Second, "state ok", isOK)

	// A plugin starts with Admiral's environment overlaid with its fleet's env
	// (~ expanded); a fleet with no env changes nothing.
	mark := filepath.Join(w.userHom, "mark")
	for p, want := range map[*tplug]string{
		pa: "FLEET_MARK=" + mark + "\nADMIRAL_BASE=base\nADMIRAL_OVER=fleet-over",
		pb: "ADMIRAL_BASE=base\nADMIRAL_OVER=base",
	} {
		if got := p.read("env"); got != want {
			t.Errorf("plugin env = %q, want %q", got, want)
		}
	}
}
