package chat

import (
	"net/http"
	"reflect"
	"strings"
	"testing"

	"github.com/jbuffin/admiral/internal/inbox"
	"github.com/jbuffin/admiral/internal/needcard"
	"github.com/jbuffin/admiral/internal/ships"
)

// A card's tap by choice id: the generic body {"choice": "<id>"} on the answer route, mapped to each kind's Inbox path.

// oneQuestion is a question item with one single-select question, the one a push offers a choice per option for.
func oneQuestion(id, created string) map[string]any {
	return map[string]any{"version": 1, "id": id, "kind": "question", "created": created, "title": "Which way?",
		"question": map[string]any{"questions": []any{
			map[string]any{"id": "1", "text": "Pick one", "multi": false,
				"options": []any{map[string]any{"label": "Alpha"}, map[string]any{"label": "Beta"}}},
		}}}
}

func choose(id string) map[string]any { return map[string]any{"choice": id} }

func TestAChoiceOnAQuestionCardAnswersWithTheOptionAtThatIndexReadFromTheItem(t *testing.T) {
	r := newNeedRig(t)
	r.item("s-work", "q-1", oneQuestion("q-1", createdA))
	r.start()
	card := r.waitLogged("work", 1)[0]

	rec := r.post("work", card.ID, choose("1"))
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"answered":"q-1"`) {
		t.Fatalf("POST choice 1 = %d %s, want 200 answered q-1", rec.Code, rec.Body)
	}
	got := r.answerOf("s-work", "q-1")
	want := map[string]any{"version": float64(1), "id": "q-1", "kind": "question",
		"answers": map[string]any{"1": map[string]any{"choice": "Beta"}}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("answer file = %v, want %v: option 1 is Beta", got, want)
	}
	r.waitState("work", card.ID, "answered", "chat")

	if rec := r.post("work", card.ID, choose("0")); rec.Code != http.StatusConflict {
		t.Errorf("second POST = %d %s, want 409 once the card is answered", rec.Code, rec.Body)
	}
}

func TestAChoiceThatIsNoOptionOfTheQuestionIs400AndLeavesTheCardOpen(t *testing.T) {
	r := newNeedRig(t)
	r.item("s-work", "q-1", oneQuestion("q-1", createdA))
	r.start()
	card := r.waitLogged("work", 1)[0]

	for _, id := range []string{"2", "-1", "Beta", "", " 0"} {
		if rec := r.post("work", card.ID, choose(id)); rec.Code != http.StatusBadRequest {
			t.Errorf("POST choice %q = %d %s, want 400", id, rec.Code, rec.Body)
		}
	}
	if _, ok := r.answerFile("s-work", "q-1"); ok {
		t.Error("a refused choice wrote an answer file")
	}
	if rec := r.post("work", card.ID, choose("0")); rec.Code != http.StatusOK {
		t.Errorf("POST choice 0 after the refusals = %d %s, want 200: a refused choice leaves the card open", rec.Code, rec.Body)
	}
}

func TestAChoiceOnACardWithNoChoicesIs400AndWritesNothing(t *testing.T) {
	r := newNeedRig(t)
	r.fleetHooks()
	r.item("s-work", "q-2", needQuestion("q-2", createdA)) // two questions
	r.item("s-work", "fd-1", needFindings("fd-1", createdB))
	r.item("_admiral", "allow-1", fleetAllow("allow-1", "work", viewRuleCard()))
	r.start()

	for name, item := range map[string]struct{ session, id string }{
		"a question item with two questions": {"s-work", "q-2"},
		"a findings item":                    {"s-work", "fd-1"},
		"an allow rules item":                {"_admiral", "allow-1"},
	} {
		card := r.cardFor("work", item.id)
		for _, id := range []string{"0", "approve", "allow", "add"} {
			if rec := r.post("work", card.ID, choose(id)); rec.Code != http.StatusBadRequest {
				t.Errorf("%s: POST choice %q = %d %s, want 400", name, id, rec.Code, rec.Body)
			}
		}
		if b, ok := r.answerFile(item.session, item.id); ok {
			t.Errorf("%s: a choice wrote the answer file %s", name, b)
		}
	}
}

func TestAChoiceOnAReviewCardSubmitsItsEventOrDiscardsThroughTheInboxsPath(t *testing.T) {
	for _, tc := range []struct{ choice, call, outcome string }{
		{"COMMENT", `"event":"COMMENT"`, "review_submitted"},
		{"APPROVE", `"event":"APPROVE"`, "review_submitted"},
		{"REQUEST_CHANGES", `"event":"REQUEST_CHANGES"`, "review_submitted"},
		{"discard", "--method DELETE", "review_discarded"},
	} {
		t.Run(tc.choice, func(t *testing.T) {
			g := newFakeGH(t)
			r := newNeedRig(t)
			r.item("s-work", "rv-1", needReview("rv-1", createdA, "the summary", 11))
			r.start()
			card := r.waitLogged("work", 1)[0]

			rec := r.post("work", card.ID, choose(tc.choice))
			if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"answered":"rv-1"`) {
				t.Fatalf("POST choice %s = %d %s, want 200 answered rv-1", tc.choice, rec.Code, rec.Body)
			}
			calls := g.calls()
			if len(calls) == 0 || !strings.Contains(calls[len(calls)-1], tc.call) {
				t.Errorf("gh calls = %q, want the last to carry %s", calls, tc.call)
			}
			out := r.outcomesOf("s-work")
			if len(out) != 1 || out[0]["event"] != tc.outcome {
				t.Errorf("outcomes = %v, want one %s", out, tc.outcome)
			}
			r.waitState("work", card.ID, "answered", "chat")
		})
	}
}

func TestAChoiceOnAReviewCardThatIsNoEventIs400AndRunsNoGh(t *testing.T) {
	g := newFakeGH(t)
	r := newNeedRig(t)
	r.item("s-work", "rv-1", needReview("rv-1", createdA, "the summary", 11))
	r.start()
	card := r.waitLogged("work", 1)[0]

	for _, id := range []string{"approve", "MERGE", "launch", "0", ""} {
		if rec := r.post("work", card.ID, choose(id)); rec.Code != http.StatusBadRequest {
			t.Errorf("POST choice %q = %d %s, want 400", id, rec.Code, rec.Body)
		}
	}
	if calls := g.calls(); len(calls) != 0 {
		t.Errorf("refused choices ran gh: %q", calls)
	}
}

func TestAChoiceOnARoutineProposalCardLaunchesOrDismissesIt(t *testing.T) {
	t.Run("launch", func(t *testing.T) {
		r := newNeedRig(t)
		f := r.fleetHooks()
		r.item("_admiral", "routine-t1", fleetRoutine("t1", "home", createdA, inAnHour(), sweepAction))
		r.start()
		card := r.cardFor("home", "routine-t1")

		rec := r.post("home", card.ID, choose("launch"))
		if !is2xx(rec) {
			t.Fatalf("POST choice launch = %d %s, want success", rec.Code, rec.Body)
		}
		bodyIs(t, rec, `{"ship":{"session":"ship-1"}}`)
		if f.launched() != 1 || f.dismissed() != 0 {
			t.Errorf("hooks: %d launches %d dismissals, want one launch", f.launched(), f.dismissed())
		}
		r.waitState("home", card.ID, "answered", "chat")
	})
	t.Run("dismiss", func(t *testing.T) {
		r := newNeedRig(t)
		f := r.fleetHooks()
		r.item("_admiral", "routine-t1", fleetRoutine("t1", "home", createdA, inAnHour(), sweepAction))
		r.start()
		card := r.cardFor("home", "routine-t1")

		if rec := r.post("home", card.ID, choose("dismiss")); !is2xx(rec) {
			t.Fatalf("POST choice dismiss = %d %s, want success", rec.Code, rec.Body)
		}
		if f.launched() != 0 || f.dismissed() != 1 {
			t.Errorf("hooks: %d launches %d dismissals, want one dismissal", f.launched(), f.dismissed())
		}
		r.waitState("home", card.ID, "answered", "chat")
	})
	t.Run("an unknown id", func(t *testing.T) {
		r := newNeedRig(t)
		f := r.fleetHooks()
		r.item("_admiral", "routine-t1", fleetRoutine("t1", "home", createdA, inAnHour(), sweepAction))
		r.start()
		card := r.cardFor("home", "routine-t1")

		if rec := r.post("home", card.ID, choose("create")); rec.Code != http.StatusBadRequest {
			t.Errorf("POST choice create on a routine = %d %s, want 400", rec.Code, rec.Body)
		}
		if f.launched() != 0 || f.dismissed() != 0 {
			t.Error("a refused choice ran a hook")
		}
	})
}

func TestAChoiceOnACampaignDraftCardCreatesItFromTheItemsOwnDraftOrDismissesIt(t *testing.T) {
	t.Run("create", func(t *testing.T) {
		r := newNeedRig(t)
		f := r.fleetHooks()
		r.item("_admiral", "camp-1", fleetDraft("camp-1", "home", createdA, "Speed up the fleet map", "Load the map in under a second."))
		r.start()
		card := r.cardFor("home", "camp-1")

		rec := r.post("home", card.ID, choose("create"))
		if !is2xx(rec) {
			t.Fatalf("POST choice create = %d %s, want success", rec.Code, rec.Body)
		}
		bodyIs(t, rec, `{"campaign":{"id":"camp-1","url":"`+campaignOK+`"}}`)
		want := inbox.CampaignDraft{Fleet: "home", Repo: "acme/widgets", Title: "Speed up the fleet map", Body: "Load the map in under a second.", Via: "AdmiralBot"}
		if f.created() != 1 || f.creates[0] != want {
			t.Errorf("create hook got %+v, want one call with the item's own draft %+v", f.creates, want)
		}
		r.waitState("home", card.ID, "answered", "chat")
	})
	t.Run("a draft with no body keeps the Inbox's refusal", func(t *testing.T) {
		r := newNeedRig(t)
		f := r.fleetHooks()
		r.item("_admiral", "camp-1", fleetDraft("camp-1", "home", createdA, "Speed up the fleet map", ""))
		r.start()
		card := r.cardFor("home", "camp-1")

		if rec := r.post("home", card.ID, choose("create")); rec.Code != http.StatusBadRequest {
			t.Errorf("POST choice create on an empty draft = %d %s, want the Inbox's 400", rec.Code, rec.Body)
		}
		if f.created() != 0 {
			t.Error("a refused create filed a campaign")
		}
	})
	t.Run("dismiss", func(t *testing.T) {
		r := newNeedRig(t)
		f := r.fleetHooks()
		r.item("_admiral", "camp-1", fleetDraft("camp-1", "home", createdA, "Speed up the fleet map", "Load the map in under a second."))
		r.start()
		card := r.cardFor("home", "camp-1")

		if rec := r.post("home", card.ID, choose("dismiss")); !is2xx(rec) {
			t.Fatalf("POST choice dismiss = %d %s, want success", rec.Code, rec.Body)
		}
		if f.created() != 0 {
			t.Errorf("a dismissal created %d campaigns", f.created())
		}
		r.waitState("home", card.ID, "answered", "chat")
	})
	t.Run("an unknown id", func(t *testing.T) {
		r := newNeedRig(t)
		f := r.fleetHooks()
		r.item("_admiral", "camp-1", fleetDraft("camp-1", "home", createdA, "Speed up the fleet map", "Load the map in under a second."))
		r.start()
		card := r.cardFor("home", "camp-1")

		if rec := r.post("home", card.ID, choose("launch")); rec.Code != http.StatusBadRequest {
			t.Errorf("POST choice launch on a campaign draft = %d %s, want 400", rec.Code, rec.Body)
		}
		if f.created() != 0 {
			t.Error("a refused choice filed a campaign")
		}
	})
}

func TestAChoiceOnAPermissionStopCardIsRefusedWhenItsIdIsUnknown(t *testing.T) {
	r := newNeedRig(t)
	r.setShips(needing("s-work", "work", "permission:Bash", "p-1"))
	r.start()
	c := r.stopCardFor("work", "s-work")

	if rec := r.post("work", c.ID, choose("approve")); rec.Code != http.StatusBadRequest {
		t.Errorf("POST choice approve = %d %s, want 400", rec.Code, rec.Body)
	}
	if rec := r.post("work", c.ID, choose("deny")); rec.Code != http.StatusOK {
		t.Fatalf("POST choice deny = %d %s, want 200", rec.Code, rec.Body)
	}
	if rec := r.post("work", c.ID, choose("allow")); rec.Code != http.StatusConflict {
		t.Errorf("second POST = %d %s, want 409", rec.Code, rec.Body)
	}
}

func TestAStaleCardIsRefusedWith409BeforeItsChoiceIsJudged(t *testing.T) {
	for name, id := range map[string]string{"a good choice": "APPROVE", "an unknown choice": "bogus"} {
		t.Run(name, func(t *testing.T) {
			g := newFakeGH(t)
			r := newNeedRig(t)
			r.item("s-work", "rv-1", needReview("rv-1", createdA, "sum", 11))
			r.start()
			card := r.waitLogged("work", 1)[0]

			r.item("s-work", "rv-1", needReview("rv-1", createdB, "sum", 11))
			r.in.Refresh()
			rec := r.post("work", card.ID, choose(id))
			if rec.Code != http.StatusConflict {
				t.Errorf("POST %s on a replaced item = %d %s, want 409", id, rec.Code, rec.Body)
			}
			if calls := g.calls(); len(calls) != 0 {
				t.Errorf("a refused card ran gh: %q", calls)
			}
		})
	}
}

func TestAnAnsweredCardRefusesAChoiceWith409EvenWhenTheIdIsUnknown(t *testing.T) {
	g := newFakeGH(t)
	r := newNeedRig(t)
	r.item("s-work", "rv-1", needReview("rv-1", createdA, "sum", 11))
	r.start()
	card := r.waitLogged("work", 1)[0]
	if rec := r.post("work", card.ID, choose("APPROVE")); rec.Code != http.StatusOK {
		t.Fatalf("first POST = %d %s, want 200", rec.Code, rec.Body)
	}
	r.waitState("work", card.ID, "answered", "chat")
	n := len(g.calls())

	for _, id := range []string{"APPROVE", "discard", "bogus"} {
		if rec := r.post("work", card.ID, choose(id)); rec.Code != http.StatusConflict {
			t.Errorf("POST choice %q on an answered card = %d %s, want 409", id, rec.Code, rec.Body)
		}
	}
	if got := len(g.calls()); got != n {
		t.Errorf("late answers ran gh %d more times, want none", got-n)
	}
}

// The push names a card and its choices through needcard; the chat posts the card. The two must agree on every id.
func TestTheIdsThePushNamesAreTheIdsOfTheCardsTheChatPosted(t *testing.T) {
	r := newNeedRig(t)
	r.fleetHooks()
	r.item("s-work", "q-1", oneQuestion("q-1", createdA))
	r.item("s-work", "rv-1", needReview("rv-1", createdB, "sum", 11))
	r.item("s-work", "fd-1", needFindings("fd-1", createdA))
	r.item("_admiral", "routine-t1", fleetRoutine("t1", "work", createdA, inAnHour(), sweepAction))
	r.item("_admiral", "camp-1", fleetDraft("camp-1", "work", createdA, "Speed up the fleet map", "Load the map."))
	r.item("_admiral", "allow-1", fleetAllow("allow-1", "work", viewRuleCard()))
	perm := needing("s-work", "work", "permission:Bash", "p-1")
	perm.Session, perm.Name = "s-perm", "perm"
	stop := needing("s-work", "work", "stop", "t-1")
	stop.Session, stop.Name = "s-stop", "stop"
	r.setShips(perm, stop)
	r.start()
	waitFor(t, "every need card posted", func() bool { return len(r.logged("work")) == 8 })

	res := needcard.Resolver{Configured: func(fleet string) bool { _, _, ok := r.cfg.ChatFleet(fleet); return ok }}
	byItem := map[string]needEntry{}
	for _, c := range r.logged("work") {
		byItem[c.Card.Item] = c
	}
	for _, it := range r.in.List() {
		id, _ := res.ForItem(it)
		want := byItem[it.ID]
		if want.ID == "" || id != want.ID {
			t.Errorf("item %s: needcard says card %q, the chat posted %q", it.ID, id, want.ID)
		}
	}
	for _, sh := range []ships.Ship{perm, stop} {
		id, _ := res.ForShip(sh)
		var posted string
		for _, c := range r.cardsFor("work", sh.Session) {
			posted = c.ID
		}
		if posted == "" || id != posted {
			t.Errorf("ship %s: needcard says card %q, the chat posted %q", sh.Session, id, posted)
		}
	}
}
