package inbox_test

import (
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/jbuffin/admiral/internal/allowrules"
	"github.com/jbuffin/admiral/internal/inbox"
)

const ruleItemID = "allow-rules-work"

func ruleCard(id, harness, repo, rule string, pattern []string) map[string]any {
	c := map[string]any{"id": id, "harness": harness, "count": 2,
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
	}
	if harness == "codex" {
		c["every_repo"] = true
	}
	return c
}

func allowRulesItem(id, fleet string, cards ...map[string]any) map[string]any {
	return map[string]any{"version": 1, "id": id, "kind": "allow_rules", "created": "2026-11-10T10:00:00Z",
		"title":       "Allow rules for " + fleet,
		"allow_rules": map[string]any{"fleet": fleet, "cards": cards}}
}

var (
	viewRule  = allowrules.Rule{Harness: "claude", Repo: "acme/widgets", Rule: "Bash(gh pr view:*)"}
	listRule  = allowrules.Rule{Harness: "claude", Repo: "acme/widgets", Rule: "Bash(ls:*)"}
	checkRule = allowrules.Rule{Harness: "codex", Pattern: []string{"make", "check"}}
)

func (r *crewRig) withStore() (*allowrules.Store, *[]string) {
	store := allowrules.Open(r.home)
	var changed []string
	store.OnChange = func(fleet string) { changed = append(changed, fleet) }
	r.s.AllowRules = store
	return store, &changed
}

func (r *crewRig) answerCards(id string, decisions map[string]string) (int, map[string]any) {
	r.t.Helper()
	cards := map[string]any{}
	for card, d := range decisions {
		cards[card] = map[string]any{"decision": d}
	}
	return r.postJSON("/api/inbox/_admiral/"+id+"/allow-rules", map[string]any{"cards": cards})
}

func (r *crewRig) seedThreeCards() {
	r.seed(allowRulesItem(ruleItemID, "work",
		ruleCard("c1", "claude", "acme/widgets", viewRule.Rule, nil),
		ruleCard("c2", "claude", "acme/widgets", listRule.Rule, nil),
		ruleCard("c3", "codex", "", "", []string{"make", "check"})))
}

func TestAnAllowRuleItemListsWithItsFleetAndOnlyItsOwnObjectAndOnlyFromTheAdmiralInbox(t *testing.T) {
	r := newCrewRig(t)
	r.withStore()
	r.seedThreeCards()
	writeItem(t, shipDir(t, r.home, "s1"), "stray.json", allowRulesItem("stray", "work", ruleCard("c1", "claude", "acme/widgets", viewRule.Rule, nil)))
	r.s.Refresh()

	res, err := http.Get(r.srv.URL + "/api/inbox")
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	b, _ := io.ReadAll(res.Body)
	var got struct{ Items []map[string]any }
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatal(err)
	}
	if len(got.Items) != 1 {
		t.Fatalf("items = %s, want only the one in the _admiral inbox", b)
	}
	it := got.Items[0]
	if it["kind"] != "allow_rules" || it["fleet"] != "work" || it["fleet_name"] != "Work" || it["fleet_color"] != "#38bdf8" {
		t.Errorf("item = %v, want kind allow_rules in fleet work, Work, #38bdf8", it)
	}
	obj, _ := it["allow_rules"].(map[string]any)
	if cards, _ := obj["cards"].([]any); len(cards) != 3 {
		t.Errorf("allow_rules = %v, want its three cards", obj)
	}
	for _, other := range []string{"question", "review", "findings", "crew_notes", "mission"} {
		if _, has := it[other]; has {
			t.Errorf("item carries a %s object", other)
		}
	}
}

func TestAddingCardsPutsTheRuleInTheFleetsStoreAndSkippingKeepsItFromBeingProposedAgain(t *testing.T) {
	r := newCrewRig(t)
	store, changed := r.withStore()
	r.seedThreeCards()

	code, body := r.answerCards(ruleItemID, map[string]string{"c1": "add", "c2": "skip", "c3": "add"})
	if code != 200 || body["answered"] != ruleItemID {
		t.Fatalf("answer = %d %v, want 200", code, body)
	}

	fl, err := store.List("work")
	if err != nil {
		t.Fatal(err)
	}
	wantRules := []allowrules.Rule{viewRule, checkRule}
	if len(fl.Rules) != 2 || !fl.Rules[0].Same(wantRules[0]) || !fl.Rules[1].Same(wantRules[1]) {
		t.Errorf("the fleet's rules = %+v, want the two added ones", fl.Rules)
	}
	if len(fl.Skipped) != 1 || !fl.Skipped[0].Same(listRule) {
		t.Errorf("the fleet's skips = %+v, want only %s", fl.Skipped, listRule.Rule)
	}
	// The next launch in that repo gets the added rule, and Codex's fleet-wide rule gets it for any repo.
	if got, _ := store.For("work", "claude", "acme/widgets"); len(got) != 1 || !got[0].Same(viewRule) {
		t.Errorf("rules for the next Claude ship in acme/widgets = %+v, want the added one", got)
	}
	if got, _ := store.For("work", "codex", "acme/anything"); len(got) != 1 || !got[0].Same(checkRule) {
		t.Errorf("rules for the next Codex ship = %+v, want make check", got)
	}
	if !reflect.DeepEqual(*changed, []string{"work", "work"}) {
		t.Errorf("rules-changed callbacks = %v, want one per added rule and none for the skip", *changed)
	}

	a := answerOf(t, r.home, ruleItemID)
	if a["kind"] != "allow_rules" || a["version"] != float64(1) || a["answered"] == nil {
		t.Errorf("answer file = %v, want kind allow_rules, version 1 and answered", a)
	}
	cards, _ := a["cards"].(map[string]any)
	for card, want := range map[string]string{"c1": "add", "c2": "skip", "c3": "add"} {
		if c, _ := cards[card].(map[string]any); c["decision"] != want {
			t.Errorf("answer file card %s = %v, want %s", card, cards[card], want)
		}
	}

	var added, skipped int
	for _, rec := range r.recs() {
		switch rec["event"] {
		case "allow_rule_added":
			added++
		case "allow_rule_skipped":
			skipped++
		default:
			continue
		}
		if rec["item"] != ruleItemID || rec["kind"] != "allow_rules" || rec["card"] == nil {
			t.Errorf("outcome %v, want item, kind and card fields", rec)
		}
	}
	if added != 2 || skipped != 1 {
		t.Errorf("outcomes: %d added, %d skipped, want 2 and 1", added, skipped)
	}
}

func TestAnAllowRuleAnswerThatDoesNotDecideEveryCardWithAddOrSkipChangesNothing(t *testing.T) {
	for name, decisions := range map[string]map[string]string{
		"a card with no decision": {"c1": "add", "c2": "skip"},
		"editing is not a choice": {"c1": "add", "c2": "skip", "c3": "edit"},
		"a card the item lacks":   {"c1": "add", "c2": "skip", "c3": "add", "c9": "add"},
	} {
		t.Run(name, func(t *testing.T) {
			r := newCrewRig(t)
			store, _ := r.withStore()
			r.seedThreeCards()

			if code, body := r.answerCards(ruleItemID, decisions); code != 400 {
				t.Fatalf("answer = %d %v, want 400", code, body)
			}
			if fl, _ := store.List("work"); len(fl.Rules)+len(fl.Skipped) != 0 {
				t.Errorf("the store holds %+v after a refused answer", fl)
			}
			r.assertUnanswered(ruleItemID)
		})
	}
}

func TestRemovingAnAllowRuleItemHidesItAndSkipsNothing(t *testing.T) {
	r := newCrewRig(t)
	store, _ := r.withStore()
	r.seedThreeCards()

	if code, body := r.postJSON("/api/inbox/_admiral/"+ruleItemID+"/remove", map[string]any{}); code != 200 {
		t.Fatalf("remove = %d %v", code, body)
	}
	if got := r.s.List(); len(got) != 0 {
		t.Errorf("the inbox still lists %d items after Remove", len(got))
	}
	if _, err := os.Stat(filepath.Join(r.home, "inbox", inbox.AdmiralSession, ruleItemID+".answer.json")); err != nil {
		t.Errorf("Remove left no answer file: %v", err)
	}
	if fl, _ := store.List("work"); len(fl.Rules) != 0 || len(fl.Skipped) != 0 {
		t.Errorf("the store holds %+v after Remove, want neither rules nor skips: only Skip stops a proposal", fl)
	}
}
