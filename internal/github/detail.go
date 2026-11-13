package github

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/jbuffin/admiral/internal/moor"
)

var detailFields = fmt.Sprintf(`number title body state isDraft headRefName baseRefName reviewDecision mergeable mergeStateStatus
 statusCheckRollup { state }
 reviews(last: %d) { nodes { author { __typename login } state } }
 reviewThreads(last: %d) { nodes { isResolved path line comments(last: %d) { nodes { author { __typename login } createdAt body url } } } }
 comments(last: %d) { nodes { author { __typename login } createdAt body url } }
 reviewRequests(first: %d) { nodes { requestedReviewer { __typename ... on User { login } ... on Bot { login } ... on Team { slug } } } }
 commits(last: 1) { nodes { commit { statusCheckRollup { contexts(first: %d) { nodes { __typename
  ... on CheckRun { name conclusion detailsUrl }
  ... on StatusContext { context state targetUrl } } } } } } }
 stack { number size baseRefName entries(first: %d) { nodes { position pullRequest { number url state headRefName } } } }`,
	maxReviews, maxThreads, maxThreadComments, maxComments, maxRequests, maxContexts, maxStackEntries)

const maxStackEntries = 50

type prDetailNode struct {
	Number            int    `json:"number"`
	Title             string `json:"title"`
	Body              string `json:"body"`
	State             string `json:"state"`
	IsDraft           bool   `json:"isDraft"`
	HeadRefName       string `json:"headRefName"`
	BaseRefName       string `json:"baseRefName"`
	ReviewDecision    string `json:"reviewDecision"`
	Mergeable         string `json:"mergeable"`
	MergeStateStatus  string `json:"mergeStateStatus"`
	StatusCheckRollup *struct {
		State string `json:"state"`
	} `json:"statusCheckRollup"`
	Reviews nodes[struct {
		Author *gqlActor `json:"author"`
		State  string    `json:"state"`
	}] `json:"reviews"`
	ReviewThreads nodes[struct {
		IsResolved bool              `json:"isResolved"`
		Path       string            `json:"path"`
		Line       int               `json:"line"`
		Comments   nodes[gqlComment] `json:"comments"`
	}] `json:"reviewThreads"`
	Comments       nodes[gqlComment] `json:"comments"`
	ReviewRequests nodes[gqlRequest] `json:"reviewRequests"`
	Commits        nodes[struct {
		Commit struct {
			StatusCheckRollup *struct {
				Contexts nodes[struct {
					Type       string `json:"__typename"`
					Name       string `json:"name"`
					Conclusion string `json:"conclusion"`
					DetailsURL string `json:"detailsUrl"`
					Context    string `json:"context"`
					State      string `json:"state"`
					TargetURL  string `json:"targetUrl"`
				}] `json:"contexts"`
			} `json:"statusCheckRollup"`
		} `json:"commit"`
	}] `json:"commits"`
	Stack *struct {
		Number      int    `json:"number"`
		Size        int    `json:"size"`
		BaseRefName string `json:"baseRefName"`
		Entries     nodes[struct {
			Position    int `json:"position"`
			PullRequest struct {
				Number      int    `json:"number"`
				URL         string `json:"url"`
				State       string `json:"state"`
				HeadRefName string `json:"headRefName"`
			} `json:"pullRequest"`
		}] `json:"entries"`
	} `json:"stack"`
}

func detailActor(a *gqlActor) moor.DetailActor {
	x := a.actor()
	return moor.DetailActor{Login: x.Login, Bot: x.Type == "Bot"}
}

func detailComment(c gqlComment) moor.DetailComment {
	return moor.DetailComment{Author: detailActor(c.Author), Body: c.Body, URL: c.URL, CreatedAt: c.CreatedAt}
}

// PullRequestDetail reads one pull request in full with a single query. The body is whole; the lists keep the poll's caps.
func (c *Client) PullRequestDetail(ctx context.Context, url string) (moor.Detail, error) {
	m := pullURL.FindStringSubmatch(url)
	if m == nil {
		return moor.Detail{}, errors.New("pull request lookup failed: not a pull request URL")
	}
	q := fmt.Sprintf("query { d: repository(owner: %q, name: %q) { pullRequest(number: %s) { %s } } }", m[1], m[2], m[3], detailFields)
	data, notFound, gone, err := c.batchQuery(ctx, "pull request", q, map[string]string{"d": url})
	if err != nil {
		return moor.Detail{}, err
	}
	var r *struct{ PullRequest *prDetailNode }
	if len(notFound)+len(gone) > 0 || json.Unmarshal(data["d"], &r) != nil || r == nil || r.PullRequest == nil {
		return moor.Detail{}, errors.New("pull request lookup failed: GitHub has no such pull request")
	}
	return r.PullRequest.detail(url), nil
}

func (n *prDetailNode) detail(url string) moor.Detail {
	d := moor.Detail{Number: n.Number, URL: url, Title: n.Title, Body: n.Body, State: n.State, Draft: n.IsDraft,
		Head: n.HeadRefName, Base: n.BaseRefName, Review: n.ReviewDecision, Mergeable: n.Mergeable, MergeState: n.MergeStateStatus,
		FailingChecks: []moor.DetailCheck{}, Reviews: []moor.DetailReview{}, Requested: []moor.DetailActor{},
		Threads: []moor.DetailThread{}, Comments: []moor.DetailComment{}}
	if n.StatusCheckRollup != nil {
		d.Checks = n.StatusCheckRollup.State
	}
	for _, c := range n.Commits.Nodes {
		if c.Commit.StatusCheckRollup == nil {
			continue
		}
		for _, x := range c.Commit.StatusCheckRollup.Contexts.Nodes {
			switch {
			case x.Type == "CheckRun" && (x.Conclusion == "FAILURE" || x.Conclusion == "TIMED_OUT" || x.Conclusion == "STARTUP_FAILURE"):
				d.FailingChecks = append(d.FailingChecks, moor.DetailCheck{Name: x.Name, URL: x.DetailsURL})
			case x.Type == "StatusContext" && (x.State == "FAILURE" || x.State == "ERROR"):
				d.FailingChecks = append(d.FailingChecks, moor.DetailCheck{Name: x.Context, URL: x.TargetURL})
			}
		}
	}
	for _, r := range n.Reviews.Nodes {
		d.Reviews = append(d.Reviews, moor.DetailReview{Author: detailActor(r.Author), State: r.State})
	}
	for _, r := range n.ReviewRequests.Nodes {
		if r.RequestedReviewer != nil {
			d.Requested = append(d.Requested, detailActor(r.RequestedReviewer))
		}
	}
	for _, t := range n.ReviewThreads.Nodes {
		th := moor.DetailThread{Path: t.Path, Line: t.Line, Resolved: t.IsResolved, Comments: []moor.DetailComment{}}
		for _, c := range t.Comments.Nodes {
			th.Comments = append(th.Comments, detailComment(c))
		}
		d.Threads = append(d.Threads, th)
	}
	for _, c := range n.Comments.Nodes {
		d.Comments = append(d.Comments, detailComment(c))
	}
	if s := n.Stack; s != nil {
		st := &moor.DetailStack{Number: s.Number, Size: s.Size, Base: s.BaseRefName, Entries: []moor.DetailStackEntry{}}
		if h, repo, _, ok := moor.ParsePR(url); ok {
			st.URL = fmt.Sprintf("https://%s/%s/stack/%d", h, repo, s.Number)
		}
		for _, e := range s.Entries.Nodes {
			st.Entries = append(st.Entries, moor.DetailStackEntry{Position: e.Position, Number: e.PullRequest.Number,
				URL: e.PullRequest.URL, State: e.PullRequest.State, Head: e.PullRequest.HeadRefName})
		}
		d.Stack = st
	}
	return d
}
