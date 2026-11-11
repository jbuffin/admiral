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
	Number    int
	Title     string
	URL       string
	Repo      string // nameWithOwner, which may differ from the parent's
	Closed    bool
	Labels    []string
	Assignees []string // logins; empty when nobody is assigned
	BlockedBy []Blocker
	Body      string
	Children  []*TreeNode
}

// maxTreeDepth is GitHub's limit on sub-issue nesting.
const maxTreeDepth = 8

// nodeFields is what every issue answers, a sub-issue or a search hit alike; each list is read up to
// 100 long and paged when GitHub says there is more.
const nodeFields = `__typename ... on Issue { number title url state body repository { nameWithOwner }
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
	r := &treeReader{c: c, ctx: ctx, token: token, seen: map[string]bool{}, looked: map[string]*TreeNode{}}
	root, err := r.issue(owner, repo, number, 0)
	if err != nil {
		return nil, err
	}
	if err := r.partOf(root); err != nil {
		return nil, err
	}
	return root, nil
}

type treeReader struct {
	c      *Client
	ctx    context.Context
	token  string
	seen   map[string]bool
	looked map[string]*TreeNode // blockers read outside the tree, by repo#number
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

// issue reads one issue, every page of its sub-issues, and descends into those that have their own.
func (r *treeReader) issue(owner, repo string, number, depth int) (*TreeNode, error) {
	first, err := r.page(owner, repo, number, "")
	if err != nil {
		return nil, err
	}
	r.seen[strings.ToLower(first.URL)] = true
	subs := first.SubIssues.Nodes
	for pi := first.SubIssues.PageInfo; pi.HasNextPage && pi.EndCursor != ""; {
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
	for i := range subs {
		s := &subs[i]
		if s.Number == 0 || s.URL == "" {
			continue
		}
		var child *TreeNode
		if s.SubIssues.TotalCount > 0 && depth < maxTreeDepth && !r.seen[strings.ToLower(s.URL)] {
			o, rp, _ := strings.Cut(s.Repository.NameWithOwner, "/")
			child, err = r.issue(o, rp, s.Number, depth+1)
		} else {
			child, err = r.finish(s)
		}
		if err != nil {
			return nil, err
		}
		node.Children = append(node.Children, child)
	}
	return node, nil
}

// finish turns a node into a tree node, reading the pages of labels and blockers past the first 100.
func (r *treeReader) finish(g *gqlNode) (*TreeNode, error) {
	n := g.treeNode()
	o, rp, _ := strings.Cut(g.Repository.NameWithOwner, "/")
	for pi := g.Labels.PageInfo; pi.HasNextPage && pi.EndCursor != ""; {
		d, err := r.extra(labelsQuery, o, rp, g.Number, pi.EndCursor)
		if err != nil {
			return nil, err
		}
		for _, l := range d.Labels.Nodes {
			n.Labels = append(n.Labels, l.Name)
		}
		pi = d.Labels.PageInfo
	}
	for pi := g.BlockedBy.PageInfo; pi.HasNextPage && pi.EndCursor != ""; {
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

// partOf adds the children whose body says "Part of" the root and that are not sub-issues, then the
// blockers their "Blocked by:" lines name. GitHub caps sub-issues at 100, so a long campaign has these.
func (r *treeReader) partOf(root *TreeNode) error {
	in := map[string]*TreeNode{}
	var index func(n *TreeNode)
	index = func(n *TreeNode) {
		in[refKey(n.Repo, n.Number)] = n
		for _, c := range n.Children {
			index(c)
		}
	}
	index(root)

	line := partOfLine(root)
	q := fmt.Sprintf(`repo:%s is:issue sort:created-asc "Part of #%d"`, root.Repo, root.Number)
	var added []*TreeNode
	for after := ""; ; {
		args := []string{"-f", "query=" + partOfQuery, "-f", "q=" + q}
		if after != "" {
			args = append(args, "-f", "after="+after)
		}
		data, err := r.c.graphql(r.ctx, r.token, root.Repo, args)
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
			if g.Typename != "Issue" || g.URL == "" || in[refKey(g.Repository.NameWithOwner, g.Number)] != nil || !line.MatchString(g.Body) {
				continue
			}
			n, err := r.finish(g)
			if err != nil {
				return err
			}
			in[refKey(n.Repo, n.Number)] = n
			added = append(added, n)
		}
		if pi := d.Search.PageInfo; !pi.HasNextPage || pi.EndCursor == "" {
			break
		} else {
			after = pi.EndCursor
		}
	}
	for _, n := range added {
		if err := r.bodyBlockers(n, in); err != nil {
			return err
		}
		root.Children = append(root.Children, n)
	}
	return nil
}

// partOfLine matches a body line that names the parent: #n, owner/repo#n or its URL.
func partOfLine(p *TreeNode) *regexp.Regexp {
	return regexp.MustCompile(fmt.Sprintf(`(?mi)^[ \t]*Part of[ \t]+(?:#%d|%s#%d|%s)[ \t\r]*$`,
		p.Number, regexp.QuoteMeta(p.Repo), p.Number, regexp.QuoteMeta(p.URL)))
}

var (
	blockedByLine = regexp.MustCompile(`(?mi)^[ \t]*Blocked by:?[ \t]*(.+)$`)
	issueRefs     = regexp.MustCompile(`https?://[^/\s]+/([A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+)/issues/(\d+)|([A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+)#(\d+)|#(\d+)`)
)

// bodyBlockers adds the blockers a child's "Blocked by:" lines name. One outside the tree is read
// so that an open blocker elsewhere still blocks; one that can't be read counts as open.
func (r *treeReader) bodyBlockers(n *TreeNode, in map[string]*TreeNode) error {
	have := map[string]bool{}
	for _, b := range n.BlockedBy {
		have[strings.ToLower(b.URL)] = true
	}
	for _, l := range blockedByLine.FindAllStringSubmatch(n.Body, -1) {
		for _, m := range issueRefs.FindAllStringSubmatch(l[1], -1) {
			repo, num := n.Repo, 0
			switch {
			case m[1] != "":
				repo, num = m[1], atoi(m[2])
			case m[3] != "":
				repo, num = m[3], atoi(m[4])
			default:
				num = atoi(m[5])
			}
			b, err := r.blocker(repo, num, in)
			if err != nil {
				return err
			}
			if b == nil || have[strings.ToLower(b.URL)] {
				continue
			}
			have[strings.ToLower(b.URL)] = true
			n.BlockedBy = append(n.BlockedBy, Blocker{URL: b.URL, Title: b.Title, Closed: b.Closed})
		}
	}
	return nil
}

func atoi(s string) int { n, _ := strconv.Atoi(s); return n }

func (r *treeReader) blocker(repo string, number int, in map[string]*TreeNode) (*TreeNode, error) {
	key := refKey(repo, number)
	if n := in[key]; n != nil {
		return n, nil
	}
	if n, ok := r.looked[key]; ok {
		return n, nil
	}
	o, rp, _ := strings.Cut(repo, "/")
	g, err := r.page(o, rp, number, "")
	var n *TreeNode
	switch {
	case err != nil && r.ctx.Err() != nil:
		return nil, err
	case err == nil:
		n = g.treeNode()
	default: // unreadable: keep it open, so the child stays blocked and names it
		n = &TreeNode{Number: number, Repo: repo, URL: fmt.Sprintf("https://%s/%s/issues/%d", r.c.host, repo, number)}
	}
	r.looked[key] = n
	return n, nil
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
