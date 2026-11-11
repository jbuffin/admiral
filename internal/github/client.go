package github

import (
	"context"
	"time"
)

// DefaultHost is the host a client talks to when its settings name none.
const DefaultHost = "github.com"

// MaxItems caps one search's answer.
const MaxItems = 50

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
	return &Client{host: host, account: account, hotLabels: hotLabels, env: env}
}

// Host is the GitHub host, for Route(host, nameWithOwner).
func (c *Client) Host() string { return "" }

// Token runs `gh auth token`. Errors: *SignInError when gh is missing or
// signed out; "Couldn't reach <host>" (not a *SignInError) on a timeout.
func (c *Client) Token(ctx context.Context) (string, error) { return "", nil }

// Search runs one GitHub search with token and returns at most MaxItems items.
func (c *Client) Search(ctx context.Context, token, query string) ([]Item, error) {
	return nil, nil
}
