package github

import (
	"context"
	"encoding/json"
	"errors"
	"net/url"
	"strings"
)

// ResolveCommit turns a branch, tag or SHA into the 40-hex commit it names (commits/{ref}, the ref's "/" escaped as %2F).
func (c *Client) ResolveCommit(ctx context.Context, owner, repo, ref string) (string, error) {
	base, err := repoEndpoint(owner, repo)
	if err != nil {
		return "", err
	}
	if ref == "" {
		return "", errors.New("a ref is required")
	}
	out, err := c.rest(ctx, "GET", base+"/commits/"+strings.ReplaceAll(url.PathEscape(ref), "/", "%2F"), nil)
	if err != nil {
		return "", err
	}
	var got struct {
		SHA string `json:"sha"`
	}
	if json.Unmarshal(out, &got) != nil || !commitSHA.MatchString(got.SHA) {
		return "", errors.New("GitHub's answer was not a commit")
	}
	return got.SHA, nil
}

// ReadFileAt reads one file at a commit (contents/{path}?ref={sha}); ReadFile reads it at the default branch.
func (c *Client) ReadFileAt(ctx context.Context, owner, repo, path, sha string) (File, error) {
	if !commitSHA.MatchString(sha) {
		return File{}, errors.New("a commit SHA is required")
	}
	endpoint, err := contentsEndpoint(owner, repo, path)
	if err != nil {
		return File{}, err
	}
	return c.readFile(ctx, endpoint+"?ref="+sha)
}

// TagAt returns the first of the repo's first 100 tags (tags?per_page=100) whose commit is sha, or "" when none is.
func (c *Client) TagAt(ctx context.Context, owner, repo, sha string) (string, error) {
	base, err := repoEndpoint(owner, repo)
	if err != nil {
		return "", err
	}
	out, err := c.rest(ctx, "GET", base+"/tags?per_page=100", nil)
	if err != nil {
		return "", err
	}
	var got []struct {
		Name   string `json:"name"`
		Commit struct {
			SHA string `json:"sha"`
		} `json:"commit"`
	}
	if json.Unmarshal(out, &got) != nil {
		return "", errors.New("GitHub's answer was not a tag list")
	}
	for _, t := range got {
		if t.Commit.SHA == sha {
			return t.Name, nil
		}
	}
	return "", nil
}
