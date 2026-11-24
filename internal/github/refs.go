package github

import (
	"context"
	"errors"
)

var errNotBuilt = errors.New("not built")

// ResolveCommit turns a branch, tag or SHA into the 40-hex commit it names (commits/{ref}, the ref's "/" escaped as %2F).
func (c *Client) ResolveCommit(ctx context.Context, owner, repo, ref string) (string, error) {
	return "", errNotBuilt
}

// ReadFileAt reads one file at a commit (contents/{path}?ref={sha}); ReadFile reads it at the default branch.
func (c *Client) ReadFileAt(ctx context.Context, owner, repo, path, sha string) (File, error) {
	return File{}, errNotBuilt
}

// TagAt returns the first of the repo's first 100 tags (tags?per_page=100) whose commit is sha, or "" when none is.
func (c *Client) TagAt(ctx context.Context, owner, repo, sha string) (string, error) {
	return "", errNotBuilt
}
