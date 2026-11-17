package github

import (
	"context"
	"regexp"
	"strings"
	"testing"
)

// reviewedDetail is a PR whose reviewers carry node ids: hubot (a User), a Copilot-like Bot, and one that is neither.
const reviewedDetail = `{
  "id": "PR_fake1", "number": 7, "title": "Add widgets", "body": "", "state": "OPEN", "isDraft": false,
  "author": {"__typename": "User", "login": "octocat", "id": "U_author"},
  "reviews": {"nodes": [
    {"author": {"__typename": "User", "login": "hubot", "id": "U_fake1"}, "state": "APPROVED"},
    {"author": {"__typename": "Bot", "login": "copilot-pull-request-reviewer", "id": "BOT_fake1"}, "state": "COMMENTED"},
    {"author": {"__typename": "Mannequin", "login": "imported-reviewer", "id": "M_fake1"}, "state": "COMMENTED"}
  ]},
  "reviewRequests": {"nodes": []}, "reviewThreads": {"nodes": []}, "comments": {"nodes": []},
  "commits": {"nodes": []}, "stack": null
}`

// mutationCalls are the recorded gh calls that ran requestReviews, as one string each.
func (f *fakeGH) mutationCalls(t *testing.T) []string {
	t.Helper()
	var out []string
	for _, c := range f.calls(t, "api") {
		if joined := strings.Join(c.argv, " "); strings.Contains(joined, "requestReviews") {
			out = append(out, joined)
		}
	}
	return out
}

func TestReRequestReviewAddsAUserByTheirNodeIdWithoutReplacingTheRequests(t *testing.T) {
	cl, f := prClient(t)
	f.prDetail(t, prA, reviewedDetail)

	if err := cl.ReRequestReview(context.Background(), prA, "hubot"); err != nil {
		t.Fatalf("ReRequestReview: %v", err)
	}
	got := f.mutationCalls(t)
	if len(got) != 1 {
		t.Fatalf("requestReviews ran %d times, want once: %v", len(got), got)
	}
	for _, want := range []string{`union:\s*true`, `pullRequestId:\s*"PR_fake1"`, `userIds:\s*\[\s*"U_fake1"\s*\]`} {
		if !regexp.MustCompile(want).MatchString(got[0]) {
			t.Errorf("the mutation %q lacks %s", got[0], want)
		}
	}
	if strings.Contains(got[0], "botIds") {
		t.Errorf("the mutation %q names botIds for a user", got[0])
	}
}

func TestReRequestReviewAddsABotByItsNodeIdAsABotId(t *testing.T) {
	cl, f := prClient(t)
	f.prDetail(t, prA, reviewedDetail)

	if err := cl.ReRequestReview(context.Background(), prA, "copilot-pull-request-reviewer"); err != nil {
		t.Fatalf("ReRequestReview: %v", err)
	}
	got := f.mutationCalls(t)
	if len(got) != 1 {
		t.Fatalf("requestReviews ran %d times, want once: %v", len(got), got)
	}
	for _, want := range []string{`union:\s*true`, `pullRequestId:\s*"PR_fake1"`, `botIds:\s*\[\s*"BOT_fake1"\s*\]`} {
		if !regexp.MustCompile(want).MatchString(got[0]) {
			t.Errorf("the mutation %q lacks %s", got[0], want)
		}
	}
	if strings.Contains(got[0], "userIds") {
		t.Errorf("the mutation %q names userIds for a bot", got[0])
	}
}

func TestReRequestReviewRefusesAReviewerThatIsNeitherUserNorBotWithoutRequesting(t *testing.T) {
	cl, f := prClient(t)
	f.prDetail(t, prA, reviewedDetail)

	err := cl.ReRequestReview(context.Background(), prA, "imported-reviewer")
	if err == nil || !strings.Contains(err.Error(), "imported-reviewer") {
		t.Fatalf("ReRequestReview = %v, want an error naming the reviewer", err)
	}
	if got := f.mutationCalls(t); len(got) != 0 {
		t.Errorf("requestReviews ran for a Mannequin: %v", got)
	}
}

func TestReRequestReviewReturnsGitHubsOwnRefusal(t *testing.T) {
	cl, f := prClient(t)
	f.prDetail(t, prA, reviewedDetail)
	f.write(t, "rerequest.fail", "gh: Review cannot be requested from pull request author.\n")

	err := cl.ReRequestReview(context.Background(), prA, "hubot")
	if err == nil || err.Error() != "Review cannot be requested from pull request author." {
		t.Errorf("ReRequestReview = %v, want GitHub's message alone, without gh's prefix", err)
	}
}
