package push_test

import (
	"encoding/json"
	"reflect"
	"regexp"
	"testing"

	"github.com/coder/websocket"
	"github.com/jbuffin/admiral/internal/inbox"
	"github.com/jbuffin/admiral/internal/needcard"
	"github.com/jbuffin/admiral/internal/push"
	"github.com/jbuffin/admiral/internal/sessionhost"
	"github.com/jbuffin/admiral/internal/ships"
)

// The card ids are need-<24 hex of sha256(session NUL item NUL created)>, the ids the chat already posts; a stop's item is "stop:<stop id>".
const (
	cardStopP1      = "need-dad7746573b6ba098ed2b27e"
	cardStopP2      = "need-03a049de91fbd399b8246885"
	cardQuestionQ1  = "need-d8471bf8d7496df53d376a99"
	cardFindingsF1  = "need-b337fae4764b225b4ba491ce"
	cardStopP3      = "need-d9e48c2e34c526846acb4ee2"
	cardNotStarted0 = "need-1ad1888cf19057614b10026b"
)

var anyCardID = regexp.MustCompile(`^need-[0-9a-f]{24}$`)

type choice = map[string]any

func want(id, label string) choice { return choice{"id": id, "label": label} }

// choicesRig is a follower rig whose Watcher reads cards for the fleet work only.
func choicesRig(t *testing.T) (*followRig, func() msg, *websocket.Conn) {
	t.Helper()
	r := newFollowRig(t)
	r.w.WithCards(needcard.Resolver{Configured: func(fleet string) bool { return fleet == "work" }})
	live := r.follower()
	return r, func() msg { return r.wantNext(live, "push") }, live
}

// stopping is Dory waiting on the prompt stopID for the reason, as the hooks write it.
func stopping(reason, stopID string) ships.Ship {
	s := needsYou("Dory", reason)
	s.Harness, s.StopID, s.StopSince = "claude", stopID, "2026-11-28T09:00:00Z"
	s.Launched = "2026-11-28T08:00:00Z"
	return s
}

func (r *followRig) shipsAre(list ...ships.Ship) { r.w.Ships(append(list, working("Nemo"))) }

func questionItem(t *testing.T, session, id string, questions ...any) inbox.Item {
	t.Helper()
	body := map[string]any{"version": 1, "id": id, "kind": "question", "created": "2026-10-22T10:00:00Z", "title": "Which way?",
		"question": map[string]any{"questions": questions}}
	b, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	it, err := inbox.ParseItem(b, session, id)
	if err != nil {
		t.Fatal(err)
	}
	it.Fleet = "work"
	return it
}

func ask(id string, multi bool, labels ...string) map[string]any {
	opts := []any{}
	for _, l := range labels {
		opts = append(opts, map[string]any{"label": l})
	}
	return map[string]any{"id": id, "text": "Pick", "multi": multi, "options": opts}
}

// cardOf is the push's card, "" when the push names none.
func cardOf(m msg) string { s, _ := m["card"].(string); return s }

// choicesOf fails the test unless the push carries choices as an array.
func choicesOf(t *testing.T, m msg) []any {
	t.Helper()
	v, ok := m["choices"]
	if !ok {
		t.Fatalf("push %v has no choices, want an array on every push", m)
	}
	got, ok := v.([]any)
	if !ok {
		t.Fatalf("push choices = %#v, want an array", v)
	}
	return got
}

func TestAnAnswerablePermissionStopPushesItsCardWithAllowAndDeny(t *testing.T) {
	r, next, _ := choicesRig(t)
	r.shipsAre(stopping("permission:Bash", "p1"))

	m := next()
	if cardOf(m) != cardStopP1 {
		t.Errorf("card = %q, want %q", cardOf(m), cardStopP1)
	}
	if got, w := choicesOf(t, m), []any{want("allow", "Allow once"), want("deny", "Deny")}; !reflect.DeepEqual(got, w) {
		t.Errorf("choices = %v, want %v", got, w)
	}
}

func TestAQuestionItemWithOneSingleSelectQuestionPushesOneChoicePerOptionCleanedOfHiddenCharacters(t *testing.T) {
	r, next, _ := choicesRig(t)
	r.w.Inbox([]inbox.Item{questionItem(t, "s-dory", "q-1", ask("1", false, "Alpha", "Be‮ta"))})

	m := next()
	if cardOf(m) != cardQuestionQ1 {
		t.Errorf("card = %q, want %q", cardOf(m), cardQuestionQ1)
	}
	if got, w := choicesOf(t, m), []any{want("0", "Alpha"), want("1", "Beta")}; !reflect.DeepEqual(got, w) {
		t.Errorf("choices = %v, want %v, the labels without the bidi override", got, w)
	}
}

func TestANeedWithACardButNoFixedChoicesPushesItsCardAndEmptyChoices(t *testing.T) {
	findings := func() inbox.Item {
		b, _ := json.Marshal(map[string]any{"version": 1, "id": "f-1", "kind": "findings", "created": "2026-10-22T10:00:00Z", "title": "Findings",
			"findings": map[string]any{"pr_url": "https://example.com/pull/1", "review_id": 1}})
		it, err := inbox.ParseItem(b, "s-dory", "f-1")
		if err != nil {
			t.Fatal(err)
		}
		it.Fleet = "work"
		return it
	}
	notStarted := func() ships.Ship {
		s := stopping("not_started", "")
		s.Launched = "2020-01-01T00:00:00Z"
		return s
	}
	for _, c := range []struct {
		name, card string
		raise      func(r *followRig, t *testing.T)
	}{
		{"findings", cardFindingsF1, func(r *followRig, t *testing.T) { r.w.Inbox([]inbox.Item{findings()}) }},
		{"a ship that never started", cardNotStarted0, func(r *followRig, _ *testing.T) { r.shipsAre(notStarted()) }},
		{"a permission the card cannot answer", cardStopP3, func(r *followRig, _ *testing.T) { r.shipsAre(stopping("permission:AskUserQuestion", "p3")) }},
		{"a question item with two questions", "", func(r *followRig, t *testing.T) {
			r.w.Inbox([]inbox.Item{questionItem(t, "s-dory", "q-2", ask("1", false, "A", "B"), ask("2", false, "C"))})
		}},
		{"a multi-select question", "", func(r *followRig, t *testing.T) {
			r.w.Inbox([]inbox.Item{questionItem(t, "s-dory", "q-3", ask("1", true, "A", "B"))})
		}},
		{"a question with no options", "", func(r *followRig, t *testing.T) {
			r.w.Inbox([]inbox.Item{questionItem(t, "s-dory", "q-4", ask("1", false))})
		}},
	} {
		t.Run(c.name, func(t *testing.T) {
			r, next, _ := choicesRig(t)
			c.raise(r, t)
			m := next()
			if c.card != "" && cardOf(m) != c.card {
				t.Errorf("card = %q, want %q", cardOf(m), c.card)
			}
			if !anyCardID.MatchString(cardOf(m)) {
				t.Errorf("card = %q, want the need card's id", cardOf(m))
			}
			if got := choicesOf(t, m); len(got) != 0 {
				t.Errorf("choices = %v, want none", got)
			}
		})
	}
}

func TestANeedWithNoCardPushesNoCardAndEmptyChoices(t *testing.T) {
	crewAsks := func() ships.Ship {
		s := needsYou("Dory", ships.ReasonCrew)
		s.Harness = "claude"
		s.Crew = []ships.CrewBoat{{Name: "scout", Status: "needs_you", Reason: "permission:Bash"}}
		return s
	}
	elsewhere := stopping("permission:Bash", "p1")
	elsewhere.Fleet = "elsewhere"
	moored := func(ready bool) ships.Ship {
		s := ship("Dory", ships.StatusMoored, "")
		s.StackReady, s.PolledAt = ready, "2026-10-22T10:00:00Z"
		return s
	}
	for _, c := range []struct {
		name  string
		raise func(r *followRig, t *testing.T)
	}{
		{"a wake proposal", func(r *followRig, t *testing.T) {
			r.w.Inbox([]inbox.Item{item(t, inbox.AdmiralSession, "w-1", "wake_proposal", "Wake Dory")})
		}},
		{"a question in the chat session", func(r *followRig, t *testing.T) {
			r.w.Inbox([]inbox.Item{questionItem(t, sessionhost.ChatSession("work"), "q-1", ask("1", false, "A"))})
		}},
		{"a question of the Admiral session", func(r *followRig, t *testing.T) {
			r.w.Inbox([]inbox.Item{questionItem(t, inbox.AdmiralSession, "q-1", ask("1", false, "A"))})
		}},
		{"a crew boat's permission", func(r *followRig, _ *testing.T) { r.w.Ships([]ships.Ship{crewAsks(), working("Nemo")}) }},
		{"a ship asking a question", func(r *followRig, _ *testing.T) { r.shipsAre(stopping("question", "p1")) }},
		{"a ship ringing the bell", func(r *followRig, _ *testing.T) { r.shipsAre(stopping("bell", "p1")) }},
		{"a ship at an elicitation dialog", func(r *followRig, _ *testing.T) { r.shipsAre(stopping("notification:elicitation_dialog", "p1")) }},
		{"a stop with no identity yet", func(r *followRig, _ *testing.T) { r.shipsAre(stopping("permission:Bash", "")) }},
		{"a ship of a fleet that is not configured", func(r *followRig, _ *testing.T) { r.shipsAre(elsewhere) }},
		{"an anchored ship", func(r *followRig, _ *testing.T) { r.shipsAre(ship("Dory", ships.StatusAnchored, "")) }},
		{"a ship ready to merge", func(r *followRig, _ *testing.T) {
			r.w.Ships([]ships.Ship{moored(false), working("Nemo")})
			r.w.Ships([]ships.Ship{moored(true), working("Nemo")})
		}},
		{"a report reply", func(r *followRig, _ *testing.T) {
			r.w.Emit(push.ReportReplyEvent(push.Reply{Fleet: "work", FleetName: "Work", Ship: "Dory", Result: "done", Card: "c1", Message: "m1", Text: "All done"}))
		}},
	} {
		t.Run(c.name, func(t *testing.T) {
			r, next, _ := choicesRig(t)
			c.raise(r, t)
			m := next()
			if _, has := m["card"]; has {
				t.Errorf("card = %v, want none", m["card"])
			}
			if got := choicesOf(t, m); len(got) != 0 {
				t.Errorf("choices = %v, want none", got)
			}
		})
	}
}

func TestAShipThatMovesFromOneAskingStopToAnotherPushesAgainUnderTheSameTagWithTheNewCard(t *testing.T) {
	r, next, live := choicesRig(t)
	r.shipsAre(stopping("permission:Bash", "p1"))
	first := next()

	r.shipsAre(stopping("permission:Bash", "p2"))
	second := next()
	if cardOf(first) != cardStopP1 || cardOf(second) != cardStopP2 {
		t.Errorf("cards %q then %q, want %q then %q", cardOf(first), cardOf(second), cardStopP1, cardStopP2)
	}
	if first["tag"] != "s-Dory" || second["tag"] != "s-Dory" {
		t.Errorf("tags %v and %v, want both s-Dory so the repeat replaces the first", first["tag"], second["tag"])
	}
	if second.num("seq") != first.num("seq")+1 {
		t.Errorf("seq %d then %d, want the repeat as the next event", first.num("seq"), second.num("seq"))
	}
	if got, w := choicesOf(t, second), []any{want("allow", "Allow once"), want("deny", "Deny")}; !reflect.DeepEqual(got, w) {
		t.Errorf("choices = %v, want %v", got, w)
	}

	r.shipsAre(stopping("permission:Bash", "p2"))
	r.quiet(live)
}
