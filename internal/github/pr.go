package github

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"regexp"
	"strings"
	"time"

	"github.com/jbuffin/admiral/internal/moor"
)

// PRState is a pull request's state as GitHub reports it: OPEN, CLOSED or MERGED.
// Its error is a failed lookup, not a pull request GitHub does not know: that
// one has no entry.
func (c *Client) PRState(ctx context.Context, token, url string) (state string, found bool, err error) {
	out, stderr, err := c.gh(ctx, []string{"pr", "view", url, "--json", "state"}, c.tokenVar(), token)
	if err != nil && ctx.Err() != nil {
		return "", false, ctx.Err()
	}
	if err != nil {
		msg := redact(strings.TrimSpace(stderr), token)
		if strings.Contains(msg, "Could not resolve to a PullRequest") || strings.Contains(msg, "no pull requests found") {
			return "", false, nil
		}
		if errors.Is(err, errTimeout) {
			return "", false, c.unreachableErr()
		}
		return "", false, fmt.Errorf("pull request lookup failed: %s", strings.TrimPrefix(msg, "gh: "))
	}
	var body struct {
		State string `json:"state"`
	}
	if json.Unmarshal([]byte(out), &body) != nil || body.State == "" {
		return "", false, errors.New("pull request lookup failed: GitHub's answer was not understood")
	}
	return body.State, true, nil
}

// OpenPRs maps each pull request URL that GitHub knows to whether it is open; a
// URL it does not know is left out. Any failed lookup fails the whole call.
func (c *Client) OpenPRs(ctx context.Context, urls []string) (map[string]bool, error) {
	token, err := c.Token(ctx)
	if err != nil {
		return nil, err
	}
	open := map[string]bool{}
	for _, u := range urls {
		state, found, err := c.PRState(ctx, token, u)
		if err != nil {
			return nil, err
		}
		if found {
			open[u] = state == "OPEN"
		}
	}
	return open, nil
}

var pullURL = regexp.MustCompile(`^https://[^/\s]+/([^/\s"\\]+)/([^/\s"\\]+)/pull/([0-9]+)$`)

// Bounds on one poll's answer, so a busy PR keeps the query small. Bodies are cut to maxBody bytes.
const (
	maxReviews        = 20
	maxThreads        = 20
	maxThreadComments = 10
	maxComments       = 20
	maxRequests       = 20
	maxContexts       = 50
	maxBody           = 4096
)

var actor = "author { __typename login }"

var prFields = fmt.Sprintf(`state author { __typename login } createdAt reviewDecision mergeable statusCheckRollup { state } headRefOid mergeStateStatus number
 baseRef { branchProtectionRule { requiresStrictStatusChecks } rules(first: 20) { nodes { type parameters { ... on RequiredStatusChecksParameters { strictRequiredStatusChecksPolicy } } } } }
 reviews(last: %d) { nodes { id commit { oid } %s state submittedAt body url comments { totalCount } } }
 reviewThreads(last: %d) { nodes { id comments(last: %d) { nodes { id %s createdAt body url } } } }
 comments(last: %d) { nodes { id %s createdAt body url } }
 reviewRequests(first: %d) { nodes { requestedReviewer { __typename ... on User { login } ... on Bot { login } ... on Team { slug } } } }
 timelineItems(last: %d, itemTypes: [REVIEW_REQUESTED_EVENT]) { nodes { ... on ReviewRequestedEvent { requestedReviewer { __typename ... on User { login } ... on Bot { login } ... on Team { slug } } } } }
 commits(last: 1) { nodes { commit { oid statusCheckRollup { contexts(first: %d) { nodes { __typename
  ... on CheckRun { name conclusion isRequired(pullRequestNumber: %%d) checkSuite { app { slug } workflowRun { id } } }
  ... on StatusContext { context state isRequired(pullRequestNumber: %%d) } } } } } } }`,
	maxReviews, actor, maxThreads, maxThreadComments, actor, maxComments, actor, maxRequests, maxRequests, maxContexts)

// PullRequests asks for every URL's state and chips in one aliased query. gh exits 1
// when some PR is unknown yet still prints the others under data, so data counts
// whatever the exit code. A URL GitHub answers NOT_FOUND for is left out; no data
// is a failed call, and a repository GitHub cannot find leaves its PRs unanswered: a *moor.Unanswered
// beside the answer for the rest.
func (c *Client) PullRequests(ctx context.Context, urls []string) (map[string]moor.Status, error) {
	out, _, err := c.PullRequestsOrGone(ctx, urls)
	return out, err
}

// PullRequestsOrGone is PullRequests that also names the URLs GitHub answered NOT_FOUND for; a URL in neither the
// answer nor gone (nor a *moor.Unanswered) was not answered.
func (c *Client) PullRequestsOrGone(ctx context.Context, urls []string) (map[string]moor.Status, []string, error) {
	var q strings.Builder
	q.WriteString("query { viewer { login }")
	aliases := map[string]string{}
	for i, u := range urls {
		m := pullURL.FindStringSubmatch(u)
		if m == nil {
			continue
		}
		alias := fmt.Sprintf("p%d", i)
		aliases[alias] = u
		fmt.Fprintf(&q, " %s: repository(owner: %q, name: %q) { pullRequest(number: %s) { %s } }", alias, m[1], m[2], m[3], strings.ReplaceAll(prFields, "%d", m[3]))
	}
	q.WriteString(" }")
	out := map[string]moor.Status{}
	if len(aliases) == 0 {
		return out, nil, nil
	}
	data, notFound, goneAliases, err := c.batchQuery(ctx, "pull request", q.String(), aliases)
	if err != nil {
		return nil, nil, err
	}
	var gone []string
	for _, alias := range goneAliases {
		gone = append(gone, aliases[alias])
	}
	var unanswered []string
	for _, alias := range notFound {
		unanswered = append(unanswered, aliases[alias])
	}
	var viewer struct {
		Login string `json:"login"`
	}
	_ = json.Unmarshal(data["viewer"], &viewer)
	for alias, u := range aliases {
		var r *struct{ PullRequest *prNode }
		if json.Unmarshal(data[alias], &r) == nil && r != nil && r.PullRequest != nil {
			st := r.PullRequest.status()
			st.Viewer = viewer.Login
			out[u] = st
		}
	}
	if len(unanswered) > 0 {
		return out, gone, &moor.Unanswered{URLs: unanswered}
	}
	return out, gone, nil
}

type gqlActor struct {
	Type  string `json:"__typename"`
	Login string `json:"login"`
	Slug  string `json:"slug"`
}

func (a *gqlActor) actor() moor.Actor {
	if a == nil {
		return moor.Actor{}
	}
	if a.Login == "" {
		return moor.Actor{Type: a.Type, Login: a.Slug}
	}
	return moor.Actor{Type: a.Type, Login: a.Login}
}

type gqlComment struct {
	ID        string    `json:"id"`
	Author    *gqlActor `json:"author"`
	CreatedAt time.Time `json:"createdAt"`
	Body      string    `json:"body"`
	URL       string    `json:"url"`
}

type gqlRequest struct {
	RequestedReviewer *gqlActor `json:"requestedReviewer"`
}

type nodes[T any] struct {
	Nodes []T `json:"nodes"`
}

type prNode struct {
	State             string    `json:"state"`
	Author            *gqlActor `json:"author"`
	CreatedAt         time.Time `json:"createdAt"`
	ReviewDecision    string    `json:"reviewDecision"`
	Mergeable         string    `json:"mergeable"`
	MergeStateStatus  string    `json:"mergeStateStatus"`
	HeadRefOid        string    `json:"headRefOid"`
	StatusCheckRollup *struct {
		State string `json:"state"`
	} `json:"statusCheckRollup"`
	BaseRef *struct {
		BranchProtectionRule *struct {
			RequiresStrictStatusChecks bool `json:"requiresStrictStatusChecks"`
		} `json:"branchProtectionRule"`
		Rules *nodes[struct {
			Type       string `json:"type"`
			Parameters struct {
				Strict bool `json:"strictRequiredStatusChecksPolicy"`
			} `json:"parameters"`
		}] `json:"rules"`
	} `json:"baseRef"`
	Reviews nodes[struct {
		gqlComment
		State       string    `json:"state"`
		SubmittedAt time.Time `json:"submittedAt"`
		Commit      *struct {
			Oid string `json:"oid"`
		} `json:"commit"`
		Comments struct {
			TotalCount int `json:"totalCount"`
		} `json:"comments"`
	}] `json:"reviews"`
	ReviewThreads nodes[struct {
		ID       string            `json:"id"`
		Comments nodes[gqlComment] `json:"comments"`
	}] `json:"reviewThreads"`
	Comments       nodes[gqlComment] `json:"comments"`
	ReviewRequests nodes[gqlRequest] `json:"reviewRequests"`
	TimelineItems  nodes[gqlRequest] `json:"timelineItems"`
	Commits        nodes[struct {
		Commit struct {
			Oid               string `json:"oid"`
			StatusCheckRollup *struct {
				Contexts nodes[struct {
					Type       string `json:"__typename"`
					Name       string `json:"name"`
					Conclusion string `json:"conclusion"`
					Context    string `json:"context"`
					State      string `json:"state"`
					IsRequired bool   `json:"isRequired"`
					CheckSuite *struct {
						App         *struct{ Slug string } `json:"app"`
						WorkflowRun *struct{ ID string }   `json:"workflowRun"`
					} `json:"checkSuite"`
				}] `json:"contexts"`
			} `json:"statusCheckRollup"`
		} `json:"commit"`
	}] `json:"commits"`
}

func cut(s string) string {
	if len(s) <= maxBody {
		return s
	}
	return strings.ToValidUTF8(s[:maxBody], "") + "\n[cut]"
}

func (n *prNode) status() moor.Status {
	st := moor.Status{State: n.State, Review: n.ReviewDecision, Mergeable: n.Mergeable, MergeState: n.MergeStateStatus, Head: n.HeadRefOid,
		Author: n.Author.actor().Login, CreatedAt: n.CreatedAt}
	if n.StatusCheckRollup != nil {
		st.Checks = n.StatusCheckRollup.State
	}
	if b := n.BaseRef; b != nil {
		if b.BranchProtectionRule != nil && b.BranchProtectionRule.RequiresStrictStatusChecks {
			st.StrictBase = true
		}
		if b.Rules != nil {
			for _, r := range b.Rules.Nodes {
				if r.Type == "REQUIRED_STATUS_CHECKS" && r.Parameters.Strict {
					st.StrictBase = true
				}
			}
		}
	}
	for _, r := range n.Reviews.Nodes {
		rv := moor.Review{ID: r.ID, Author: r.Author.actor(), State: r.State, SubmittedAt: r.SubmittedAt,
			Body: cut(r.Body), URL: r.URL, Comments: r.Comments.TotalCount}
		if r.Commit != nil {
			rv.Commit = r.Commit.Oid
		}
		st.Reviews = append(st.Reviews, rv)
	}
	toComment := func(c gqlComment) moor.Comment {
		return moor.Comment{ID: c.ID, Author: c.Author.actor(), CreatedAt: c.CreatedAt, Body: cut(c.Body), URL: c.URL}
	}
	for _, t := range n.ReviewThreads.Nodes {
		for _, c := range t.Comments.Nodes {
			tc := toComment(c)
			tc.Thread = t.ID
			st.ThreadComments = append(st.ThreadComments, tc)
		}
	}
	for _, c := range n.Comments.Nodes {
		st.Comments = append(st.Comments, toComment(c))
	}
	seen := map[moor.Actor]bool{}
	for _, list := range [][]gqlRequest{n.ReviewRequests.Nodes, n.TimelineItems.Nodes} {
		for _, r := range list {
			if r.RequestedReviewer == nil {
				continue
			}
			if a := r.RequestedReviewer.actor(); !seen[a] {
				seen[a] = true
				st.Requested = append(st.Requested, a)
			}
		}
	}
	for _, c := range n.Commits.Nodes {
		if c.Commit.StatusCheckRollup == nil {
			continue
		}
		for _, x := range c.Commit.StatusCheckRollup.Contexts.Nodes {
			cc := moor.CheckContext{Kind: x.Type, Required: x.IsRequired}
			switch x.Type {
			case "CheckRun":
				cc.Name, cc.State = x.Name, x.Conclusion
				if x.CheckSuite != nil {
					if x.CheckSuite.App != nil {
						cc.AppSlug = x.CheckSuite.App.Slug
					}
					cc.HasWorkflowRun = x.CheckSuite.WorkflowRun != nil
				}
			case "StatusContext":
				cc.Name, cc.State = x.Context, x.State
			default:
				continue
			}
			st.Contexts = append(st.Contexts, cc)
		}
	}
	return st
}

// batchQuery runs one aliased GraphQL query. gh exits 1 when some item is unknown yet still prints the others under
// data, so data counts whatever the exit code. notFound lists the aliases whose repository itself is NOT_FOUND; an
// item-level NOT_FOUND is a null under its alias, and gone lists those aliases: GitHub said the item does not exist.
// what names the lookup in an error.
func (c *Client) batchQuery(ctx context.Context, what, query string, aliases map[string]string) (data map[string]json.RawMessage, notFound, gone []string, err error) {
	token, err := c.Token(ctx)
	if err != nil {
		return nil, nil, nil, err
	}
	stdout, stderr, err := c.gh(ctx, []string{"api", "graphql", "--hostname", c.host, "-f", "query=" + query}, c.tokenVar(), token)
	if err != nil && ctx.Err() != nil {
		return nil, nil, nil, ctx.Err()
	}
	var body struct {
		Data   map[string]json.RawMessage `json:"data"`
		Errors []struct {
			Type string `json:"type"`
			Path []any  `json:"path"`
		} `json:"errors"`
	}
	if json.Unmarshal([]byte(stdout), &body) != nil || body.Data == nil {
		if errors.Is(err, errTimeout) || errors.Is(err, exec.ErrNotFound) {
			return nil, nil, nil, c.unreachableErr()
		}
		msg := strings.TrimPrefix(redact(strings.TrimSpace(stderr), token), "gh: ")
		if msg == "" {
			msg = "GitHub's answer was not understood"
		}
		return nil, nil, nil, fmt.Errorf("%s lookup failed: %s", what, msg)
	}
	// A NOT_FOUND ending at the item is one GitHub does not have; one on the repository itself says nothing about
	// its items (a token that cannot see it, say), so they stay unanswered.
	for _, e := range body.Errors {
		if e.Type != "NOT_FOUND" {
			return nil, nil, nil, fmt.Errorf("%s lookup failed: GitHub answered %s", what, e.Type)
		}
		if len(e.Path) == 0 {
			continue
		}
		if alias, _ := e.Path[0].(string); aliases[alias] != "" {
			if len(e.Path) == 1 {
				notFound = append(notFound, alias)
			} else {
				gone = append(gone, alias)
			}
		}
	}
	return body.Data, notFound, gone, nil
}

var issueURL = regexp.MustCompile(`^https://[^/\s]+/([^/\s"\\]+)/([^/\s"\\]+)/issues/([0-9]+)$`)

// IssueStates maps each issue URL GitHub knows to OPEN or CLOSED, in one aliased query; a URL it answers NOT_FOUND for
// is left out, and a *moor.Unanswered beside the answer names the issues in a repository it could not look in.
func (c *Client) IssueStates(ctx context.Context, urls []string) (map[string]string, error) {
	out, _, err := c.IssueStatesOrGone(ctx, urls)
	return out, err
}

// IssueStatesOrGone is IssueStates that also names the URLs GitHub answered NOT_FOUND for.
func (c *Client) IssueStatesOrGone(ctx context.Context, urls []string) (map[string]string, []string, error) {
	var q strings.Builder
	q.WriteString("query {")
	aliases := map[string]string{}
	for i, u := range urls {
		m := issueURL.FindStringSubmatch(u)
		if m == nil {
			continue
		}
		alias := fmt.Sprintf("i%d", i)
		aliases[alias] = u
		fmt.Fprintf(&q, " %s: repository(owner: %q, name: %q) { issue(number: %s) { state } }", alias, m[1], m[2], m[3])
	}
	q.WriteString(" }")
	out := map[string]string{}
	if len(aliases) == 0 {
		return out, nil, nil
	}
	data, notFound, goneAliases, err := c.batchQuery(ctx, "issue", q.String(), aliases)
	if err != nil {
		return nil, nil, err
	}
	var gone []string
	for _, alias := range goneAliases {
		gone = append(gone, aliases[alias])
	}
	for alias, u := range aliases {
		var r *struct{ Issue *struct{ State string } }
		if json.Unmarshal(data[alias], &r) == nil && r != nil && r.Issue != nil && r.Issue.State != "" {
			out[u] = r.Issue.State
		}
	}
	if len(notFound) > 0 {
		var un []string
		for _, alias := range notFound {
			un = append(un, aliases[alias])
		}
		return out, gone, &moor.Unanswered{URLs: un}
	}
	return out, gone, nil
}
