package ships

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

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

func TestPostShipsRefusesTheCampaignSource(t *testing.T) {
	s := &Service{}
	rec := launchPost(t, s, `{"fleet":"work","harness":"claude","source":"campaign","order_id":"https://github.com/acme/widgets/issues/21","dir":"/x"}`)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("POST /api/ships from source campaign = %d %s, want 400: a campaign child launches through its campaign", rec.Code, rec.Body)
	}
}

const sliceA, sliceB = "https://github.com/acme/widgets/issues/21", "https://github.com/acme/widgets/issues/29"

func TestClaimsOfHoldsEveryIssueOfASliceShip(t *testing.T) {
	list := []Ship{{Name: "slice", Status: StatusUnderway, URL: sliceA, Issues: []string{sliceA, "https://GitHub.com/acme/widgets/issues/29/"}}}
	got := ClaimsOf(list)
	for _, u := range []string{sliceA, sliceB} {
		if k, _ := ClaimKey(u); got[k] != "slice" {
			t.Errorf("claims[%s] = %q, want the slice ship (claims %v)", u, got[k], got)
		}
	}
}

func TestClaimsHoldEveryIssueOfAPendingSliceLaunch(t *testing.T) {
	home := t.TempDir()
	s := &Service{Host: sessionhost.New("tmux", home+"/tmux.sock"), Launcher: &launcher.Launcher{Home: home, Config: func() *config.Config { return &config.Config{} }}}
	key := OrderKey{"campaign", "c1/" + sliceA}
	s.holdUnlisted(key, "ship-9", "c1", nil)
	s.listing[key] = unlisted{session: "ship-9", panel: "c1", at: s.listing[key].at, url: sliceA, issues: []string{sliceA, sliceB}}
	if p := s.Pending(); len(p) != 1 || len(p[0].Issues) != 2 {
		t.Errorf("Pending = %+v, want one ship carrying both issues", p)
	}
	got := s.Claims()
	for _, u := range []string{sliceA, sliceB} {
		if k, _ := ClaimKey(u); got[k] == "" {
			t.Errorf("Claims = %v, want %s held by the pending slice launch", got, u)
		}
	}
}

func TestLockIssuesTakesEveryIssueOfASliceOrNone(t *testing.T) {
	s := &Service{}
	const free = "https://github.com/acme/widgets/issues/30"
	if holder, ok := s.lockIssues([]string{sliceA, sliceB}, "slice launch"); !ok {
		t.Fatalf("lockIssues on free issues refused, held by %q", holder)
	}
	for _, u := range []string{sliceA, sliceB} {
		if holder, ok := s.lockIssue(u, "single launch"); ok || holder != "slice launch" {
			t.Errorf("lockIssue(%s) = %q, %v, want a refusal naming the slice launch", u, holder, ok)
		}
	}
	if holder, ok := s.lockIssues([]string{free, sliceB}, "overlap"); ok || holder != "slice launch" {
		t.Errorf("an overlapping slice got %q, %v, want a refusal naming the holder", holder, ok)
	}
	if _, ok := s.lockIssue(free, "single launch"); !ok {
		t.Errorf("the refused slice left %s locked", free)
	}
	s.unlockIssues([]string{sliceA, sliceB})
	for _, u := range []string{sliceA, sliceB} {
		if holder, ok := s.lockIssue(u, "again"); !ok {
			t.Errorf("lockIssue(%s) refused after the slice launch finished, held by %q", u, holder)
		}
	}
}

func TestFromSessionReadsASliceShipsIssues(t *testing.T) {
	opts := map[string]string{"name": "Slice", "url": sliceA, "issues": sliceA + "," + sliceB}
	sh := FromSession(sessionhost.Session{Name: "slice", Options: opts}, time.Now())
	if len(sh.Issues) != 2 || sh.Issues[0] != sliceA || sh.Issues[1] != sliceB || sh.URL != sliceA {
		t.Errorf("ship url=%q issues=%v, want url the first issue and both issues in order", sh.URL, sh.Issues)
	}
}

func TestAShipSerialisesItsIssuesForClients(t *testing.T) {
	b, err := json.Marshal(Ship{URL: sliceA, Issues: []string{sliceA, sliceB}})
	if err != nil {
		t.Fatal(err)
	}
	var got struct {
		URL    string   `json:"url"`
		Issues []string `json:"issues"`
	}
	if err := json.Unmarshal(b, &got); err != nil || got.URL != sliceA || len(got.Issues) != 2 || got.Issues[1] != sliceB {
		t.Errorf("ship JSON = %s, want url %s and issues [%s %s]", b, sliceA, sliceA, sliceB)
	}
}

func TestLaunchedOutcomeNamesEveryChildOfASlice(t *testing.T) {
	f := mooredService(t, nil)
	req := launcher.Request{Harness: "claude", Skill: launcher.Skill{Kind: "commodore"}, URL: sliceA, Issues: []string{sliceA, sliceB}}
	f.s.launched(req, &launcher.Launched{Session: "slice", Options: map[string]string{"url": sliceA}}, "")
	evs := f.events(t, "slice", "launched")
	if len(evs) != 1 {
		t.Fatalf("launched events = %v, want one", evs)
	}
	urls, _ := evs[0]["order_urls"].([]any)
	if len(urls) != 2 || urls[0] != sliceA || urls[1] != sliceB {
		t.Errorf("order_urls = %v, want both children in order", evs[0]["order_urls"])
	}
}
