package github

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// treeGH is a gh that answers each tree or search call from a file named by its variables, and records argv.
type treeGH struct {
	dir string
	env []string
	gh  string
}

const treeGHScript = `#!/bin/sh
d="$FAKEGH_TREE"
mkdir -p "$d/calls"
n=$(ls "$d/calls" | grep -c argv)
printf '%s\0' "$@" > "$d/calls/$n.argv"
printf '%s\n' "$GH_TOKEN" > "$d/calls/$n.token"
owner= repo= number= after= q=
for a in "$@"; do
	case "$a" in
	owner=*) owner=${a#owner=} ;;
	repo=*) repo=${a#repo=} ;;
	number=*) number=${a#number=} ;;
	after=*) after=${a#after=} ;;
	q=*) q=1 ;;
	esac
done
if [ -n "$q" ]; then key=search; else key=${owner}_${repo}_${number}; fi
[ -n "$after" ] && key=${key}_${after}
if [ -e "$d/resp/$key.fail" ]; then cat "$d/resp/$key.fail" >&2; exit 1; fi
if [ -e "$d/resp/$key.json" ]; then cat "$d/resp/$key.json"; exit 0; fi
echo "no canned answer for $key" >&2
exit 1
`

func newTreeGH(t *testing.T, resp map[string]string) *treeGH {
	t.Helper()
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "resp"), 0o755); err != nil {
		t.Fatal(err)
	}
	gh := filepath.Join(dir, "gh")
	if err := os.WriteFile(gh, []byte(treeGHScript), 0o755); err != nil {
		t.Fatal(err)
	}
	for name, body := range resp {
		if err := os.WriteFile(filepath.Join(dir, "resp", name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return &treeGH{dir: dir, gh: gh, env: append(os.Environ(), "FAKEGH_TREE="+dir)}
}

func (g *treeGH) client(host string) *Client {
	c := NewClient(host, "", nil, g.env)
	c.Gh = g.gh
	return c
}

func (g *treeGH) argvs(t *testing.T) [][]string {
	t.Helper()
	var out [][]string
	for i := 0; ; i++ {
		raw, err := os.ReadFile(filepath.Join(g.dir, "calls", fmt.Sprintf("%d.argv", i)))
		if err != nil {
			return out
		}
		out = append(out, strings.Split(strings.TrimSuffix(string(raw), "\x00"), "\x00"))
	}
}

// recorded copies the recorded campaign tree into the fake's answers.
func recorded(t *testing.T) *treeGH {
	t.Helper()
	files, err := filepath.Glob("../campaigns/testdata/tree/*.json")
	if err != nil || len(files) == 0 {
		t.Fatalf("recorded tree not found: %v", err)
	}
	resp := map[string]string{}
	for _, f := range files {
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		resp[filepath.Base(f)] = string(b)
	}
	return newTreeGH(t, resp)
}

func outline(n *TreeNode, depth int, sb *strings.Builder) {
	state := "open"
	if n.Closed {
		state = "closed"
	}
	parts := []string{fmt.Sprintf("%s#%d", n.Repo, n.Number), state}
	if len(n.Labels) > 0 {
		parts = append(parts, "labels="+strings.Join(n.Labels, ","))
	}
	if len(n.Assignees) > 0 {
		parts = append(parts, "assignees="+strings.Join(n.Assignees, ","))
	}
	var bs []string
	for _, b := range n.BlockedBy {
		s := "open"
		if b.Closed {
			s = "closed"
		}
		bs = append(bs, b.URL+"("+s+")")
	}
	if len(bs) > 0 {
		parts = append(parts, "blockers="+strings.Join(bs, ","))
	}
	sb.WriteString(strings.Repeat("  ", depth) + strings.Join(parts, " ") + "\n")
	for _, c := range n.Children {
		outline(c, depth+1, sb)
	}
}

func find(n *TreeNode, number int) *TreeNode {
	if n.Number == number {
		return n
	}
	for _, c := range n.Children {
		if f := find(c, number); f != nil {
			return f
		}
	}
	return nil
}

func TestTreeReadsTheCampaign(t *testing.T) {
	g := recorded(t)
	tree, err := g.client("github.com").Tree(context.Background(), "fake-token-work-2", "acme", "campaign", 1)
	if err != nil {
		t.Fatal(err)
	}
	const five = "https://github.com/acme/campaign/issues/5"
	want := `acme/campaign#1 open labels=wayfinder:map
  acme/campaign#2 open labels=ready-for-agent
  acme/campaign#3 closed labels=wayfinder:research
  acme/campaign#4 open labels=wayfinder:grilling blockers=https://github.com/acme/campaign/issues/3(closed)
  acme/campaign#5 open labels=wayfinder:research blockers=https://github.com/acme/other/issues/90(open)
  acme/campaign#6 open labels=ready-for-agent assignees=octocat
  acme/campaign#7 open labels=wayfinder:map
    acme/campaign#8 open labels=wayfinder:task
    acme/campaign#9 open labels=wayfinder:task,needs-human-action
  acme/other#40 open labels=ready-for-human
  acme/campaign#10 open labels=wayfinder:map blockers=` + five + `(open)
    acme/campaign#11 open labels=ready-for-agent
    acme/campaign#52 open labels=ready-for-agent
  acme/campaign#12 closed labels=wayfinder:map
    acme/campaign#13 open labels=needs-triage
  acme/campaign#14 open
  acme/campaign#50 open labels=ready-for-agent blockers=` + five + `(open)
  acme/campaign#51 open labels=ready-for-agent
`
	var got strings.Builder
	outline(tree, 0, &got)
	if got.String() != want {
		t.Errorf("tree:\n%s\nwant:\n%s", got.String(), want)
	}
	if n := find(tree, 5); n != nil && (len(n.BlockedBy) != 1 || n.BlockedBy[0].Title != "Vendor quota decision") {
		t.Errorf("blocker outside the tree keeps its title: %+v", n.BlockedBy)
	}
	if n := find(tree, 50); n != nil && !strings.Contains(n.Body, "Part of #1") {
		t.Errorf("a Part of child keeps its body: %q", n.Body)
	}
	if n := find(tree, 1); n != nil && n.URL != "https://github.com/acme/campaign/issues/1" {
		t.Errorf("root URL = %q", n.URL)
	}
}

func TestTreeRequest(t *testing.T) {
	g := recorded(t)
	if _, err := g.client("acme.ghe.com").Tree(context.Background(), "fake-token-work-2", "acme", "campaign", 1); err != nil {
		t.Fatal(err)
	}
	argvs := g.argvs(t)
	if len(argvs) == 0 {
		t.Fatal("gh was not called")
	}
	first := argvs[0]
	if got := strings.Join(first[:4], " "); got != "api graphql --hostname acme.ghe.com" {
		t.Errorf("argv starts %q", got)
	}
	joined := strings.Join(first, "\x00")
	for _, want := range []string{"-f\x00owner=acme", "-f\x00repo=campaign", "-F\x00number=1"} {
		if !strings.Contains(joined, want) {
			t.Errorf("argv lacks %q: %q", strings.ReplaceAll(want, "\x00", " "), first)
		}
	}
	for i, argv := range argvs {
		for _, a := range argv {
			if strings.Contains(a, "fake-token-work-2") {
				t.Errorf("call %d carries the token in argv: %q", i, argv)
			}
			if q, ok := strings.CutPrefix(a, "query="); ok {
				if strings.Contains(strings.ToLower(q), "mutation") {
					t.Errorf("call %d sends a mutation: %s", i, q)
				}
				if strings.Contains(q, "acme") || strings.Contains(q, "campaign") {
					t.Errorf("call %d splices the repo into the query text: %s", i, q)
				}
			}
		}
		tok, _ := os.ReadFile(filepath.Join(g.dir, "calls", fmt.Sprintf("%d.token", i)))
		if strings.TrimSpace(string(tok)) != "fake-token-work-2" {
			t.Errorf("call %d token env = %q", i, tok)
		}
	}
}

func TestTreeFallbackSearchPagesAndFilters(t *testing.T) {
	g := recorded(t)
	if _, err := g.client("github.com").Tree(context.Background(), "fake-token-work-2", "acme", "campaign", 1); err != nil {
		t.Fatal(err)
	}
	var searches [][]string
	for _, argv := range g.argvs(t) {
		for _, a := range argv {
			if strings.HasPrefix(a, "q=") && strings.Contains(a, "repo:acme/campaign") && strings.Contains(a, `"Part of #1"`) {
				searches = append(searches, argv)
			}
		}
	}
	if len(searches) != 2 {
		t.Fatalf("want the root's Part of search and its second page, got %d searches", len(searches))
	}
	var q string
	for _, a := range searches[0] {
		if v, ok := strings.CutPrefix(a, "q="); ok {
			q = v
		}
	}
	if !strings.Contains(q, "repo:acme/campaign") || !strings.Contains(q, "Part of #1") {
		t.Errorf("search text %q names neither the parent's repo nor the Part of line", q)
	}
	if !strings.Contains(strings.Join(searches[1], "\x00"), "after=S1") {
		t.Errorf("second page does not pass the cursor: %q", searches[1])
	}
}

func TestTreeReadsEverySubIssuePage(t *testing.T) {
	leaf := func(n int) string {
		return fmt.Sprintf(`{"__typename":"Issue","number":%d,"title":"Child %d","url":"https://github.com/acme/campaign/issues/%d","state":"OPEN","body":"",`+
			`"repository":{"nameWithOwner":"acme/campaign"},"labels":{"nodes":[],"pageInfo":{"hasNextPage":false,"endCursor":null}},`+
			`"assignees":{"nodes":[]},"blockedBy":{"nodes":[],"pageInfo":{"hasNextPage":false,"endCursor":null}},`+
			`"subIssues":{"totalCount":0,"nodes":[],"pageInfo":{"hasNextPage":false,"endCursor":null}}}`, n, n, n)
	}
	page := func(next string, kids ...int) string {
		var nodes []string
		for _, k := range kids {
			nodes = append(nodes, leaf(k))
		}
		pi := `{"hasNextPage":false,"endCursor":null}`
		if next != "" {
			pi = `{"hasNextPage":true,"endCursor":"` + next + `"}`
		}
		return `{"data":{"repository":{"issueOrPullRequest":{"__typename":"Issue","number":1,"title":"Root","url":"https://github.com/acme/campaign/issues/1",` +
			`"state":"OPEN","body":"","repository":{"nameWithOwner":"acme/campaign"},"labels":{"nodes":[],"pageInfo":{"hasNextPage":false,"endCursor":null}},` +
			`"assignees":{"nodes":[]},"blockedBy":{"nodes":[],"pageInfo":{"hasNextPage":false,"endCursor":null}},` +
			`"subIssues":{"totalCount":3,"nodes":[` + strings.Join(nodes, ",") + `],"pageInfo":` + pi + `}}}}}`
	}
	g := newTreeGH(t, map[string]string{
		"acme_campaign_1.json":    page("C1", 2, 3),
		"acme_campaign_1_C1.json": page("", 4),
		"search.json":             `{"data":{"search":{"nodes":[],"pageInfo":{"hasNextPage":false,"endCursor":null}}}}`,
	})
	tree, err := g.client("github.com").Tree(context.Background(), "fake-token-work-2", "acme", "campaign", 1)
	if err != nil {
		t.Fatal(err)
	}
	var got []int
	for _, c := range tree.Children {
		got = append(got, c.Number)
	}
	if b, _ := json.Marshal(got); string(b) != "[2,3,4]" {
		t.Errorf("children = %s, want [2,3,4] in sub-issue order", b)
	}
}

func TestTreeErrors(t *testing.T) {
	notFound := `{"data":{"repository":null},"errors":[{"type":"NOT_FOUND","message":"Could not resolve to a Repository with the name 'acme/campaign'."}]}`
	pr := `{"data":{"repository":{"issueOrPullRequest":{"__typename":"PullRequest","number":1,"title":"A change","url":"https://github.com/acme/campaign/pull/1"}}}}`
	cases := []struct {
		name  string
		files map[string]string
		want  []string // every one must appear, case-insensitively
	}{
		{"not found", map[string]string{"acme_campaign_1.json": notFound}, []string{"not found"}},
		{"not found by gh's exit", map[string]string{"acme_campaign_1.fail": "gh: Could not resolve to a Repository with the name 'acme/campaign'. (repository)"}, []string{"not found"}},
		{"no access", map[string]string{"acme_campaign_1.fail": "gh: Resource protected by organization SAML enforcement. (HTTP 403)"}, []string{"access"}},
		{"a pull request", map[string]string{"acme_campaign_1.json": pr}, []string{"pull request"}},
		{"host unreachable", map[string]string{"acme_campaign_1.fail": "error connecting to api.github.com"}, []string{"couldn't reach github.com"}},
		{"token redacted", map[string]string{"acme_campaign_1.fail": "gh: bad credentials fake-token-work-2 (HTTP 401)"}, []string{"***"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			g := newTreeGH(t, tc.files)
			tree, err := g.client("github.com").Tree(context.Background(), "fake-token-work-2", "acme", "campaign", 1)
			if err == nil {
				t.Fatalf("want an error, got tree %+v", tree)
			}
			msg := strings.ToLower(err.Error())
			for _, w := range tc.want {
				if !strings.Contains(msg, w) {
					t.Errorf("error %q lacks %q", err, w)
				}
			}
			if strings.Contains(err.Error(), "fake-token-work-2") {
				t.Errorf("error shows the token: %q", err)
			}
		})
	}
}

func issueJSON(repo string, n int, state, body string, extra string) string {
	return fmt.Sprintf(`{"__typename":"Issue","number":%d,"title":"Issue %d","url":"https://github.com/%s/issues/%d","state":"%s","body":%q,"authorAssociation":"MEMBER",`+
		`"repository":{"nameWithOwner":"%s"},"labels":{"nodes":[],"pageInfo":{"hasNextPage":false,"endCursor":null}},"assignees":{"nodes":[]},`+
		`%s"subIssues":{"totalCount":0,"nodes":[],"pageInfo":{"hasNextPage":false,"endCursor":null}}}`, n, n, repo, n, state, body, repo, extra)
}

const noBlockers = `"blockedBy":{"nodes":[],"pageInfo":{"hasNextPage":false,"endCursor":null}},`

func issueAnswer(inner string) string {
	return `{"data":{"repository":{"issueOrPullRequest":` + inner + `}}}`
}

func searchAnswer(nodes ...string) string {
	return `{"data":{"search":{"nodes":[` + strings.Join(nodes, ",") + `],"pageInfo":{"hasNextPage":false,"endCursor":null}}}}`
}

func TestTreeLooksUpABodyBlockerOutsideTheTree(t *testing.T) {
	root := issueJSON("acme/campaign", 1, "OPEN", "", noBlockers)
	g := newTreeGH(t, map[string]string{
		"acme_campaign_1.json": issueAnswer(root),
		"search.json":          searchAnswer(issueJSON("acme/campaign", 60, "OPEN", "Part of #1\n\nBlocked by: acme/other#90, #1", noBlockers)),
		"acme_other_90.json":   issueAnswer(issueJSON("acme/other", 90, "OPEN", "", noBlockers)),
	})
	tree, err := g.client("github.com").Tree(context.Background(), "fake-token-work-2", "acme", "campaign", 1)
	if err != nil {
		t.Fatal(err)
	}
	n := find(tree, 60)
	if n == nil || len(n.BlockedBy) != 2 {
		t.Fatalf("child 60 = %+v, want two blockers", n)
	}
	if b := n.BlockedBy[0]; b.URL != "https://github.com/acme/other/issues/90" || b.Closed || b.Title != "Issue 90" {
		t.Errorf("outside blocker = %+v, want the open issue acme/other#90 read from GitHub", b)
	}
	if b := n.BlockedBy[1]; b.URL != "https://github.com/acme/campaign/issues/1" {
		t.Errorf("in-tree blocker = %+v", b)
	}
}

func TestTreeReadsEveryPageOfBlockers(t *testing.T) {
	blocker := func(n int) string {
		return fmt.Sprintf(`{"url":"https://github.com/acme/other/issues/%d","title":"B%d","state":"OPEN"}`, n, n)
	}
	child := issueJSON("acme/campaign", 2, "OPEN", "", `"blockedBy":{"nodes":[`+blocker(91)+`],"pageInfo":{"hasNextPage":true,"endCursor":"B1"}},`)
	root := strings.Replace(issueJSON("acme/campaign", 1, "OPEN", "", noBlockers), `"subIssues":{"totalCount":0,"nodes":[]`, `"subIssues":{"totalCount":1,"nodes":[`+child+`]`, 1)
	g := newTreeGH(t, map[string]string{
		"acme_campaign_1.json":    issueAnswer(root),
		"acme_campaign_2_B1.json": issueAnswer(`{"blockedBy":{"nodes":[` + blocker(92) + `],"pageInfo":{"hasNextPage":false,"endCursor":null}}}`),
		"search.json":             searchAnswer(),
	})
	tree, err := g.client("github.com").Tree(context.Background(), "fake-token-work-2", "acme", "campaign", 1)
	if err != nil {
		t.Fatal(err)
	}
	if n := find(tree, 2); n == nil || len(n.BlockedBy) != 2 || n.BlockedBy[1].Title != "B92" {
		t.Errorf("child 2 = %+v, want both pages of blockers", n)
	}
}

func TestTreeKeepsAnUnreadableBodyBlockerOpen(t *testing.T) {
	g := newTreeGH(t, map[string]string{
		"acme_campaign_1.json": issueAnswer(issueJSON("acme/campaign", 1, "OPEN", "", noBlockers)),
		"search.json":          searchAnswer(issueJSON("acme/campaign", 60, "OPEN", "Part of #1\nBlocked by: acme/other#90", noBlockers)),
		"acme_other_90.fail":   "gh: Could not resolve to a Repository with the name 'acme/other'. (repository)",
	})
	tree, err := g.client("github.com").Tree(context.Background(), "fake-token-work-2", "acme", "campaign", 1)
	if err != nil {
		t.Fatal(err)
	}
	n := find(tree, 60)
	if n == nil || len(n.BlockedBy) != 1 || n.BlockedBy[0].Closed || n.BlockedBy[0].URL != "https://github.com/acme/other/issues/90" {
		t.Errorf("child 60 = %+v, want an open blocker naming acme/other#90 when it can't be read", n)
	}
}

const noSubs = `"subIssues":{"totalCount":0,"nodes":[]`

// withSubs gives a node JSON string sub-issues: total as GitHub counts them, kids as it returned them.
func withSubs(node string, total int, kids ...string) string {
	return strings.Replace(node, noSubs, fmt.Sprintf(`"subIssues":{"totalCount":%d,"nodes":[%s]`, total, strings.Join(kids, ",")), 1)
}

func withAssociation(node, assoc string) string {
	return strings.Replace(node, `"authorAssociation":"MEMBER"`, `"authorAssociation":"`+assoc+`"`, 1)
}

func (g *treeGH) calls(t *testing.T, contains string) int {
	t.Helper()
	n := 0
	for _, argv := range g.argvs(t) {
		if strings.Contains(strings.Join(argv, "\x00"), contains) {
			n++
		}
	}
	return n
}

func treeOf(t *testing.T, g *treeGH) *TreeNode {
	t.Helper()
	tree, err := g.client("github.com").Tree(context.Background(), "fake-token-work-2", "acme", "campaign", 1)
	if err != nil {
		t.Fatal(err)
	}
	return tree
}

func TestTreeCapsBodyBlockersAndFailsClosed(t *testing.T) {
	var refs []string
	resp := map[string]string{}
	for i := 100; i < 140; i++ {
		refs = append(refs, fmt.Sprintf("acme/other#%d", i))
		resp[fmt.Sprintf("acme_other_%d.json", i)] = issueAnswer(issueJSON("acme/other", i, "CLOSED", "", noBlockers))
	}
	resp["acme_campaign_1.json"] = issueAnswer(issueJSON("acme/campaign", 1, "OPEN", "", noBlockers))
	resp["search.json"] = searchAnswer(issueJSON("acme/campaign", 60, "OPEN", "Part of #1\nBlocked by: "+strings.Join(refs, ", "), noBlockers))
	g := newTreeGH(t, resp)
	n := find(treeOf(t, g), 60)
	if n == nil {
		t.Fatal("child 60 missing")
	}
	if got := g.calls(t, "query="+blockerQuery); got > maxBodyRefs {
		t.Errorf("%d blocker lookups for one body, want at most %d", got, maxBodyRefs)
	}
	open := 0
	for _, b := range n.BlockedBy {
		if !b.Closed {
			open++
		}
	}
	if open == 0 {
		t.Errorf("refs past the cap must leave an open blocker, got %+v", n.BlockedBy)
	}
}

func TestTreeCapsLookupsOutsideTheTreePerCall(t *testing.T) {
	resp := map[string]string{"acme_campaign_1.json": issueAnswer(issueJSON("acme/campaign", 1, "OPEN", "", noBlockers))}
	var hits []string
	for c := 0; c < 5; c++ {
		var refs []string
		for i := 0; i < 15; i++ {
			num := 1000 + c*100 + i
			refs = append(refs, fmt.Sprintf("acme/other#%d", num))
			resp[fmt.Sprintf("acme_other_%d.json", num)] = issueAnswer(issueJSON("acme/other", num, "CLOSED", "", noBlockers))
		}
		hits = append(hits, issueJSON("acme/campaign", 60+c, "OPEN", "Part of #1\nBlocked by: "+strings.Join(refs, " "), noBlockers))
	}
	resp["search.json"] = searchAnswer(hits...)
	g := newTreeGH(t, resp)
	tree := treeOf(t, g)
	if got := g.calls(t, "query="+blockerQuery); got > maxLookups {
		t.Errorf("%d lookups outside the tree in one call, want at most %d", got, maxLookups)
	}
	if n := find(tree, 64); n == nil || len(n.BlockedBy) == 0 || n.BlockedBy[len(n.BlockedBy)-1].Closed {
		t.Errorf("a child whose blockers went unread must stay blocked: %+v", n)
	}
}

func TestTreeSearchesPartOfUnderEveryParent(t *testing.T) {
	sub := withSubs(issueJSON("acme/campaign", 2, "OPEN", "", noBlockers), 1, issueJSON("acme/campaign", 3, "OPEN", "", noBlockers))
	g := newTreeGH(t, map[string]string{
		"acme_campaign_1.json": issueAnswer(withSubs(issueJSON("acme/campaign", 1, "OPEN", "", noBlockers), 1, sub)),
		"acme_campaign_2.json": issueAnswer(sub),
		"search.json":          searchAnswer(issueJSON("acme/campaign", 60, "OPEN", "Part of #2", noBlockers)),
	})
	tree := treeOf(t, g)
	two := find(tree, 2)
	if find(two, 60) == nil {
		t.Errorf("a Part of child of the nested parent #2 is missing from it: %+v", two)
	}
	if find(tree, 60) != nil && find(tree, 60) != find(two, 60) {
		t.Errorf("#60 is in the tree twice")
	}
}

func TestTreeFindsPartOfChildrenFiledInAnotherRepoOnce(t *testing.T) {
	g := newTreeGH(t, map[string]string{
		"acme_campaign_1.json": issueAnswer(issueJSON("acme/campaign", 1, "OPEN", "", noBlockers)),
		"search.json": searchAnswer(
			issueJSON("acme/other", 70, "OPEN", "Part of acme/campaign#1", noBlockers),
			issueJSON("acme/other", 71, "OPEN", "Part of #1", noBlockers),
		),
	})
	tree := treeOf(t, g)
	n := 0
	for _, c := range tree.Children {
		if c.Number == 70 && c.Repo == "acme/other" {
			n++
		}
		if c.Number == 71 {
			t.Errorf("a bare #1 in another repo names that repo's #1, not the campaign")
		}
	}
	if n != 1 {
		t.Errorf("acme/other#70 appears %d times, want once", n)
	}
	if g.calls(t, "user:acme") == 0 || g.calls(t, "Part of acme/campaign#1") == 0 {
		t.Errorf("no search for children filed in another repo of the owner")
	}
}

func TestTreeDescendsIntoAPartOfChildWithSubIssues(t *testing.T) {
	hit := withSubs(issueJSON("acme/campaign", 60, "OPEN", "Part of #1", noBlockers), 1)
	full := withSubs(issueJSON("acme/campaign", 60, "OPEN", "Part of #1", noBlockers), 1, issueJSON("acme/campaign", 61, "OPEN", "", noBlockers))
	g := newTreeGH(t, map[string]string{
		"acme_campaign_1.json":  issueAnswer(issueJSON("acme/campaign", 1, "OPEN", "", noBlockers)),
		"acme_campaign_60.json": issueAnswer(full),
		"search.json":           searchAnswer(hit),
	})
	n := find(treeOf(t, g), 60)
	if n == nil || !n.Parent || len(n.Children) != 1 || n.Children[0].Number != 61 {
		t.Errorf("a Part of child with sub-issues = %+v, want a parent holding #61", n)
	}
}

func TestTreeMarksSubIssuesItCouldNotRead(t *testing.T) {
	sub := withSubs(issueJSON("acme/campaign", 2, "OPEN", "", noBlockers), 3)
	g := newTreeGH(t, map[string]string{
		"acme_campaign_1.json": issueAnswer(withSubs(issueJSON("acme/campaign", 1, "OPEN", "", noBlockers), 1, sub)),
		"acme_campaign_2.json": issueAnswer(sub),
		"search.json":          searchAnswer(),
	})
	n := find(treeOf(t, g), 2)
	if n == nil || !n.Parent || n.Unreadable != 3 || len(n.Children) != 0 {
		t.Errorf("#2 = %+v, want a parent with 3 unreadable children", n)
	}
}

func TestTreeIgnoresPartOfLinesFromOutsiders(t *testing.T) {
	g := newTreeGH(t, map[string]string{
		"acme_campaign_1.json": issueAnswer(issueJSON("acme/campaign", 1, "OPEN", "", noBlockers)),
		"search.json": searchAnswer(
			withAssociation(issueJSON("acme/campaign", 80, "OPEN", "Part of #1", noBlockers), "NONE"),
			withAssociation(issueJSON("acme/campaign", 81, "OPEN", "Part of #1", noBlockers), "FIRST_TIME_CONTRIBUTOR"),
			withAssociation(issueJSON("acme/campaign", 82, "OPEN", "Part of #1", noBlockers), "COLLABORATOR"),
		),
	})
	tree := treeOf(t, g)
	if find(tree, 80) != nil || find(tree, 81) != nil {
		t.Errorf("an outsider's Part of line put a child in the tree")
	}
	if find(tree, 82) == nil {
		t.Errorf("a collaborator's Part of child is missing")
	}
}

func TestTreeReadsOnlyBodyURLsOnItsOwnHost(t *testing.T) {
	g := newTreeGH(t, map[string]string{
		"acme_campaign_1.json": issueAnswer(issueJSON("acme/campaign", 1, "OPEN", "", noBlockers)),
		"search.json": searchAnswer(issueJSON("acme/campaign", 60, "OPEN",
			"Part of #1\nBlocked by: https://evil.example.com/acme/other/issues/90", noBlockers)),
	})
	n := find(treeOf(t, g), 60)
	if g.calls(t, "number=90") != 0 {
		t.Errorf("a URL on another host caused a lookup")
	}
	if n == nil || len(n.BlockedBy) != 0 {
		t.Errorf("child 60 = %+v, want no blockers from a foreign URL", n)
	}
}

func TestTreeStopsWhenAPagingCursorRepeats(t *testing.T) {
	root := withSubs(issueJSON("acme/campaign", 1, "OPEN", "", noBlockers), 1, issueJSON("acme/campaign", 2, "OPEN", "", noBlockers))
	pageWith := strings.Replace(root, `"pageInfo":{"hasNextPage":false,"endCursor":null}}}`, `"pageInfo":{"hasNextPage":true,"endCursor":"C1"}}}`, -1)
	g := newTreeGH(t, map[string]string{
		"acme_campaign_1.json":    issueAnswer(pageWith),
		"acme_campaign_1_C1.json": issueAnswer(pageWith),
		"search.json":             strings.Replace(searchAnswer(), `"hasNextPage":false,"endCursor":null`, `"hasNextPage":true,"endCursor":"S"`, 1),
		"search_S.json":           strings.Replace(searchAnswer(), `"hasNextPage":false,"endCursor":null`, `"hasNextPage":true,"endCursor":"S"`, 1),
	})
	done := make(chan struct{})
	go func() { treeOf(t, g); close(done) }()
	select {
	case <-done:
	case <-time.After(20 * time.Second):
		t.Fatal("Tree keeps paging when GitHub repeats a cursor")
	}
	if n := len(g.argvs(t)); n > 12 {
		t.Errorf("%d gh calls on a repeating cursor", n)
	}
}

func TestTreeReadsEveryPageOfLabels(t *testing.T) {
	label := func(n string) string { return `{"name":"` + n + `"}` }
	child := strings.Replace(issueJSON("acme/campaign", 2, "OPEN", "", noBlockers),
		`"labels":{"nodes":[]`, `"labels":{"nodes":[`+label("a")+`]`, 1)
	child = strings.Replace(child, `"labels":{"nodes":[`+label("a")+`],"pageInfo":{"hasNextPage":false,"endCursor":null}`,
		`"labels":{"nodes":[`+label("a")+`],"pageInfo":{"hasNextPage":true,"endCursor":"L1"}`, 1)
	g := newTreeGH(t, map[string]string{
		"acme_campaign_1.json":    issueAnswer(withSubs(issueJSON("acme/campaign", 1, "OPEN", "", noBlockers), 1, child)),
		"acme_campaign_2_L1.json": issueAnswer(`{"labels":{"nodes":[` + label("ready-for-agent") + `],"pageInfo":{"hasNextPage":false,"endCursor":null}}}`),
		"search.json":             searchAnswer(),
	})
	n := find(treeOf(t, g), 2)
	if n == nil || strings.Join(n.Labels, ",") != "a,ready-for-agent" {
		t.Errorf("labels = %+v, want both pages", n)
	}
}

func TestTreeStopsDescendingAtTheDepthLimit(t *testing.T) {
	resp := map[string]string{"search.json": searchAnswer()}
	for i := 1; i <= maxTreeDepth+4; i++ {
		resp[fmt.Sprintf("acme_campaign_%d.json", i)] = issueAnswer(withSubs(issueJSON("acme/campaign", i, "OPEN", "", noBlockers), 1,
			withSubs(issueJSON("acme/campaign", i+1, "OPEN", "", noBlockers), 1)))
	}
	g := newTreeGH(t, resp)
	depth, n := 0, treeOf(t, g)
	for len(n.Children) > 0 {
		n, depth = n.Children[0], depth+1
	}
	if depth != maxTreeDepth+1 {
		t.Errorf("tree is %d deep, want %d", depth, maxTreeDepth+1)
	}
	if !n.Parent || n.Unreadable != 1 {
		t.Errorf("the node at the limit = %+v, want a parent with one unread sub-issue", n)
	}
}

func TestTreeTimeoutReadsAsUnreachable(t *testing.T) {
	dir := t.TempDir()
	gh := filepath.Join(dir, "gh")
	if err := os.WriteFile(gh, []byte("#!/bin/sh\nexec sleep 5\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	c := NewClient("github.com", "", nil, os.Environ())
	c.Gh = gh
	c.Timeout = 300 * time.Millisecond
	_, err := c.Tree(context.Background(), "fake-token-work-2", "acme", "campaign", 1)
	if err == nil || !strings.Contains(err.Error(), "Couldn't reach github.com") {
		t.Errorf("error = %v, want Couldn't reach github.com", err)
	}
}
