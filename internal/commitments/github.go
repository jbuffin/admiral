package commitments

import (
	"context"
	"time"

	"github.com/jbuffin/admiral/internal/delay"
	"github.com/jbuffin/admiral/internal/moor"
)

// GHPoll is how often the runner asks GitHub about the PRs and issues its commitments wait on.
var GHPoll = delay.From("ADMIRAL_COMMITMENT_GH_POLL", 2*time.Minute)

// GitHub is the part of *github.Client the runner polls with: one batched call per host.
type GitHub interface {
	// PullRequests leaves out a PR GitHub answers NOT_FOUND for; a *moor.Unanswered beside the answer is a repository it could not look in.
	PullRequests(ctx context.Context, urls []string) (map[string]moor.Status, error)
	// IssueStates maps each issue URL to OPEN or CLOSED, leaving out an issue GitHub answers NOT_FOUND for, with the same *moor.Unanswered rule.
	IssueStates(ctx context.Context, urls []string) (map[string]string, error)
}
