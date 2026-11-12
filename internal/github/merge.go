package github

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"time"

	"github.com/jbuffin/admiral/internal/moor"
)

// MergeTimeout is how long one merge may run before its result counts as unknown.
var MergeTimeout = 2 * time.Minute

// DefaultMergeMethod is the repository's default merge method as the viewer sees it: merge, squash or rebase.
func (c *Client) DefaultMergeMethod(ctx context.Context, nameWithOwner string) (string, error) {
	owner, name, ok := strings.Cut(nameWithOwner, "/")
	if !ok || owner == "" || name == "" {
		return "", fmt.Errorf("%q is not a repository", nameWithOwner)
	}
	token, err := c.Token(ctx)
	if err != nil {
		return "", err
	}
	q := fmt.Sprintf("query { repository(owner: %q, name: %q) { viewerDefaultMergeMethod } }", owner, name)
	out, stderr, err := c.gh(ctx, []string{"api", "graphql", "--hostname", c.host, "-f", "query=" + q}, c.tokenVar(), token)
	if err != nil {
		if ctx.Err() != nil {
			return "", ctx.Err()
		}
		if errors.Is(err, errTimeout) || errors.Is(err, exec.ErrNotFound) {
			return "", c.unreachableErr()
		}
		return "", fmt.Errorf("default merge method lookup failed: %s", strings.TrimPrefix(redact(strings.TrimSpace(stderr), token), "gh: "))
	}
	var body struct {
		Data struct {
			Repository *struct {
				Method string `json:"viewerDefaultMergeMethod"`
			} `json:"repository"`
		} `json:"data"`
	}
	if json.Unmarshal([]byte(out), &body) != nil || body.Data.Repository == nil {
		return "", errors.New("default merge method lookup failed: GitHub's answer was not understood")
	}
	switch m := strings.ToLower(body.Data.Repository.Method); m {
	case "merge", "squash", "rebase":
		return m, nil
	}
	return "", fmt.Errorf("default merge method lookup failed: GitHub answered %q", body.Data.Repository.Method)
}

// MergePR runs `gh pr merge URL --METHOD`: no --admin and no --auto, so it merges only what GitHub lets through now.
func (c *Client) MergePR(ctx context.Context, url, method string) error {
	return c.merge(ctx, "", []string{"pr", "merge", url, "--" + method}, method)
}

// MergeStack runs `gh stack merge --yes --merge-method METHOD` in dir with no target, so gh-stack merges the current
// branch's whole stack; a number would let it pick another stack.
func (c *Client) MergeStack(ctx context.Context, dir, method string) error {
	return c.merge(ctx, dir, []string{"stack", "merge", "--yes", "--merge-method", method}, method)
}

func (c *Client) merge(ctx context.Context, dir string, args []string, method string) error {
	switch method {
	case "merge", "squash", "rebase":
	default:
		return fmt.Errorf("unknown merge method %q", method)
	}
	token, err := c.Token(ctx)
	if err != nil {
		return err
	}
	_, stderr, err := c.ghWithin(ctx, MergeTimeout, dir, args, c.tokenVar(), token)
	switch {
	case err == nil:
		return nil
	case ctx.Err() != nil:
		return ctx.Err()
	case errors.Is(err, errTimeout):
		return moor.ErrMergeTimeout
	case errors.Is(err, exec.ErrNotFound):
		return c.unreachableErr()
	}
	msg := strings.TrimPrefix(redact(strings.TrimSpace(stderr), token), "gh: ")
	if msg == "" {
		msg = "gh could not merge the pull request"
	}
	return errors.New(msg)
}

// StackPRs is the pull request URL of each layer of the stack checked out in dir, bottom first, from `gh stack view
// --json`; a layer with no pull request is "". Keys it does not know are ignored.
func (c *Client) StackPRs(ctx context.Context, dir string) ([]string, error) {
	token, err := c.Token(ctx)
	if err != nil {
		return nil, err
	}
	timeout := c.Timeout
	if timeout == 0 {
		timeout = ghTimeout
	}
	out, stderr, err := c.ghWithin(ctx, timeout, dir, []string{"stack", "view", "--json"}, c.tokenVar(), token)
	if err != nil {
		switch {
		case ctx.Err() != nil:
			return nil, ctx.Err()
		case errors.Is(err, errTimeout), errors.Is(err, exec.ErrNotFound):
			return nil, c.unreachableErr()
		}
		msg := strings.TrimPrefix(redact(strings.TrimSpace(stderr), token), "gh: ")
		if msg == "" {
			msg = "gh could not read the stack"
		}
		return nil, errors.New(msg)
	}
	var view struct {
		Branches []struct {
			PR *struct {
				URL string `json:"url"`
			} `json:"pr"`
		} `json:"branches"`
	}
	if json.Unmarshal([]byte(out), &view) != nil || len(view.Branches) == 0 {
		return nil, errors.New("gh stack view's answer was not understood")
	}
	urls := make([]string, len(view.Branches))
	for i, b := range view.Branches {
		if b.PR != nil {
			urls[i] = b.PR.URL
		}
	}
	return urls, nil
}
