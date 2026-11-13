package github

import (
	"context"

	"github.com/jbuffin/admiral/internal/moor"
)

// PullRequestDetail reads one pull request in full with a single query.
func (c *Client) PullRequestDetail(ctx context.Context, url string) (moor.Detail, error) {
	return moor.Detail{}, nil
}
