package ships

import (
	"context"
	"net/http"
	"testing"

	"github.com/jbuffin/admiral/internal/config"
	"github.com/jbuffin/admiral/internal/launcher"
	"github.com/jbuffin/admiral/internal/sessionhost"
)

func TestClaimKeyReadsOneIssueWhateverItsSpelling(t *testing.T) {
	a, okA := ClaimKey("https://github.com/acme/widgets/issues/21")
	for _, spelling := range []string{
		"https://GitHub.com/Acme/Widgets/issues/21",
		"https://github.com/acme/widgets/issues/21/",
		"https://github.com/acme/widgets/issues/21#issuecomment-99",
	} {
		b, ok := ClaimKey(spelling)
		if !okA || !ok || a != b {
			t.Errorf("ClaimKey(%q) = %q, %v; the plain URL = %q, %v; want one key for one issue", spelling, b, ok, a, okA)
		}
	}
	other, _ := ClaimKey("https://github.com/acme/widgets/issues/22")
	if a == other {
		t.Errorf("issues 21 and 22 share the key %q", a)
	}
}

func TestClaimKeyIgnoresURLsThatAreNotIssues(t *testing.T) {
	for _, u := range []string{"", "https://dashboard.example.com/orders/7", "https://github.com/acme/widgets/pull/5", "https://github.com/acme/widgets"} {
		if k, ok := ClaimKey(u); ok {
			t.Errorf("ClaimKey(%q) = %q, true; a URL that is not a GitHub issue must not be claimed", u, k)
		}
	}
}

func TestClaimsOfCountsListedShipsOfEveryStatus(t *testing.T) {
	const u = "https://github.com/acme/widgets/issues/"
	list := []Ship{
		{Name: "a", Status: StatusUnderway, URL: u + "21"},
		{Name: "b", Status: StatusNeedsYou, URL: u + "22"},
		{Name: "c", Status: "sailing_off", URL: "https://GitHub.com/acme/widgets/issues/23/"},
		{Name: "d", Status: "anchored", URL: u + "24"},
		{Name: "e", Status: StatusUnderway, URL: ""},
		{Name: "f", Status: StatusUnderway, URL: "https://dashboard.example.com/orders/7"},
	}
	got := ClaimsOf(list)
	for _, c := range []struct{ url, ship string }{{u + "21", "a"}, {u + "22", "b"}, {u + "23", "c"}, {u + "24", "d"}} {
		k, _ := ClaimKey(c.url)
		if got[k] != c.ship {
			t.Errorf("claims[%s] = %q, want ship %s (claims %v)", c.url, got[k], c.ship, got)
		}
	}
	if len(got) != 4 {
		t.Errorf("claims = %v, want only the four ships that carry an issue URL", got)
	}
	// A ship that is gone releases its issue.
	if k, _ := ClaimKey(u + "21"); ClaimsOf(list[1:])[k] != "" {
		t.Errorf("issue 21 is still claimed after its ship left the list")
	}
}

func TestClaimsHoldAnIssueWhileItsLaunchIsPendingOrInFlight(t *testing.T) {
	home := t.TempDir()
	s := &Service{Host: sessionhost.New("tmux", home+"/tmux.sock"), Launcher: &launcher.Launcher{Home: home, Config: func() *config.Config { return &config.Config{} }}}
	const pending, flying = "https://github.com/acme/widgets/issues/31", "https://github.com/acme/widgets/issues/32"
	s.holdUnlisted(OrderKey{"campaign", pending}, "ship-9", "c1", nil)
	s.listing[OrderKey{"campaign", pending}] = unlisted{session: "ship-9", panel: "c1", at: s.listing[OrderKey{"campaign", pending}].at, url: pending}
	if holder, ok := s.lockIssue(flying, "campaign launch"); !ok {
		t.Fatalf("lockIssue on a free issue refused, held by %q", holder)
	}
	pk, _ := ClaimKey(pending)
	fk, _ := ClaimKey(flying)
	got := s.Claims()
	if got[pk] == "" || got[fk] == "" {
		t.Fatalf("Claims = %v, want the pending launch of %s and the in-flight launch of %s held", got, pending, flying)
	}
	if holder, ok := s.lockIssue("https://GitHub.com/Acme/Widgets/issues/32/", "second launch"); ok || holder == "" {
		t.Errorf("a second launch of the same issue in another spelling got ok=%v holder=%q, want a refusal naming the holder", ok, holder)
	}
	s.unlockIssue(flying)
	if s.Claims()[fk] != "" {
		t.Errorf("the issue is still claimed after its launch finished")
	}
	if _, ok := s.lockIssue(flying, "again"); !ok {
		t.Errorf("the issue could not be locked again once released")
	}
}

func TestLaunchKeepsTheCloneAndSourceRefusalsOfThePostRoute(t *testing.T) {
	s := &Service{
		Host:     sessionhost.New("tmux", t.TempDir()+"/tmux.sock"), // never run: not ready
		Orders:   oneOrder{},
		Launcher: &launcher.Launcher{Config: func() *config.Config { return &config.Config{} }},
	}
	_, lerr := s.Launch(context.Background(), launcher.Request{Fleet: "work", Harness: "claude", Dir: "/tmp", Clone: true, Skill: launcher.Skill{Kind: "none"}})
	if lerr == nil || lerr.Status != http.StatusBadRequest {
		t.Errorf("Launch with dir and clone = %v, want a 400 \"send dir or clone, not both\"", lerr)
	}
	_, lerr = s.Launch(context.Background(), launcher.Request{Fleet: "work", Harness: "claude", Dir: t.TempDir(), Skill: launcher.Skill{Kind: "none"}, Source: "s", OrderID: "o1"})
	if lerr == nil || lerr.Status != http.StatusServiceUnavailable {
		t.Errorf("Launch of an order before the host is ready = %v, want a 503", lerr)
	}
}
