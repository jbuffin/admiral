package github

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

const viewerLogin = "fake-viewer"

// searchNodes answers every search with the nodes and the viewer, as GitHub does, and searches as the viewer.
func searchNodes(t *testing.T, nodes ...string) []Item {
	t.Helper()
	f := newFake(t)
	f.answer(t, map[string]string{"json": `{"data":{"viewer":{"login":"` + viewerLogin + `"},"search":{"nodes":[` + strings.Join(nodes, ",") + `]}}}`})
	items, _, err := NewClient("github.com", "", nil, f.env).SearchViewer(context.Background(), "fake-token-work-2", "is:open")
	if err != nil {
		t.Fatal(err)
	}
	return items
}

// orderNode is a search node for number 7 of acme/widgets: a pull request when it has a state, with extra's members added.
func orderNode(t *testing.T, kind string, extra map[string]any) string {
	t.Helper()
	n := map[string]any{"number": 7, "title": "T", "url": "https://github.com/acme/widgets/" + kind + "/7",
		"repository": map[string]any{"nameWithOwner": "acme/widgets", "url": "https://github.com/acme/widgets"},
		"labels":     map[string]any{"nodes": []any{}}}
	if kind == "pull" {
		for k, v := range map[string]any{"state": "OPEN", "reviewDecision": nil, "mergeable": "MERGEABLE", "mergeStateStatus": "CLEAN",
			"statusCheckRollup": nil, "headRefOid": "head1"} {
			n[k] = v
		}
	}
	for k, v := range extra {
		n[k] = v
	}
	b, err := json.Marshal(n)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func onlyItem(t *testing.T, items []Item) Item {
	t.Helper()
	if len(items) != 1 {
		t.Fatalf("got %d items, want 1: %+v", len(items), items)
	}
	return items[0]
}

func mustTime(t *testing.T, s string) time.Time {
	t.Helper()
	at, err := time.Parse(time.RFC3339, s)
	if err != nil {
		t.Fatal(err)
	}
	return at
}

type j = map[string]any

func reviewRequest(at, actor, reviewer string) j {
	r := j{}
	if reviewer != "" {
		r["login"] = reviewer
	}
	return j{"__typename": "ReviewRequestedEvent", "createdAt": at, "actor": j{"login": actor}, "requestedReviewer": r}
}

func timeline(events ...j) j {
	nodes := make([]any, len(events))
	for i, e := range events {
		nodes[i] = e
	}
	return j{"nodes": nodes}
}

func checkRollup(runs, statuses []j) j {
	return j{"state": "FAILURE", "contexts": j{"checkRunCountsByState": runs, "statusContextCountsByState": statuses}}
}

func TestSearchReadsWhoOpenedAPullRequestWhenItsBodyAndWhatNeedsTheViewer(t *testing.T) {
	it := onlyItem(t, searchNodes(t, orderNode(t, "pull", j{
		"author":    j{"login": "alice"},
		"createdAt": "2026-11-10T09:30:00Z",
		"bodyText":  "Fixes the login race.",
		"statusCheckRollup": checkRollup(
			[]j{{"state": "FAILURE", "count": 2}, {"state": "CANCELLED", "count": 1}, {"state": "SUCCESS", "count": 4}}, nil),
		"viewerLatestReview": j{"state": "APPROVED", "commit": j{"oid": "head1"}},
		"timelineItems":      timeline(reviewRequest("2026-11-10T10:00:00Z", "bob", viewerLogin)),
	})))
	if it.Author != "alice" || !it.OpenedAt.Equal(mustTime(t, "2026-11-10T09:30:00Z")) || it.Excerpt != "Fixes the login race." {
		t.Errorf("author %q opened %v excerpt %q; want alice, 2026-11-10T09:30:00Z, the body", it.Author, it.OpenedAt, it.Excerpt)
	}
	if it.FailingChecks != 2 {
		t.Errorf("FailingChecks = %d, want 2 (two failing runs; the cancelled run is not failing)", it.FailingChecks)
	}
	if !it.ReviewRequestedAt.Equal(mustTime(t, "2026-11-10T10:00:00Z")) {
		t.Errorf("ReviewRequestedAt = %v, want 2026-11-10T10:00:00Z", it.ReviewRequestedAt)
	}
	if !it.ReviewedSincePush {
		t.Error("ReviewedSincePush = false, want true: the viewer's approval is on the head")
	}
	if !it.EventAt.IsZero() || it.Actor != "" {
		t.Errorf("EventAt %v Actor %q: a plain search leaves the acting fields to SearchActing", it.EventAt, it.Actor)
	}
}

func TestSearchSaysReviewedSinceTheLastPushOnlyForASubmittedReviewOnTheHead(t *testing.T) {
	for _, c := range []struct {
		name   string
		review any
		want   bool
	}{
		{"approved on the head", j{"state": "APPROVED", "commit": j{"oid": "head1"}}, true},
		{"changes requested on the head", j{"state": "CHANGES_REQUESTED", "commit": j{"oid": "head1"}}, true},
		{"commented on the head", j{"state": "COMMENTED", "commit": j{"oid": "head1"}}, true},
		{"approved before a new push", j{"state": "APPROVED", "commit": j{"oid": "older"}}, false},
		{"pending on the head", j{"state": "PENDING", "commit": j{"oid": "head1"}}, false},
		{"dismissed on the head", j{"state": "DISMISSED", "commit": j{"oid": "head1"}}, false},
		{"no review", nil, false},
	} {
		t.Run(c.name, func(t *testing.T) {
			it := onlyItem(t, searchNodes(t, orderNode(t, "pull", j{"viewerLatestReview": c.review})))
			if it.ReviewedSincePush != c.want {
				t.Errorf("ReviewedSincePush = %v, want %v", it.ReviewedSincePush, c.want)
			}
		})
	}
}

func TestSearchDatesTheViewersLatestReviewRequestAndIgnoresOthersAndTeams(t *testing.T) {
	for _, c := range []struct {
		name   string
		events []j
		want   string // "" for none
	}{
		{"a request for the viewer", []j{reviewRequest("2026-11-10T10:00:00Z", "bob", viewerLogin)}, "2026-11-10T10:00:00Z"},
		{"the latest of two requests for the viewer", []j{
			reviewRequest("2026-11-09T08:00:00Z", "bob", viewerLogin), reviewRequest("2026-11-11T08:00:00Z", "carol", viewerLogin)}, "2026-11-11T08:00:00Z"},
		{"a later request for someone else does not count", []j{
			reviewRequest("2026-11-09T08:00:00Z", "bob", viewerLogin), reviewRequest("2026-11-12T08:00:00Z", "bob", "dave")}, "2026-11-09T08:00:00Z"},
		{"a request that went to a team", []j{reviewRequest("2026-11-10T10:00:00Z", "bob", "")}, ""},
		{"no events", nil, ""},
	} {
		t.Run(c.name, func(t *testing.T) {
			it := onlyItem(t, searchNodes(t, orderNode(t, "pull", j{"timelineItems": timeline(c.events...)})))
			if c.want == "" {
				if !it.ReviewRequestedAt.IsZero() {
					t.Errorf("ReviewRequestedAt = %v, want none", it.ReviewRequestedAt)
				}
				return
			}
			if !it.ReviewRequestedAt.Equal(mustTime(t, c.want)) {
				t.Errorf("ReviewRequestedAt = %v, want %s", it.ReviewRequestedAt, c.want)
			}
		})
	}
}

func TestSearchCountsTheFailingChecksTheMoorPollCountsAndNoOthers(t *testing.T) {
	for _, c := range []struct {
		name           string
		runs, statuses []j
		want           int
	}{
		{"failing run states", []j{{"state": "FAILURE", "count": 1}, {"state": "TIMED_OUT", "count": 2},
			{"state": "STARTUP_FAILURE", "count": 1}, {"state": "ACTION_REQUIRED", "count": 3}}, nil, 7},
		{"failing status contexts", nil, []j{{"state": "ERROR", "count": 1}, {"state": "FAILURE", "count": 2}, {"state": "PENDING", "count": 5}}, 3},
		{"runs and statuses add up", []j{{"state": "FAILURE", "count": 1}}, []j{{"state": "ERROR", "count": 1}}, 2},
		{"states that are not failing", []j{{"state": "CANCELLED", "count": 1}, {"state": "SUCCESS", "count": 2}, {"state": "SKIPPED", "count": 1},
			{"state": "NEUTRAL", "count": 1}, {"state": "IN_PROGRESS", "count": 1}, {"state": "STALE", "count": 1}},
			[]j{{"state": "SUCCESS", "count": 1}, {"state": "EXPECTED", "count": 1}}, 0},
		{"no checks", nil, nil, 0},
	} {
		t.Run(c.name, func(t *testing.T) {
			it := onlyItem(t, searchNodes(t, orderNode(t, "pull", j{"statusCheckRollup": checkRollup(c.runs, c.statuses)})))
			if it.FailingChecks != c.want {
				t.Errorf("FailingChecks = %d, want %d", it.FailingChecks, c.want)
			}
		})
	}
	t.Run("a pull request with no rollup", func(t *testing.T) {
		if it := onlyItem(t, searchNodes(t, orderNode(t, "pull", nil))); it.FailingChecks != 0 {
			t.Errorf("FailingChecks = %d, want 0", it.FailingChecks)
		}
	})
}

func TestSearchMakesTheExcerptOneShortLineOfTheBody(t *testing.T) {
	long := "a\nb\t" + strings.Repeat("é", 400)
	atCap := strings.Repeat("é", 280)
	for _, c := range []struct{ name, body, want string }{
		{"whitespace runs become one space", "a\nb", "a b"},
		{"a longer body is cut on a rune boundary with an ellipsis", long, "a b " + strings.Repeat("é", 276) + "…"},
		{"a body of exactly 280 runes is kept whole", atCap, atCap},
		{"a body of 281 runes is cut", atCap + "é", atCap + "…"},
		{"control characters are dropped and the ends trimmed", "  \n x\u0007y \t\n", "xy"},
		{"zero-width, bidi and C1 characters are dropped", "a\u200bb\u202e c\u2066d\ufeff\u0090e", "ab cde"},
		{"an empty body leaves no excerpt", "", ""},
		{"a whitespace-only body leaves no excerpt", " \n\t \r\n", ""},
	} {
		t.Run(c.name, func(t *testing.T) {
			for _, kind := range []string{"pull", "issues"} {
				it := onlyItem(t, searchNodes(t, orderNode(t, kind, j{"bodyText": c.body})))
				if it.Excerpt != c.want {
					t.Errorf("%s: Excerpt = %q, want %q", kind, it.Excerpt, c.want)
				}
			}
		})
	}
}

func TestSearchGivesAnIssueItsAuthorOpenedTimeAndExcerptAndNoPullRequestFields(t *testing.T) {
	it := onlyItem(t, searchNodes(t, orderNode(t, "issues", j{
		"author": j{"login": "erin"}, "createdAt": "2026-10-01T12:00:00Z", "bodyText": "Crash on start.", "subIssuesSummary": j{"total": 0},
	})))
	if it.Author != "erin" || !it.OpenedAt.Equal(mustTime(t, "2026-10-01T12:00:00Z")) || it.Excerpt != "Crash on start." {
		t.Errorf("issue = author %q opened %v excerpt %q; want erin, 2026-10-01T12:00:00Z, Crash on start.", it.Author, it.OpenedAt, it.Excerpt)
	}
	if it.PR != nil || it.FailingChecks != 0 || it.ReviewedSincePush || !it.ReviewRequestedAt.IsZero() {
		t.Errorf("issue carries pull request fields: %+v", it)
	}
}
