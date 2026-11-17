package github

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

// ReRequestTimeout bounds the requestReviews call; the lookup before it has the default gh limit.
var ReRequestTimeout = 20 * time.Second

// ReRequestReview asks GitHub to request a review from login, who reviewed the PR at prURL, keeping the requests already there.
func (c *Client) ReRequestReview(ctx context.Context, prURL, login string) error {
	m := pullURL.FindStringSubmatch(prURL)
	if m == nil {
		return errors.New("review request failed: not a pull request URL")
	}
	// Actor has no id, so each review author names its own through the User and Bot fragments.
	q := fmt.Sprintf(`query { d: repository(owner: %q, name: %q) { pullRequest(number: %s) { id
 reviews(last: %d) { nodes { author { __typename login ... on User { id } ... on Bot { id } } } } } } }`, m[1], m[2], m[3], maxReviews)
	data, notFound, gone, err := c.batchQuery(ctx, "review request", q, map[string]string{"d": prURL})
	if err != nil {
		return err
	}
	var r *struct {
		PullRequest *struct {
			ID      string `json:"id"`
			Reviews nodes[struct {
				Author *struct {
					Type  string `json:"__typename"`
					Login string `json:"login"`
					ID    string `json:"id"`
				} `json:"author"`
			}] `json:"reviews"`
		} `json:"pullRequest"`
	}
	if len(notFound)+len(gone) > 0 || json.Unmarshal(data["d"], &r) != nil || r == nil || r.PullRequest == nil || r.PullRequest.ID == "" {
		return errors.New("review request failed: GitHub has no such pull request")
	}
	field, id := "", ""
loop:
	for _, n := range r.PullRequest.Reviews.Nodes {
		if a := n.Author; a != nil && a.Login == login {
			switch a.Type {
			case "User":
				field, id = "userIds", a.ID
			case "Bot":
				field, id = "botIds", a.ID
			default:
				return fmt.Errorf("review request failed: %s is a %s, and GitHub's API only re-requests a User or a Bot", login, a.Type)
			}
			break loop
		}
	}
	if id == "" {
		return fmt.Errorf("review request failed: %s has no review in the last %d of this pull request", login, maxReviews)
	}
	mutation := fmt.Sprintf(`mutation { requestReviews(input: {pullRequestId: %q, %s: [%q], union: true}) { pullRequest { id } } }`, r.PullRequest.ID, field, id)
	return c.runReRequest(ctx, mutation)
}

func (c *Client) runReRequest(ctx context.Context, mutation string) error {
	token, err := c.Token(ctx)
	if err != nil {
		return err
	}
	args := []string{"api", "graphql", "--hostname", c.host, "-f", "query=" + mutation}
	stdout, stderr, err := c.ghWithin(ctx, ReRequestTimeout, "", nil, args, c.tokenVar(), token)
	switch {
	case err == nil:
		return nil
	case ctx.Err() != nil:
		return ctx.Err()
	case errors.Is(err, errTimeout):
		return errors.New("the review request did not finish in time; look at the pull request on GitHub before asking again")
	case errors.Is(err, exec.ErrNotFound):
		return c.unreachableErr()
	}
	// On a graphql refusal gh prints the JSON body on stdout and GitHub's message on stderr.
	msg := mutationMessage("", stderr, token)
	if msg == "" {
		msg = mutationMessage(stdout, "", token)
	}
	if msg == "" {
		msg = "gh could not request the review"
	}
	return errors.New(strings.TrimSpace(msg))
}
