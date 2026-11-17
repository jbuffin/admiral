package github

import (
	"context"
	"errors"
)

// ReRequestReview is a stub: it requests nothing yet.
func (c *Client) ReRequestReview(ctx context.Context, prURL, login string) error {
	return errors.New("not implemented")
}
