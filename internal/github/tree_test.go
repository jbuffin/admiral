package github

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
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
n=$(ls "$d/calls" | wc -l | tr -d ' ')
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
			if strings.HasPrefix(a, "q=") {
				searches = append(searches, argv)
			}
		}
	}
	if len(searches) != 2 {
		t.Fatalf("want the Part of search and its second page, got %d searches", len(searches))
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
