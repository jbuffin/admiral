package github

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jbuffin/admiral/internal/config"
)

func TestRoute(t *testing.T) {
	fleets := []RouteFleet{
		{Key: "a", Owners: []string{"acme"}},
		{Key: "b", Repos: []string{"github.com/acme/widgets", "ghe.example.com/corp/tools"}, Owners: []string{"Beta"}},
	}
	cases := []struct {
		name, host, repo, want string
	}{
		{"checkout beats another fleet's owner claim", "github.com", "acme/widgets", "b"},
		{"owner claim with no checkout", "github.com", "beta/other", "b"},
		{"owner claim is case-insensitive", "github.com", "BETA/Other", "b"},
		{"no match goes to the default fleet", "github.com", "nobody/thing", "a"},
		{"case-insensitive repo", "github.com", "Acme/Widgets", "b"},
		{"remote on another host is no project for github.com", "github.com", "corp/tools", "a"},
		{"same remote on its own host is a project", "ghe.example.com", "corp/tools", "b"},
		{"owner claims ignore host", "ghe.example.com", "acme/anything", "a"},
	}
	r := NewRouter(fleets)
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := r.Route(c.host, c.repo); got != c.want {
				t.Errorf("Route(%q, %q) = %q, want %q", c.host, c.repo, got, c.want)
			}
		})
	}
	if got := NewRouter(nil).Route("github.com", "acme/widgets"); got != "" {
		t.Errorf("no fleets: Route = %q, want empty", got)
	}
}

func TestRouteConflicts(t *testing.T) {
	t.Run("same repo checked out in two fleets", func(t *testing.T) {
		r := NewRouter([]RouteFleet{
			{Key: "a", Repos: []string{"github.com/acme/widgets"}},
			{Key: "b", Repos: []string{"github.com/acme/widgets"}},
			{Key: "c"},
		})
		if got := r.Route("github.com", "acme/widgets"); got != "a" {
			t.Errorf("winner = %q, want a", got)
		}
		wantWarning(t, r, "a", "acme/widgets", "b")
		wantWarning(t, r, "b", "acme/widgets", "a")
		if w := r.Warnings("c"); len(w) != 0 {
			t.Errorf("uninvolved fleet c has warnings: %v", w)
		}
	})
	t.Run("same owner in two fleets", func(t *testing.T) {
		r := NewRouter([]RouteFleet{
			{Key: "a"},
			{Key: "b", Owners: []string{"acme"}},
			{Key: "c", Owners: []string{"ACME"}},
		})
		if got := r.Route("github.com", "acme/x"); got != "b" {
			t.Errorf("winner = %q, want b", got)
		}
		wantWarning(t, r, "b", "acme", "c")
		wantWarning(t, r, "c", "acme", "b")
		if w := r.Warnings("a"); len(w) != 0 {
			t.Errorf("uninvolved fleet a has warnings: %v", w)
		}
	})
	t.Run("not conflicts", func(t *testing.T) {
		r := NewRouter([]RouteFleet{
			{Key: "a", Repos: []string{"github.com/acme/widgets", "github.com/acme/widgets"}},
			{Key: "b", Repos: []string{"ghe.example.com/acme/widgets"}, Owners: []string{"acme"}},
		})
		for _, k := range []string{"a", "b"} {
			if w := r.Warnings(k); len(w) != 0 {
				t.Errorf("fleet %s has warnings %v, want none", k, w)
			}
		}
		if got := r.Route("github.com", "acme/widgets"); got != "a" {
			t.Errorf("Route = %q, want a", got)
		}
		// The checkout on another host belongs to b; the owner claim doesn't pull github.com items from a.
		if got := r.Route("ghe.example.com", "acme/widgets"); got != "b" {
			t.Errorf("Route on ghe = %q, want b", got)
		}
	})
}

func wantWarning(t *testing.T, r *Router, fleet, names, other string) {
	t.Helper()
	for _, w := range r.Warnings(fleet) {
		lw := strings.ToLower(w)
		if strings.Contains(lw, names) && strings.Contains(lw, other) {
			return
		}
	}
	t.Errorf("fleet %s: want a warning naming %q and fleet %q, got %v", fleet, names, other, r.Warnings(fleet))
}

func mkCheckout(t *testing.T, dir, remoteURL string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(dir, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	cfg := "[remote \"origin\"]\n\turl = " + remoteURL + "\n"
	if err := os.WriteFile(filepath.Join(dir, ".git", "config"), []byte(cfg), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestRouterFromFleets(t *testing.T) {
	tmp := t.TempDir()
	rootA := filepath.Join(tmp, "a")
	rootB := filepath.Join(tmp, "b")
	rootC := filepath.Join(tmp, "c") // itself a checkout of acme/gadgets: not a project
	mkCheckout(t, filepath.Join(rootB, "widgets"), "https://github.com/acme/widgets.git")
	mkCheckout(t, filepath.Join(rootB, "group", "tools"), "git@ghe.example.com:corp/tools.git")
	mkCheckout(t, rootC, "https://github.com/acme/gadgets.git")
	if err := os.MkdirAll(rootA, 0o755); err != nil {
		t.Fatal(err)
	}
	r := RouterFromFleets([]config.Fleet{
		{Key: "a", SearchRoots: []string{rootA}, Owners: []string{"acme"}},
		{Key: "b", SearchRoots: []string{rootB}},
		{Key: "c", SearchRoots: []string{rootC}},
	})
	cases := []struct{ host, repo, want string }{
		{"github.com", "acme/widgets", "b"},    // depth 1 checkout beats a's owner claim
		{"ghe.example.com", "corp/tools", "b"}, // depth 2 checkout, any host kept
		{"github.com", "corp/tools", "a"},      // other host is no project here
		{"github.com", "acme/gadgets", "a"},    // a search root that is a checkout is not a project; owner claim wins
		{"github.com", "nobody/thing", "a"},    // default fleet
	}
	for _, c := range cases {
		if got := r.Route(c.host, c.repo); got != c.want {
			t.Errorf("Route(%q, %q) = %q, want %q", c.host, c.repo, got, c.want)
		}
	}
}

func TestRouteWarningWithThreeClaimants(t *testing.T) {
	r := NewRouter([]RouteFleet{
		{Key: "a", Repos: []string{"github.com/acme/widgets"}},
		{Key: "b", Repos: []string{"github.com/acme/widgets"}},
		{Key: "c", Repos: []string{"github.com/acme/widgets"}},
	})
	const want = "github.com/acme/widgets is checked out in fleets a, b and c; a takes its items"
	for _, k := range []string{"a", "b", "c"} {
		ws := r.Warnings(k)
		if len(ws) != 1 || ws[0] != want {
			t.Errorf("fleet %s warnings = %q, want [%q]", k, ws, want)
		}
	}
}
