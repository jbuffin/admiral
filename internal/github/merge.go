package github

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"regexp"
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
			return "", c.callFailed(err)
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

// UpdateBranch runs `gh pr update-branch URL`: the merge method, no --rebase, so the head keeps its commits.
func (c *Client) UpdateBranch(ctx context.Context, url string) error {
	return c.runMutation(ctx, "", nil, []string{"pr", "update-branch", url}, "gh could not update the branch")
}

// MergeStack runs `gh stack merge N --yes --merge-method METHOD` for GitHub's stack number N, in an empty folder of its
// own so gh-stack reads no repository's git config; N is an integer from GitHub, never text from a request.
func (c *Client) MergeStack(ctx context.Context, repo string, stackNumber int, method string) error {
	return c.stackRun(ctx, repo, []string{"stack", "merge", strconv.Itoa(stackNumber), "--yes", "--merge-method", method}, "gh could not merge the pull request")
}

// LinkStack runs `gh stack link --base BASE N...` with numbers of PRs GitHub answered in repo, which every one shares, bottom to top.
// Without --base gh-stack asks git for the default branch, and the empty folder has none; a base other than the bottom PR's
// own would retarget it. No --open: it would take drafts out of draft.
func (c *Client) LinkStack(ctx context.Context, repo, base string, numbers []int) error {
	if base == "" || strings.HasPrefix(base, "-") {
		return fmt.Errorf("cannot link a stack on base branch %q", base)
	}
	args := []string{"stack", "link", "--base", base}
	for _, n := range numbers {
		args = append(args, strconv.Itoa(n))
	}
	return c.stackRun(ctx, repo, args, "gh could not link the pull requests")
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
	return c.runMutation(ctx, dir, extra, args, "gh could not merge the pull request")
}

// stackRun runs a gh stack command in a fresh empty folder with the repo pinned.
func (c *Client) stackRun(ctx context.Context, repo string, args []string, fallback string) error {
	dir, err := os.MkdirTemp("", "admiral-stack-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(dir)
	return c.runMutation(ctx, dir, c.stackEnv(repo), args, fallback)
}

// runMutation runs a gh command that changes GitHub; a run past MergeTimeout is a result nobody knows.
func (c *Client) runMutation(ctx context.Context, dir string, extra, args []string, fallback string) error {
	token, err := c.Token(ctx)
	if err != nil {
		return err
	}
	stdout, stderr, err := c.ghWithin(ctx, MergeTimeout, dir, extra, args, c.tokenVar(), token)
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
	msg := mutationMessage(stdout, stderr, token)
	if msg == "" {
		msg = fallback
	}
	return errors.New(msg)
}

// mutationMessageCap bounds what a failed gh command may put on the screen and in the log.
const mutationMessageCap = 4000

var ansiEscape = regexp.MustCompile("\x1b(?:\\[[0-9;?]*[ -/]*[@-~]|\\][^\x07\x1b]*(?:\x07|\x1b\\\\))")

// mutationMessage is everything a failed gh command printed, stdout first since gh-stack puts its reason there: the token
// stripped of escapes (so one cannot split the token), redacted, cut to mutationMessageCap.
func mutationMessage(stdout, stderr, token string) string {
	var parts []string
	for _, out := range []string{stdout, stderr} {
		out = strings.TrimSpace(redact(ansiEscape.ReplaceAllString(out, ""), token))
		if out != "" {
			parts = append(parts, strings.TrimPrefix(out, "gh: "))
		}
	}
	msg := strings.Join(parts, "\n")
	if len(msg) > mutationMessageCap {
		msg = strings.ToValidUTF8(msg[:mutationMessageCap-len("…")], "") + "…"
	}
	return msg
}

// StackOpenPRs is the open layers of GitHub's stack number in owner/repo, bottom first, as PR URLs on this client's host.
// found is false when GitHub answers 404 for the stack; any other failed call is an error.
func (c *Client) StackOpenPRs(ctx context.Context, owner, repo string, number int) ([]string, bool, error) {
	token, err := c.Token(ctx)
	if err != nil {
		return nil, false, err
	}
	endpoint := fmt.Sprintf("repos/%s/%s/stacks/%d", owner, repo, number)
	out, stderr, err := c.gh(ctx, []string{"api", "--hostname", c.host, endpoint}, c.tokenVar(), token)
	if err != nil {
		switch {
		case ctx.Err() != nil:
			return nil, false, ctx.Err()
		case errors.Is(err, errTimeout), errors.Is(err, exec.ErrNotFound):
			return nil, false, c.callFailed(err)
		}
		msg := strings.TrimPrefix(redact(strings.TrimSpace(stderr), token), "gh: ")
		if strings.Contains(msg, "HTTP 404") {
			return nil, false, nil
		}
		if msg == "" {
			msg = "gh could not read the stack"
		}
		return nil, false, errors.New("stack lookup failed: " + msg)
	}
	var body struct {
		PullRequests []struct {
			Number int    `json:"number"`
			State  string `json:"state"`
		} `json:"pull_requests"`
	}
	if json.Unmarshal([]byte(out), &body) != nil || len(body.PullRequests) == 0 {
		return nil, false, errors.New("stack lookup failed: GitHub's answer was not understood")
	}
	var urls []string
	for _, p := range body.PullRequests {
		if p.State == "open" && p.Number > 0 {
			urls = append(urls, fmt.Sprintf("https://%s/%s/%s/pull/%d", c.host, owner, repo, p.Number))
		}
	}
	return urls, true, nil
}
