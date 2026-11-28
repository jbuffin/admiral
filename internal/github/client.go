package github

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/jbuffin/admiral/internal/moor"
)

// DefaultHost is the host a client talks to when its settings name none.
const DefaultHost = "github.com"

// MaxItems caps one search's answer.
const MaxItems = 50

// CallTimeout is the default limit for one gh call; Admiral gives a request 10 seconds.
var CallTimeout = 8 * time.Second

// SearchTimeout is the limit for a background search, which reads up to MaxItems items with their rollups and timelines.
var SearchTimeout = 60 * time.Second

// KillGrace is how long a gh killed at its limit gets to close the pipes a child of it still holds.
var KillGrace = time.Second

const itemFields = "number title url createdAt bodyText author { login } repository { nameWithOwner url } labels(first: 100) { nodes { name } }"

// prStateFields are the PullRequest fields the chips, ready and the draft mark read, the poll's own plus isDraft and no more, so a search stays light.
const prStateFields = " state isDraft reviewDecision mergeable mergeStateStatus statusCheckRollup { state contexts { checkRunCountsByState { state count } statusContextCountsByState { state count } } } headRefOid viewerLatestReview { state commit { oid } }"

// searchQuery is GitHub's advanced search, the one github.com uses: it knows
// qualifiers such as is:blocked, and it reads AND, OR and parentheses.
var searchQuery = searchQueryWith("", reviewRequests)

// What happened to the viewer on an item: the timeline events that name them, with when. A mention names no one.
const (
	issueTimeline = " timelineItems(last: 20, itemTypes: [ASSIGNED_EVENT, MENTIONED_EVENT]) { nodes { __typename ... on AssignedEvent { createdAt actor { login } assignee { ... on User { login } } } ... on MentionedEvent { createdAt } } }"
	prTimeline    = " timelineItems(last: 20, itemTypes: [REVIEW_REQUESTED_EVENT, ASSIGNED_EVENT, MENTIONED_EVENT]) { nodes { __typename ... on ReviewRequestedEvent { createdAt actor { login } requestedReviewer { ... on User { login } } } ... on AssignedEvent { createdAt actor { login } assignee { ... on User { login } } } ... on MentionedEvent { createdAt } } }"
)

// reviewRequests is the viewer's review requests on a pull request; the connection stays at 10 to keep the query's cost low.
const reviewRequests = " timelineItems(last: 10, itemTypes: [REVIEW_REQUESTED_EVENT]) { nodes { __typename ... on ReviewRequestedEvent { createdAt requestedReviewer { ... on User { login } } } } }"

// prBranchFields are what chain detection reads: a pull request's branches and its repository's default branch.
const prBranchFields = " headRefName baseRefName repository { defaultBranchRef { name } }"

func searchQueryWith(issueExtra, prExtra string) string {
	return fmt.Sprintf("query($q: String!) { viewer { login } search(query: $q, type: ISSUE_ADVANCED, first: %d) { nodes { "+
		"... on Issue { %s subIssuesSummary { total } parent { number title url repository { nameWithOwner url } }%s } ... on PullRequest { %s%s%s stack { number size } stackEntry { position }%s } } } }", MaxItems, itemFields, issueExtra, itemFields, prStateFields, prBranchFields, prExtra)
}

// unreachable lists what gh prints when the network, not GitHub, failed. It
// prints the first for a DNS error and the rest raw from Go's transport.
var unreachable = []string{"error connecting to", "dial tcp", "i/o timeout", "TLS handshake", "x509:",
	"connection refused", "connection reset", "no route to host", "network is unreachable", "proxyconnect"}

var hasSort = regexp.MustCompile(`(?i)(^|\s)-?sort:`)

// defaultHotLabels mark an item hot when a fleet names none.
var defaultHotLabels = []string{"priority: high", "P0", "P1", "urgent"}

// Item is one issue or pull request a search found.
type Item struct {
	ID      string // owner/repo#number
	Title   string
	Text    string // the item's URL
	URL     string
	RepoURL string
	Repo    string // the repo's nameWithOwner, for Route(host, Repo)
	Labels  []string
	Hot     bool
	// SubIssues is how many sub-issues an issue has; 0 for a pull request.
	SubIssues int
	// Parent is an issue's GitHub parent; nil for no parent, one the viewer can't read, and a pull request.
	Parent *Parent
	// Stack is a pull request's GitHub stack; nil for no stack, one the viewer can't read, and an issue.
	Stack *Stack
	// Head and Base are a pull request's branches and DefaultBranch its repository's; empty for an issue and when GitHub answered none.
	Head, Base, DefaultBranch string
	// HeadSHA is the pull request's head commit; empty for an issue.
	HeadSHA string
	// PR is a pull request's state as the poll would serve it; nil for an issue.
	PR *moor.PR
	// Draft is true for a pull request still in draft; false for an issue.
	Draft bool
	// OpenedAt is when it was opened, Excerpt its body as one short line.
	OpenedAt time.Time
	Excerpt  string
	// FailingChecks counts a pull request's failing checks, ReviewRequestedAt is when the viewer was last asked to review
	// it, and ReviewedSincePush says the viewer's latest submitted review is on its head.
	FailingChecks     int
	ReviewRequestedAt time.Time
	ReviewedSincePush bool
	// Author is who opened the item. Actor is who last review-requested or assigned the viewer on it, and EventAt when
	// the latest such event (or mention) happened, when the search asked (SearchActing) and the timeline says; empty
	// and zero otherwise.
	Author  string
	Actor   string
	EventAt time.Time
}

// Parent is the issue an issue sits under, with the same id form as Item.ID.
type Parent struct {
	ID      string
	Title   string
	URL     string
	RepoURL string
}

// Stack is the GitHub stack a pull request sits in: its number, its URL, the pull request's layer and the stack's size.
type Stack struct {
	Number   int
	URL      string
	Position int
	Size     int
}

// The two ways a SignInError can come about.
const (
	KindSignedOut = "signed_out"
	KindGHMissing = "gh_missing"
)

// SignInError means gh is missing or signed out, as opposed to a search that failed.
type SignInError struct {
	Message string
	Kind    string // KindSignedOut or KindGHMissing
	Detail  string // what gh said, when it said anything
}

func (e *SignInError) Error() string { return e.Message }

// Client runs the gh CLI for one fleet's GitHub source.
type Client struct {
	host      string
	account   string
	hotLabels []string
	env       []string

	// Gh overrides the binary; empty means "gh" found on PATH.
	Gh string
	// Timeout is the per-call limit; zero means 8 seconds.
	Timeout time.Duration
}

// NewClient returns a client. env is the base environment for every gh call
// (os.Environ() or a fleet's); hotLabels are matched case-insensitively.
func NewClient(host, account string, hotLabels, env []string) *Client {
	lower := make([]string, len(hotLabels))
	for i, l := range hotLabels {
		lower[i] = strings.ToLower(l)
	}
	return &Client{host: host, account: account, hotLabels: lower, env: env}
}

// Host is the GitHub host, for Route(host, nameWithOwner).
func (c *Client) Host() string { return c.host }

// Token runs `gh auth token`. Errors: *SignInError when gh is missing or
// signed out; "GitHub took too long to answer" (not a *SignInError) on a timeout.
func (c *Client) Token(ctx context.Context) (string, error) {
	args := []string{"auth", "token", "-h", c.host}
	if c.account != "" {
		args = append(args, "-u", c.account)
	}
	out, stderr, err := c.gh(ctx, args, "", "")
	var token string
	switch {
	case errors.Is(err, exec.ErrNotFound):
		return "", &SignInError{Message: "Install the GitHub CLI (brew install gh), then run gh auth login", Kind: KindGHMissing}
	case ctx.Err() != nil: // the caller gave up: neither a sign-out nor a failure
		return "", ctx.Err()
	case errors.Is(err, errTimeout): // a locked keychain, say: not a sign-out
		return "", errSlow
	case err == nil:
		token = strings.TrimSpace(out)
	}
	if token == "" {
		return "", &SignInError{Message: "Run gh auth login -h " + c.host, Kind: KindSignedOut, Detail: strings.TrimSpace(stderr)}
	}
	return token, nil
}

// Search runs one GitHub search with token and returns at most MaxItems items.
// Its errors read as the message to show, with the token redacted.
func (c *Client) Search(ctx context.Context, token, query string) ([]Item, error) {
	items, _, err := c.search(ctx, token, query, searchQuery, "")
	return items, err
}

// SearchActing is Search that also reads the latest timeline event of the kind aimed at the viewer on each item (a
// review request, an assignment or a mention; any other kind reads none): its actor, when it names one, and its time.
// It returns the viewer's login.
func (c *Client) SearchActing(ctx context.Context, token, query, kind string) ([]Item, string, error) {
	return c.search(ctx, token, query, searchQueryWith(issueTimeline, prTimeline), kind)
}

// SearchViewer is Search that also returns the viewer's login: the signed-in account the search ran as.
func (c *Client) SearchViewer(ctx context.Context, token, query string) ([]Item, string, error) {
	return c.search(ctx, token, query, searchQuery, "")
}

func (c *Client) search(ctx context.Context, token, query, gql, actorKind string) ([]Item, string, error) {
	if !hasSort.MatchString(query) {
		query += " sort:updated-desc"
	}
	data, err := c.runQuery(ctx, token, gql, true, SearchTimeout, "q="+query)
	if err != nil {
		return nil, "", err
	}
	search, _ := data["search"].(map[string]any)
	nodes, ok := search["nodes"].([]any)
	if !ok {
		return nil, "", errNotUnderstood
	}
	viewerObj, _ := data["viewer"].(map[string]any)
	login, _ := viewerObj["login"].(string)
	var items []Item
	for _, n := range nodes {
		if it, ok := c.item(n, actorKind, login); ok {
			items = append(items, it)
		}
	}
	if len(items) > MaxItems {
		items = items[:MaxItems]
	}
	return items, login, nil
}

// IssueHit is one issue as the campaign search shows it.
type IssueHit struct {
	Number    int
	Title     string
	URL       string
	Repo      string // nameWithOwner
	Closed    bool
	SubIssues int // total sub-issues; 0 for a leaf
	SubClosed int
}

// lookupMax is how many issues one lookup reads, enough for a type-ahead list.
const lookupMax = 20

const lookupFields = "number title url state repository { nameWithOwner } subIssuesSummary { total completed }"

var lookupQuery = fmt.Sprintf("query($q: String!) { search(query: $q, type: ISSUE_ADVANCED, first: %d) { nodes { ... on Issue { %s } } } }", lookupMax, lookupFields)

const lookupOneQuery = `query($owner: String!, $repo: String!, $number: Int!) {
	repository(owner: $owner, name: $repo) { issueOrPullRequest(number: $number) { __typename ... on Issue { ` + lookupFields + ` } } } }`

type lookupNode struct {
	Typename   string `json:"__typename"`
	Number     int    `json:"number"`
	Title      string `json:"title"`
	URL        string `json:"url"`
	State      string `json:"state"`
	Repository struct {
		NameWithOwner string `json:"nameWithOwner"`
	} `json:"repository"`
	SubIssuesSummary *struct {
		Total     int `json:"total"`
		Completed int `json:"completed"`
	} `json:"subIssuesSummary"`
}

func (n lookupNode) hit() IssueHit {
	h := IssueHit{Number: n.Number, Title: n.Title, URL: n.URL, Repo: n.Repository.NameWithOwner, Closed: n.State == "CLOSED"}
	if n.SubIssuesSummary != nil {
		h.SubIssues, h.SubClosed = n.SubIssuesSummary.Total, n.SubIssuesSummary.Completed
	}
	return h
}

// SearchIssues runs one GitHub search light enough for a type-ahead: at most lookupMax issues in the order GitHub ranks
// them, under the quick-call limit and cancelled through ctx. Pull requests are left out.
func (c *Client) SearchIssues(ctx context.Context, token, query string) ([]IssueHit, error) {
	data, err := c.runQuery(ctx, token, lookupQuery, false, 0, "q="+query)
	if err != nil {
		return nil, err
	}
	search, _ := data["search"].(map[string]any)
	raw, ok := search["nodes"].([]any)
	if !ok {
		return nil, errNotUnderstood
	}
	var out []IssueHit
	for _, r := range raw {
		b, _ := json.Marshal(r)
		var n lookupNode
		if json.Unmarshal(b, &n) != nil || n.Number == 0 || n.Repository.NameWithOwner == "" {
			continue // a pull request answers an empty node
		}
		out = append(out, n.hit())
	}
	return out, nil
}

// LookupIssue reads one issue by number. It answers nil for a pull request and for an issue GitHub can't find.
func (c *Client) LookupIssue(ctx context.Context, token, owner, repo string, number int) (*IssueHit, error) {
	ref := fmt.Sprintf("%s/%s#%d", owner, repo, number)
	raw, err := c.graphql(ctx, token, ref, []string{"-f", "query=" + lookupOneQuery, "-f", "owner=" + owner, "-f", "repo=" + repo, "-F", "number=" + strconv.Itoa(number)})
	if IsNotFound(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var d struct {
		Repository *struct {
			Issue *lookupNode `json:"issueOrPullRequest"`
		} `json:"repository"`
	}
	if json.Unmarshal(raw, &d) != nil || d.Repository == nil || d.Repository.Issue == nil || d.Repository.Issue.Typename != "Issue" {
		return nil, nil
	}
	h := d.Repository.Issue.hit()
	return &h, nil
}

const countQuery = "query($q: String!) { search(query: $q, type: ISSUE_ADVANCED, first: 1) { issueCount } }"

// Count is GitHub's total for a search (its issueCount), not the capped list Search returns.
func (c *Client) Count(ctx context.Context, token, query string) (int, error) {
	data, err := c.runQuery(ctx, token, countQuery, false, 0, "q="+query)
	if err != nil {
		return 0, err
	}
	search, _ := data["search"].(map[string]any)
	n, ok := search["issueCount"].(float64)
	if !ok {
		return 0, errNotUnderstood
	}
	return int(n), nil
}

// Viewer is who the token signs in as, asked without a search.
func (c *Client) Viewer(ctx context.Context, token string) (string, error) {
	data, err := c.runQuery(ctx, token, "query { viewer { login } }", false, 0)
	if err != nil {
		return "", err
	}
	viewerObj, _ := data["viewer"].(map[string]any)
	login, _ := viewerObj["login"].(string)
	if login == "" {
		return "", errNotUnderstood
	}
	return login, nil
}

// runQuery runs one query with gh and returns its data object. limit is the call's time limit; zero means the client's quick-call limit. fields are further -f name=value pairs.
func (c *Client) runQuery(ctx context.Context, token, gql string, optional bool, limit time.Duration, fields ...string) (map[string]any, error) {
	args := []string{"api", "graphql", "--hostname", c.host, "-f", "query=" + gql}
	for _, f := range fields {
		args = append(args, "-f", f)
	}
	var out, stderr string
	var err error
	if limit > 0 {
		out, stderr, err = c.ghWithin(ctx, limit, "", nil, args, c.tokenVar(), token)
	} else {
		out, stderr, err = c.gh(ctx, args, c.tokenVar(), token)
	}
	if err != nil && ctx.Err() != nil {
		return nil, ctx.Err()
	}
	if errors.Is(err, exec.ErrNotFound) || errors.Is(err, errTimeout) {
		return nil, c.callFailed(err)
	}
	if err != nil && !(optional && onlyOptionalErrors(out)) {
		msg := redact(strings.TrimSpace(stderr), token)
		for _, m := range unreachable {
			if strings.Contains(msg, m) {
				return nil, c.unreachableErr()
			}
		}
		if msg = strings.TrimPrefix(msg, "gh: "); msg == "" {
			msg = "gh exited"
			if ee := (*exec.ExitError)(nil); errors.As(err, &ee) {
				msg = fmt.Sprintf("gh exited with status %d", ee.ExitCode())
			}
		}
		return nil, refusal(msg)
	}
	var body any
	if json.Unmarshal([]byte(out), &body) != nil {
		return nil, errors.New("Search failed: GitHub's answer was not JSON")
	}
	obj, _ := body.(map[string]any)
	if obj == nil {
		return nil, errNotUnderstood
	}
	if errs, _ := obj["errors"].([]any); len(errs) > 0 && !(optional && onlyOptionalErrors(out)) {
		first, _ := errs[0].(map[string]any)
		msg, _ := first["message"].(string)
		if msg == "" {
			msg = "unknown error"
		}
		return nil, refusal(redact(msg, token))
	}
	data, _ := obj["data"].(map[string]any)
	if data == nil {
		return nil, errNotUnderstood
	}
	return data, nil
}

// refusalError is GitHub or gh turning a query down; BadCredentials is a 401, a token GitHub no longer accepts.
type refusalError struct {
	msg            string
	badCredentials bool
}

func (e *refusalError) Error() string { return e.msg }

func refusal(msg string) error {
	low := strings.ToLower(msg)
	return &refusalError{"Search failed: " + msg, strings.Contains(low, "bad credentials") || strings.Contains(low, "http 401")}
}

// IsBadCredentials says GitHub refused the token itself, as a revoked or expired one is.
func IsBadCredentials(err error) bool {
	var r *refusalError
	return errors.As(err, &r) && r.badCredentials
}

var errNotUnderstood = errors.New("Search failed: GitHub's answer was not understood")

type unreachableError struct{ host string }

func (e *unreachableError) Error() string { return "Couldn't reach " + e.host }

// errSlow is a gh call that ran past its limit: GitHub or gh was slow, which says nothing about the network.
var errSlow = errors.New("GitHub took too long to answer")

// callFailed is the error for a gh call that did not run to the end: gh missing reads as unreachable, a call past its limit as slow.
func (c *Client) callFailed(err error) error {
	if errors.Is(err, errTimeout) {
		return errSlow
	}
	return c.unreachableErr()
}

// IsSlow says err is a call that ran out of time.
func IsSlow(err error) bool { return errors.Is(err, errSlow) }

// IsUnreachable says err is a call that failed on the network, not a refusal, a sign-out or a call that ran out of time.
func IsUnreachable(err error) bool {
	var u *unreachableError
	return errors.As(err, &u)
}

func (c *Client) unreachableErr() error { return &unreachableError{c.host} }

// redact hides token in text that may reach the screen.
func redact(text, token string) string {
	if token == "" {
		return text
	}
	return strings.ReplaceAll(text, token, "***")
}

// item reads one search node. A node of another type has none of the fields
// and is skipped.
func (c *Client) item(raw any, actorKind, viewer string) (Item, bool) {
	var n struct {
		prNode
		Number     *int    `json:"number"`
		Title      *string `json:"title"`
		URL        *string `json:"url"`
		Repository *struct {
			NameWithOwner    *string `json:"nameWithOwner"`
			URL              *string `json:"url"`
			DefaultBranchRef *struct {
				Name string `json:"name"`
			} `json:"defaultBranchRef"`
		} `json:"repository"`
		HeadRefName *string `json:"headRefName"`
		BaseRefName *string `json:"baseRefName"`
		Labels      *struct {
			Nodes *[]struct {
				Name *string `json:"name"`
			} `json:"nodes"`
		} `json:"labels"`
		SubIssuesSummary *struct {
			Total int `json:"total"`
		} `json:"subIssuesSummary"`
		Parent *struct {
			Number     *int    `json:"number"`
			Title      *string `json:"title"`
			URL        *string `json:"url"`
			Repository *struct {
				NameWithOwner *string `json:"nameWithOwner"`
				URL           *string `json:"url"`
			} `json:"repository"`
		} `json:"parent"`
		Stack *struct {
			Number *int `json:"number"`
			Size   *int `json:"size"`
		} `json:"stack"`
		StackEntry *struct {
			Position *int `json:"position"`
		} `json:"stackEntry"`
		Author        *struct{ Login string } `json:"author"`
		TimelineItems *struct {
			Nodes []struct {
				Typename  string    `json:"__typename"`
				CreatedAt time.Time `json:"createdAt"`
				Actor     *struct{ Login string }
				Assignee  *struct{ Login string }
				Reviewer  *struct{ Login string } `json:"requestedReviewer"`
			} `json:"nodes"`
		} `json:"timelineItems"`
	}
	b, _ := json.Marshal(raw)
	if json.Unmarshal(b, &n) != nil || n.Number == nil || n.Title == nil || n.URL == nil ||
		n.Repository == nil || n.Repository.NameWithOwner == nil || n.Repository.URL == nil ||
		n.Labels == nil || n.Labels.Nodes == nil {
		return Item{}, false
	}
	it := Item{ID: itemID(*n.Repository.NameWithOwner, *n.Number), Title: *n.Title,
		Text: *n.URL, URL: *n.URL, RepoURL: *n.Repository.URL, Repo: *n.Repository.NameWithOwner,
		Labels: []string{}}
	if n.Author != nil {
		it.Author = n.Author.Login
	}
	it.OpenedAt, it.Excerpt = n.CreatedAt, Excerpt(n.BodyText)
	if n.TimelineItems != nil {
		want := map[string]string{"review_requested": "ReviewRequestedEvent", "assigned": "AssignedEvent", "mentioned": "MentionedEvent"}[actorKind]
		for _, ev := range n.TimelineItems.Nodes { // the latest event of the kind aimed at the viewer wins
			if ev.Typename != want || ev.CreatedAt.Before(it.EventAt) {
				continue
			}
			target := ev.Assignee
			switch ev.Typename {
			case "ReviewRequestedEvent":
				target = ev.Reviewer
			case "MentionedEvent": // names no mentioner, and the timeline does not say whom it mentioned
				it.EventAt, it.Actor = ev.CreatedAt, ""
				continue
			}
			if target != nil && strings.EqualFold(target.Login, viewer) {
				it.EventAt, it.Actor = ev.CreatedAt, ""
				if ev.Actor != nil {
					it.Actor = ev.Actor.Login
				}
			}
		}
	}
	if n.TimelineItems != nil {
		for _, ev := range n.TimelineItems.Nodes {
			if ev.Typename == "ReviewRequestedEvent" && ev.Reviewer != nil && strings.EqualFold(ev.Reviewer.Login, viewer) && ev.CreatedAt.After(it.ReviewRequestedAt) {
				it.ReviewRequestedAt = ev.CreatedAt
			}
		}
	}
	if n.SubIssuesSummary != nil {
		it.SubIssues = n.SubIssuesSummary.Total
	}
	if p := n.Parent; p != nil && n.State == "" && p.Number != nil && p.Title != nil && p.URL != nil &&
		p.Repository != nil && p.Repository.NameWithOwner != nil && p.Repository.URL != nil {
		it.Parent = &Parent{ID: itemID(*p.Repository.NameWithOwner, *p.Number), Title: *p.Title, URL: *p.URL, RepoURL: *p.Repository.URL}
	}
	if n.State != "" { // only the pull request fragment asks for state
		pr := moor.View(it.URL, n.status(), true)
		it.PR = &pr
		it.Draft = n.IsDraft
		if n.HeadRefName != nil && n.BaseRefName != nil && n.Repository.DefaultBranchRef != nil {
			it.Head, it.Base, it.DefaultBranch = *n.HeadRefName, *n.BaseRefName, n.Repository.DefaultBranchRef.Name
		}
		it.HeadSHA = n.HeadRefOid
		it.FailingChecks = n.failingChecks()
		it.ReviewedSincePush = n.reviewedSincePush()
	}
	if s, e := n.Stack, n.StackEntry; s != nil && e != nil && s.Number != nil && s.Size != nil && e.Position != nil && n.State != "" {
		if host, repo, _, ok := moor.ParsePR(it.URL); ok {
			it.Stack = &Stack{Number: *s.Number, URL: fmt.Sprintf("https://%s/%s/stack/%d", host, repo, *s.Number), Position: *e.Position, Size: *s.Size}
		}
	}
	for _, l := range *n.Labels.Nodes {
		if l.Name == nil {
			return Item{}, false
		}
		it.Labels = append(it.Labels, *l.Name)
		for _, h := range c.hotLabels {
			if strings.EqualFold(*l.Name, h) {
				it.Hot = true
			}
		}
	}
	return it, true
}

// onlyOptionalErrors reports an answer that carries data and whose every error is on a parent or GitHub stack field:
// GitHub answers one the token can't read with null and an error, and gh may exit non-zero for it, so the item has
// none and the rest of the answer stands.
func onlyOptionalErrors(out string) bool {
	var body struct {
		Data   map[string]any `json:"data"`
		Errors []struct {
			Path []any `json:"path"`
		} `json:"errors"`
	}
	if json.Unmarshal([]byte(out), &body) != nil || body.Data == nil || len(body.Errors) == 0 {
		return false
	}
	for _, e := range body.Errors {
		if !optionalPath(e.Path) {
			return false
		}
	}
	return true
}

func optionalPath(path []any) bool {
	for _, p := range path {
		if p == "parent" || p == "stack" || p == "stackEntry" {
			return true
		}
	}
	return false
}

// itemID is an issue's or pull request's id, the form Item.ID and Parent.ID share.
func itemID(nameWithOwner string, number int) string {
	return fmt.Sprintf("%s#%d", nameWithOwner, number)
}

// errTimeout means a gh call ran past the client's own limit.
var errTimeout = errors.New("gh timed out")

// tokenVar is the variable gh reads the token from on this host: GH_TOKEN for
// github.com and GHE Cloud, GH_ENTERPRISE_TOKEN for a GHE Server.
func (c *Client) tokenVar() string {
	if c.host == DefaultHost || strings.HasSuffix(c.host, ".ghe.com") {
		return "GH_TOKEN"
	}
	return "GH_ENTERPRISE_TOKEN"
}

// gh runs the gh CLI and returns its output; a failed run is exec.ErrNotFound,
// errTimeout on the client's own timeout, or the caller's ctx.Err().
func (c *Client) gh(ctx context.Context, args []string, tokenVar, token string) (stdout, stderr string, err error) {
	timeout := c.Timeout
	if timeout == 0 {
		timeout = CallTimeout
	}
	return c.ghWithin(ctx, timeout, "", nil, args, tokenVar, token)
}

// ghWithin runs gh in dir, or in Admiral's own directory when dir is empty, with extra after the usual environment.
func (c *Client) ghWithin(ctx context.Context, timeout time.Duration, dir string, extra, args []string, tokenVar, token string) (stdout, stderr string, err error) {
	return c.ghStdin(ctx, timeout, dir, extra, args, nil, 0, tokenVar, token)
}

// ghStdin is ghWithin with stdin, nil for none, and a limit on stdout's bytes, 0 for none; a longer answer is errTooBig.
func (c *Client) ghStdin(ctx context.Context, timeout time.Duration, dir string, extra, args []string, stdin []byte, limit int, tokenVar, token string) (stdout, stderr string, err error) {
	bin, err := c.binary()
	if err != nil {
		return "", "", err
	}
	tctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	cmd := exec.CommandContext(tctx, bin, args...)
	cmd.Dir = dir
	cmd.Env = append(c.environ(tokenVar, token), extra...)
	if stdin != nil {
		cmd.Stdin = bytes.NewReader(stdin)
	}
	var out, errOut bytes.Buffer
	capped := &capWriter{w: &out, limit: limit}
	cmd.Stdout, cmd.Stderr = capped, &errOut
	cmd.WaitDelay = KillGrace
	err = cmd.Run()
	if err != nil {
		if ctx.Err() != nil {
			err = ctx.Err()
		} else if tctx.Err() != nil {
			err = errTimeout
		}
	}
	if err == nil && capped.over {
		err = errTooBig
	}
	return out.String(), errOut.String(), err
}

// errTooBig means gh's answer ran past the limit the caller set.
var errTooBig = errors.New("GitHub's answer is too large")

// capWriter keeps the first limit bytes and drops the rest, so gh is never cut off by a failed write.
type capWriter struct {
	w     *bytes.Buffer
	limit int // 0 keeps everything
	over  bool
}

func (c *capWriter) Write(p []byte) (int, error) {
	if c.limit > 0 && c.w.Len()+len(p) > c.limit {
		c.over = true
		c.w.Write(p[:c.limit-c.w.Len()])
		return len(p), nil
	}
	return c.w.Write(p)
}

// binary finds gh on the client's own PATH first, since a fleet's env may set one.
func (c *Client) binary() (string, error) {
	if c.Gh != "" {
		return c.Gh, nil
	}
	return FindGh(c.env)
}

// FindGh finds gh on the PATH in env, which a fleet may set, else on Admiral's own.
func FindGh(env []string) (string, error) {
	for i := len(env) - 1; i >= 0; i-- {
		path, ok := strings.CutPrefix(env[i], "PATH=")
		if !ok {
			continue
		}
		for _, dir := range filepath.SplitList(path) {
			if dir == "" {
				continue
			}
			p := filepath.Join(dir, "gh")
			if info, err := os.Stat(p); err == nil && !info.IsDir() && info.Mode()&0o111 != 0 {
				return p, nil
			}
		}
		break
	}
	return exec.LookPath("gh")
}

// environ drops every inherited GitHub token variable, which could override the fleet's account.
func (c *Client) environ(tokenVar, token string) []string {
	env := make([]string, 0, len(c.env)+3)
	for _, kv := range c.env {
		switch name, _, _ := strings.Cut(kv, "="); name {
		case "GH_TOKEN", "GITHUB_TOKEN", "GH_ENTERPRISE_TOKEN", "GITHUB_ENTERPRISE_TOKEN",
			"GH_NO_UPDATE_NOTIFIER", "GH_PROMPT_DISABLED":
		default:
			env = append(env, kv)
		}
	}
	env = append(env, "GH_NO_UPDATE_NOTIFIER=1", "GH_PROMPT_DISABLED=1")
	if token != "" {
		env = append(env, tokenVar+"="+token)
	}
	return env
}
