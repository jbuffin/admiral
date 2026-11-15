package github

import (
	"context"
	"errors"
)

// IssueRecord is what GitHub's REST "get an issue" says about an issue that Admiral needs to link it.
type IssueRecord struct {
	RESTID    int64 // the REST id, which is neither the issue number nor Item.ID
	ParentURL string
}

// CreatedIssue is an issue GitHub just created.
type CreatedIssue struct {
	Number int
	URL    string
}

var errNotImplemented = errors.New("not implemented")

func (c *Client) IssueRecord(ctx context.Context, owner, repo string, number int) (IssueRecord, error) {
	return IssueRecord{}, errNotImplemented
}

func (c *Client) CreateIssue(ctx context.Context, owner, repo, title, body string) (CreatedIssue, error) {
	return CreatedIssue{}, errNotImplemented
}

func (c *Client) AddSubIssue(ctx context.Context, owner, repo string, parent int, childRESTID int64) error {
	return errNotImplemented
}
