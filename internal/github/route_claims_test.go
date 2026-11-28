package github

import (
	"reflect"
	"testing"
)

func TestRouterClaimsListWhatRoutesToAFleet(t *testing.T) {
	r := NewRouter([]RouteFleet{
		{Key: "a", Repos: []string{"github.com/acme/widgets", "github.com/Acme/Zed", "ghe.example.com/corp/tools"}, Owners: []string{"Globex"}},
		{Key: "b", Repos: []string{"github.com/acme/widgets", "github.com/bravo/app"}, Owners: []string{"globex", "Initech"}},
	}, "")
	a := r.Claims("a", "github.com")
	if want := []string{"acme/widgets", "acme/zed"}; !reflect.DeepEqual(a.Repos, want) {
		t.Errorf("a repos = %v, want %v: lowercase, this host only, the host stripped", a.Repos, want)
	}
	if want := []string{"globex"}; !reflect.DeepEqual(a.Owners, want) {
		t.Errorf("a owners = %v, want %v", a.Owners, want)
	}
	if !a.Default {
		t.Error("a is the first fleet, so it is the default")
	}
	b := r.Claims("b", "github.com")
	if want := []string{"bravo/app"}; !reflect.DeepEqual(b.Repos, want) {
		t.Errorf("b repos = %v, want %v: acme/widgets is a's, the first claimant's", b.Repos, want)
	}
	if want := []string{"initech"}; !reflect.DeepEqual(b.Owners, want) {
		t.Errorf("b owners = %v, want %v: globex is a's", b.Owners, want)
	}
	if b.Default {
		t.Error("b is not the default fleet")
	}
	if got := r.Claims("a", "ghe.example.com").Repos; !reflect.DeepEqual(got, []string{"corp/tools"}) {
		t.Errorf("a repos on ghe.example.com = %v, want [corp/tools]", got)
	}
}

func TestRouterClaimsOfAFleetWithNoneAreEmptyLists(t *testing.T) {
	c := NewRouter([]RouteFleet{{Key: "a"}, {Key: "b"}}, "").Claims("b", "github.com")
	if c.Repos == nil || c.Owners == nil || len(c.Repos) != 0 || len(c.Owners) != 0 {
		t.Errorf("claims = %+v, want empty non-nil lists so they encode as []", c)
	}
}

func TestRouterClaimsAgreeWithRoute(t *testing.T) {
	r := NewRouter([]RouteFleet{
		{Key: "a", Repos: []string{"github.com/acme/widgets"}},
		{Key: "b", Repos: []string{"github.com/acme/widgets", "github.com/bravo/app"}},
	}, "")
	for _, fleet := range []string{"a", "b"} {
		for _, repo := range r.Claims(fleet, "github.com").Repos {
			if got := r.Route("github.com", repo); got != fleet {
				t.Errorf("%s lists %s but Route sends it to %s", fleet, repo, got)
			}
		}
	}
}
