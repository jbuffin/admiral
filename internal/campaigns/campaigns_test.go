package campaigns

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/jbuffin/admiral/internal/github"
)

type rawNode struct {
	Number     int    `json:"number"`
	Title      string `json:"title"`
	URL        string `json:"url"`
	State      string `json:"state"`
	Body       string `json:"body"`
	Repository struct {
		NameWithOwner string `json:"nameWithOwner"`
	} `json:"repository"`
	Labels    struct{ Nodes []struct{ Name string } }  `json:"labels"`
	Assignees struct{ Nodes []struct{ Login string } } `json:"assignees"`
	BlockedBy struct {
		Nodes []struct{ URL, Title, State string }
	} `json:"blockedBy"`
	SubIssues struct {
		TotalCount int
		Nodes      []rawNode
	} `json:"subIssues"`
}

func readJSON(t *testing.T, name string, into any) {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", "tree", name))
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(b, into); err != nil {
		t.Fatalf("%s: %v", name, err)
	}
}

func convert(r rawNode) *github.TreeNode {
	n := &github.TreeNode{Number: r.Number, Title: r.Title, URL: r.URL, Repo: r.Repository.NameWithOwner,
		Closed: r.State == "CLOSED", Body: r.Body}
	for _, l := range r.Labels.Nodes {
		n.Labels = append(n.Labels, l.Name)
	}
	for _, a := range r.Assignees.Nodes {
		n.Assignees = append(n.Assignees, a.Login)
	}
	for _, b := range r.BlockedBy.Nodes {
		n.BlockedBy = append(n.BlockedBy, github.Blocker{URL: b.URL, Title: b.Title, Closed: b.State == "CLOSED"})
	}
	return n
}

// load turns the recorded GraphQL answers into the tree the reader hands over.
func load(t *testing.T, repo string, number int) *github.TreeNode {
	t.Helper()
	var doc struct {
		Data struct {
			Repository struct {
				Issue rawNode `json:"issueOrPullRequest"`
			}
		}
	}
	readJSON(t, fmt.Sprintf("%s_%d.json", strings.ReplaceAll(repo, "/", "_"), number), &doc)
	root := convert(doc.Data.Repository.Issue)
	for _, c := range doc.Data.Repository.Issue.SubIssues.Nodes {
		if c.SubIssues.TotalCount > 0 {
			root.Children = append(root.Children, load(t, c.Repository.NameWithOwner, c.Number))
		} else {
			root.Children = append(root.Children, convert(c))
		}
	}
	return root
}

func recordedTree(t *testing.T) *github.TreeNode {
	t.Helper()
	root := load(t, "acme/campaign", 1)
	have := map[string]*github.TreeNode{}
	for _, c := range root.Children {
		have[c.URL] = c
	}
	part := regexp.MustCompile(`(?m)^Part of #1$`)
	blocked := regexp.MustCompile(`(?m)^Blocked by: #(\d+)$`)
	for _, page := range []string{"search.json", "search_S1.json"} {
		var doc struct {
			Data struct{ Search struct{ Nodes []rawNode } }
		}
		readJSON(t, page, &doc)
		for _, r := range doc.Data.Search.Nodes {
			if !part.MatchString(r.Body) || have[r.URL] != nil {
				continue
			}
			n := convert(r)
			if m := blocked.FindStringSubmatch(r.Body); m != nil {
				num, _ := strconv.Atoi(m[1])
				for _, c := range root.Children {
					if c.Number == num {
						n.BlockedBy = append(n.BlockedBy, github.Blocker{URL: c.URL, Title: c.Title, Closed: c.Closed})
					}
				}
			}
			root.Children = append(root.Children, n)
		}
	}
	return root
}

func u(n int) string { return fmt.Sprintf("https://github.com/acme/campaign/issues/%d", n) }

const other40 = "https://github.com/acme/other/issues/40"

func nodeByURL(n *Node, url string) *Node {
	if n == nil {
		return nil
	}
	if n.URL == url {
		return n
	}
	for _, c := range n.Children {
		if f := nodeByURL(c, url); f != nil {
			return f
		}
	}
	return nil
}

func mustNode(t *testing.T, c Campaign, url string) *Node {
	t.Helper()
	n := nodeByURL(c.Root, url)
	if n == nil {
		t.Fatalf("no node %s in the campaign", url)
	}
	return n
}

func urls(ns []*Node) []string {
	var out []string
	for _, n := range ns {
		out = append(out, n.URL)
	}
	return out
}

func TestRecordedTreeStatusesAndReadyOrder(t *testing.T) {
	c := Build(Input{Tree: recordedTree(t)})
	want := map[string]Status{
		u(2): Ready, u(3): Done, u(4): Ready, u(5): Blocked, u(6): ClaimedNoShip,
		u(8): Ready, u(9): Ready, other40: Ready, u(11): Blocked, u(13): Ready,
		u(14): Ready, u(50): Blocked, u(51): Ready,
	}
	for url, s := range want {
		if got := mustNode(t, c, url).Status; got != s {
			t.Errorf("%s is %q, want %q", url, got, s)
		}
	}
	wantReady := []string{u(2), u(4), u(8), u(9), other40, u(13), u(14), u(51)}
	if got := urls(c.Ready); strings.Join(got, " ") != strings.Join(wantReady, " ") {
		t.Errorf("ready set in tree order:\n got %v\nwant %v", got, wantReady)
	}
}

func TestRecordedTreeProgressAndCounts(t *testing.T) {
	c := Build(Input{Tree: recordedTree(t)})
	if c.Progress != (Progress{Closed: 1, Total: 13}) {
		t.Errorf("progress = %+v, want 1 closed of 13 launchable children (nested parents not counted)", c.Progress)
	}
	want := map[Status]int{Ready: 8, Blocked: 3, ClaimedNoShip: 1, Done: 1}
	for s, n := range want {
		if c.Counts[s] != n {
			t.Errorf("count of %q = %d, want %d", s, c.Counts[s], n)
		}
	}
	if c.Counts[Underway] != 0 {
		t.Errorf("underway = %d, want 0 with no claims", c.Counts[Underway])
	}
}

func TestBlockedChildNamesItsOpenBlockers(t *testing.T) {
	c := Build(Input{Tree: recordedTree(t)})
	for _, tc := range []struct{ child, blocker, why string }{
		{u(5), "https://github.com/acme/other/issues/90", "a blocker outside the tree"},
		{u(11), u(5), "a blocked parent's blocker"},
		{u(50), u(5), "a Blocked by line"},
	} {
		n := mustNode(t, c, tc.child)
		var got []string
		for _, b := range n.Blockers {
			got = append(got, b.URL)
		}
		if len(got) != 1 || got[0] != tc.blocker {
			t.Errorf("%s names blockers %v, want [%s] (%s)", tc.child, got, tc.blocker, tc.why)
		}
	}
	if b := mustNode(t, c, u(4)).Blockers; len(b) != 0 {
		t.Errorf("a child whose blocker is closed names none, got %+v", b)
	}
}

func TestNestedParentsFoldIntoTheTree(t *testing.T) {
	c := Build(Input{Tree: recordedTree(t)})
	for _, url := range []string{u(7), u(10), u(12)} {
		p := mustNode(t, c, url)
		if !p.Parent || p.Status != "" || p.Mode != "" || p.Skill != (Skill{}) {
			t.Errorf("%s is a nested parent: never launchable, so no status, mode or skill: %+v", url, p)
		}
	}
	if got := urls(mustNode(t, c, u(7)).Children); strings.Join(got, " ") != u(8)+" "+u(9) {
		t.Errorf("sub-map children = %v", got)
	}
	if !mustNode(t, c, u(12)).Closed {
		t.Error("a closed nested parent shows closed")
	}
	if s := mustNode(t, c, u(13)).Status; s != Ready {
		t.Errorf("an open child of a closed parent keeps its own status, got %q", s)
	}
}

func TestLiveClaimMakesAChildUnderway(t *testing.T) {
	c := Build(Input{Tree: recordedTree(t), Claims: map[string]string{u(6): "Eagle", u(2): "Falcon"}})
	six := mustNode(t, c, u(6))
	if six.Status != Underway || six.Ship != "Eagle" {
		t.Errorf("assigned child with a live ship: status %q ship %q, want underway by Eagle", six.Status, six.Ship)
	}
	if two := mustNode(t, c, u(2)); two.Status != Underway || two.Ship != "Falcon" {
		t.Errorf("unassigned child with a live ship: status %q ship %q", two.Status, two.Ship)
	}
	if c.Counts[Underway] != 2 || c.Counts[ClaimedNoShip] != 0 || c.Counts[Ready] != 7 {
		t.Errorf("counts = %v", c.Counts)
	}
	if got := urls(c.Ready); strings.Contains(strings.Join(got, " "), u(2)) {
		t.Errorf("an underway child is not in the ready set: %v", got)
	}
}

func TestOverridesWinOverDefaults(t *testing.T) {
	wayfinder := Skill{Kind: "skill", Name: "wayfinder", Args: "pick the next ticket"}
	c := Build(Input{Tree: recordedTree(t), Overrides: map[string]Override{
		u(2):    {Mode: WithYou},
		u(4):    {Skill: &Skill{Kind: "commodore"}},
		u(14):   {Mode: WithoutYou, Skill: &wayfinder},
		u(6):    {Mode: WithYou},
		other40: {},
	}})
	if n := mustNode(t, c, u(2)); n.Mode != WithYou || n.Skill != (Skill{Kind: "commodore"}) {
		t.Errorf("mode override keeps the default skill: %+v", n)
	}
	if n := mustNode(t, c, u(4)); n.Mode != WithYou || n.Skill != (Skill{Kind: "commodore"}) {
		t.Errorf("skill override keeps the default mode: %+v", n)
	}
	if n := mustNode(t, c, u(14)); n.Mode != WithoutYou || n.Skill != wayfinder {
		t.Errorf("both overridden: %+v", n)
	}
	if n := mustNode(t, c, other40); n.Mode != WithYou || n.Skill.Kind != "none" {
		t.Errorf("an empty override changes nothing: %+v", n)
	}
	if n := mustNode(t, c, u(6)); n.Status != ClaimedNoShip {
		t.Errorf("an override does not change the status: %q", n.Status)
	}
}

func TestPrecedenceAndFoldingOnSmallTrees(t *testing.T) {
	const ship = "Eagle"
	cases := []struct {
		name   string
		child  *github.TreeNode
		claims bool
		want   Status
	}{
		{"closed beats a claim", child(2, closed()), true, Done},
		{"a live ship beats an assignee", child(2, assigned("octocat")), true, Underway},
		{"a live ship beats an open blocker", child(2, blockedBy(openBlocker(90))), true, Underway},
		{"an open blocker beats an assignee", child(2, assigned("octocat"), blockedBy(openBlocker(90))), false, Blocked},
		{"assigned with no ship", child(2, assigned("octocat")), false, ClaimedNoShip},
		{"every blocker closed", child(2, blockedBy(github.Blocker{URL: u(90), Closed: true})), false, Ready},
		{"one open blocker of two", child(2, blockedBy(github.Blocker{URL: u(90), Closed: true}, openBlocker(91))), false, Blocked},
		{"open and free", child(2), false, Ready},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			in := Input{Tree: tree(tc.child)}
			if tc.claims {
				in.Claims = map[string]string{u(2): ship}
			}
			if got := mustNode(t, Build(in), u(2)).Status; got != tc.want {
				t.Errorf("status = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestBlockedParentBlocksItsSubtreeAndKeepsOrder(t *testing.T) {
	sub := child(3, blockedBy(openBlocker(90)), kids(child(4), child(5, assigned("octocat"))))
	c := Build(Input{Tree: tree(child(2), sub, child(6), child(7, closed(), kids(child(8))))})
	for n, want := range map[int]Status{2: Ready, 4: Blocked, 5: Blocked, 6: Ready, 8: Ready} {
		if got := mustNode(t, c, u(n)).Status; got != want {
			t.Errorf("#%d is %q, want %q", n, got, want)
		}
	}
	if got := urls(c.Ready); strings.Join(got, " ") != u(2)+" "+u(6)+" "+u(8) {
		t.Errorf("ready set = %v, want tree order 2, 6, 8", got)
	}
}

func TestDefaultsFollowTheTable(t *testing.T) {
	m, tk := u(1), u(2)
	wf := func(args bool) Skill {
		s := Skill{Kind: "skill", Name: "wayfinder"}
		if args {
			s.Args = m + " " + tk
		}
		return s
	}
	cases := []struct {
		name   string
		labels []string
		mode   Mode
		skill  Skill
	}{
		{"wayfinder grilling", []string{"wayfinder:grilling"}, WithYou, wf(true)},
		{"wayfinder prototype", []string{"wayfinder:prototype"}, WithYou, wf(true)},
		{"wayfinder research", []string{"wayfinder:research"}, WithoutYou, wf(true)},
		{"wayfinder task", []string{"wayfinder:task"}, WithoutYou, wf(false)},
		{"wayfinder task needing a human", []string{"wayfinder:task", "needs-human-action"}, WithYou, wf(false)},
		{"plain ready-for-agent", []string{"ready-for-agent"}, WithoutYou, Skill{Kind: "commodore"}},
		{"ready-for-human", []string{"ready-for-human"}, WithYou, Skill{Kind: "none"}},
		{"needs-triage", []string{"needs-triage"}, WithYou, Skill{Kind: "none"}},
		{"no row matches", []string{"bug"}, WithYou, Skill{Kind: "none"}},
		{"no labels", nil, WithYou, Skill{Kind: "none"}},
		{"a wayfinder label beats a triage label", []string{"needs-triage", "wayfinder:research"}, WithoutYou, wf(true)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			n := mustNode(t, Build(Input{Tree: tree(child(2, labelled(tc.labels...)))}), tk)
			if n.Mode != tc.mode || n.Skill != tc.skill {
				t.Errorf("got mode %q skill %+v, want %q %+v", n.Mode, n.Skill, tc.mode, tc.skill)
			}
		})
	}
}

func TestWayfinderMapIsTheNearestParent(t *testing.T) {
	sub := child(3, labelled("wayfinder:map"), kids(crossRepo(41, "acme/other", labelled("wayfinder:research"))))
	c := Build(Input{Tree: tree(sub)})
	n := mustNode(t, c, "https://github.com/acme/other/issues/41")
	want := Skill{Kind: "skill", Name: "wayfinder", Args: u(3) + " https://github.com/acme/other/issues/41"}
	if n.Skill != want {
		t.Errorf("skill = %+v, want the nested map and the cross-repo ticket URLs: %+v", n.Skill, want)
	}
}

// Small trees hang under a root whose URL is u(1).
type opt func(*github.TreeNode)

func child(n int, opts ...opt) *github.TreeNode {
	nd := &github.TreeNode{Number: n, Title: fmt.Sprintf("Child %d", n), URL: u(n), Repo: "acme/campaign"}
	for _, o := range opts {
		o(nd)
	}
	return nd
}

func crossRepo(n int, repo string, opts ...opt) *github.TreeNode {
	nd := child(n, opts...)
	nd.Repo, nd.URL = repo, fmt.Sprintf("https://github.com/%s/issues/%d", repo, n)
	return nd
}

func tree(children ...*github.TreeNode) *github.TreeNode {
	r := child(1)
	r.Children = children
	return r
}

func closed() opt                    { return func(n *github.TreeNode) { n.Closed = true } }
func assigned(who string) opt        { return func(n *github.TreeNode) { n.Assignees = []string{who} } }
func labelled(l ...string) opt       { return func(n *github.TreeNode) { n.Labels = l } }
func kids(k ...*github.TreeNode) opt { return func(n *github.TreeNode) { n.Children = k } }
func blockedBy(b ...github.Blocker) opt {
	return func(n *github.TreeNode) { n.BlockedBy = b }
}
func openBlocker(n int) github.Blocker { return github.Blocker{URL: u(n), Title: "Blocker"} }

func TestRootsOwnBlockersDoNotBlockItsChildren(t *testing.T) {
	root := tree(child(2), child(3, blockedBy(openBlocker(90))))
	root.BlockedBy = []github.Blocker{openBlocker(91)}
	c := Build(Input{Tree: root})
	if got := mustNode(t, c, u(2)).Status; got != Ready {
		t.Errorf("a child of a blocked campaign root is %q, want ready", got)
	}
	if n := mustNode(t, c, u(3)); n.Status != Blocked || len(n.Blockers) != 1 || n.Blockers[0].URL != u(90) {
		t.Errorf("a child keeps its own blockers and not the root's: %+v", n)
	}
}

func TestParentWithUnreadableChildrenIsNeverLaunchable(t *testing.T) {
	hidden := child(3)
	hidden.Parent, hidden.Unreadable = true, 2
	c := Build(Input{Tree: tree(child(2), hidden)})
	n := mustNode(t, c, u(3))
	if !n.Parent || n.Status != "" || n.Unreadable != 2 {
		t.Errorf("a node with sub-issues it couldn't read = %+v, want a parent that says 2 are unreadable", n)
	}
	if got := urls(c.Ready); strings.Join(got, " ") != u(2) {
		t.Errorf("ready set = %v, want only #2", got)
	}
}
