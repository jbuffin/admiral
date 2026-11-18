package follow

import (
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/jbuffin/admiral/internal/github"
)

type searchRow struct {
	Title     string `json:"title"`
	Reference string `json:"reference"`
	URL       string `json:"url"`
	State     string `json:"state"`
	Parent    bool   `json:"parent"`
	Progress  struct {
		Closed int `json:"closed"`
		Total  int `json:"total"`
	} `json:"progress"`
	Status string `json:"status"`
}

// scope sets what the work fleet claims on github.com.
func (r *rig) scope(repos, owners []string) {
	r.svc.FleetScope = func(fleet string) (string, github.FleetClaims, bool) {
		return "github.com", github.FleetClaims{Repos: repos, Owners: owners}, true
	}
}

func (r *rig) search(fleet, q string) (int, []searchRow, string) {
	r.t.Helper()
	code, body := r.do("GET", "/api/fleets/"+fleet+"/campaigns/search?q="+url.QueryEscape(q), nil)
	var out struct {
		Results []searchRow `json:"results"`
		Error   string      `json:"error"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		r.t.Fatalf("search JSON: %v\n%s", err, body)
	}
	return code, out.Results, out.Error
}

func (r *rig) mustSearch(fleet, q string) []searchRow {
	r.t.Helper()
	code, rows, msg := r.search(fleet, q)
	if code != http.StatusOK {
		r.t.Fatalf("search %q = %d %s, want 200", q, code, msg)
	}
	return rows
}

func refs(rows []searchRow) []string {
	out := []string{}
	for _, row := range rows {
		out = append(out, row.Reference)
	}
	return out
}

func sameList(a, b []string) bool { return strings.Join(a, " ") == strings.Join(b, " ") }

func TestSearchPutsParentsFirstAndShowsAParentBothQueriesFoundOnce(t *testing.T) {
	r := newRig(t, fixtureTree())
	r.scope([]string{"acme/widgets"}, nil)
	r.gh.respondSearch(
		[]hit{{repo: "acme/widgets", num: 5}, {repo: "acme/widgets", num: 7, total: 3, closedKids: 1}, {repo: "acme/widgets", num: 9, closed: true}},
		[]hit{{repo: "acme/widgets", num: 7, total: 3, closedKids: 1}},
	)
	rows := r.mustSearch("work", "widget")
	if want := []string{"acme/widgets#7", "acme/widgets#5", "acme/widgets#9"}; !sameList(refs(rows), want) {
		t.Fatalf("rows = %v, want %v: the parent first and once", refs(rows), want)
	}
	if p := rows[0]; !p.Parent || p.Progress.Closed != 1 || p.Progress.Total != 3 || p.State != "open" || p.Title != "Issue 7" || p.URL != "https://github.com/acme/widgets/issues/7" {
		t.Errorf("parent row = %+v, want a parent with 1 of 3 closed, open, with its title and URL", p)
	}
	if rows[1].Parent || rows[2].State != "closed" {
		t.Errorf("other rows = %+v %+v, want 5 not a parent and 9 closed (closed issues stay in a text search)", rows[1], rows[2])
	}
	var parents, plain int
	for _, q := range r.gh.searchQueries() {
		if !strings.HasPrefix(q, "is:issue ") || strings.Contains(q, "is:open") {
			t.Errorf("query %q, want it to start with is:issue and leave closed issues in", q)
		}
		if strings.Contains(q, "has:sub-issue") {
			parents++
		} else {
			plain++
		}
	}
	if parents != 1 || plain != 1 {
		t.Errorf("queries = %q, want one plain and one has:sub-issue copy", r.gh.searchQueries())
	}
}

func TestSearchWithNoTextListsOnlyTheFleetsOpenParents(t *testing.T) {
	r := newRig(t, fixtureTree())
	r.scope([]string{"acme/other", "acme/widgets"}, []string{"octo"})
	r.gh.respondSearch([]hit{{repo: "acme/widgets", num: 5}}, []hit{{repo: "acme/widgets", num: 7, total: 2}})
	rows := r.mustSearch("work", "")
	if !sameList(refs(rows), []string{"acme/widgets#7"}) {
		t.Errorf("rows = %v, want only the parent", refs(rows))
	}
	qs := r.gh.searchQueries()
	if len(qs) == 0 {
		t.Fatal("no search reached gh")
	}
	for _, q := range qs {
		if want := "is:issue is:open has:sub-issue (repo:acme/other OR repo:acme/widgets OR user:octo)"; strings.TrimSpace(q) != want {
			t.Errorf("query %q, want %q", q, want)
		}
	}
}

func TestSearchOfAFleetWithNoReposOrOwnersAnswersEmptyWithoutCallingGitHub(t *testing.T) {
	r := newRig(t, fixtureTree())
	r.scope(nil, nil)
	if rows := r.mustSearch("work", "widget"); len(rows) != 0 {
		t.Errorf("rows = %v, want none", refs(rows))
	}
	if n := r.gh.ghCalls(); n != 0 {
		t.Errorf("gh ran %d times, want none for an empty scope", n)
	}
}

func TestSearchOfABareNumberListsEveryMatchInTheFleetsReposAndDropsOtherNumbers(t *testing.T) {
	r := newRig(t, fixtureTree())
	r.scope([]string{"acme/other", "acme/widgets"}, nil)
	r.gh.respondSearch(
		[]hit{{repo: "acme/widgets", num: 12}, {repo: "acme/other", num: 12, total: 2}, {repo: "acme/widgets", num: 112}},
		[]hit{{repo: "acme/other", num: 12, total: 2}},
	)
	rows := r.mustSearch("work", "#12")
	if want := []string{"acme/other#12", "acme/widgets#12"}; !sameList(refs(rows), want) {
		t.Errorf("rows = %v, want %v: both repos' #12, the parent first, #112 dropped", refs(rows), want)
	}
}

func TestTheDefaultFleetSearchesOnlyItsOwnReposAndOwnersAndDropsAnOwnersRepoAnotherFleetHolds(t *testing.T) {
	r := newRig(t, fixtureTree())
	r.svc.FleetScope = func(fleet string) (string, github.FleetClaims, bool) {
		return "github.com", github.FleetClaims{Repos: []string{"acme/widgets"}, Owners: []string{"octo"}, Default: true}, true
	}
	r.svc.Clients = func(string, string) (*github.Client, bool) { return r.gh.client("github.com"), true }
	r.svc.Route = func(host, nwo string) string {
		if nwo == "octo/theirs" {
			return "work"
		}
		return "home"
	}
	r.gh.respondSearch([]hit{{repo: "octo/mine", num: 1}, {repo: "octo/theirs", num: 2}}, nil)
	rows := r.mustSearch("home", "thing")
	if !sameList(refs(rows), []string{"octo/mine#1"}) {
		t.Errorf("rows = %v, want only octo/mine#1: octo/theirs routes to another fleet", refs(rows))
	}
	for _, q := range r.gh.searchQueries() {
		if !strings.Contains(q, "(repo:acme/widgets OR user:octo) thing") {
			t.Errorf("query %q, want the scope to be only the default fleet's own repos and owners", q)
		}
	}
}

func TestSearchRowsMarkFollowedUnfollowedAndEndedCampaigns(t *testing.T) {
	r := newRig(t, fixtureTree())
	r.scope([]string{"acme/widgets"}, nil)
	open := func(n int) iss { return iss{num: n, kids: []iss{{num: n + 100}}} }
	shut := func(n int) iss { return iss{num: n, closed: true, kids: []iss{{num: n + 100, closed: true}}} }
	for _, tree := range []iss{open(30), open(31), shut(32)} {
		r.gh.set(tree)
	}
	r.follow("acme/widgets#30")
	unfollowed := r.follow("acme/widgets#31")
	if code, body := r.do("POST", "/api/campaigns/"+unfollowed.ID+"/unfollow", nil); code != http.StatusOK {
		t.Fatalf("unfollow = %d %s", code, body)
	}
	r.follow("acme/widgets#32") // its tree is closed, so following ends it at once
	// records written before an end reason was kept
	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	tree := func(closed bool) *github.TreeNode {
		return &github.TreeNode{Closed: closed, Children: []*github.TreeNode{{Closed: closed}}}
	}
	for url, closed := range map[string]bool{"https://github.com/acme/widgets/issues/40": false, "https://github.com/acme/widgets/issues/41": true} {
		if _, _, err := r.svc.Store.Follow(Followed{URL: url, Fleet: "work", FollowedAt: now, EndedAt: now, Tree: tree(closed)}); err != nil {
			t.Fatal(err)
		}
	}
	var plain []hit
	for _, n := range []int{30, 31, 32, 40, 41, 50} {
		plain = append(plain, hit{repo: "acme/widgets", num: n, total: 1})
	}
	r.gh.respondSearch(plain, nil)
	got := map[string]string{}
	for _, row := range r.mustSearch("work", "issue") {
		got[row.Reference] = row.Status
	}
	want := map[string]string{
		"acme/widgets#30": "followed", "acme/widgets#31": "unfollowed", "acme/widgets#32": "ended",
		"acme/widgets#40": "unfollowed", "acme/widgets#41": "ended", "acme/widgets#50": "",
	}
	for ref, status := range want {
		if got[ref] != status {
			t.Errorf("%s status = %q, want %q (all: %v)", ref, got[ref], status, got)
		}
	}
}

func TestAReferenceWithNoSearchHitComesBackAsItsOwnResolvedRow(t *testing.T) {
	r := newRig(t, fixtureTree())
	r.scope([]string{"acme/widgets"}, nil)
	r.gh.respondIssue(hit{repo: "acme/widgets", num: 77, total: 4, closedKids: 1})
	for _, in := range []string{"acme/widgets#77", "https://github.com/acme/widgets/issues/77"} {
		rows := r.mustSearch("work", in)
		if len(rows) != 1 {
			t.Fatalf("rows for %q = %v, want its own row", in, refs(rows))
		}
		if row := rows[0]; row.Reference != "acme/widgets#77" || row.Title != "Issue 77" || !row.Parent || row.Progress.Total != 4 || row.Progress.Closed != 1 || row.State != "open" {
			t.Errorf("row for %q = %+v, want the resolved parent with 1 of 4 closed", in, row)
		}
	}
}

func TestAReferenceThatIsNotAnIssueOfThisFleetGetsNoRow(t *testing.T) {
	r := newRig(t, fixtureTree())
	r.scope([]string{"acme/widgets"}, nil)
	r.gh.respondPullRequest("acme/widgets", 78)
	r.gh.failFile("acme_widgets_79", "GraphQL: Could not resolve to an issue or pull request")
	r.gh.respondIssue(hit{repo: "acme/elsewhere", num: 80})
	r.svc.Route = func(host, nwo string) string {
		if nwo == "acme/elsewhere" {
			return "home"
		}
		return "work"
	}
	for _, in := range []string{"acme/widgets#78", "acme/widgets#79", "acme/elsewhere#80"} {
		if rows := r.mustSearch("work", in); len(rows) != 0 {
			t.Errorf("rows for %q = %v, want none: a pull request, a missing issue and another fleet's issue get no row", in, refs(rows))
		}
	}
}

func TestSearchAnswersTheReasonWhenGitHubFails(t *testing.T) {
	r := newRig(t, fixtureTree())
	r.scope([]string{"acme/widgets"}, nil)
	r.gh.failFile("search", "gh: HTTP 502 bad gateway")
	r.gh.failFile("search_parents", "gh: HTTP 502 bad gateway")
	code, _, msg := r.search("work", "widget")
	if code != http.StatusUnprocessableEntity || !strings.Contains(msg, "502") {
		t.Errorf("search = %d %q, want a 422 whose error carries GitHub's reason", code, msg)
	}
}

func TestFollowWithAFleetThatDoesNotOwnTheReferenceIsRefusedWithItsReason(t *testing.T) {
	r := newRig(t, fixtureTree())
	code, body := r.do("POST", "/api/campaigns", map[string]string{"reference": "acme/widgets#20", "fleet": "home"})
	var out struct{ Error string }
	_ = json.Unmarshal(body, &out)
	if code != http.StatusUnprocessableEntity || !strings.Contains(out.Error, "work") {
		t.Fatalf("follow with fleet home = %d %.120s, want a 422 naming the owning fleet work", code, body)
	}
	if _, body := r.do("GET", "/api/campaigns", nil); strings.Contains(string(body), "acme/widgets") {
		t.Errorf("campaigns = %s, want nothing followed", body)
	}
	if code, body := r.do("POST", "/api/campaigns", map[string]string{"reference": "acme/widgets#20", "fleet": "work"}); code != http.StatusCreated {
		t.Errorf("follow with the owning fleet = %d %s, want 201", code, body)
	}
}
