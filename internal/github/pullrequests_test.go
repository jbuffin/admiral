package github

import (
	"context"
	"errors"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

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
		g := got[u]
		if g.State != w.State || g.Review != w.Review || g.Checks != w.Checks || g.Mergeable != w.Mergeable {
			t.Errorf("%s = %+v, want %+v", u, g, w)
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

// A NOT_FOUND on the repository says nothing about its PRs: they are unanswered, not closed.
func TestPullRequestsLeavesAMissingRepositoryUnansweredBesideTheRest(t *testing.T) {
	cl, f := prClient(t)
	f.pr(t, prA, "OPEN", "", "", "MERGEABLE")
	f.write(t, "repo_notfound/globex_app", "x")

	got, err := cl.PullRequests(context.Background(), []string{prA, prC})
	var un *moor.Unanswered
	if !errors.As(err, &un) || len(un.URLs) != 1 || un.URLs[0] != prC {
		t.Fatalf("error = %v, want unanswered for %s", err, prC)
	}
	if got[prA].State != "OPEN" {
		t.Errorf("the PR in the repository that exists = %+v, want its answer", got[prA])
	}
}

func (f *fakeGH) prEvents(t *testing.T, url, json string) {
	t.Helper()
	f.write(t, "pr_events/"+keyOf.Replace(url), json)
}

const eventsA = `{
  "headRefOid": "abc123",
  "mergeStateStatus": "BEHIND",
  "baseRef": {
    "branchProtectionRule": null,
    "rules": {"nodes": [
      {"type": "DELETION", "parameters": {}},
      {"type": "REQUIRED_STATUS_CHECKS", "parameters": {"strictRequiredStatusChecksPolicy": true}}
    ]}
  },
  "reviews": {"nodes": [
    {"id": "R1", "author": {"__typename": "User", "login": "alice"}, "state": "CHANGES_REQUESTED",
     "submittedAt": "2026-11-02T12:10:00Z", "body": "rename it", "url": "https://github.com/acme/widgets/pull/7#pullrequestreview-1", "comments": {"totalCount": 2}},
    {"id": "R2", "author": {"__typename": "Bot", "login": "coderabbitai"}, "state": "COMMENTED",
     "submittedAt": "2026-11-02T12:11:00Z", "body": "", "url": "https://github.com/acme/widgets/pull/7#pullrequestreview-2", "comments": {"totalCount": 0}}
  ]},
  "reviewThreads": {"nodes": [
    {"comments": {"nodes": [
      {"id": "T1", "author": {"__typename": "User", "login": "alice"}, "createdAt": "2026-11-02T12:12:00Z",
       "body": "here too", "url": "https://github.com/acme/widgets/pull/7#discussion_r1"}
    ]}}
  ]},
  "comments": {"nodes": [
    {"id": "C1", "author": {"__typename": "User", "login": "bob"}, "createdAt": "2026-11-02T12:13:00Z",
     "body": "ping", "url": "https://github.com/acme/widgets/pull/7#issuecomment-1"},
    {"id": "C2", "author": null, "createdAt": "2026-11-02T12:14:00Z", "body": "from a deleted account", "url": "https://github.com/acme/widgets/pull/7#issuecomment-2"}
  ]},
  "reviewRequests": {"nodes": [
    {"requestedReviewer": {"__typename": "User", "login": "carol"}},
    {"requestedReviewer": {"__typename": "Team", "slug": "platform"}},
    {"requestedReviewer": null}
  ]},
  "timelineItems": {"nodes": [
    {"requestedReviewer": {"__typename": "User", "login": "carol"}},
    {"requestedReviewer": {"__typename": "User", "login": "dave"}},
    {"requestedReviewer": null}
  ]},
  "commits": {"nodes": [{"commit": {"oid": "abc123", "statusCheckRollup": {"contexts": {"nodes": [
    {"__typename": "CheckRun", "name": "CI / test", "conclusion": "FAILURE", "isRequired": false,
     "checkSuite": {"app": {"slug": "github-actions"}, "workflowRun": {"id": "55"}}},
    {"__typename": "CheckRun", "name": "build", "conclusion": null, "isRequired": true,
     "checkSuite": {"app": {"slug": "circleci"}, "workflowRun": null}},
    {"__typename": "StatusContext", "context": "ci/legacy", "state": "ERROR", "isRequired": true}
  ]}}}}]}
}`

func TestPullRequestsReadsTheEventsOfEachPR(t *testing.T) {
	cl, f := prClient(t)
	f.pr(t, prA, "OPEN", "", "", "CONFLICTING")
	f.prEvents(t, prA, eventsA)
	f.write(t, "viewer", "jbuffin\n")

	got, err := cl.PullRequests(context.Background(), []string{prA})
	if err != nil {
		t.Fatalf("PullRequests: %v", err)
	}
	st := got[prA]
	if st.Viewer != "jbuffin" {
		t.Errorf("Viewer = %q, want the login gh runs as", st.Viewer)
	}
	if st.Head != "abc123" || st.MergeState != "BEHIND" || st.Mergeable != "CONFLICTING" {
		t.Errorf("head, merge state, mergeable = %q, %q, %q", st.Head, st.MergeState, st.Mergeable)
	}
	if !st.StrictBase {
		t.Error("StrictBase = false, want true: a ruleset requires up-to-date branches")
	}

	if len(st.Reviews) != 2 || st.Reviews[0].ID != "R1" || st.Reviews[0].Author != (moor.Actor{Type: "User", Login: "alice"}) ||
		st.Reviews[0].State != "CHANGES_REQUESTED" || st.Reviews[0].Body != "rename it" || st.Reviews[0].Comments != 2 ||
		st.Reviews[0].URL != "https://github.com/acme/widgets/pull/7#pullrequestreview-1" ||
		!st.Reviews[0].SubmittedAt.Equal(time.Date(2026, 11, 2, 12, 10, 0, 0, time.UTC)) {
		t.Errorf("Reviews[0] = %+v", st.Reviews)
	}
	if len(st.Reviews) == 2 && st.Reviews[1].Author != (moor.Actor{Type: "Bot", Login: "coderabbitai"}) {
		t.Errorf("a bot review's author = %+v", st.Reviews[1].Author)
	}
	if len(st.ThreadComments) != 1 || st.ThreadComments[0].ID != "T1" || st.ThreadComments[0].Body != "here too" ||
		!st.ThreadComments[0].CreatedAt.Equal(time.Date(2026, 11, 2, 12, 12, 0, 0, time.UTC)) {
		t.Errorf("ThreadComments = %+v", st.ThreadComments)
	}
	if len(st.Comments) != 2 || st.Comments[0].Author.Login != "bob" || st.Comments[1].Author != (moor.Actor{}) {
		t.Errorf("Comments = %+v; want bob's, then one from a deleted account with no author", st.Comments)
	}

	var req []string
	for _, a := range st.Requested {
		req = append(req, a.Type+":"+a.Login)
	}
	sort.Strings(req)
	if want := []string{"Team:platform", "User:carol", "User:dave"}; !reflect.DeepEqual(req, want) {
		t.Errorf("Requested = %v, want %v: pending and past requests, each once, a null left out", req, want)
	}

	want := []moor.CheckContext{
		{Kind: "CheckRun", Name: "CI / test", State: "FAILURE", AppSlug: "github-actions", HasWorkflowRun: true},
		{Kind: "CheckRun", Name: "build", AppSlug: "circleci", Required: true},
		{Kind: "StatusContext", Name: "ci/legacy", State: "ERROR", Required: true},
	}
	if !reflect.DeepEqual(st.Contexts, want) {
		t.Errorf("Contexts = %+v, want %+v", st.Contexts, want)
	}
	if n := len(f.calls(t, "api")); n != 1 {
		t.Errorf("gh api calls = %d, want 1", n)
	}
}

func TestPullRequestsReadsTheStrictRuleFromEitherPlaceAndNullMeansOff(t *testing.T) {
	for _, c := range []struct {
		name, baseRef string
		want          bool
	}{
		{"classic protection requiring up-to-date branches", `{"branchProtectionRule": {"requiresStrictStatusChecks": true}, "rules": {"nodes": []}}`, true},
		{"classic protection not requiring it", `{"branchProtectionRule": {"requiresStrictStatusChecks": false}, "rules": {"nodes": []}}`, false},
		{"a ruleset with the policy off", `{"branchProtectionRule": null, "rules": {"nodes": [{"type": "REQUIRED_STATUS_CHECKS", "parameters": {"strictRequiredStatusChecksPolicy": false}}]}}`, false},
		{"a rule GitHub does not show this token", `{"branchProtectionRule": null, "rules": null}`, false},
		{"no base ref", `null`, false},
	} {
		t.Run(c.name, func(t *testing.T) {
			cl, f := prClient(t)
			f.pr(t, prA, "OPEN", "", "", "MERGEABLE")
			f.prEvents(t, prA, `{"mergeStateStatus": "BEHIND", "baseRef": `+c.baseRef+`}`)
			got, err := cl.PullRequests(context.Background(), []string{prA})
			if err != nil {
				t.Fatal(err)
			}
			if got[prA].StrictBase != c.want || got[prA].MergeState != "BEHIND" {
				t.Errorf("StrictBase, MergeState = %v, %q; want %v, BEHIND", got[prA].StrictBase, got[prA].MergeState, c.want)
			}
		})
	}
}

func TestPullRequestsReadsEventsBesideANotFound(t *testing.T) {
	cl, f := prClient(t)
	f.pr(t, prA, "OPEN", "", "", "MERGEABLE")
	f.prEvents(t, prA, eventsA)
	f.write(t, "pr_notfound/"+keyOf.Replace(prB), "x")
	got, _ := cl.PullRequests(context.Background(), []string{prA, prB})
	if len(got[prA].Reviews) != 2 || got[prA].Viewer == "" {
		t.Errorf("the PR beside the missing one lost its events: %+v", got[prA])
	}
}

func TestPullRequestsWithNoEventsFillsNothingIn(t *testing.T) {
	cl, f := prClient(t)
	f.pr(t, prA, "OPEN", "APPROVED", "SUCCESS", "MERGEABLE")
	got, err := cl.PullRequests(context.Background(), []string{prA})
	if err != nil {
		t.Fatal(err)
	}
	st := got[prA]
	if len(st.Reviews)+len(st.Comments)+len(st.ThreadComments)+len(st.Requested)+len(st.Contexts) != 0 || st.StrictBase {
		t.Errorf("a PR with nothing to read = %+v", st)
	}
	if st.Viewer != "fake-viewer" {
		t.Errorf("Viewer = %q, want the fake's default", st.Viewer)
	}
}

func (f *fakeGH) stack(t *testing.T, endpoint string, status int, body, stderr string) {
	t.Helper()
	f.write(t, "rest/stack.endpoint", endpoint)
	f.write(t, "rest/stack.status", strconv.Itoa(status))
	f.write(t, "rest/stack.json", body)
	if stderr != "" {
		f.write(t, "rest/stack.stderr", stderr)
	}
}

func TestStackOpenPRsAsksForTheStackAndAnswersItsOpenLayersBottomFirst(t *testing.T) {
	cl, f := prClient(t)
	f.stack(t, "repos/acme/widgets/stacks/12", 200, `{"number":12,"open":true,"base":{"ref":"main"},"pull_requests":[
		{"number":21,"state":"open","head":{"ref":"l1","sha":"a1"}},
		{"number":22,"state":"merged","head":{"ref":"l2","sha":"a2"}},
		{"number":23,"state":"open","head":{"ref":"l3","sha":"a3"}},
		{"number":24,"state":"closed","head":{"ref":"l4","sha":"a4"}}]}`, "")

	got, found, err := cl.StackOpenPRs(context.Background(), "acme", "widgets", 12)
	if err != nil || !found {
		t.Fatalf("StackOpenPRs = %v, found %v, err %v; want an answer", got, found, err)
	}
	want := []string{"https://github.com/acme/widgets/pull/21", "https://github.com/acme/widgets/pull/23"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("layers = %v, want %v: the open ones, bottom first", got, want)
	}
	calls := f.calls(t, "api")
	if len(calls) != 1 {
		t.Fatalf("gh api calls = %d, want 1", len(calls))
	}
	if !strings.Contains(strings.Join(calls[0].argv, " "), "repos/acme/widgets/stacks/12") || strings.Contains(strings.Join(calls[0].argv, " "), "graphql") {
		t.Errorf("argv = %v, want REST on repos/acme/widgets/stacks/12", calls[0].argv)
	}
}

func TestStackOpenPRsTellsAStackGitHubDoesNotHaveFromAFailedCall(t *testing.T) {
	cl, f := prClient(t)
	f.stack(t, "repos/acme/widgets/stacks/12", 404, `{"message":"Not Found"}`, "gh: Not Found (HTTP 404)")
	got, found, err := cl.StackOpenPRs(context.Background(), "acme", "widgets", 12)
	if err != nil || found || len(got) != 0 {
		t.Errorf("a 404 = %v, found %v, err %v; want no layers, not found and no error", got, found, err)
	}

	f.stack(t, "repos/acme/widgets/stacks/12", 502, `{}`, "gh: Bad Gateway (HTTP 502)")
	if _, _, err := cl.StackOpenPRs(context.Background(), "acme", "widgets", 12); err == nil {
		t.Error("a 502 answered no error, want a failed call")
	}
	f.stack(t, "repos/acme/widgets/stacks/99", 200, `{}`, "")
	if _, _, err := cl.StackOpenPRs(context.Background(), "acme", "widgets", 12); err == nil {
		t.Error("a call gh has no answer for (exit 1) gave no error, want a failed call")
	}
}
