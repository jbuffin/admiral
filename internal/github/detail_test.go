package github

// PullRequestDetail reads one PR in full; the poll's PullRequests also reads its author and creation time.

import (
	"context"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/jbuffin/admiral/internal/moor"
)

func TestPullRequestsReadsTheAuthorAndCreationTimeOfEachPR(t *testing.T) {
	cl, f := prClient(t)
	f.pr(t, prA, "OPEN", "", "", "MERGEABLE")
	f.pr(t, prB, "OPEN", "", "", "MERGEABLE")
	f.prEvents(t, prA, `{"author": {"__typename": "User", "login": "jbuffin"}, "createdAt": "2026-11-10T09:30:00Z"}`)
	f.prEvents(t, prB, `{"author": null, "createdAt": "2026-11-11T10:00:00Z"}`)

	got, err := cl.PullRequests(context.Background(), []string{prA, prB})
	if err != nil {
		t.Fatalf("PullRequests: %v", err)
	}
	if a := got[prA]; a.Author != "jbuffin" || !a.CreatedAt.Equal(time.Date(2026, 11, 10, 9, 30, 0, 0, time.UTC)) {
		t.Errorf("author, created = %q, %s; want jbuffin, 2026-11-10T09:30:00Z", a.Author, a.CreatedAt)
	}
	if b := got[prB]; b.Author != "" || !b.CreatedAt.Equal(time.Date(2026, 11, 11, 10, 0, 0, 0, time.UTC)) {
		t.Errorf("a PR from a deleted account = author %q, created %s; want no author and its creation time", b.Author, b.CreatedAt)
	}
}

func (f *fakeGH) prDetail(t *testing.T, url, json string) {
	t.Helper()
	f.write(t, "pr_detail/"+keyOf.Replace(url), json)
}

// longBody is longer than the poll's 4096-byte cut.
var longBody = strings.Repeat("A line of the description.\n", 400)

func detailNode() string {
	return `{
  "number": 7, "title": "Add widgets", "body": ` + jsonString(longBody) + `, "state": "OPEN", "isDraft": true,
  "headRefName": "feature/w", "baseRefName": "main", "reviewDecision": "CHANGES_REQUESTED",
  "mergeable": "CONFLICTING", "mergeStateStatus": "DIRTY",
  "statusCheckRollup": {"state": "FAILURE"},
  "reviews": {"nodes": [
    {"id": "R1", "author": {"__typename": "User", "login": "alice"}, "state": "CHANGES_REQUESTED", "url": "https://github.com/acme/widgets/pull/7#pullrequestreview-1"},
    {"id": "R2", "author": {"__typename": "Bot", "login": "coderabbitai"}, "state": "COMMENTED", "url": "https://github.com/acme/widgets/pull/7#pullrequestreview-2"}
  ]},
  "reviewThreads": {"nodes": [
    {"isResolved": true, "path": "widgets/a.go", "line": 12, "comments": {"nodes": [
      {"id": "T1", "author": {"__typename": "Bot", "login": "coderabbitai"}, "createdAt": "2026-11-02T12:12:00Z", "body": "nit: rename", "url": "https://github.com/acme/widgets/pull/7#discussion_r1"},
      {"id": "T2", "author": {"__typename": "User", "login": "jbuffin"}, "createdAt": "2026-11-02T12:20:00Z", "body": "done", "url": "https://github.com/acme/widgets/pull/7#discussion_r2"}]}},
    {"isResolved": false, "path": "widgets/b.go", "line": 40, "comments": {"nodes": [
      {"id": "T3", "author": {"__typename": "User", "login": "alice"}, "createdAt": "2026-11-02T13:00:00Z", "body": "why?", "url": "https://github.com/acme/widgets/pull/7#discussion_r3"}]}}
  ]},
  "comments": {"nodes": [
    {"id": "C1", "author": {"__typename": "Bot", "login": "github-actions"}, "createdAt": "2026-11-02T12:13:00Z", "body": "coverage 80%", "url": "https://github.com/acme/widgets/pull/7#issuecomment-1"},
    {"id": "C2", "author": {"__typename": "User", "login": "bob"}, "createdAt": "2026-11-02T12:14:00Z", "body": "ping", "url": "https://github.com/acme/widgets/pull/7#issuecomment-2"}
  ]},
  "reviewRequests": {"nodes": [
    {"requestedReviewer": {"__typename": "User", "login": "carol"}},
    {"requestedReviewer": {"__typename": "Team", "slug": "platform"}}
  ]},
  "timelineItems": {"nodes": [
    {"requestedReviewer": {"__typename": "User", "login": "carol"}},
    {"requestedReviewer": {"__typename": "User", "login": "dave"}}
  ]},
  "commits": {"nodes": [{"commit": {"oid": "abc123", "statusCheckRollup": {"contexts": {"nodes": [
    {"__typename": "CheckRun", "name": "CI / test", "conclusion": "FAILURE", "detailsUrl": "https://ci.example/run/1"},
    {"__typename": "CheckRun", "name": "lint", "conclusion": "SUCCESS", "detailsUrl": "https://ci.example/run/2"},
    {"__typename": "StatusContext", "context": "ci/legacy", "state": "ERROR", "targetUrl": "https://ci.example/legacy"}
  ]}}}}]},
  "baseRepository": {"defaultBranchRef": {"name": "trunk"}},
  "stack": {"number": 4, "size": 2, "baseRefName": "main", "entries": {"nodes": [
    {"position": 1, "pullRequest": {"number": 7, "url": "https://github.com/acme/widgets/pull/7", "state": "OPEN", "headRefName": "feature/w"}},
    {"position": 2, "pullRequest": {"number": 8, "url": "https://github.com/acme/widgets/pull/8", "state": "OPEN", "headRefName": "feature/x"}}
  ]}}
}`
}

func jsonString(s string) string {
	return `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`, "\n", `\n`).Replace(s) + `"`
}

func TestPullRequestDetailReadsEveryFieldOfOnePRWithOneQuery(t *testing.T) {
	cl, f := prClient(t)
	f.prDetail(t, prA, detailNode())

	d, err := cl.PullRequestDetail(context.Background(), prA)
	if err != nil {
		t.Fatalf("PullRequestDetail: %v", err)
	}
	if d.Number != 7 || d.URL != prA || d.Title != "Add widgets" || d.State != "OPEN" || !d.Draft || d.Head != "feature/w" || d.Base != "main" {
		t.Errorf("number, url, title, state, draft, head, base = %d %q %q %q %v %q %q", d.Number, d.URL, d.Title, d.State, d.Draft, d.Head, d.Base)
	}
	if d.DefaultBranch != "trunk" {
		t.Errorf("default branch = %q, want trunk: the repository's default branch, which is not the PR's base", d.DefaultBranch)
	}
	if d.Body != longBody {
		t.Errorf("body is %d bytes (cut: %v), want the whole %d-byte Markdown as GitHub returns it", len(d.Body), strings.Contains(d.Body, "[cut]"), len(longBody))
	}
	if d.Review != "CHANGES_REQUESTED" || d.Checks != "FAILURE" || d.Mergeable != "CONFLICTING" || d.MergeState != "DIRTY" {
		t.Errorf("review, checks, mergeable, merge state = %q %q %q %q", d.Review, d.Checks, d.Mergeable, d.MergeState)
	}

	wantFailing := []moor.DetailCheck{{Name: "CI / test", URL: "https://ci.example/run/1"}, {Name: "ci/legacy", URL: "https://ci.example/legacy"}}
	if len(d.FailingChecks) != 2 || d.FailingChecks[0] != wantFailing[0] || d.FailingChecks[1] != wantFailing[1] {
		t.Errorf("failing checks = %+v, want %+v: each failing check with its name and link, the passing one left out", d.FailingChecks, wantFailing)
	}

	if len(d.Reviews) != 2 || d.Reviews[0].Author != (moor.DetailActor{Login: "alice"}) || d.Reviews[0].State != "CHANGES_REQUESTED" ||
		d.Reviews[1].Author != (moor.DetailActor{Login: "coderabbitai", Bot: true}) || d.Reviews[1].State != "COMMENTED" {
		t.Errorf("reviews = %+v, want alice's verdict and the bot's, bot marked", d.Reviews)
	}
	if len(d.Requested) != 2 || d.Requested[0].Login != "carol" || d.Requested[1].Login != "platform" {
		t.Errorf("requested = %+v, want the pending carol and the platform team, not dave who was requested once", d.Requested)
	}

	if len(d.Threads) != 2 {
		t.Fatalf("threads = %+v, want 2", d.Threads)
	}
	if th := d.Threads[0]; th.Path != "widgets/a.go" || th.Line != 12 || !th.Resolved || len(th.Comments) != 2 ||
		!th.Comments[0].Author.Bot || th.Comments[0].Author.Login != "coderabbitai" || th.Comments[0].Body != "nit: rename" ||
		th.Comments[1].Author.Bot || th.Comments[1].Author.Login != "jbuffin" {
		t.Errorf("thread 0 = %+v, want a resolved thread on widgets/a.go:12 with a bot comment then a person's", th)
	}
	if th := d.Threads[1]; th.Path != "widgets/b.go" || th.Line != 40 || th.Resolved || len(th.Comments) != 1 {
		t.Errorf("thread 1 = %+v, want an open thread on widgets/b.go:40", th)
	}

	if len(d.Comments) != 2 || !d.Comments[0].Author.Bot || d.Comments[1].Author != (moor.DetailActor{Login: "bob"}) || d.Comments[1].Body != "ping" ||
		!d.Comments[1].CreatedAt.Equal(time.Date(2026, 11, 2, 12, 14, 0, 0, time.UTC)) {
		t.Errorf("comments = %+v, want a bot's then bob's", d.Comments)
	}

	if s := d.Stack; s == nil || s.Number != 4 || s.Size != 2 || s.Base != "main" || s.URL != "https://github.com/acme/widgets/stack/4" ||
		len(s.Entries) != 2 || s.Entries[1].Position != 2 || s.Entries[1].Number != 8 || s.Entries[1].Head != "feature/x" || s.Entries[1].State != "OPEN" {
		t.Errorf("stack = %+v, want stack 4 of 2 at https://github.com/acme/widgets/stack/4 with its entries", s)
	}

	calls := f.calls(t, "api")
	if len(calls) != 1 {
		t.Fatalf("%d gh api calls, want exactly one query for the PR", len(calls))
	}
	query := strings.Join(calls[0].argv, " ")
	if !strings.Contains(query, "defaultBranchRef") {
		t.Errorf("the query does not ask for the repository's default branch")
	}
	for _, c := range []struct{ field, limit string }{
		{"reviews", `last:\s*20`}, {"reviewThreads", `last:\s*20`}, {"comments", `last:\s*10`}, {"comments", `last:\s*20`},
		{"reviewRequests", `first:\s*20`}, {"contexts", `first:\s*50`}, {"entries", `first:\s*50`},
	} {
		if !regexp.MustCompile(c.field + `\(` + c.limit + `\)`).MatchString(query) {
			t.Errorf("the query has no %s(%s): the poll's list caps hold", c.field, c.limit)
		}
	}
}

func TestPullRequestDetailOfAPRInNoStackHasNoStack(t *testing.T) {
	cl, f := prClient(t)
	f.prDetail(t, prA, `{"number": 7, "title": "t", "body": "", "state": "OPEN", "isDraft": false, "headRefName": "h", "baseRefName": "main", "stack": null}`)
	d, err := cl.PullRequestDetail(context.Background(), prA)
	if err != nil {
		t.Fatalf("PullRequestDetail: %v", err)
	}
	if d.Title != "t" || d.Stack != nil {
		t.Errorf("title, stack = %q, %+v; want t and no stack", d.Title, d.Stack)
	}
}
