package github

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"strings"
)

// IssueRecord is what GitHub's REST "get an issue" says about an issue that Admiral needs to link it.
type IssueRecord struct {
	RESTID      int64 // the REST id, which is neither the issue number nor Item.ID
	ParentURL   string
	PullRequest bool // GitHub's issue endpoint also answers for a pull request
}

// CreatedIssue is an issue GitHub just created.
type CreatedIssue struct {
	Number int
	URL    string
}

// IssueRecord reads an issue's REST id and its parent's URL, empty when it has none.
func (c *Client) IssueRecord(ctx context.Context, owner, repo string, number int) (IssueRecord, error) {
	out, err := c.rest(ctx, "GET", fmt.Sprintf("repos/%s/%s/issues/%d", owner, repo, number), nil)
	if err != nil {
		return IssueRecord{}, err
	}
	var got struct {
		ID     int64   `json:"id"`
		Parent *string `json:"parent_issue_url"`
		PR     any     `json:"pull_request"`
	}
	if json.Unmarshal(out, &got) != nil || got.ID == 0 {
		return IssueRecord{}, errors.New("GitHub's answer was not an issue")
	}
	rec := IssueRecord{RESTID: got.ID, PullRequest: got.PR != nil}
	if got.Parent != nil {
		rec.ParentURL = *got.Parent
	}
	return rec, nil
}

// CreateIssue files a new issue; title and body travel on stdin, never in args.
func (c *Client) CreateIssue(ctx context.Context, owner, repo, title, body string) (CreatedIssue, error) {
	out, err := c.rest(ctx, "POST", fmt.Sprintf("repos/%s/%s/issues", owner, repo), map[string]string{"title": title, "body": body})
	if err != nil {
		return CreatedIssue{}, err
	}
	var got struct {
		Number  int    `json:"number"`
		HTMLURL string `json:"html_url"`
	}
	if json.Unmarshal(out, &got) != nil || got.Number == 0 || got.HTMLURL == "" {
		return CreatedIssue{}, errors.New("GitHub's answer was not an issue")
	}
	return CreatedIssue{Number: got.Number, URL: got.HTMLURL}, nil
}

// AddSubIssue links the issue with childRESTID under parent, without replace_parent, so a child that has a parent is refused.
func (c *Client) AddSubIssue(ctx context.Context, owner, repo string, parent int, childRESTID int64) error {
	_, err := c.rest(ctx, "POST", fmt.Sprintf("repos/%s/%s/issues/%d/sub_issues", owner, repo, parent), map[string]int64{"sub_issue_id": childRESTID})
	return err
}

// rest runs one gh api call as the client's own account, with any JSON body on stdin.
func (c *Client) rest(ctx context.Context, method, endpoint string, body any) ([]byte, error) {
	token, err := c.Token(ctx)
	if err != nil {
		return nil, err
	}
	args := []string{"api", "--method", method, "--hostname", c.host}
	var stdin []byte
	if body != nil {
		stdin, _ = json.Marshal(body)
		args = append(args, "--input", "-")
	}
	args = append(args, endpoint)
	timeout := c.Timeout
	if timeout == 0 {
		timeout = ghTimeout
	}
	out, stderr, err := c.ghStdin(ctx, timeout, "", nil, args, stdin, c.tokenVar(), token)
	switch {
	case err == nil:
		return []byte(out), nil
	case ctx.Err() != nil:
		return nil, ctx.Err()
	case errors.Is(err, exec.ErrNotFound), errors.Is(err, errTimeout):
		return nil, c.callFailed(err)
	}
	msg := strings.TrimPrefix(redact(strings.TrimSpace(stderr), token), "gh: ")
	if msg == "" {
		msg = err.Error()
	}
	return nil, fmt.Errorf("%s %s failed: %s", method, endpoint, msg)
}
