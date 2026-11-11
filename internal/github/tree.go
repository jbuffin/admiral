package github

import (
	"context"
	"errors"
)

// Blocker is an issue that blocks another, in or outside the tree.
type Blocker struct {
	URL    string
	Title  string
	Closed bool
}

// TreeNode is one issue of a campaign's tree, with its sub-issues in order.
type TreeNode struct {
	Number    int
	Title     string
	URL       string
	Repo      string // nameWithOwner, which may differ from the parent's
	Closed    bool
	Labels    []string
	Assignees []string // logins; empty when nobody is assigned
	BlockedBy []Blocker
	Body      string
	Children  []*TreeNode
}

// Tree reads the issue owner/repo#number and everything under it. It never writes.
func (c *Client) Tree(ctx context.Context, token, owner, repo string, number int) (*TreeNode, error) {
	return nil, errors.New("tree read not implemented")
}
