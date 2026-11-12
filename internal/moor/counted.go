package moor

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/jbuffin/admiral/internal/delay"
)

// RetryWindow bounds how long a failing GitHub is retried before the ship sails off.
var RetryWindow = delay.From("ADMIRAL_MOOR_RETRY", 8*time.Second)

// GitHub is the fleet's built-in GitHub client.
type GitHub interface {
	Host() string
	// OpenPRs maps each URL GitHub knows to whether it is open; a URL left out
	// is one GitHub could not confirm.
	OpenPRs(ctx context.Context, urls []string) (map[string]bool, error)
	// PullRequests asks for every URL in one call. A URL GitHub answers NOT_FOUND
	// for is left out; an error is a failed call, not an answer, except an *Unanswered
	// beside a good answer for the PRs not named in it.
	PullRequests(ctx context.Context, urls []string) (map[string]Status, error)
	// DefaultMergeMethod is the repository's default merge method: merge, squash or rebase.
	DefaultMergeMethod(ctx context.Context, nameWithOwner string) (string, error)
	// MergePR merges one pull request with the method. Only the engineer's Merge act calls it. An error is gh's
	// message; ErrMergeTimeout says gh ran past its limit, so the merge may have happened.
	MergePR(ctx context.Context, url, method string) error
	// StackPRs is each layer of the stack checked out in dir, bottom first, as the checkout's own record has it; gh runs
	// against repo (OWNER/NAME) on this client's host, whatever the checkout's remotes say.
	StackPRs(ctx context.Context, dir, repo string) ([]StackLayer, error)
	// MergeStack merges the whole stack checked out in dir with the method, naming no target. Its errors are MergePR's.
	MergeStack(ctx context.Context, dir, repo, method string) error
}

// StackLayer is one layer of a local stack; a field the record lacks is zero.
type StackLayer struct {
	URL    string // "" for a layer with no pull request
	Number int
	Head   string // the branch's commit SHA
}

// ErrMergeTimeout is a merge whose result is unknown.
var ErrMergeTimeout = errors.New("the merge did not finish in time, so its result is unknown")

// Check is what counting a ship's PRs needs.
type Check struct {
	Fleet  string
	GitHub GitHub // nil when the fleet has no GitHub source
	Route  func(host, nameWithOwner string) string
}

// Counted returns the PRs that count towards mooring: GitHub confirms them open,
// they are on the client's host and they route to the ship's fleet.
func Counted(ctx context.Context, c Check, prs []string) []string {
	if c.GitHub == nil || c.Route == nil {
		return nil
	}
	var candidates []string
	for _, u := range prs {
		m := prURL.FindStringSubmatch(u)
		if m == nil || !strings.EqualFold(m[1], c.GitHub.Host()) || c.Route(m[1], m[2]+"/"+m[3]) != c.Fleet {
			continue
		}
		candidates = append(candidates, u)
	}
	if len(candidates) == 0 {
		return nil
	}
	ctx, cancel := context.WithTimeout(ctx, RetryWindow)
	defer cancel()
	pause := min(RetryWindow/5, 500*time.Millisecond)
	for {
		open, err := c.GitHub.OpenPRs(ctx, candidates)
		if err == nil {
			var out []string
			for _, u := range candidates {
				if open[u] {
					out = append(out, u)
				}
			}
			return out
		}
		select {
		case <-ctx.Done():
			return nil
		case <-time.After(pause):
		}
	}
}
