package github

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
)

// Blocker is an issue that blocks another, in or outside the tree.
type Blocker struct {
	URL    string
	Title  string
	Closed bool
}

// TreeNode is one issue of a campaign's tree, with its sub-issues in order.
type TreeNode struct {
	Number     int
	Title      string
	URL        string
	Repo       string // nameWithOwner, which may differ from the parent's
	Closed     bool
	Labels     []string
	Assignees  []string // logins; empty when nobody is assigned
	BlockedBy  []Blocker
	Body       string
	Children   []*TreeNode
	Parent     bool // GitHub says it has sub-issues, whether or not they could be read
	Unreadable int  // sub-issues GitHub counts that the read couldn't return
}

// maxTreeDepth is GitHub's limit on sub-issue nesting.
const maxTreeDepth = 8

// A body that names more blockers than maxBodyRefs, or a tree that needs more lookups outside it than
// maxLookups, counts the rest as unreadable, which is open: any author can write a body, and each
// lookup is a gh call on the engineer's token.
const (
	maxBodyRefs = 20
	maxLookups  = 50
)

// nodeFields is what every issue answers, a sub-issue or a search hit alike; each list is read up to
// 100 long and paged when GitHub says there is more.
const nodeFields = `__typename ... on Issue { number title url state body authorAssociation repository { nameWithOwner }
	labels(first: 100) { nodes { name } pageInfo { hasNextPage endCursor } }
	assignees(first: 20) { nodes { login } }
	blockedBy(first: 100) { nodes { url title state } pageInfo { hasNextPage endCursor } }
	subIssues(first: 1) { totalCount } }`

const pageInfo = "pageInfo { hasNextPage endCursor }"

var (
	issueQuery = `query($owner: String!, $repo: String!, $number: Int!, $after: String) {
	repository(owner: $owner, name: $repo) { issueOrPullRequest(number: $number) { __typename
	... on Issue { number title url state body repository { nameWithOwner }
	labels(first: 100) { nodes { name } ` + pageInfo + ` }
	assignees(first: 20) { nodes { login } }
	blockedBy(first: 100) { nodes { url title state } ` + pageInfo + ` }
	subIssues(first: 100, after: $after) { totalCount nodes { ` + nodeFields + ` } ` + pageInfo + ` } } } } }`

	labelsQuery = `query($owner: String!, $repo: String!, $number: Int!, $after: String) {
	repository(owner: $owner, name: $repo) { issueOrPullRequest(number: $number) {
	... on Issue { labels(first: 100, after: $after) { nodes { name } ` + pageInfo + ` } } } } }`

	blockedByQuery = `query($owner: String!, $repo: String!, $number: Int!, $after: String) {
	repository(owner: $owner, name: $repo) { issueOrPullRequest(number: $number) {
	... on Issue { blockedBy(first: 100, after: $after) { nodes { url title state } ` + pageInfo + ` } } } } }`

	blockerQuery = `query($owner: String!, $repo: String!, $number: Int!) {
	repository(owner: $owner, name: $repo) { issueOrPullRequest(number: $number) {
	__typename ... on Issue { url title state } ... on PullRequest { url title state } } } }`

	partOfQuery = `query($q: String!, $after: String) {
	search(query: $q, type: ISSUE_ADVANCED, first: 100, after: $after) { nodes { ` + nodeFields + ` } ` + pageInfo + ` } }`
)

type pageInfoJSON struct {
	HasNextPage bool   `json:"hasNextPage"`
	EndCursor   string `json:"endCursor"`
}

type gqlNode struct {
	Typename   string `json:"__typename"`
	Number     int    `json:"number"`
	Title      string `json:"title"`
	URL        string `json:"url"`
	State      string `json:"state"`
	Body       string `json:"body"`
	Author     string `json:"authorAssociation"`
	Repository struct {
		NameWithOwner string `json:"nameWithOwner"`
	} `json:"repository"`
	Labels struct {
		Nodes    []struct{ Name string } `json:"nodes"`
		PageInfo pageInfoJSON            `json:"pageInfo"`
	} `json:"labels"`
	Assignees struct {
		Nodes []struct{ Login string } `json:"nodes"`
	} `json:"assignees"`
	BlockedBy struct {
		Nodes    []struct{ URL, Title, State string } `json:"nodes"`
		PageInfo pageInfoJSON                         `json:"pageInfo"`
	} `json:"blockedBy"`
	SubIssues struct {
		TotalCount int          `json:"totalCount"`
		Nodes      []gqlNode    `json:"nodes"`
		PageInfo   pageInfoJSON `json:"pageInfo"`
	} `json:"subIssues"`
}

func (g *gqlNode) treeNode() *TreeNode {
	n := &TreeNode{Number: g.Number, Title: g.Title, URL: g.URL, Repo: g.Repository.NameWithOwner,
		Closed: g.State == "CLOSED", Body: g.Body}
	for _, l := range g.Labels.Nodes {
		n.Labels = append(n.Labels, l.Name)
	}
	for _, a := range g.Assignees.Nodes {
		n.Assignees = append(n.Assignees, a.Login)
	}
	for _, b := range g.BlockedBy.Nodes {
		n.BlockedBy = append(n.BlockedBy, Blocker{URL: b.URL, Title: b.Title, Closed: b.State == "CLOSED"})
	}
	return n
}

// Tree reads the issue owner/repo#number and everything under it. It never writes.
// Its errors read as the message to show, with the token redacted.
func (c *Client) Tree(ctx context.Context, token, owner, repo string, number int) (*TreeNode, error) {
	r := &treeReader{c: c, ctx: ctx, token: token, seen: map[string]bool{}, placed: map[string]bool{},
		looked: map[string]*TreeNode{}, urlRefs: urlRefs(c.host)}
	root, err := r.issue(owner, repo, number, 0)
	if err != nil {
		return nil, err
	}
	index := map[string]*TreeNode{}
	var walk func(n *TreeNode)
	walk = func(n *TreeNode) {
		index[refKey(n.Repo, n.Number)] = n
		for _, ch := range n.Children {
			walk(ch)
		}
	}
	walk(root)
	for _, n := range r.pending {
		if err := r.bodyBlockers(n, index); err != nil {
			return nil, err
		}
	}
	return root, nil
}

type treeReader struct {
	c       *Client
	ctx     context.Context
	token   string
	seen    map[string]bool      // issues descended into, so a loop can't recurse
	placed  map[string]bool      // issues in the tree, so a Part of hit isn't added twice
	pending []*TreeNode          // Part of children, whose Blocked by lines resolve once the tree is whole
	looked  map[string]*TreeNode // blockers read outside the tree, by repo#number
	lookups int
	urlRefs *regexp.Regexp
}

func refKey(repo string, number int) string {
	return strings.ToLower(repo) + "#" + strconv.Itoa(number)
}

// page reads one issue's first page of sub-issues, or the page after cursor.
func (r *treeReader) page(owner, repo string, number int, after string) (*gqlNode, error) {
	args := []string{"-f", "query=" + issueQuery, "-f", "owner=" + owner, "-f", "repo=" + repo, "-F", "number=" + strconv.Itoa(number)}
	if after != "" {
		args = append(args, "-f", "after="+after)
	}
	ref := fmt.Sprintf("%s/%s#%d", owner, repo, number)
	data, err := r.c.graphql(r.ctx, r.token, ref, args)
	if err != nil {
		return nil, err
	}
	var d struct {
		Repository *struct {
			Issue *gqlNode `json:"issueOrPullRequest"`
		} `json:"repository"`
	}
	if json.Unmarshal(data, &d) != nil || d.Repository == nil || d.Repository.Issue == nil {
		return nil, r.c.notFound(ref)
	}
	n := d.Repository.Issue
	if n.Typename == "PullRequest" {
		return nil, fmt.Errorf("%s is a pull request, not an issue", ref)
	}
	if n.Typename != "Issue" {
		return nil, errors.New("Tree read failed: GitHub's answer was not understood")
	}
	return n, nil
}

func readable(g *gqlNode) bool { return g.Number != 0 && g.URL != "" }

// issue reads one issue, every page of its sub-issues, and descends into those that have their own.
// It then adds the children that name it in a "Part of" line.
func (r *treeReader) issue(owner, repo string, number, depth int) (*TreeNode, error) {
	first, err := r.page(owner, repo, number, "")
	if err != nil {
		return nil, err
	}
	r.seen[strings.ToLower(first.URL)] = true
	r.placed[refKey(first.Repository.NameWithOwner, first.Number)] = true
	subs := first.SubIssues.Nodes
	prev := ""
	for pi := first.SubIssues.PageInfo; pi.HasNextPage && pi.EndCursor != "" && pi.EndCursor != prev; {
		prev = pi.EndCursor
		next, err := r.page(owner, repo, number, pi.EndCursor)
		if err != nil {
			return nil, err
		}
		subs = append(subs, next.SubIssues.Nodes...)
		pi = next.SubIssues.PageInfo
	}
	node, err := r.finish(first)
	if err != nil {
		return nil, err
	}
	node.Parent = first.SubIssues.TotalCount > 0
	for i := range subs {
		if readable(&subs[i]) {
			r.placed[refKey(subs[i].Repository.NameWithOwner, subs[i].Number)] = true
		}
	}
	got := 0
	for i := range subs {
		s := &subs[i]
		if !readable(s) {
			continue
		}
		got++
		child, err := r.child(s, depth)
		if err != nil {
			return nil, err
		}
		node.Children = append(node.Children, child)
	}
	node.Unreadable = max(0, first.SubIssues.TotalCount-got)
	if err := r.partOf(node, depth); err != nil {
		return nil, err
	}
	return node, nil
}

// child reads a sub-issue or a Part of hit found under a parent at depth; one with children of its own is read as a parent.
func (r *treeReader) child(g *gqlNode, depth int) (*TreeNode, error) {
	if g.SubIssues.TotalCount > 0 && depth < maxTreeDepth && !r.seen[strings.ToLower(g.URL)] {
		o, rp, _ := strings.Cut(g.Repository.NameWithOwner, "/")
		return r.issue(o, rp, g.Number, depth+1)
	}
	return r.finish(g)
}

// finish turns a node into a tree node, reading the pages of labels and blockers past the first 100.
// A node with sub-issues it didn't read, at the depth limit, is a parent with unreadable children.
func (r *treeReader) finish(g *gqlNode) (*TreeNode, error) {
	n := g.treeNode()
	n.Parent = g.SubIssues.TotalCount > 0
	n.Unreadable = g.SubIssues.TotalCount
	o, rp, _ := strings.Cut(g.Repository.NameWithOwner, "/")
	prev := ""
	for pi := g.Labels.PageInfo; pi.HasNextPage && pi.EndCursor != "" && pi.EndCursor != prev; {
		prev = pi.EndCursor
		d, err := r.extra(labelsQuery, o, rp, g.Number, pi.EndCursor)
		if err != nil {
			return nil, err
		}
		for _, l := range d.Labels.Nodes {
			n.Labels = append(n.Labels, l.Name)
		}
		pi = d.Labels.PageInfo
	}
	prev = ""
	for pi := g.BlockedBy.PageInfo; pi.HasNextPage && pi.EndCursor != "" && pi.EndCursor != prev; {
		prev = pi.EndCursor
		d, err := r.extra(blockedByQuery, o, rp, g.Number, pi.EndCursor)
		if err != nil {
			return nil, err
		}
		for _, b := range d.BlockedBy.Nodes {
			n.BlockedBy = append(n.BlockedBy, Blocker{URL: b.URL, Title: b.Title, Closed: b.State == "CLOSED"})
		}
		pi = d.BlockedBy.PageInfo
	}
	return n, nil
}

func (r *treeReader) extra(query, owner, repo string, number int, after string) (*gqlNode, error) {
	ref := fmt.Sprintf("%s/%s#%d", owner, repo, number)
	data, err := r.c.graphql(r.ctx, r.token, ref, []string{"-f", "query=" + query, "-f", "owner=" + owner,
		"-f", "repo=" + repo, "-F", "number=" + strconv.Itoa(number), "-f", "after=" + after})
	if err != nil {
		return nil, err
	}
	var d struct {
		Repository *struct {
			Issue *gqlNode `json:"issueOrPullRequest"`
		} `json:"repository"`
	}
	if json.Unmarshal(data, &d) != nil || d.Repository == nil || d.Repository.Issue == nil {
		return nil, r.c.notFound(ref)
	}
	return d.Repository.Issue, nil
}

// trusted are the authors whose "Part of" line makes an issue a child: GitHub lets only people with
// write access add a sub-issue, but anyone can open an issue that says "Part of".
var trusted = map[string]bool{"OWNER": true, "MEMBER": true, "COLLABORATOR": true}

// partOf adds the children, not already in the tree, whose body says "Part of" this parent. GitHub
// caps sub-issues at 100, so a long campaign has these. A second search finds the ones filed in another
// repo of the parent's owner, which write the parent's owner/repo#n.
func (r *treeReader) partOf(parent *TreeNode, depth int) error {
	if parent.Repo == "" || parent.Number == 0 {
		return nil
	}
	owner, _, _ := strings.Cut(parent.Repo, "/")
	qs := []string{
		fmt.Sprintf(`repo:%s is:issue sort:created-asc "Part of #%d"`, parent.Repo, parent.Number),
		fmt.Sprintf(`user:%s is:issue sort:created-asc "Part of %s#%d"`, owner, parent.Repo, parent.Number),
	}
	same, other := partOfLine(parent, true), partOfLine(parent, false)
	for _, q := range qs {
		prev := ""
		for after := ""; ; {
			args := []string{"-f", "query=" + partOfQuery, "-f", "q=" + q}
			if after != "" {
				args = append(args, "-f", "after="+after)
			}
			data, err := r.c.graphql(r.ctx, r.token, parent.Repo, args)
			if err != nil {
				return err
			}
			var d struct {
				Search *struct {
					Nodes    []gqlNode    `json:"nodes"`
					PageInfo pageInfoJSON `json:"pageInfo"`
				} `json:"search"`
			}
			if json.Unmarshal(data, &d) != nil || d.Search == nil {
				return errors.New("Tree read failed: GitHub's answer was not understood")
			}
			for i := range d.Search.Nodes {
				g := &d.Search.Nodes[i]
				line := other
				if strings.EqualFold(g.Repository.NameWithOwner, parent.Repo) {
					line = same
				}
				if g.Typename != "Issue" || !readable(g) || r.placed[refKey(g.Repository.NameWithOwner, g.Number)] ||
					!trusted[g.Author] || !line.MatchString(g.Body) {
					continue
				}
				r.placed[refKey(g.Repository.NameWithOwner, g.Number)] = true
				n, err := r.child(g, depth)
				if err != nil {
					return err
				}
				r.pending = append(r.pending, n)
				parent.Children = append(parent.Children, n)
			}
			pi := d.Search.PageInfo
			if !pi.HasNextPage || pi.EndCursor == "" || pi.EndCursor == prev {
				break
			}
			prev, after = pi.EndCursor, pi.EndCursor
		}
	}
	return nil
}

// partOfLine matches a body line that names the parent: owner/repo#n or its URL, and #n where the
// child is in the parent's own repo.
func partOfLine(p *TreeNode, bare bool) *regexp.Regexp {
	alts := fmt.Sprintf(`%s#%d|%s`, regexp.QuoteMeta(p.Repo), p.Number, regexp.QuoteMeta(p.URL))
	if bare {
		alts = fmt.Sprintf(`#%d|`, p.Number) + alts
	}
	return regexp.MustCompile(`(?mi)^[ \t]*Part of[ \t]+(?:` + alts + `)[ \t\r]*$`)
}

var blockedByLine = regexp.MustCompile(`(?mi)^[ \t]*Blocked by:?[ \t]*(.+)$`)

// urlRefs matches an issue ref in a body: a URL on the client's host, owner/repo#n, or #n. A URL on
// another host names an issue this client can't read.
func urlRefs(host string) *regexp.Regexp {
	return regexp.MustCompile(`(?i)https?://` + regexp.QuoteMeta(host) + `/([A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+)/issues/(\d+)|([A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+)#(\d+)|#(\d+)`)
}

// bodyBlockers adds the blockers a child's "Blocked by:" lines name, up to maxBodyRefs. One outside
// the tree is read so that an open blocker elsewhere still blocks; one that can't be read counts as open.
func (r *treeReader) bodyBlockers(n *TreeNode, in map[string]*TreeNode) error {
	have := map[string]bool{}
	for _, b := range n.BlockedBy {
		have[strings.ToLower(b.URL)] = true
	}
	named := map[string]bool{}
	for _, l := range blockedByLine.FindAllStringSubmatch(n.Body, -1) {
		for _, m := range r.urlRefs.FindAllStringSubmatch(l[1], -1) {
			repo, num := n.Repo, 0
			switch {
			case m[1] != "":
				repo, num = m[1], atoi(m[2])
			case m[3] != "":
				repo, num = m[3], atoi(m[4])
			default:
				num = atoi(m[5])
			}
			key := refKey(repo, num)
			if named[key] {
				continue
			}
			if len(named) == maxBodyRefs {
				n.BlockedBy = append(n.BlockedBy, Blocker{URL: n.URL, Title: "More Blocked by lines than Admiral reads; check the issue"})
				return nil
			}
			named[key] = true
			b, err := r.blocker(repo, num, in)
			if err != nil {
				return err
			}
			if have[strings.ToLower(b.URL)] {
				continue
			}
			have[strings.ToLower(b.URL)] = true
			n.BlockedBy = append(n.BlockedBy, Blocker{URL: b.URL, Title: b.Title, Closed: b.Closed})
		}
	}
	return nil
}

func atoi(s string) int { n, _ := strconv.Atoi(s); return n }

// blocker finds an issue a body names: in the tree, else one light read, else an open stand-in.
func (r *treeReader) blocker(repo string, number int, in map[string]*TreeNode) (*TreeNode, error) {
	key := refKey(repo, number)
	if n := in[key]; n != nil {
		return n, nil
	}
	if n := r.looked[key]; n != nil {
		return n, nil
	}
	unreadable := &TreeNode{Number: number, Repo: repo, URL: fmt.Sprintf("https://%s/%s/issues/%d", r.c.host, repo, number)}
	if r.lookups >= maxLookups {
		r.looked[key] = unreadable
		return unreadable, nil
	}
	r.lookups++
	o, rp, _ := strings.Cut(repo, "/")
	n, err := r.light(o, rp, number)
	switch {
	case err != nil && r.ctx.Err() != nil:
		return nil, err
	case err != nil:
		n = unreadable
	}
	r.looked[key] = n
	return n, nil
}

func (r *treeReader) light(owner, repo string, number int) (*TreeNode, error) {
	ref := fmt.Sprintf("%s/%s#%d", owner, repo, number)
	data, err := r.c.graphql(r.ctx, r.token, ref, []string{"-f", "query=" + blockerQuery, "-f", "owner=" + owner,
		"-f", "repo=" + repo, "-F", "number=" + strconv.Itoa(number)})
	if err != nil {
		return nil, err
	}
	var d struct {
		Repository *struct {
			Issue *gqlNode `json:"issueOrPullRequest"`
		} `json:"repository"`
	}
	if json.Unmarshal(data, &d) != nil || d.Repository == nil || d.Repository.Issue == nil || d.Repository.Issue.URL == "" {
		return nil, r.c.notFound(ref)
	}
	g := d.Repository.Issue
	return &TreeNode{Number: number, Repo: repo, Title: g.Title, URL: g.URL, Closed: g.State != "OPEN"}, nil
}

// graphql runs one query and returns its data object. ref names the issue for the messages.
func (c *Client) graphql(ctx context.Context, token, ref string, vars []string) (json.RawMessage, error) {
	args := append([]string{"api", "graphql", "--hostname", c.host}, vars...)
	out, stderr, err := c.gh(ctx, args, c.tokenVar(), token)
	if err != nil && ctx.Err() != nil {
		return nil, ctx.Err()
	}
	if errors.Is(err, exec.ErrNotFound) || errors.Is(err, errTimeout) {
		return nil, c.unreachableErr()
	}
	if err != nil {
		msg := redact(strings.TrimSpace(stderr), token)
		if msg = strings.TrimPrefix(msg, "gh: "); msg == "" {
			msg = "gh exited"
			if ee := (*exec.ExitError)(nil); errors.As(err, &ee) {
				msg = fmt.Sprintf("gh exited with status %d", ee.ExitCode())
			}
		}
		return nil, c.treeError(ref, msg)
	}
	var body struct {
		Data   json.RawMessage `json:"data"`
		Errors []struct {
			Type    string `json:"type"`
			Message string `json:"message"`
		} `json:"errors"`
	}
	if json.Unmarshal([]byte(out), &body) != nil {
		return nil, errors.New("Tree read failed: GitHub's answer was not JSON")
	}
	// Errors beside data, such as a blocker in a repo the token can't see, leave the rest readable.
	if len(body.Data) == 0 || string(body.Data) == "null" {
		if len(body.Errors) > 0 {
			e := body.Errors[0]
			if e.Type == "NOT_FOUND" {
				return nil, c.notFound(ref)
			}
			return nil, c.treeError(ref, redact(e.Message, token))
		}
		return nil, errors.New("Tree read failed: GitHub's answer was not understood")
	}
	return body.Data, nil
}

func (c *Client) notFound(ref string) error {
	return fmt.Errorf("%s was not found, or you don't have access to it", ref)
}

// treeError turns gh's or GraphQL's message into one to show.
func (c *Client) treeError(ref, msg string) error {
	for _, m := range unreachable {
		if strings.Contains(msg, m) {
			return c.unreachableErr()
		}
	}
	lower := strings.ToLower(msg)
	switch {
	case strings.Contains(lower, "could not resolve") || strings.Contains(lower, "not_found") ||
		strings.Contains(lower, "not found") || strings.Contains(lower, "http 404"):
		return c.notFound(ref)
	case strings.Contains(lower, "http 403") || strings.Contains(lower, "forbidden") ||
		strings.Contains(lower, "saml") || strings.Contains(lower, "protected"):
		return fmt.Errorf("You don't have access to %s: %s", ref, msg)
	}
	return fmt.Errorf("Tree read failed: %s", msg)
}
