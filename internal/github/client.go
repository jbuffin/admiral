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
	"strings"
	"time"
)

// DefaultHost is the host a client talks to when its settings name none.
const DefaultHost = "github.com"

// MaxItems caps one search's answer.
const MaxItems = 50

// ghTimeout is the default limit for one gh call; Admiral gives a request 10 seconds.
const ghTimeout = 8 * time.Second

const itemFields = "number title url repository { nameWithOwner url } labels(first: 100) { nodes { name } }"

// searchQuery is GitHub's advanced search, the one github.com uses: it knows
// qualifiers such as is:blocked, and it reads AND, OR and parentheses.
var searchQuery = fmt.Sprintf("query($q: String!) { search(query: $q, type: ISSUE_ADVANCED, first: %d) { nodes { "+
	"... on Issue { %s subIssuesSummary { total } } ... on PullRequest { %s } } } }", MaxItems, itemFields, itemFields)

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
}

// SignInError means gh is missing or signed out, as opposed to a search that failed.
type SignInError struct{ Message string }

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
// signed out; "Couldn't reach <host>" (not a *SignInError) on a timeout.
func (c *Client) Token(ctx context.Context) (string, error) {
	args := []string{"auth", "token", "-h", c.host}
	if c.account != "" {
		args = append(args, "-u", c.account)
	}
	out, _, err := c.gh(ctx, args, "", "")
	var token string
	switch {
	case errors.Is(err, exec.ErrNotFound):
		return "", &SignInError{"Install the GitHub CLI (brew install gh), then run gh auth login"}
	case ctx.Err() != nil: // the caller gave up: neither a sign-out nor a failure
		return "", ctx.Err()
	case errors.Is(err, errTimeout): // a locked keychain, say: not a sign-out
		return "", c.unreachableErr()
	case err == nil:
		token = strings.TrimSpace(out)
	}
	if token == "" {
		return "", &SignInError{"Run gh auth login -h " + c.host}
	}
	return token, nil
}

// Search runs one GitHub search with token and returns at most MaxItems items.
// Its errors read as the message to show, with the token redacted.
func (c *Client) Search(ctx context.Context, token, query string) ([]Item, error) {
	if !hasSort.MatchString(query) {
		query += " sort:updated-desc"
	}
	out, stderr, err := c.gh(ctx, []string{"api", "graphql", "--hostname", c.host, "-f", "query=" + searchQuery, "-f", "q=" + query}, c.tokenVar(), token)
	if err != nil && ctx.Err() != nil {
		return nil, ctx.Err()
	}
	if errors.Is(err, exec.ErrNotFound) || errors.Is(err, errTimeout) {
		return nil, c.unreachableErr()
	}
	if err != nil {
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
		return nil, fmt.Errorf("Search failed: %s", msg)
	}
	var body any
	if json.Unmarshal([]byte(out), &body) != nil {
		return nil, errors.New("Search failed: GitHub's answer was not JSON")
	}
	obj, _ := body.(map[string]any)
	if obj == nil {
		return nil, errNotUnderstood
	}
	if errs, _ := obj["errors"].([]any); len(errs) > 0 {
		first, _ := errs[0].(map[string]any)
		msg, _ := first["message"].(string)
		if msg == "" {
			msg = "unknown error"
		}
		return nil, fmt.Errorf("Search failed: %s", redact(msg, token))
	}
	data, _ := obj["data"].(map[string]any)
	search, _ := data["search"].(map[string]any)
	nodes, ok := search["nodes"].([]any)
	if !ok {
		return nil, errNotUnderstood
	}
	var items []Item
	for _, n := range nodes {
		if it, ok := c.item(n); ok {
			items = append(items, it)
		}
	}
	if len(items) > MaxItems {
		items = items[:MaxItems]
	}
	return items, nil
}

var errNotUnderstood = errors.New("Search failed: GitHub's answer was not understood")

func (c *Client) unreachableErr() error { return fmt.Errorf("Couldn't reach %s", c.host) }

// redact hides token in text that may reach the screen.
func redact(text, token string) string {
	if token == "" {
		return text
	}
	return strings.ReplaceAll(text, token, "***")
}

// item reads one search node. A node of another type has none of the fields
// and is skipped.
func (c *Client) item(raw any) (Item, bool) {
	var n struct {
		Number     *int    `json:"number"`
		Title      *string `json:"title"`
		URL        *string `json:"url"`
		Repository *struct {
			NameWithOwner *string `json:"nameWithOwner"`
			URL           *string `json:"url"`
		} `json:"repository"`
		Labels *struct {
			Nodes *[]struct {
				Name *string `json:"name"`
			} `json:"nodes"`
		} `json:"labels"`
		SubIssuesSummary *struct {
			Total int `json:"total"`
		} `json:"subIssuesSummary"`
	}
	b, _ := json.Marshal(raw)
	if json.Unmarshal(b, &n) != nil || n.Number == nil || n.Title == nil || n.URL == nil ||
		n.Repository == nil || n.Repository.NameWithOwner == nil || n.Repository.URL == nil ||
		n.Labels == nil || n.Labels.Nodes == nil {
		return Item{}, false
	}
	it := Item{ID: fmt.Sprintf("%s#%d", *n.Repository.NameWithOwner, *n.Number), Title: *n.Title,
		Text: *n.URL, URL: *n.URL, RepoURL: *n.Repository.URL, Repo: *n.Repository.NameWithOwner,
		Labels: []string{}}
	if n.SubIssuesSummary != nil {
		it.SubIssues = n.SubIssuesSummary.Total
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
		timeout = ghTimeout
	}
	return c.ghWithin(ctx, timeout, args, tokenVar, token)
}

func (c *Client) ghWithin(ctx context.Context, timeout time.Duration, args []string, tokenVar, token string) (stdout, stderr string, err error) {
	bin, err := c.binary()
	if err != nil {
		return "", "", err
	}
	tctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	cmd := exec.CommandContext(tctx, bin, args...)
	cmd.Env = c.environ(tokenVar, token)
	var out, errOut bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errOut
	cmd.WaitDelay = time.Second
	err = cmd.Run()
	if err != nil {
		if ctx.Err() != nil {
			err = ctx.Err()
		} else if tctx.Err() != nil {
			err = errTimeout
		}
	}
	return out.String(), errOut.String(), err
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
