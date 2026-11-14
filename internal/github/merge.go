package github

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strconv"
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
	return c.merge(ctx, "", nil, []string{"pr", "merge", url, "--" + method}, method)
}

// MergeStack runs `gh stack merge N --yes --merge-method METHOD` for GitHub's stack number N, in an empty folder of its
// own so gh-stack reads no repository's git config; N is an integer from GitHub, never text from a request.
func (c *Client) MergeStack(ctx context.Context, repo string, stackNumber int, method string) error {
	dir, err := os.MkdirTemp("", "admiral-stack-merge-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(dir)
	return c.merge(ctx, dir, c.stackEnv(repo), []string{"stack", "merge", strconv.Itoa(stackNumber), "--yes", "--merge-method", method}, method)
}

// stackEnv pins gh-stack to the repo and host given here, whatever the environment holds, and its git config can name no program to run.
func (c *Client) stackEnv(repo string) []string {
	return []string{
		"GH_HOST=" + c.host, "GH_REPO=" + c.host + "/" + repo,
		"GIT_CONFIG_COUNT=3",
		"GIT_CONFIG_KEY_0=core.fsmonitor", "GIT_CONFIG_VALUE_0=false",
		"GIT_CONFIG_KEY_1=core.hooksPath", "GIT_CONFIG_VALUE_1=/dev/null",
		"GIT_CONFIG_KEY_2=core.sshCommand", "GIT_CONFIG_VALUE_2=",
	}
}

func (c *Client) merge(ctx context.Context, dir string, extra, args []string, method string) error {
	switch method {
	case "merge", "squash", "rebase":
	default:
		return fmt.Errorf("unknown merge method %q", method)
	}
	token, err := c.Token(ctx)
	if err != nil {
		return err
	}
	_, stderr, err := c.ghWithin(ctx, MergeTimeout, dir, extra, args, c.tokenVar(), token)
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
