package github

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
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
