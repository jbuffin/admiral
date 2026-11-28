package chat

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jbuffin/admiral/internal/allowrules"
	"github.com/jbuffin/admiral/internal/chatlog"
	"github.com/jbuffin/admiral/internal/inbox"
	"github.com/jbuffin/admiral/internal/permlog"
)

// Need cards for a fleet's routine proposal, allow rule proposal and campaign draft, items that live under _admiral and name their fleet.

const (
	createdA   = "2026-11-20T10:00:00Z"
	createdB   = "2026-11-21T10:00:00Z"
	campaignOK = "https://github.com/acme/widgets/issues/99"
)

// fleetFakes are the hooks Admiral wires into the Inbox for these kinds, over a real allow rule store under the rig's home.
type fleetFakes struct {
	mu          sync.Mutex
	launches    []inbox.RoutineProposal
	dismissals  []inbox.RoutineProposal
	creates     []inbox.CampaignDraft
	launchReply func() (any, int, string)
	createReply func(inbox.CampaignDraft) (inbox.CreatedCampaign, int, string)
	noLaunch    bool
	noCreate    bool
	store       *allowrules.Store
}

func (r *needRig) fleetHooks() *fleetFakes {
	f := &fleetFakes{store: allowrules.Open(r.home)}
	r.wire = func(s *inbox.Service) {
		s.Fleet = func(key string) (inbox.FleetInfo, bool) {
			return inbox.FleetInfo{Key: key, Name: key}, key == "home" || key == "work" || key == "off"
		}
		s.FleetRepos = func(string) []string { return []string{"acme/widgets", "acme/api"} }
		if !f.noLaunch {
			s.LaunchRoutine = f.launch
		}
		s.DismissRoutine = func(p inbox.RoutineProposal) {
			f.mu.Lock()
			defer f.mu.Unlock()
			f.dismissals = append(f.dismissals, p)
		}
		if !f.noCreate {
			s.CreateCampaign = f.create
		}
		s.PostCampaignCard = func(fleet string, c inbox.CampaignCard) {
			_ = r.svc.PostCampaignDraftCard(fleet, CampaignDraftCard{ID: c.ID, Title: c.Title, URL: c.URL, Created: c.Created, Failed: c.Failed})
		}
		s.AllowRules = f.store
	}
	return f
}

func (f *fleetFakes) launch(_ context.Context, p inbox.RoutineProposal) (any, int, string) {
	f.mu.Lock()
	f.launches = append(f.launches, p)
	reply := f.launchReply
	f.mu.Unlock()
	if reply != nil {
		return reply()
	}
	return map[string]any{"session": "ship-1"}, 0, ""
}

func (f *fleetFakes) create(_ context.Context, d inbox.CampaignDraft) (inbox.CreatedCampaign, int, string) {
	f.mu.Lock()
	f.creates = append(f.creates, d)
	reply := f.createReply
	f.mu.Unlock()
	if reply != nil {
		return reply(d)
	}
	return inbox.CreatedCampaign{ID: "camp-1", URL: campaignOK}, 0, ""
}

func (f *fleetFakes) launched() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.launches)
}

func (f *fleetFakes) created() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.creates)
}

func (f *fleetFakes) dismissed() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.dismissals)
}

func fleetRoutine(trigger, fleet, created string, expires time.Time, action string) map[string]any {
	return map[string]any{"version": 1, "id": "routine-" + trigger, "kind": "routine_proposal", "created": created, "title": "Run Nightly sweep?",
		"routine_proposal": map[string]any{"fleet": fleet, "routine": "r1", "routine_name": "Nightly sweep", "trigger": trigger,
			"action": action, "reason": "The sweep has not run this week.", "expires_at": expires.UTC().Format(time.RFC3339)}}
}

const sweepAction = "launch claude in /tmp/repo: sweep the board"

func inAnHour() time.Time { return time.Now().Add(time.Hour) }

func fleetDraft(id, fleet, created, title, body string) map[string]any {
	return map[string]any{"version": 1, "id": id, "kind": "campaign_draft", "created": created, "title": "AdmiralBot drafted a campaign",
		"campaign_draft": map[string]any{"fleet": fleet, "repo": "acme/widgets", "title": title, "body": body, "via": "AdmiralBot"}}
}

func ruleCardItem(id, harness, repo, rule string, pattern []string) map[string]any {
	c := map[string]any{"id": id, "harness": harness, "count": 2, "reason": "asked twice this week",
		"prompts": []any{
			map[string]any{"ship": "saltpaw", "id": "p1", "at": "2026-11-10T10:00:00Z", "tool": "Bash", "command": "gh pr view 7"},
			map[string]any{"ship": "dory", "id": "p2", "at": "2026-11-10T11:00:00Z", "tool": "Bash", "command": "gh pr view 9"},
		}}
	if repo != "" {
		c["repo"] = repo
	}
	if rule != "" {
		c["rule"] = rule
	}
	if pattern != nil {
		c["pattern"] = pattern
		c["every_repo"] = true
	}
	return c
}

func fleetAllow(id, fleet string, cards ...map[string]any) map[string]any {
	return map[string]any{"version": 1, "id": id, "kind": "allow_rules", "created": createdA, "title": "Allow rules for " + fleet,
		"allow_rules": map[string]any{"fleet": fleet, "cards": cards}}
}

var (
	viewRuleCard  = func() map[string]any { return ruleCardItem("c1", "claude", "acme/widgets", "Bash(gh pr view:*)", nil) }
	checkRuleCard = func() map[string]any { return ruleCardItem("c2", "codex", "", "", []string{"make", "check"}) }
	viewRule      = allowrules.Rule{Harness: "claude", Repo: "acme/widgets", Rule: "Bash(gh pr view:*)"}
	checkRule     = allowrules.Rule{Harness: "codex", Pattern: []string{"make", "check"}}
)

// inboxPost is the Inbox's own route, the reference a card's tap must answer like.
func (r *needRig) inboxPost(path, body string) *httptest.ResponseRecorder {
	r.t.Helper()
	mux := http.NewServeMux()
	r.in.Routes(mux)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest("POST", path, strings.NewReader(body)))
	return rec
}

// outcomesFor is the outcome records written for an _admiral item.
func (r *needRig) outcomesFor(item string) []map[string]any {
	var out []map[string]any
	for _, rec := range r.outcomesOf("_admiral") {
		if rec["item"] == item {
			out = append(out, rec)
		}
	}
	return out
}

func (r *needRig) waitOutcomes(item string, n int) []map[string]any {
	r.t.Helper()
	waitFor(r.t, "outcome records for "+item, func() bool { return len(r.outcomesFor(item)) >= n })
	return r.outcomesFor(item)
}

// campaignCards is the campaign_draft_card outcome entries in the fleet's chat log.
func (r *needRig) campaignCards(fleet string) []chatlog.Entry {
	r.t.Helper()
	es, err := chatlog.New(r.home, time.Now).Recent(fleet, 500)
	if err != nil {
		r.t.Fatal(err)
	}
	var out []chatlog.Entry
	for _, e := range es {
		if e.Kind == chatlog.KindCampaignDraftCard {
			out = append(out, e)
		}
	}
	return out
}

func bodyIs(t *testing.T, rec *httptest.ResponseRecorder, want string) {
	t.Helper()
	var got, exp any
	if json.Unmarshal(rec.Body.Bytes(), &got) != nil || json.Unmarshal([]byte(want), &exp) != nil || !reflect.DeepEqual(got, exp) {
		t.Errorf("body = %s, want %s", strings.TrimSpace(rec.Body.String()), want)
	}
}

func is2xx(rec *httptest.ResponseRecorder) bool { return rec.Code/100 == 2 }

func (r *needRig) noAnswer(id string) {
	r.t.Helper()
	if b, ok := r.answerFile("_admiral", id); ok {
		r.t.Errorf("answer file for %s = %s, want none", id, b)
	}
}

func TestFleetNeedCardEachFleetItemPostsOneCardInItsOwnFleetsChatWhetherOrNotThatChatIsOn(t *testing.T) {
	r := newNeedRig(t)
	r.fleetHooks()
	r.item("_admiral", "routine-t1", fleetRoutine("t1", "home", createdA, inAnHour(), sweepAction))
	r.item("_admiral", "allow-1", fleetAllow("allow-1", "off", viewRuleCard()))
	r.item("_admiral", "camp-1", fleetDraft("camp-1", "home", createdA, "Speed up the fleet map", "Load the map in under a second."))
	// fleets the config does not hold, and another _admiral kind that names a fleet
	r.item("_admiral", "routine-t9", fleetRoutine("t9", "ghost", createdA, inAnHour(), sweepAction))
	r.item("_admiral", "allow-9", fleetAllow("allow-9", "ghost", viewRuleCard()))
	r.item("_admiral", "camp-9", fleetDraft("camp-9", "ghost", createdA, "Ghost", "Nowhere to post."))
	r.item("_admiral", "end-1", map[string]any{"version": 1, "id": "end-1", "kind": "drafting_ended", "created": createdA, "title": "A drafting ship ended",
		"drafting_ended": map[string]any{"ship": "saltpaw", "name": "saltpaw", "fleet": "home", "harness": "claude"}})
	r.start()

	home := r.waitLogged("home", 2)
	off := r.waitLogged("off", 1)
	count := func() string {
		if n := len(r.logged("home")); n != 2 {
			return "the home chat holds a different number of cards than its routine proposal and its campaign draft"
		}
		if n := len(r.logged("off")); n != 1 {
			return "the off chat holds a different number of cards than its allow rule proposal"
		}
		if n := len(r.logged("work")) + len(r.logged("ghost")); n != 0 {
			return "a card landed in the work chat, which is the fleet the _admiral session maps to, or in a fleet the config does not hold"
		}
		return ""
	}
	holdCards(t, 300*time.Millisecond, count)

	kinds := map[string]string{}
	for _, c := range home {
		kinds[c.Card.Item] = c.Card.ItemKind
		if _, has := rawCard(t, c)["ship"]; has {
			t.Errorf("card for %s carries a ship; a fleet's card has none", c.Card.Item)
		}
		if c.Card.State != "open" {
			t.Errorf("card for %s is %q, want open", c.Card.Item, c.Card.State)
		}
	}
	if want := map[string]string{"routine-t1": "routine_proposal", "camp-1": "campaign_draft"}; !reflect.DeepEqual(kinds, want) {
		t.Errorf("home's cards = %v, want %v", kinds, want)
	}
	if c := off[0].Card; c.Item != "allow-1" || c.ItemKind != "allow_rules" {
		t.Errorf("off's card = %+v, want the allow rule proposal allow-1: a fleet with its chat off still gets its card", c)
	}

	r.restart()
	r.in.Refresh()
	holdCards(t, 300*time.Millisecond, count)
}

func TestFleetNeedCardCarriesWhatItsChoicesNeed(t *testing.T) {
	r := newNeedRig(t)
	r.fleetHooks()
	exp := inAnHour()
	r.item("_admiral", "routine-t1", fleetRoutine("t1", "home", createdA, exp, sweepAction))
	r.item("_admiral", "allow-1", fleetAllow("allow-1", "home", viewRuleCard(), checkRuleCard()))
	r.item("_admiral", "camp-1", fleetDraft("camp-1", "home", createdA, "Speed up the fleet map", "Load the map in under a second."))
	r.start()
	r.waitLogged("home", 3)

	routine := rawCard(t, r.cardFor("home", "routine-t1"))
	if routine["action"] != sweepAction || routine["reason"] != "The sweep has not run this week." {
		t.Errorf("routine card = %v, want the proposal's action and reason", routine)
	}
	at, err := time.Parse(time.RFC3339, str(routine["expires_at"]))
	if err != nil || !at.Equal(exp.Truncate(time.Second)) {
		t.Errorf("routine card expires_at = %v (%v), want %s", routine["expires_at"], err, exp.UTC().Format(time.RFC3339))
	}

	allow := rawCard(t, r.cardFor("home", "allow-1"))
	cards, _ := allow["cards"].([]any)
	if len(cards) != 2 {
		t.Fatalf("allow rule card cards = %v, want both proposed rules", allow["cards"])
	}
	first, _ := cards[0].(map[string]any)
	second, _ := cards[1].(map[string]any)
	if first["id"] != "c1" || first["harness"] != "claude" || first["repo"] != "acme/widgets" || first["rule"] != "Bash(gh pr view:*)" ||
		first["count"] != float64(2) || first["reason"] != "asked twice this week" {
		t.Errorf("first rule = %v, want c1: claude, acme/widgets, Bash(gh pr view:*), count 2 and its reason", first)
	}
	if ps, _ := first["prompts"].([]any); len(ps) != 2 || ps[0].(map[string]any)["command"] != "gh pr view 7" || ps[1].(map[string]any)["ship"] != "dory" {
		t.Errorf("first rule prompts = %v, want the two logged prompts", first["prompts"])
	}
	if second["id"] != "c2" || second["harness"] != "codex" || second["every_repo"] != true || !reflect.DeepEqual(second["pattern"], []any{"make", "check"}) {
		t.Errorf("second rule = %v, want c2: codex, every repo, pattern make check", second)
	}

	draft := rawCard(t, r.cardFor("home", "camp-1"))
	if draft["repo"] != "acme/widgets" || draft["title"] != "Speed up the fleet map" || draft["body"] != "Load the map in under a second." {
		t.Errorf("campaign card = %v, want the draft's repo, title and body as Create's seed", draft)
	}
}

func str(v any) string { s, _ := v.(string); return s }

func TestFleetNeedCardATapOnARoutineProposalLaunchesOrDismissesItThroughTheInboxsPath(t *testing.T) {
	t.Run("launch", func(t *testing.T) {
		r := newNeedRig(t)
		f := r.fleetHooks()
		r.item("_admiral", "routine-t1", fleetRoutine("t1", "home", createdA, inAnHour(), sweepAction))
		r.start()
		card := r.cardFor("home", "routine-t1")

		rec := r.post("home", card.ID, map[string]any{"launch": true})
		if !is2xx(rec) {
			t.Fatalf("launch tap = %d %s, want success", rec.Code, rec.Body)
		}
		bodyIs(t, rec, `{"ship":{"session":"ship-1"}}`)
		if f.launched() != 1 || f.launches[0].Routine != "r1" || f.launches[0].Trigger != "t1" || f.launches[0].Fleet != "home" {
			t.Errorf("launch hook got %+v, want one call for routine r1's trigger t1 in home", f.launches)
		}
		if got, want := r.answerOf("_admiral", "routine-t1"), (map[string]any{"version": float64(1), "id": "routine-t1", "kind": "routine_proposal", "launched": true}); !reflect.DeepEqual(got, want) {
			t.Errorf("answer file = %v, want %v", got, want)
		}
		want := []map[string]any{{"event": "proposal_launched", "item": "routine-t1", "routine": "r1", "fleet": "home"}}
		if got := r.waitOutcomes("routine-t1", 1); !reflect.DeepEqual(got, want) {
			t.Errorf("outcomes = %v, want %v", got, want)
		}
		r.waitState("home", card.ID, "answered", "chat")
	})
	t.Run("dismiss", func(t *testing.T) {
		r := newNeedRig(t)
		f := r.fleetHooks()
		r.item("_admiral", "routine-t1", fleetRoutine("t1", "home", createdA, inAnHour(), sweepAction))
		r.start()
		card := r.cardFor("home", "routine-t1")

		if rec := r.post("home", card.ID, map[string]any{"dismiss": true}); !is2xx(rec) {
			t.Fatalf("dismiss tap = %d %s, want success", rec.Code, rec.Body)
		}
		if f.launched() != 0 || f.dismissed() != 1 || f.dismissals[0].Routine != "r1" || f.dismissals[0].Trigger != "t1" {
			t.Errorf("hooks: %d launches, dismissals %+v, want no launch and one dismissal of routine r1's trigger t1", f.launched(), f.dismissals)
		}
		if got, want := r.answerOf("_admiral", "routine-t1"), (map[string]any{"version": float64(1), "id": "routine-t1", "kind": "routine_proposal", "dismissed": true}); !reflect.DeepEqual(got, want) {
			t.Errorf("answer file = %v, want %v", got, want)
		}
		want := []map[string]any{{"event": "proposal_dismissed", "item": "routine-t1", "routine": "r1", "fleet": "home"}}
		if got := r.waitOutcomes("routine-t1", 1); !reflect.DeepEqual(got, want) {
			t.Errorf("outcomes = %v, want %v", got, want)
		}
		r.waitState("home", card.ID, "answered", "chat")
	})
}

func TestFleetNeedCardATapOnAnAllowRuleProposalAddsAndSkipsEachRuleThroughTheInboxsPath(t *testing.T) {
	r := newNeedRig(t)
	f := r.fleetHooks()
	r.item("_admiral", "allow-1", fleetAllow("allow-1", "home", viewRuleCard(), checkRuleCard()))
	r.item("_admiral", "allow-2", fleetAllow("allow-2", "off", viewRuleCard()))
	r.start()
	card := r.cardFor("home", "allow-1")
	offCard := r.cardFor("off", "allow-2")

	decisions := map[string]any{"cards": map[string]any{"c1": map[string]any{"decision": "add"}, "c2": map[string]any{"decision": "skip"}}}
	rec := r.post("home", card.ID, decisions)
	if !is2xx(rec) {
		t.Fatalf("tap = %d %s, want success", rec.Code, rec.Body)
	}
	bodyIs(t, rec, `{"answered":"allow-1"}`)
	if got, _ := f.store.List("home"); !reflect.DeepEqual(got.Rules, []allowrules.Rule{viewRule}) || !reflect.DeepEqual(got.Skipped, []allowrules.Rule{checkRule}) {
		t.Errorf("home's store = rules %+v skipped %+v, want the claude rule added and the codex rule skipped", got.Rules, got.Skipped)
	}
	want := map[string]any{"version": float64(1), "id": "allow-1", "kind": "allow_rules",
		"cards": map[string]any{"c1": map[string]any{"decision": "add"}, "c2": map[string]any{"decision": "skip"}}}
	if got := r.answerOf("_admiral", "allow-1"); !reflect.DeepEqual(got, want) {
		t.Errorf("answer file = %v, want %v", got, want)
	}
	got := r.waitOutcomes("allow-1", 2)
	sort.Slice(got, func(i, j int) bool { return str(got[i]["card"]) < str(got[j]["card"]) })
	wantOut := []map[string]any{
		{"event": "allow_rule_added", "item": "allow-1", "kind": "allow_rules", "card": "c1", "fleet": "home"},
		{"event": "allow_rule_skipped", "item": "allow-1", "kind": "allow_rules", "card": "c2", "fleet": "home"},
	}
	if !reflect.DeepEqual(got, wantOut) {
		t.Errorf("outcomes = %v, want %v", got, wantOut)
	}
	r.waitState("home", card.ID, "answered", "chat")

	if rec := r.post("off", offCard.ID, map[string]any{"cards": map[string]any{"c1": map[string]any{"decision": "add"}}}); !is2xx(rec) {
		t.Errorf("tap in the fleet with its chat off = %d %s, want success", rec.Code, rec.Body)
	}
	if got, _ := f.store.List("off"); !reflect.DeepEqual(got.Rules, []allowrules.Rule{viewRule}) {
		t.Errorf("off's store rules = %+v, want the rule added to its own fleet", got.Rules)
	}
}

func TestFleetNeedCardATapOnACampaignDraftCreatesWithTheEditsOrDismissesAndPostsTheOutcomeCard(t *testing.T) {
	t.Run("create", func(t *testing.T) {
		r := newNeedRig(t)
		f := r.fleetHooks()
		r.item("_admiral", "camp-1", fleetDraft("camp-1", "home", createdA, "Speed up the fleet map", "Load the map in under a second."))
		r.start()
		card := r.cardFor("home", "camp-1")

		rec := r.post("home", card.ID, map[string]any{"repo": "acme/api", "title": "Faster map", "body": "Edited body."})
		if !is2xx(rec) {
			t.Fatalf("create tap = %d %s, want success", rec.Code, rec.Body)
		}
		bodyIs(t, rec, `{"campaign":{"id":"camp-1","url":"`+campaignOK+`"}}`)
		wantDraft := inbox.CampaignDraft{Fleet: "home", Repo: "acme/api", Title: "Faster map", Body: "Edited body.", Via: "AdmiralBot"}
		if f.created() != 1 || f.creates[0] != wantDraft {
			t.Errorf("create hook got %+v, want one call with the edits: %+v", f.creates, wantDraft)
		}
		wantAnswer := map[string]any{"version": float64(1), "id": "camp-1", "kind": "campaign_draft", "created": true, "url": campaignOK}
		if got := r.answerOf("_admiral", "camp-1"); !reflect.DeepEqual(got, wantAnswer) {
			t.Errorf("answer file = %v, want %v", got, wantAnswer)
		}
		wantOut := []map[string]any{{"event": "campaign_draft_created", "item": "camp-1", "fleet": "home"}}
		if got := r.waitOutcomes("camp-1", 1); !reflect.DeepEqual(got, wantOut) {
			t.Errorf("outcomes = %v, want %v", got, wantOut)
		}
		waitFor(t, "the campaign_draft_card outcome entry", func() bool { return len(r.campaignCards("home")) == 1 })
		e := r.campaignCards("home")[0]
		if e.ID != "camp-1" || !strings.Contains(string(e.Card), `"created":true`) || !strings.Contains(string(e.Card), campaignOK) || !strings.Contains(string(e.Card), "Faster map") {
			t.Errorf("outcome entry = %s %s, want the created campaign's card for camp-1", e.ID, e.Card)
		}
		r.waitState("home", card.ID, "answered", "chat")
	})
	t.Run("dismiss", func(t *testing.T) {
		r := newNeedRig(t)
		f := r.fleetHooks()
		r.item("_admiral", "camp-1", fleetDraft("camp-1", "home", createdA, "Speed up the fleet map", "Load the map in under a second."))
		r.start()
		card := r.cardFor("home", "camp-1")

		if rec := r.post("home", card.ID, map[string]any{"dismiss": true}); !is2xx(rec) {
			t.Fatalf("dismiss tap = %d %s, want success", rec.Code, rec.Body)
		}
		if f.created() != 0 {
			t.Errorf("a dismissal created %d campaigns", f.created())
		}
		wantAnswer := map[string]any{"version": float64(1), "id": "camp-1", "kind": "campaign_draft", "dismissed": true}
		if got := r.answerOf("_admiral", "camp-1"); !reflect.DeepEqual(got, wantAnswer) {
			t.Errorf("answer file = %v, want %v", got, wantAnswer)
		}
		wantOut := []map[string]any{{"event": "campaign_draft_dismissed", "item": "camp-1", "fleet": "home"}}
		if got := r.waitOutcomes("camp-1", 1); !reflect.DeepEqual(got, wantOut) {
			t.Errorf("outcomes = %v, want %v", got, wantOut)
		}
		waitFor(t, "the campaign_draft_card outcome entry", func() bool { return len(r.campaignCards("home")) == 1 })
		if e := r.campaignCards("home")[0]; e.ID != "camp-1" || !strings.Contains(string(e.Card), `"created":false`) {
			t.Errorf("outcome entry = %s %s, want the thrown-away card for camp-1", e.ID, e.Card)
		}
		r.waitState("home", card.ID, "answered", "chat")
	})
}

func TestFleetNeedCardARoutineOrCampaignCardWhoseItemWasReplacedOrEditedIsRefusedWith409AndDoesNothing(t *testing.T) {
	for _, tc := range []struct {
		name  string
		first map[string]any
		after map[string]any
		tap   map[string]any
		runs  func(f *fleetFakes) int
		item  string
	}{
		{"routine replaced by a newer item", fleetRoutine("t1", "home", createdA, inAnHour(), sweepAction), fleetRoutine("t1", "home", createdB, inAnHour(), sweepAction),
			map[string]any{"launch": true}, func(f *fleetFakes) int { return f.launched() }, "routine-t1"},
		{"routine whose action changed", fleetRoutine("t1", "home", createdA, inAnHour(), sweepAction), fleetRoutine("t1", "home", createdA, inAnHour(), "launch claude in /tmp/repo: delete the board"),
			map[string]any{"launch": true}, func(f *fleetFakes) int { return f.launched() }, "routine-t1"},
		{"campaign replaced by a newer item", fleetDraft("camp-1", "home", createdA, "T", "B"), fleetDraft("camp-1", "home", createdB, "T", "B"),
			map[string]any{"repo": "acme/widgets", "title": "T", "body": "B"}, func(f *fleetFakes) int { return f.created() }, "camp-1"},
		{"campaign whose body changed", fleetDraft("camp-1", "home", createdA, "T", "B"), fleetDraft("camp-1", "home", createdA, "T", "B, but different"),
			map[string]any{"repo": "acme/widgets", "title": "T", "body": "B"}, func(f *fleetFakes) int { return f.created() }, "camp-1"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := newNeedRig(t)
			f := r.fleetHooks()
			r.item("_admiral", tc.item, tc.first)
			r.start()
			card := r.cardFor("home", tc.item)
			r.item("_admiral", tc.item, tc.after) // no Refresh: the tap meets the item as the file now holds it

			rec := r.post("home", card.ID, tc.tap)
			if rec.Code != http.StatusConflict || errorOf(rec) != "item "+tc.item+" is already answered" {
				t.Errorf("tap on the replaced item = %d %s, want 409 %q", rec.Code, rec.Body, "item "+tc.item+" is already answered")
			}
			if n := tc.runs(f); n != 0 {
				t.Errorf("a refused tap ran the hook %d times", n)
			}
			r.noAnswer(tc.item)
		})
	}
}

func TestFleetNeedCardAnAllowRuleCardRefreshesInPlaceWhenARoundAddsARuleAndAnswersOnlyOnceItCoversEveryRule(t *testing.T) {
	r := newNeedRig(t)
	f := r.fleetHooks()
	first := allowrules.Candidate{Fleet: "home", Rule: viewRule, Prompts: []permlog.Record{{ID: "p1", Ship: "saltpaw", At: "2026-11-10T10:00:00Z", Call: permlog.Call{Tool: "Bash", Command: "gh pr view 7"}}}}
	if err := inbox.WriteAllowRuleProposals(r.home, "home", []allowrules.Candidate{first}, nil); err != nil {
		t.Fatal(err)
	}
	r.start()
	posted := r.waitLogged("home", 1)[0]
	item := posted.Card.Item

	later := allowrules.Candidate{Fleet: "home", Rule: checkRule, Prompts: []permlog.Record{{ID: "p2", Ship: "dory", At: "2026-11-10T12:00:00Z", Call: permlog.Call{Tool: "Bash", Command: "make check"}}}}
	if err := inbox.WriteAllowRuleProposals(r.home, "home", []allowrules.Candidate{later}, nil); err != nil {
		t.Fatal(err)
	}
	r.in.Refresh()

	var ids []string
	waitFor(t, "the card showing both rules", func() bool {
		ids = nil
		for _, c := range r.served("home") {
			if c.Card.Item != item {
				continue
			}
			var body struct {
				Cards []struct {
					ID string `json:"id"`
				} `json:"cards"`
			}
			if json.Unmarshal(c.raw, &body) != nil {
				return false
			}
			for _, k := range body.Cards {
				ids = append(ids, k.ID)
			}
		}
		return len(ids) == 2
	})
	holdCards(t, 300*time.Millisecond, func() string {
		if n := len(r.logged("home")); n != 1 {
			return "the grown item posted a second card; it keeps its id and created, so its one card refreshes in place"
		}
		return ""
	})
	if st := r.served("home")[0]; st.ID != posted.ID || st.Card.State != "open" {
		t.Errorf("card after the round = %s %s, want the same card, still open", st.ID, st.Card.State)
	}

	partial := map[string]any{"cards": map[string]any{ids[0]: map[string]any{"decision": "add"}}}
	inboxAnswer := r.inboxPost("/api/inbox/_admiral/"+item+"/allow-rules", `{"cards":{"`+ids[0]+`":{"decision":"add"}}}`)
	if inboxAnswer.Code != http.StatusBadRequest {
		t.Fatalf("the Inbox's own route with one of two rules decided = %d %s, want 400: the test's premise", inboxAnswer.Code, inboxAnswer.Body)
	}
	rec := r.post("home", posted.ID, partial)
	if rec.Code != http.StatusBadRequest || errorOf(rec) != errorOf(inboxAnswer) || !strings.Contains(errorOf(rec), "has no decision") {
		t.Errorf("a tap that leaves a rule out = %d %s, want the Inbox's 400 %q", rec.Code, rec.Body, errorOf(inboxAnswer))
	}
	r.noAnswer(item)
	if got, _ := f.store.List("home"); len(got.Rules)+len(got.Skipped) != 0 {
		t.Errorf("a refused tap wrote to the store: %+v", got)
	}

	all := map[string]any{"cards": map[string]any{ids[0]: map[string]any{"decision": "add"}, ids[1]: map[string]any{"decision": "skip"}}}
	if rec := r.post("home", posted.ID, all); !is2xx(rec) {
		t.Fatalf("a tap deciding both rules = %d %s, want success", rec.Code, rec.Body)
	}
	r.waitState("home", posted.ID, "answered", "chat")
}

func TestFleetNeedCardOnlyTheFirstAnswerCountsWhetherItCameFromTheInboxOrAnEarlierTap(t *testing.T) {
	routineItem := fleetRoutine("t1", "home", createdA, inAnHour(), sweepAction)
	allowItem := fleetAllow("allow-1", "home", viewRuleCard())
	campItem := fleetDraft("camp-1", "home", createdA, "T", "B")
	for _, tc := range []struct {
		name    string
		item    map[string]any
		id      string
		tap     map[string]any
		inbox   func(r *needRig) error
		effects func(f *fleetFakes) int
	}{
		{"routine proposal", routineItem, "routine-t1", map[string]any{"launch": true},
			func(r *needRig) error { _, err := r.in.LaunchRoutineItem(context.Background(), "routine-t1"); return err },
			func(f *fleetFakes) int { return f.launched() }},
		{"allow rule proposal", allowItem, "allow-1", map[string]any{"cards": map[string]any{"c1": map[string]any{"decision": "add"}}},
			func(r *needRig) error {
				return r.in.AnswerAllowRules("allow-1", inbox.AllowRulesRequest{Cards: map[string]struct {
					Decision string `json:"decision"`
				}{"c1": {Decision: "skip"}}})
			},
			func(f *fleetFakes) int { got, _ := f.store.List("home"); return len(got.Rules) }},
		{"campaign draft", campItem, "camp-1", map[string]any{"repo": "acme/widgets", "title": "T", "body": "B"},
			func(r *needRig) error {
				_, err := r.in.CreateCampaignItem(context.Background(), "camp-1", inbox.CampaignDraftRequest{Repo: "acme/widgets", Title: "T", Body: "B"})
				return err
			},
			func(f *fleetFakes) int { return f.created() }},
	} {
		t.Run(tc.name+" answered in the Inbox first", func(t *testing.T) {
			r := newNeedRig(t)
			f := r.fleetHooks()
			r.item("_admiral", tc.id, tc.item)
			r.start()
			card := r.cardFor("home", tc.id)
			if err := tc.inbox(r); err != nil {
				t.Fatal(err)
			}
			before := tc.effects(f)
			answered, _ := r.answerFile("_admiral", tc.id)

			rec := r.post("home", card.ID, tc.tap)
			if rec.Code != http.StatusConflict || errorOf(rec) != "item "+tc.id+" is already answered" {
				t.Errorf("tap after the Inbox answered = %d %s, want 409 %q", rec.Code, rec.Body, "item "+tc.id+" is already answered")
			}
			if after := tc.effects(f); after != before {
				t.Errorf("a late tap changed the hook's effects from %d to %d", before, after)
			}
			if now, _ := r.answerFile("_admiral", tc.id); string(now) != string(answered) {
				t.Errorf("a late tap rewrote the answer file: %s, was %s", now, answered)
			}
		})
		t.Run(tc.name+" tapped twice", func(t *testing.T) {
			r := newNeedRig(t)
			f := r.fleetHooks()
			r.item("_admiral", tc.id, tc.item)
			r.start()
			card := r.cardFor("home", tc.id)
			if rec := r.post("home", card.ID, tc.tap); !is2xx(rec) {
				t.Fatalf("first tap = %d %s, want success", rec.Code, rec.Body)
			}
			effects := tc.effects(f)
			rec := r.post("home", card.ID, tc.tap)
			if rec.Code != http.StatusConflict || errorOf(rec) != "item "+tc.id+" is already answered" {
				t.Errorf("second tap = %d %s, want 409 %q", rec.Code, rec.Body, "item "+tc.id+" is already answered")
			}
			if tc.effects(f) != effects {
				t.Errorf("a second tap ran the hook again")
			}
		})
	}
}

func TestFleetNeedCardReadsAsTheInboxLeftItAnsweredInTheInboxOrRemoved(t *testing.T) {
	ctx := context.Background()
	for _, tc := range []struct {
		name  string
		item  map[string]any
		id    string
		act   func(r *needRig) error
		state string
		in    string
	}{
		{"routine launched", fleetRoutine("t1", "home", createdA, inAnHour(), sweepAction), "routine-t1",
			func(r *needRig) error { _, err := r.in.LaunchRoutineItem(ctx, "routine-t1"); return err }, "answered", "inbox"},
		{"routine dismissed", fleetRoutine("t1", "home", createdA, inAnHour(), sweepAction), "routine-t1",
			func(r *needRig) error { return r.in.DismissRoutineProposal("routine-t1") }, "answered", "inbox"},
		{"routine removed", fleetRoutine("t1", "home", createdA, inAnHour(), sweepAction), "routine-t1",
			func(r *needRig) error { _, err := r.in.RemoveItem("_admiral", "routine-t1"); return err }, "removed", ""},
		{"allow rules answered", fleetAllow("allow-1", "home", viewRuleCard()), "allow-1",
			func(r *needRig) error {
				return r.in.AnswerAllowRules("allow-1", inbox.AllowRulesRequest{Cards: map[string]struct {
					Decision string `json:"decision"`
				}{"c1": {Decision: "add"}}})
			}, "answered", "inbox"},
		{"allow rules removed", fleetAllow("allow-1", "home", viewRuleCard()), "allow-1",
			func(r *needRig) error { _, err := r.in.RemoveItem("_admiral", "allow-1"); return err }, "removed", ""},
		{"campaign created", fleetDraft("camp-1", "home", createdA, "T", "B"), "camp-1",
			func(r *needRig) error {
				_, err := r.in.CreateCampaignItem(ctx, "camp-1", inbox.CampaignDraftRequest{Repo: "acme/widgets", Title: "T", Body: "B"})
				return err
			}, "answered", "inbox"},
		{"campaign dismissed", fleetDraft("camp-1", "home", createdA, "T", "B"), "camp-1",
			func(r *needRig) error { return r.in.DismissCampaignDraft("camp-1") }, "answered", "inbox"},
		{"campaign removed, which dismisses it", fleetDraft("camp-1", "home", createdA, "T", "B"), "camp-1",
			func(r *needRig) error { _, err := r.in.RemoveItem("_admiral", "camp-1"); return err }, "answered", "inbox"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := newNeedRig(t)
			r.fleetHooks()
			r.item("_admiral", tc.id, tc.item)
			r.start()
			card := r.cardFor("home", tc.id)
			if err := tc.act(r); err != nil {
				t.Fatal(err)
			}
			r.waitState("home", card.ID, tc.state, tc.in)
		})
	}
}

func TestFleetNeedCardARoutineProposalThatClosesWithoutTheEngineerReadsMovedOn(t *testing.T) {
	t.Run("the routine's next run closed it", func(t *testing.T) {
		r := newNeedRig(t)
		f := r.fleetHooks()
		r.item("_admiral", "routine-t1", fleetRoutine("t1", "home", createdA, inAnHour(), sweepAction))
		r.start()
		old := r.cardFor("home", "routine-t1")

		if _, err := r.in.PublishRoutineProposal(inbox.RoutineProposal{Fleet: "home", Routine: "r1", RoutineName: "Nightly sweep", Trigger: "t2",
			Action: sweepAction, ExpiresAt: time.Now().Add(2 * time.Hour)}); err != nil {
			t.Fatal(err)
		}
		r.waitState("home", old.ID, "moved_on", "")
		newer := r.cardFor("home", "routine-t2")
		r.waitState("home", newer.ID, "open", "")

		rec := r.post("home", old.ID, map[string]any{"launch": true})
		if rec.Code != http.StatusConflict || f.launched() != 0 {
			t.Errorf("tap on the moved-on card = %d with %d launches, want 409 and none", rec.Code, f.launched())
		}
	})
	t.Run("it expired", func(t *testing.T) {
		r := newNeedRig(t)
		r.fleetHooks()
		r.item("_admiral", "routine-t1", fleetRoutine("t1", "home", createdA, inAnHour(), sweepAction))
		r.start()
		card := r.cardFor("home", "routine-t1")

		r.item("_admiral", "routine-t1", fleetRoutine("t1", "home", createdA, time.Now().Add(-time.Hour), sweepAction))
		r.in.Refresh()
		r.waitState("home", card.ID, "moved_on", "")
	})
}

func TestFleetNeedCardARoutineCardAnswersEachInboxRefusalWithTheRoutesStatusAndMessage(t *testing.T) {
	t.Run("the launch hook refuses", func(t *testing.T) {
		for _, tc := range []struct {
			status int
			msg    string
		}{{http.StatusConflict, "no free ship slot"}, {http.StatusServiceUnavailable, "the scheduler is busy"}} {
			r := newNeedRig(t)
			f := r.fleetHooks()
			f.launchReply = func() (any, int, string) { return nil, tc.status, tc.msg }
			r.item("_admiral", "routine-t1", fleetRoutine("t1", "home", createdA, inAnHour(), sweepAction))
			r.start()
			card := r.cardFor("home", "routine-t1")

			rec := r.post("home", card.ID, map[string]any{"launch": true})
			if rec.Code != tc.status || errorOf(rec) != tc.msg {
				t.Errorf("tap = %d %s, want the hook's %d %q", rec.Code, rec.Body, tc.status, tc.msg)
			}
			r.noAnswer("routine-t1")
			if c := r.served("home")[0]; c.Card.State != "open" {
				t.Errorf("card state = %q after a refused launch, want open", c.Card.State)
			}
			f.mu.Lock()
			f.launchReply = nil
			f.mu.Unlock()
			if rec := r.post("home", card.ID, map[string]any{"launch": true}); !is2xx(rec) {
				t.Errorf("launch once there is room = %d %s, want success", rec.Code, rec.Body)
			}
		}
	})
	t.Run("launching is not wired", func(t *testing.T) {
		r := newNeedRig(t)
		f := r.fleetHooks()
		f.noLaunch = true
		r.item("_admiral", "routine-t1", fleetRoutine("t1", "home", createdA, inAnHour(), sweepAction))
		r.start()
		card := r.cardFor("home", "routine-t1")
		want := r.inboxPost("/api/inbox/_admiral/routine-t1/launch", `{}`)
		if want.Code != http.StatusNotImplemented {
			t.Fatalf("the Inbox's launch route = %d %s, want 501: the test's premise", want.Code, want.Body)
		}
		rec := r.post("home", card.ID, map[string]any{"launch": true})
		if rec.Code != http.StatusNotImplemented || errorOf(rec) != errorOf(want) {
			t.Errorf("tap = %d %s, want the Inbox's %d %s", rec.Code, rec.Body, want.Code, want.Body)
		}
	})
	t.Run("the proposal expired", func(t *testing.T) {
		r := newNeedRig(t)
		f := r.fleetHooks()
		exp := time.Now().Add(3 * time.Second)
		r.item("_admiral", "routine-t1", fleetRoutine("t1", "home", createdA, exp, sweepAction))
		r.start()
		card := r.cardFor("home", "routine-t1")
		// No pass or read of the cards runs meanwhile, so the card has not yet moved on.
		waitFor(t, "the proposal's expiry", func() bool { return time.Now().After(exp) })

		rec := r.post("home", card.ID, map[string]any{"launch": true})
		if rec.Code != http.StatusGone || errorOf(rec) != "the proposal expired" {
			t.Errorf("tap after the expiry = %d %s, want 410 %q", rec.Code, rec.Body, "the proposal expired")
		}
		if f.launched() != 0 {
			t.Errorf("an expired proposal ran the routine %d times", f.launched())
		}
	})
}

func TestFleetNeedCardAnAllowRuleCardRefusesABadDecisionWith400AndTheInboxsMessageAndWritesNothing(t *testing.T) {
	for _, tc := range []struct {
		name string
		body string
	}{
		{"a rule left out", `{"cards":{"c1":{"decision":"add"}}}`},
		{"a rule the item does not have", `{"cards":{"c1":{"decision":"add"},"c2":{"decision":"add"},"nope":{"decision":"add"}}}`},
		{"a decision that is not add or skip", `{"cards":{"c1":{"decision":"maybe"},"c2":{"decision":"add"}}}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := newNeedRig(t)
			f := r.fleetHooks()
			r.item("_admiral", "allow-1", fleetAllow("allow-1", "home", viewRuleCard(), checkRuleCard()))
			r.start()
			card := r.cardFor("home", "allow-1")

			want := r.inboxPost("/api/inbox/_admiral/allow-1/allow-rules", tc.body)
			if want.Code != http.StatusBadRequest {
				t.Fatalf("the Inbox's own route = %d %s, want 400: the test's premise", want.Code, want.Body)
			}
			var body map[string]any
			_ = json.Unmarshal([]byte(tc.body), &body)
			rec := r.post("home", card.ID, body)
			if rec.Code != http.StatusBadRequest || errorOf(rec) != errorOf(want) {
				t.Errorf("tap = %d %s, want the Inbox's %d %s", rec.Code, rec.Body, want.Code, want.Body)
			}
			r.noAnswer("allow-1")
			if got, _ := f.store.List("home"); len(got.Rules)+len(got.Skipped) != 0 {
				t.Errorf("a refused tap wrote to the store: %+v", got)
			}
			if c := r.served("home")[0]; c.Card.State != "open" {
				t.Errorf("card state = %q after a refused tap, want open", c.Card.State)
			}
		})
	}
}

func TestFleetNeedCardACampaignCardAnswersEachInboxRefusalWithTheRoutesStatusAndBody(t *testing.T) {
	draft := func(t *testing.T) (*needRig, *fleetFakes, needEntry) {
		r := newNeedRig(t)
		f := r.fleetHooks()
		r.item("_admiral", "camp-1", fleetDraft("camp-1", "home", createdA, "T", "B"))
		r.start()
		return r, f, r.cardFor("home", "camp-1")
	}
	for _, tc := range []struct {
		name   string
		body   string
		status int
	}{
		{"an empty title", `{"repo":"acme/widgets","title":" ","body":"B"}`, http.StatusBadRequest},
		{"a repo that is not one of the fleet's", `{"repo":"acme/elsewhere","title":"T","body":"B"}`, http.StatusUnprocessableEntity},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r, f, card := draft(t)
			want := r.inboxPost("/api/inbox/_admiral/camp-1/create", tc.body)
			if want.Code != tc.status {
				t.Fatalf("the Inbox's own route = %d %s, want %d: the test's premise", want.Code, want.Body, tc.status)
			}
			var body map[string]any
			_ = json.Unmarshal([]byte(tc.body), &body)
			rec := r.post("home", card.ID, body)
			if rec.Code != tc.status || errorOf(rec) != errorOf(want) {
				t.Errorf("tap = %d %s, want the Inbox's %d %s", rec.Code, rec.Body, want.Code, want.Body)
			}
			if f.created() != 0 {
				t.Errorf("a refused tap created %d campaigns", f.created())
			}
			r.noAnswer("camp-1")
		})
	}
	t.Run("creating is not wired", func(t *testing.T) {
		r := newNeedRig(t)
		f := r.fleetHooks()
		f.noCreate = true
		r.item("_admiral", "camp-1", fleetDraft("camp-1", "home", createdA, "T", "B"))
		r.start()
		card := r.cardFor("home", "camp-1")
		rec := r.post("home", card.ID, map[string]any{"repo": "acme/widgets", "title": "T", "body": "B"})
		if rec.Code != http.StatusNotImplemented || errorOf(rec) != "creating a campaign from a draft is not wired" {
			t.Errorf("tap = %d %s, want 501 %q", rec.Code, rec.Body, "creating a campaign from a draft is not wired")
		}
	})
	t.Run("the create hook fails before any parent issue", func(t *testing.T) {
		r, f, card := draft(t)
		f.createReply = func(inbox.CampaignDraft) (inbox.CreatedCampaign, int, string) {
			return inbox.CreatedCampaign{}, http.StatusBadGateway, "gh: boom"
		}
		rec := r.post("home", card.ID, map[string]any{"repo": "acme/widgets", "title": "T", "body": "B"})
		if rec.Code != http.StatusBadGateway || errorOf(rec) != "gh: boom" {
			t.Errorf("tap = %d %s, want the hook's 502 %q", rec.Code, rec.Body, "gh: boom")
		}
		r.noAnswer("camp-1")
		if n := len(r.campaignCards("home")); n != 0 {
			t.Errorf("a failed create posted %d outcome cards", n)
		}
		if c := r.served("home")[0]; c.Card.State != "open" {
			t.Errorf("card state = %q after a failed create, want open", c.Card.State)
		}
	})
	t.Run("the create fails after the parent issue was filed", func(t *testing.T) {
		r, f, card := draft(t)
		f.createReply = func(inbox.CampaignDraft) (inbox.CreatedCampaign, int, string) {
			return inbox.CreatedCampaign{ID: "camp-1", URL: campaignOK}, http.StatusBadGateway, "the labels failed"
		}
		rec := r.post("home", card.ID, map[string]any{"repo": "acme/widgets", "title": "T", "body": "B"})
		if rec.Code != http.StatusBadGateway {
			t.Errorf("tap = %d %s, want 502", rec.Code, rec.Body)
		}
		bodyIs(t, rec, `{"error":"the labels failed","parent":"`+campaignOK+`","answered":"camp-1"}`)
		if got := r.answerOf("_admiral", "camp-1"); got["created"] != true || got["url"] != campaignOK || got["failed"] != "the labels failed" {
			t.Errorf("answer file = %v, want created, the parent's url and what failed", got)
		}
		waitFor(t, "the campaign_draft_card outcome entry", func() bool { return len(r.campaignCards("home")) == 1 })
		if e := r.campaignCards("home")[0]; !strings.Contains(string(e.Card), "the labels failed") {
			t.Errorf("outcome entry = %s, want it to say what failed", e.Card)
		}
		r.waitState("home", card.ID, "answered", "chat")
	})
	t.Run("an action is already running on the item", func(t *testing.T) {
		r, f, card := draft(t)
		release := make(chan struct{})
		f.createReply = func(inbox.CampaignDraft) (inbox.CreatedCampaign, int, string) {
			<-release
			return inbox.CreatedCampaign{ID: "camp-1", URL: campaignOK}, 0, ""
		}
		done := make(chan error, 1)
		go func() {
			_, err := r.in.CreateCampaignItem(context.Background(), "camp-1", inbox.CampaignDraftRequest{Repo: "acme/widgets", Title: "T", Body: "B"})
			done <- err
		}()
		waitFor(t, "the Inbox's create reaching the hook", func() bool { return f.created() == 1 })

		rec := r.post("home", card.ID, map[string]any{"repo": "acme/widgets", "title": "T", "body": "B"})
		if rec.Code != http.StatusConflict || errorOf(rec) != inbox.ErrBusy.Error() {
			t.Errorf("a tap while the create runs = %d %s, want 409 %q", rec.Code, rec.Body, inbox.ErrBusy)
		}
		if f.created() != 1 {
			t.Errorf("a busy tap ran the hook again: %d calls", f.created())
		}
		close(release)
		if err := <-done; err != nil {
			t.Fatalf("the Inbox's create: %v", err)
		}
	})
}

func TestFleetNeedCardAnAnswerUnderAnotherFleetsRouteCannotAnswerThisFleetsItem(t *testing.T) {
	r := newNeedRig(t)
	f := r.fleetHooks()
	r.item("_admiral", "routine-t1", fleetRoutine("t1", "home", createdA, inAnHour(), sweepAction))
	r.item("_admiral", "allow-1", fleetAllow("allow-1", "home", viewRuleCard()))
	r.item("_admiral", "camp-1", fleetDraft("camp-1", "home", createdA, "T", "B"))
	r.start()
	taps := map[string]map[string]any{
		"routine-t1": {"launch": true},
		"allow-1":    {"cards": map[string]any{"c1": map[string]any{"decision": "add"}}},
		"camp-1":     {"repo": "acme/widgets", "title": "T", "body": "B"},
	}
	for item, tap := range taps {
		card := r.cardFor("home", item)
		for _, other := range []string{"work", "off"} {
			if rec := r.post(other, card.ID, tap); rec.Code != http.StatusNotFound {
				t.Errorf("a tap on home's card %s under the %s chat = %d %s, want 404", item, other, rec.Code, rec.Body)
			}
		}
		r.noAnswer(item)
	}
	if got, _ := f.store.List("home"); len(got.Rules) != 0 {
		t.Errorf("a refused tap added rules: %+v", got.Rules)
	}
	if f.launched() != 0 || f.created() != 0 {
		t.Errorf("refused taps launched %d and created %d", f.launched(), f.created())
	}
}
