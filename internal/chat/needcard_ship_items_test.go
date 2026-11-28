package chat

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/jbuffin/admiral/internal/inbox"
)

// Need cards for a ship's pending review and finding cards, beside the question card in needcard_test.go.

const reviewURL = "https://github.com/acme/widgets/pull/7"

func needReview(id, created, summary string, reviewID int) map[string]any {
	return map[string]any{"version": 1, "id": id, "kind": "review", "created": created, "title": "Review PR 7",
		"review": map[string]any{"pr_url": reviewURL, "review_id": reviewID, "summary": summary}}
}

func findingCard(id, proposalKind string) map[string]any {
	c := map[string]any{"id": id, "source": "review", "quote": "q " + id, "verdict": "agree", "reason": "because " + id, "evidence": []string{}}
	if proposalKind != "" {
		c["proposal"] = map[string]any{"kind": proposalKind, "content": "proposal for " + id}
	} else {
		c["proposal"] = nil
	}
	return c
}

// needFindings holds one card per proposal kind: a diff, no proposal, a reply.
func needFindings(id, created string, cards ...map[string]any) map[string]any {
	if len(cards) == 0 {
		cards = []map[string]any{findingCard("c1", "diff"), findingCard("c2", ""), findingCard("c3", "reply")}
	}
	return map[string]any{"version": 1, "id": id, "kind": "findings", "created": created, "title": "Findings on PR 7",
		"findings": map[string]any{"cards": cards}}
}

func (r *needRig) item(session, name string, v any) {
	r.t.Helper()
	r.write(session, name+".json", v)
}

// fakeGH is a gh on PATH that logs each call (arguments and stdin) and answers from files in its directory.
type fakeGH struct {
	t   *testing.T
	dir string
}

const fakeGHScript = `#!/bin/sh
d="$FAKEGH_DIR"
in=""
case "$*" in *--input*) in=$(cat);; esac
printf '%s | %s\n' "$*" "$in" >> "$d/calls"
if [ -f "$d/hold" ]; then
  while [ ! -f "$d/release" ]; do sleep 0.05; done
fi
if [ -f "$d/fail" ]; then cat "$d/fail" >&2; exit 1; fi
case "$*" in
  *"--method GET"*/reviews/*) cat "$d/review.json";;
  *) echo '{}';;
esac
`

func newFakeGH(t *testing.T) *fakeGH {
	t.Helper()
	g := &fakeGH{t: t, dir: t.TempDir()}
	bin := t.TempDir()
	if err := os.WriteFile(filepath.Join(bin, "gh"), []byte(fakeGHScript), 0o755); err != nil {
		t.Fatal(err)
	}
	g.put("review.json", `{"state":"PENDING","body":"the body on GitHub"}`)
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("FAKEGH_DIR", g.dir)
	return g
}

func (g *fakeGH) put(name, content string) {
	g.t.Helper()
	if err := os.WriteFile(filepath.Join(g.dir, name), []byte(content), 0o600); err != nil {
		g.t.Fatal(err)
	}
}

func (g *fakeGH) calls() []string {
	b, _ := os.ReadFile(filepath.Join(g.dir, "calls"))
	if len(b) == 0 {
		return nil
	}
	return strings.Split(strings.TrimSuffix(string(b), "\n"), "\n")
}

// outcomesOf is the outcome records written for a ship so far, without the stamp fields.
func (r *needRig) outcomesOf(ship string) []map[string]any {
	files, _ := filepath.Glob(filepath.Join(r.home, "outcomes", "*.jsonl"))
	var out []map[string]any
	for _, f := range files {
		b, _ := os.ReadFile(f)
		for _, line := range strings.Split(string(b), "\n") {
			var rec map[string]any
			if line == "" || json.Unmarshal([]byte(line), &rec) != nil || rec["ship"] != ship {
				continue
			}
			delete(rec, "ts")
			delete(rec, "v")
			delete(rec, "ship")
			out = append(out, rec)
		}
	}
	return out
}

func (r *needRig) answerOf(session, id string) map[string]any {
	r.t.Helper()
	b, ok := r.answerFile(session, id)
	if !ok {
		r.t.Fatalf("no answer file for %s/%s", session, id)
	}
	var a map[string]any
	if err := json.Unmarshal(b, &a); err != nil {
		r.t.Fatalf("answer file %s: %v", b, err)
	}
	delete(a, "answered")
	return a
}

func (r *needRig) cardFor(fleet, item string) needEntry {
	r.t.Helper()
	var got needEntry
	waitFor(r.t, "a card for item "+item+" in the "+fleet+" chat", func() bool {
		for _, c := range r.logged(fleet) {
			if c.Card.Item == item {
				got = c
				return true
			}
		}
		return false
	})
	return got
}

func rawCard(t *testing.T, e needEntry) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(e.raw, &m); err != nil {
		t.Fatal(err)
	}
	return m
}

func TestAShipsReviewAndFindingsEachPostOneNeedCardInItsFleetsChatWhetherOrNotThatChatIsOn(t *testing.T) {
	r := newNeedRig(t)
	r.item("s-work", "rv-1", needReview("rv-1", "2026-11-20T10:00:00Z", "sum", 11))
	r.item("s-off", "rv-2", needReview("rv-2", "2026-11-20T10:01:00Z", "sum", 12))
	r.item("s-home", "fd-1", needFindings("fd-1", "2026-11-20T10:02:00Z"))
	r.item("s-off", "fd-2", needFindings("fd-2", "2026-11-20T10:03:00Z"))
	for _, ship := range []string{"nofleet", "orphan", "s-ghost", "_chat-work", "_admiral"} {
		r.item(ship, "rv-x", needReview("rv-x", "2026-11-20T10:04:00Z", "sum", 13))
		r.item(ship, "fd-x", needFindings("fd-x", "2026-11-20T10:05:00Z"))
	}
	r.start()

	work := r.waitLogged("work", 1)
	off := r.waitLogged("off", 2)
	home := r.waitLogged("home", 1)
	holdCards(t, 300*time.Millisecond, func() string {
		if n := len(r.logged("work")) + len(r.logged("off")) + len(r.logged("home")); n != 4 {
			return "the chats hold more or fewer than the four cards of the four ship-level items of a fleet, so an item of no fleet, an unknown fleet, the chat or _admiral posted one"
		}
		return ""
	})
	if c := work[0].Card; c.Ship != "s-work" || c.Item != "rv-1" || c.ItemKind != "review" || c.State != "open" {
		t.Errorf("work's card = %+v, want the open review card of s-work/rv-1", c)
	}
	if c := home[0].Card; c.Ship != "s-home" || c.Item != "fd-1" || c.ItemKind != "findings" || c.State != "open" {
		t.Errorf("home's card = %+v, want the open findings card of s-home/fd-1", c)
	}
	kinds := map[string]string{}
	for _, c := range off {
		kinds[c.Card.Item] = c.Card.ItemKind
	}
	if !reflect.DeepEqual(kinds, map[string]string{"rv-2": "review", "fd-2": "findings"}) {
		t.Errorf("a fleet with its chat off holds cards %v, want its review and its findings card", kinds)
	}
	if work[0].Text != "Review PR 7" {
		t.Errorf("review card text = %q, want the item's title", work[0].Text)
	}

	r.restart()
	r.in.Refresh()
	holdCards(t, 300*time.Millisecond, func() string {
		if n := len(r.logged("work")) + len(r.logged("off")) + len(r.logged("home")); n != 4 {
			return "a second pass or a restart posted a card again, or lost one"
		}
		return ""
	})
}

func TestAReviewCardCarriesTheThreeEventsAndDiscardAndAFindingsCardWhatItOffersPerFinding(t *testing.T) {
	r := newNeedRig(t)
	r.item("s-work", "rv-1", needReview("rv-1", "2026-11-20T10:00:00Z", "sum", 11))
	r.item("s-work", "fd-1", needFindings("fd-1", "2026-11-20T10:02:00Z"))
	r.start()
	r.waitLogged("work", 2)

	review := rawCard(t, r.cardFor("work", "rv-1"))
	if got, _ := json.Marshal(review["events"]); string(got) != `["COMMENT","APPROVE","REQUEST_CHANGES"]` {
		t.Errorf("review card events = %s, want [\"COMMENT\",\"APPROVE\",\"REQUEST_CHANGES\"]", got)
	}
	if review["discard"] != true {
		t.Errorf("review card discard = %v, want true: the card offers Discard", review["discard"])
	}

	if review["pr_url"] != reviewURL {
		t.Errorf("review card pr_url = %v, want %s: Edit needs the pull request's link", review["pr_url"], reviewURL)
	}

	findings := rawCard(t, r.cardFor("work", "fd-1"))
	b, _ := json.Marshal(findings["cards"])
	want := `[{"id":"c1","proposal":{"kind":"diff","content":"proposal for c1"}},{"id":"c2","proposal":null},{"id":"c3","proposal":{"kind":"reply","content":"proposal for c3"}}]`
	var gotCards, wantCards []map[string]any
	_ = json.Unmarshal(b, &gotCards)
	_ = json.Unmarshal([]byte(want), &wantCards)
	for i := range gotCards { // only the fields the choices need; the item's own text is the web's to show
		for k := range gotCards[i] {
			if k != "id" && k != "proposal" {
				delete(gotCards[i], k)
			}
		}
	}
	if !reflect.DeepEqual(gotCards, wantCards) {
		t.Errorf("findings card cards = %s, want each finding's id and proposal (kind and content, or null): %s", b, want)
	}
}

type twinSide struct {
	calls    []string
	answer   map[string]any
	outcomes []map[string]any
}

// twins runs the Inbox's own action on s-work's item and the card's tap on s-home's twin of it, and returns both sides.
func (r *needRig) twins(t *testing.T, g *fakeGH, item string, viaInbox func() error, viaCard func() *httptest.ResponseRecorder, wantOutcomes int) (inboxSide, cardSide twinSide, rec *httptest.ResponseRecorder) {
	t.Helper()
	before := len(g.calls())
	if err := viaInbox(); err != nil {
		t.Fatalf("the Inbox's own action: %v", err)
	}
	inboxSide.calls = g.calls()[before:]
	before = len(g.calls())
	rec = viaCard()
	if rec.Code != http.StatusOK {
		t.Fatalf("the card's tap = %d %s, want 200", rec.Code, rec.Body)
	}
	cardSide.calls = g.calls()[before:]
	inboxSide.answer, cardSide.answer = r.answerOf("s-work", item), r.answerOf("s-home", item)
	waitFor(t, "both ships' outcome records written", func() bool {
		return len(r.outcomesOf("s-work")) >= wantOutcomes && len(r.outcomesOf("s-home")) >= wantOutcomes
	})
	inboxSide.outcomes, cardSide.outcomes = r.outcomesOf("s-work"), r.outcomesOf("s-home")
	return
}

func sameSide(t *testing.T, in, card twinSide) {
	t.Helper()
	if !reflect.DeepEqual(in.calls, card.calls) {
		t.Errorf("gh calls through the card = %q, want the Inbox's %q", card.calls, in.calls)
	}
	if !reflect.DeepEqual(in.answer, card.answer) {
		t.Errorf("answer file through the card = %v, want the Inbox's %v", card.answer, in.answer)
	}
	if !reflect.DeepEqual(in.outcomes, card.outcomes) {
		t.Errorf("outcome records through the card = %v, want the Inbox's %v", card.outcomes, in.outcomes)
	}
}

func TestAnsweringAReviewCardSubmitsThroughTheInboxsPathWithTheSameGhCallsAnswerAndOutcome(t *testing.T) {
	g := newFakeGH(t)
	r := newNeedRig(t)
	r.item("s-work", "rv-1", needReview("rv-1", "2026-11-20T10:00:00Z", "the summary", 11))
	r.item("s-home", "rv-1", needReview("rv-1", "2026-11-20T10:00:00Z", "the summary", 11))
	r.start()
	card := r.waitLogged("home", 1)[0]

	in, cd, rec := r.twins(t, g, "rv-1",
		func() error {
			_, err := r.in.SubmitReview("s-work", "rv-1", inbox.SubmitRequest{Event: "APPROVE", Edited: true})
			return err
		},
		func() *httptest.ResponseRecorder {
			return r.post("home", card.ID, map[string]any{"event": "APPROVE", "edited": true})
		}, 1)
	if len(in.calls) != 2 || !strings.Contains(in.calls[1], "/events") || !strings.Contains(in.calls[1], `"event":"APPROVE"`) {
		t.Fatalf("the Inbox's own submit made gh calls %q, want a GET and a POST of the APPROVE event: the comparison would prove nothing", in.calls)
	}
	sameSide(t, in, cd)
	if !strings.Contains(rec.Body.String(), `"answered":"rv-1"`) || !strings.Contains(rec.Body.String(), `"action":"submitted"`) {
		t.Errorf("answer body = %s, want the Inbox's {\"answered\":\"rv-1\",\"action\":\"submitted\"}", rec.Body)
	}
	if cd.outcomes[0]["event"] != "review_submitted" || cd.outcomes[0]["review_event"] != "APPROVE" || cd.outcomes[0]["edited"] != true {
		t.Errorf("outcome = %v, want review_submitted with review_event APPROVE and edited true", cd.outcomes)
	}
	r.waitState("home", card.ID, "answered", "chat")
}

func TestAnsweringAReviewCardWithDiscardDiscardsThroughTheInboxsPath(t *testing.T) {
	g := newFakeGH(t)
	r := newNeedRig(t)
	r.item("s-work", "rv-1", needReview("rv-1", "2026-11-20T10:00:00Z", "the summary", 11))
	r.item("s-home", "rv-1", needReview("rv-1", "2026-11-20T10:00:00Z", "the summary", 11))
	r.start()
	card := r.waitLogged("home", 1)[0]

	in, cd, _ := r.twins(t, g, "rv-1",
		func() error { _, err := r.in.DiscardReview("s-work", "rv-1"); return err },
		func() *httptest.ResponseRecorder { return r.post("home", card.ID, map[string]any{"discard": true}) }, 1)
	if len(in.calls) != 1 || !strings.Contains(in.calls[0], "--method DELETE") {
		t.Fatalf("the Inbox's own discard made gh calls %q, want one DELETE", in.calls)
	}
	sameSide(t, in, cd)
	if cd.outcomes[0]["event"] != "review_discarded" {
		t.Errorf("outcome = %v, want review_discarded", cd.outcomes)
	}
	r.waitState("home", card.ID, "answered", "chat")
}

func TestAnsweringAFindingsCardWritesTheInboxsAnswerAndOutcomesAndRunsNoGh(t *testing.T) {
	g := newFakeGH(t)
	r := newNeedRig(t)
	r.item("s-work", "fd-1", needFindings("fd-1", "2026-11-20T10:00:00Z"))
	r.item("s-home", "fd-1", needFindings("fd-1", "2026-11-20T10:00:00Z"))
	r.start()
	card := r.waitLogged("home", 1)[0]

	req := inbox.FindingsRequest{Cards: []inbox.CardDecision{
		{ID: "c1", Decision: "edited", Proposal: &inbox.Proposal{Kind: "diff", Content: "my own diff"}},
		{ID: "c2", Decision: "approved"},
		{ID: "c3", Decision: "rejected"},
	}}
	in, cd, rec := r.twins(t, g, "fd-1",
		func() error { return r.in.AnswerFindings("s-work", "fd-1", req) },
		func() *httptest.ResponseRecorder { return r.post("home", card.ID, req) }, 3)
	sameSide(t, in, cd)
	if len(cd.calls) != 0 {
		t.Errorf("answering findings ran gh: %q", cd.calls)
	}
	if !strings.Contains(rec.Body.String(), `"answered":"fd-1"`) {
		t.Errorf("answer body = %s, want {\"answered\":\"fd-1\"}", rec.Body)
	}
	r.waitState("home", card.ID, "answered", "chat")
}

func TestACardWhoseItemWasReplacedIsRefusedWith409AndWritesNothing(t *testing.T) {
	const created = "2026-11-20T10:00:00Z"
	for _, tc := range []struct {
		name    string
		first   map[string]any
		replace map[string]any
		body    any
	}{
		{"review with a new created", needReview("it-1", created, "sum", 11), needReview("it-1", "2026-11-22T09:00:00Z", "sum", 11), map[string]any{"event": "APPROVE"}},
		{"review with another review", needReview("it-1", created, "sum", 11), needReview("it-1", created, "sum", 99), map[string]any{"event": "APPROVE"}},
		{"review discard with another summary", needReview("it-1", created, "sum", 11), needReview("it-1", created, "another summary", 11), map[string]any{"discard": true}},
		{"findings with a new created", needFindings("it-1", created), needFindings("it-1", "2026-11-22T09:00:00Z"), allApproved()},
		{"findings with a changed proposal", needFindings("it-1", created), needFindings("it-1", created, findingCard("c1", "diff"), findingCard("c2", ""), withContent(findingCard("c3", "reply"), "a different reply")), allApproved()},
		{"findings with other cards", needFindings("it-1", created), needFindings("it-1", created, findingCard("c1", "diff"), findingCard("c2", ""), findingCard("c3", "reply"), findingCard("c4", "")), allApproved()},
	} {
		t.Run(tc.name, func(t *testing.T) {
			g := newFakeGH(t)
			r := newNeedRig(t)
			r.item("s-work", "it-1", tc.first)
			r.start()
			card := r.waitLogged("work", 1)[0]

			r.item("s-work", "it-1", tc.replace)
			r.in.Refresh()
			rec := r.post("work", card.ID, tc.body)
			if rec.Code != http.StatusConflict {
				t.Fatalf("POST for a replaced item = %d %s, want 409", rec.Code, rec.Body)
			}
			if _, ok := r.answerFile("s-work", "it-1"); ok {
				t.Error("an answer file was written for an item the engineer never saw")
			}
			if calls := g.calls(); len(calls) != 0 {
				t.Errorf("a refused card ran gh: %q", calls)
			}
			holdCards(t, 100*time.Millisecond, func() string {
				if n := len(r.outcomesOf("s-work")); n != 0 {
					return "a refused card wrote an outcome record"
				}
				return ""
			})
		})
	}
}

func withContent(c map[string]any, content string) map[string]any {
	c["proposal"] = map[string]any{"kind": "reply", "content": content}
	return c
}

func allApproved() inbox.FindingsRequest {
	return inbox.FindingsRequest{Cards: []inbox.CardDecision{{ID: "c1", Decision: "approved"}, {ID: "c2", Decision: "approved"}, {ID: "c3", Decision: "approved"}}}
}

func TestOnlyTheFirstAnswerToAReviewOrFindingsItemCountsAndALateOneChangesNothing(t *testing.T) {
	g := newFakeGH(t)
	r := newNeedRig(t)
	r.item("s-work", "rv-1", needReview("rv-1", "2026-11-20T10:00:00Z", "sum", 11))
	r.item("s-work", "rv-2", needReview("rv-2", "2026-11-20T10:01:00Z", "sum", 12))
	r.item("s-work", "fd-1", needFindings("fd-1", "2026-11-20T10:02:00Z"))
	r.item("s-work", "fd-2", needFindings("fd-2", "2026-11-20T10:03:00Z"))
	r.start()
	r.waitLogged("work", 4)
	id := func(item string) string { return r.cardFor("work", item).ID }
	approve := map[string]any{"event": "APPROVE"}
	rejectAll := inbox.FindingsRequest{Cards: []inbox.CardDecision{{ID: "c1", Decision: "rejected"}, {ID: "c2", Decision: "rejected"}, {ID: "c3", Decision: "rejected"}}}

	if rec := r.post("work", id("rv-1"), approve); rec.Code != 200 {
		t.Fatalf("first review tap = %d %s", rec.Code, rec.Body)
	}
	if rec := r.post("work", id("fd-1"), allApproved()); rec.Code != 200 {
		t.Fatalf("first findings tap = %d %s", rec.Code, rec.Body)
	}
	if _, err := r.in.SubmitReview("s-work", "rv-2", inbox.SubmitRequest{Event: "COMMENT"}); err != nil {
		t.Fatal(err)
	}
	if err := r.in.AnswerFindings("s-work", "fd-2", allApproved()); err != nil {
		t.Fatal(err)
	}
	callsBefore := len(g.calls())
	answers := map[string]map[string]any{}
	for _, it := range []string{"rv-1", "rv-2", "fd-1", "fd-2"} {
		answers[it] = r.answerOf("s-work", it)
	}
	waitFor(t, "the first answers' records written", func() bool { return len(r.outcomesOf("s-work")) >= 8 })
	outcomeCount := len(r.outcomesOf("s-work"))

	for _, tc := range []struct {
		name, item string
		body       any
	}{
		{"a second tap on a review card", "rv-1", map[string]any{"event": "REQUEST_CHANGES"}},
		{"a tap on a review the Inbox answered", "rv-2", approve},
		{"a second tap on a findings card", "fd-1", rejectAll},
		{"a tap on findings the Inbox answered", "fd-2", rejectAll},
	} {
		rec := r.post("work", id(tc.item), tc.body)
		if rec.Code != http.StatusConflict || errorOf(rec) != "item "+tc.item+" is already answered" {
			t.Errorf("%s = %d %s, want 409 \"item %s is already answered\"", tc.name, rec.Code, rec.Body, tc.item)
		}
		if !reflect.DeepEqual(r.answerOf("s-work", tc.item), answers[tc.item]) {
			t.Errorf("%s changed the answer file", tc.name)
		}
	}
	if calls := g.calls(); len(calls) != callsBefore {
		t.Errorf("a late answer ran gh: %q", calls[callsBefore:])
	}
	holdCards(t, 100*time.Millisecond, func() string {
		if n := len(r.outcomesOf("s-work")); n != outcomeCount {
			return "a late answer wrote an outcome record"
		}
		return ""
	})
}

func TestAReviewOrFindingsItemAnsweredOrRemovedInTheInboxSettlesItsCard(t *testing.T) {
	for _, tc := range []struct {
		name         string
		item         func() map[string]any
		act          func(t *testing.T, r *needRig, card string)
		state, where string
	}{
		{"a review submitted in the Inbox", func() map[string]any { return needReview("it-1", "2026-11-20T10:00:00Z", "s", 11) }, func(t *testing.T, r *needRig, _ string) {
			if _, err := r.in.SubmitReview("s-work", "it-1", inbox.SubmitRequest{Event: "COMMENT"}); err != nil {
				t.Fatal(err)
			}
		}, "answered", "inbox"},
		{"a review discarded in the Inbox", func() map[string]any { return needReview("it-1", "2026-11-20T10:00:00Z", "s", 11) }, func(t *testing.T, r *needRig, _ string) {
			if _, err := r.in.DiscardReview("s-work", "it-1"); err != nil {
				t.Fatal(err)
			}
		}, "answered", "inbox"},
		{"a review answered through the card", func() map[string]any { return needReview("it-1", "2026-11-20T10:00:00Z", "s", 11) }, func(t *testing.T, r *needRig, card string) {
			if rec := r.post("work", card, map[string]any{"event": "COMMENT"}); rec.Code != 200 {
				t.Fatalf("POST = %d %s", rec.Code, rec.Body)
			}
		}, "answered", "chat"},
		{"a review removed in the Inbox", func() map[string]any { return needReview("it-1", "2026-11-20T10:00:00Z", "s", 11) }, func(t *testing.T, r *needRig, _ string) {
			if _, err := r.in.RemoveItem("s-work", "it-1"); err != nil {
				t.Fatal(err)
			}
		}, "removed", ""},
		{"findings answered in the Inbox", func() map[string]any { return needFindings("it-1", "2026-11-20T10:00:00Z") }, func(t *testing.T, r *needRig, _ string) {
			if err := r.in.AnswerFindings("s-work", "it-1", allApproved()); err != nil {
				t.Fatal(err)
			}
		}, "answered", "inbox"},
		{"findings answered through the card", func() map[string]any { return needFindings("it-1", "2026-11-20T10:00:00Z") }, func(t *testing.T, r *needRig, card string) {
			if rec := r.post("work", card, allApproved()); rec.Code != 200 {
				t.Fatalf("POST = %d %s", rec.Code, rec.Body)
			}
		}, "answered", "chat"},
		{"findings removed in the Inbox", func() map[string]any { return needFindings("it-1", "2026-11-20T10:00:00Z") }, func(t *testing.T, r *needRig, _ string) {
			if _, err := r.in.RemoveItem("s-work", "it-1"); err != nil {
				t.Fatal(err)
			}
		}, "removed", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			newFakeGH(t)
			r := newNeedRig(t)
			r.item("s-work", "it-1", tc.item())
			r.start()
			card := r.waitLogged("work", 1)[0]
			tc.act(t, r, card.ID)
			r.waitState("work", card.ID, tc.state, tc.where)
		})
	}
}

func TestACardRefusesABadReviewEventOrBadFindingDecisionsWith400AndTheInboxsMessage(t *testing.T) {
	g := newFakeGH(t)
	r := newNeedRig(t)
	r.item("s-work", "rv-1", needReview("rv-1", "2026-11-20T10:00:00Z", "sum", 11))
	r.item("s-work", "fd-1", needFindings("fd-1", "2026-11-20T10:01:00Z"))
	r.start()
	r.waitLogged("work", 2)
	review, findings := r.cardFor("work", "rv-1").ID, r.cardFor("work", "fd-1").ID
	cards := func(ds ...inbox.CardDecision) inbox.FindingsRequest { return inbox.FindingsRequest{Cards: ds} }

	for _, tc := range []struct {
		name, card, item string
		body             any
		msg              string
	}{
		{"an unknown review event", review, "rv-1", map[string]any{"event": "MERGE"}, "event must be COMMENT, APPROVE or REQUEST_CHANGES"},
		{"a decision outside approved, edited and rejected", findings, "fd-1", cards(inbox.CardDecision{ID: "c1", Decision: "maybe"}, inbox.CardDecision{ID: "c2", Decision: "approved"}, inbox.CardDecision{ID: "c3", Decision: "approved"}), `card c1: "maybe" is not approved, edited or rejected`},
		{"a finding card the item lacks", findings, "fd-1", cards(inbox.CardDecision{ID: "zz", Decision: "approved"}), `the item has no card "zz"`},
		{"a partial set of decisions", findings, "fd-1", cards(inbox.CardDecision{ID: "c1", Decision: "approved"}, inbox.CardDecision{ID: "c3", Decision: "approved"}), "card c2 has no decision"},
		{"an edit with no proposal", findings, "fd-1", cards(inbox.CardDecision{ID: "c1", Decision: "edited"}, inbox.CardDecision{ID: "c2", Decision: "approved"}, inbox.CardDecision{ID: "c3", Decision: "approved"}), "card c1 is edited, so it needs a proposal with kind diff or reply and some content"},
	} {
		rec := r.post("work", tc.card, tc.body)
		if rec.Code != http.StatusBadRequest || errorOf(rec) != tc.msg {
			t.Errorf("%s = %d %s, want 400 %q", tc.name, rec.Code, rec.Body, tc.msg)
		}
		if _, ok := r.answerFile("s-work", tc.item); ok {
			t.Errorf("%s wrote an answer file", tc.name)
		}
	}
	if calls := g.calls(); len(calls) != 0 {
		t.Errorf("a refused review ran gh: %q", calls)
	}
	if c := r.served("work")[0]; c.Card.State != "open" {
		t.Errorf("card state = %q after refused answers, want open", c.Card.State)
	}
}

func TestATapOnAReviewWhoseActionIsStillRunningAnswers409AndRunsNothingMore(t *testing.T) {
	g := newFakeGH(t)
	r := newNeedRig(t)
	r.item("s-work", "rv-1", needReview("rv-1", "2026-11-20T10:00:00Z", "sum", 11))
	r.start()
	card := r.waitLogged("work", 1)[0]

	g.put("hold", "")
	t.Cleanup(func() { g.put("release", "") })
	done := make(chan error, 1)
	go func() {
		_, err := r.in.SubmitReview("s-work", "rv-1", inbox.SubmitRequest{Event: "COMMENT"})
		done <- err
	}()
	waitFor(t, "the Inbox's submit reaching gh", func() bool { return len(g.calls()) == 1 })

	rec := r.post("work", card.ID, map[string]any{"event": "APPROVE"})
	if rec.Code != http.StatusConflict || errorOf(rec) != inbox.ErrBusy.Error() {
		t.Errorf("a tap while the action runs = %d %s, want 409 %q", rec.Code, rec.Body, inbox.ErrBusy)
	}
	if n := len(g.calls()); n != 1 {
		t.Errorf("a busy tap ran gh again: %q", g.calls())
	}
	g.put("release", "")
	if err := <-done; err != nil {
		t.Fatalf("the Inbox's submit: %v", err)
	}
}

func TestAGhFailureAnswersTheCardWithTheStatusTheInboxsSubmitRouteReturnsAndLeavesTheCardOpen(t *testing.T) {
	for _, tc := range []struct {
		name   string
		setup  func(t *testing.T, g *fakeGH)
		status int
	}{
		{"gh fails", func(t *testing.T, g *fakeGH) { g.put("fail", "gh: boom (HTTP 500)") }, http.StatusBadGateway},
		{"gh is not on PATH", func(t *testing.T, _ *fakeGH) { t.Setenv("PATH", t.TempDir()) }, http.StatusServiceUnavailable},
	} {
		t.Run(tc.name, func(t *testing.T) {
			g := newFakeGH(t)
			r := newNeedRig(t)
			r.item("s-work", "rv-1", needReview("rv-1", "2026-11-20T10:00:00Z", "sum", 11))
			r.start()
			card := r.waitLogged("work", 1)[0]
			tc.setup(t, g)

			inboxMux := http.NewServeMux()
			r.in.Routes(inboxMux)
			want := httptest.NewRecorder()
			inboxMux.ServeHTTP(want, httptest.NewRequest("POST", "/api/inbox/s-work/rv-1/submit", strings.NewReader(`{"event":"APPROVE"}`)))
			if want.Code != tc.status {
				t.Fatalf("the Inbox's submit route = %d %s, want %d: the test's premise", want.Code, want.Body, tc.status)
			}
			rec := r.post("work", card.ID, map[string]any{"event": "APPROVE"})
			if rec.Code != tc.status || errorOf(rec) != errorOf(want) {
				t.Errorf("the card's tap = %d %s, want the Inbox's %d %s", rec.Code, rec.Body, want.Code, want.Body)
			}
			if _, ok := r.answerFile("s-work", "rv-1"); ok {
				t.Error("a failed gh call wrote an answer file")
			}
			if c := r.served("work")[0]; c.Card.State != "open" {
				t.Errorf("card state = %q after a gh failure, want open", c.Card.State)
			}
		})
	}
}
