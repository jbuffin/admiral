package e2e

import (
	"reflect"
	"strings"
	"testing"
	"time"
)

// "Remove from inbox" (#116 u5): a kept review item that can fail forever (a
// deleted repo, lost access, a revoked token, a GHE host that's gone) needs a
// way out. POST /api/inbox/<session>/<id>/discard {"forget": true} writes
// {action: "discarded"} without running gh, records review_discarded with
// unreachable: true, and answers "forgotten": true: nothing changed on GitHub.

// Discard fails against gh, and Remove from inbox then clears the item without
// touching gh again. It is allowed on a review that never failed, too.
func TestInboxReviewForget(t *testing.T) {
	w, g := reviewWorld(t)
	ship, dir := w.ghShip("f", "forget1")
	seedItem(t, dir, "r-1", reviewAt("r-1", "2026-10-22T10:00:00Z", prURL, 123))
	seedItem(t, dir, "r-2", reviewAt("r-2", "2026-10-22T10:01:00Z", prURL, 124))
	w.in.waitInbox(t, "r-1", "r-2")
	g.rest("del", "DELETE", ghReviews, 500, map[string]any{"message": "Server Error"}, "gh: Server Error (HTTP 500)\n")

	if code, _ := w.in.act(t, ship.Session, "r-1", "discard", nil); ok2xx(code) {
		t.Fatalf("discard against a failing gh = %d, want an error", code)
	}
	w.in.waitInbox(t, "r-1", "r-2")
	calls := len(g.restCalls())

	before := time.Now()
	code, resp := w.in.act(t, ship.Session, "r-1", "discard", map[string]any{"forget": true})
	if !ok2xx(code) {
		t.Fatalf("discard with forget = %d, want 2xx: %s", code, resp)
	}
	if !strings.Contains(resp, `"forgotten":true`) {
		t.Errorf("response = %s, want forgotten true: nothing changed on GitHub", resp)
	}
	if got, want := readAnswer(t, dir, "r-1", before), map[string]any{"version": float64(1), "id": "r-1", "kind": "review", "action": "discarded"}; !reflect.DeepEqual(got, want) {
		t.Errorf("answer file = %v, want %v", got, want)
	}
	w.in.waitInbox(t, "r-2")

	if code, resp := w.in.act(t, ship.Session, "r-2", "discard", map[string]any{"forget": true}); !ok2xx(code) {
		t.Fatalf("discard with forget of a review that never failed = %d: %s", code, resp)
	}
	w.in.waitInbox(t)

	if after := len(g.restCalls()); after != calls {
		t.Errorf("gh ran %d more time(s) for Remove from inbox, want none: %v", after-calls, g.restCalls())
	}
	log := w.waitEvent(ship.Session, "review_discarded", 2)
	for _, r := range log {
		if r["event"] == "review_discarded" && (r["unreachable"] != true || r["kind"] != "review" || r["external"] != nil || r["invalid_target"] != nil) {
			t.Errorf("review_discarded = %v, want kind review and unreachable true only", r)
		}
	}
}

// u2 round-3 LOW 3: the "token may not reach it" message is for a pull request
// that answered 404. A pull request lookup that failed another way shows gh's
// own message.
func TestInboxReviewAccessMessageOnlyForA404(t *testing.T) {
	w, g := reviewWorld(t)
	ship, dir := w.ghShip("f", "forget3")
	seedItem(t, dir, "hidden", reviewAt("hidden", "2026-10-22T10:00:00Z", "https://github.com/acme/widgets/pull/13", 300))
	seedItem(t, dir, "broken", reviewAt("broken", "2026-10-22T10:01:00Z", "https://github.com/acme/widgets/pull/14", 301))
	w.in.waitInbox(t, "hidden", "broken")
	nf := map[string]any{"message": "Not Found"}
	g.rest("d1", "DELETE", "repos/acme/widgets/pulls/13/reviews/300", 404, nf, "gh: Not Found (HTTP 404)\n")
	g.rest("p1", "GET", "repos/acme/widgets/pulls/13", 404, nf, "gh: Not Found (HTTP 404)\n")
	g.rest("d2", "DELETE", "repos/acme/widgets/pulls/14/reviews/301", 404, nf, "gh: Not Found (HTTP 404)\n")
	g.rest("p2", "GET", "repos/acme/widgets/pulls/14", 500, map[string]any{"message": "Server Error"}, "gh: Server Error (HTTP 500)\n")

	const prefix = "may not reach it"
	if code, resp := w.in.act(t, ship.Session, "hidden", "discard", nil); ok2xx(code) || !strings.Contains(resp, prefix) {
		t.Errorf("discard with the pull request 404 = %d %q, want an error with %q", code, resp, prefix)
	}
	code, resp := w.in.act(t, ship.Session, "broken", "discard", nil)
	if ok2xx(code) || !strings.Contains(resp, "Server Error") || strings.Contains(resp, prefix) {
		t.Errorf("discard with the pull request lookup failing with 500 = %d %q, want gh's Server Error and no %q", code, resp, prefix)
	}
	w.in.waitInbox(t, "hidden", "broken")
}
