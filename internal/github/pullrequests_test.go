package github

import (
	"context"
	"strings"
	"testing"

	"github.com/jbuffin/admiral/internal/moor"
)

const (
	prA = "https://github.com/acme/widgets/pull/7"
	prB = "https://github.com/acme/widgets/pull/9"
	prC = "https://github.com/globex/app/pull/3"
)

var keyOf = strings.NewReplacer("/", "_", ":", "_")

func (f *fakeGH) pr(t *testing.T, url, state, review, checks, mergeable string) {
	t.Helper()
	k := keyOf.Replace(url)
	f.write(t, "pr_state/"+k, state+"\n")
	for dir, v := range map[string]string{"pr_review": review, "pr_checks": checks, "pr_mergeable": mergeable} {
		if v != "" {
			f.write(t, dir+"/"+k, v+"\n")
		}
	}
}

func prClient(t *testing.T) (*Client, *fakeGH) {
	t.Helper()
	f := newFake(t)
	f.write(t, "token", "fake-token-work-2\n")
	return NewClient("github.com", "", nil, f.env), f
}

func TestPullRequestsReadsTheChipsOfEveryPRInOneCall(t *testing.T) {
	cl, f := prClient(t)
	f.pr(t, prA, "OPEN", "APPROVED", "SUCCESS", "MERGEABLE")
	f.pr(t, prB, "OPEN", "", "", "CONFLICTING")
	f.pr(t, prC, "MERGED", "", "", "UNKNOWN")

	got, err := cl.PullRequests(context.Background(), []string{prA, prB, prC})
	if err != nil {
		t.Fatalf("PullRequests: %v", err)
	}
	want := map[string]moor.Status{
		prA: {State: "OPEN", Review: "APPROVED", Checks: "SUCCESS", Mergeable: "MERGEABLE"},
		prB: {State: "OPEN", Mergeable: "CONFLICTING"},
		prC: {State: "MERGED", Mergeable: "UNKNOWN"},
	}
	for u, w := range want {
		if got[u] != w {
			t.Errorf("%s = %+v, want %+v", u, got[u], w)
		}
	}
	if n := len(f.calls(t, "api")); n != 1 {
		t.Errorf("gh api calls = %d, want 1: one batched query for all three PRs", n)
	}
	if n := len(f.calls(t, "pr")); n != 0 {
		t.Errorf("gh pr calls = %d, want none: the poll does not ask one PR at a time", n)
	}
}

// gh exits 1 on a NOT_FOUND entry yet still prints the other PRs under data.
func TestPullRequestsKeepsTheAnswersBesideANotFound(t *testing.T) {
	cl, f := prClient(t)
	f.pr(t, prA, "OPEN", "REVIEW_REQUIRED", "PENDING", "MERGEABLE")
	f.write(t, "pr_notfound/"+keyOf.Replace(prB), "x")

	got, err := cl.PullRequests(context.Background(), []string{prA, prB})
	if err != nil {
		t.Fatalf("a partial NOT_FOUND is an answer, not a failure: %v", err)
	}
	if got[prA].State != "OPEN" || got[prA].Review != "REVIEW_REQUIRED" || got[prA].Checks != "PENDING" {
		t.Errorf("the PR beside the missing one = %+v, want its chips", got[prA])
	}
	if _, ok := got[prB]; ok {
		t.Errorf("the NOT_FOUND PR is in the answer: %+v; it must be left out", got[prB])
	}
}

func TestPullRequestsFailsWhenGhGivesNoData(t *testing.T) {
	cl, f := prClient(t)
	f.pr(t, prA, "OPEN", "", "", "MERGEABLE")
	f.write(t, "pr_batch.fail", "gh: HTTP 502\n")

	got, err := cl.PullRequests(context.Background(), []string{prA})
	if err == nil {
		t.Fatalf("PullRequests = %v, nil; want an error: a transport failure is not an answer", got)
	}
}

func TestPullRequestsAllNotFoundIsAnAnswerNotAFailure(t *testing.T) {
	cl, _ := prClient(t)
	got, err := cl.PullRequests(context.Background(), []string{prA, prB})
	if err != nil || len(got) != 0 {
		t.Errorf("PullRequests = %v, %v; want an empty answer: GitHub reached, nothing found", got, err)
	}
}
