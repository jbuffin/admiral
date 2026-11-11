package github

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"regexp"
	"strings"

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

const prFields = "state reviewDecision mergeable statusCheckRollup { state }"

// PullRequests asks for every URL's state and chips in one aliased query. gh exits 1
// when some PR is unknown yet still prints the others under data, so data counts
// whatever the exit code. A URL GitHub answers NOT_FOUND for is left out; no data
// is a failed call.
func (c *Client) PullRequests(ctx context.Context, urls []string) (map[string]moor.Status, error) {
	var q strings.Builder
	q.WriteString("query {")
	aliases := map[string]string{}
	for i, u := range urls {
		m := pullURL.FindStringSubmatch(u)
		if m == nil {
			continue
		}
		alias := fmt.Sprintf("p%d", i)
		aliases[alias] = u
		fmt.Fprintf(&q, " %s: repository(owner: %q, name: %q) { pullRequest(number: %s) { %s } }", alias, m[1], m[2], m[3], prFields)
	}
	q.WriteString(" }")
	out := map[string]moor.Status{}
	if len(aliases) == 0 {
		return out, nil
	}
	token, err := c.Token(ctx)
	if err != nil {
		return nil, err
	}
	stdout, stderr, err := c.gh(ctx, []string{"api", "graphql", "--hostname", c.host, "-f", "query=" + q.String()}, c.tokenVar(), token)
	if err != nil && ctx.Err() != nil {
		return nil, ctx.Err()
	}
	var body struct {
		Data   map[string]*struct{ PullRequest *prNode } `json:"data"`
		Errors []struct {
			Type string `json:"type"`
		} `json:"errors"`
	}
	if json.Unmarshal([]byte(stdout), &body) != nil || body.Data == nil {
		if errors.Is(err, errTimeout) || errors.Is(err, exec.ErrNotFound) {
			return nil, c.unreachableErr()
		}
		msg := strings.TrimPrefix(redact(strings.TrimSpace(stderr), token), "gh: ")
		if msg == "" {
			msg = "GitHub's answer was not understood"
		}
		return nil, fmt.Errorf("pull request lookup failed: %s", msg)
	}
	for _, e := range body.Errors {
		if e.Type != "NOT_FOUND" {
			return nil, fmt.Errorf("pull request lookup failed: GitHub answered %s", e.Type)
		}
	}
	for alias, u := range aliases {
		if r := body.Data[alias]; r != nil && r.PullRequest != nil {
			n := r.PullRequest
			st := moor.Status{State: n.State, Review: n.ReviewDecision, Mergeable: n.Mergeable}
			if n.StatusCheckRollup != nil {
				st.Checks = n.StatusCheckRollup.State
			}
			out[u] = st
		}
	}
	return out, nil
}

type prNode struct {
	State             string `json:"state"`
	ReviewDecision    string `json:"reviewDecision"`
	Mergeable         string `json:"mergeable"`
	StatusCheckRollup *struct {
		State string `json:"state"`
	} `json:"statusCheckRollup"`
}
