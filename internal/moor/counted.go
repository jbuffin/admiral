package moor

import (
	"context"
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
	// for is left out; an error is a failed call, not an answer.
	PullRequests(ctx context.Context, urls []string) (map[string]Status, error)
}

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
