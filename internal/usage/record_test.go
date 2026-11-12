package usage

import (
	"encoding/json"
	"strings"
	"testing"
)

func fieldsJSON(t *testing.T, fields map[string]any) string {
	t.Helper()
	b, err := json.Marshal(fields)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func eq(t *testing.T, got map[string]any, want string) {
	t.Helper()
	var w any
	if err := json.Unmarshal([]byte(want), &w); err != nil {
		t.Fatalf("bad golden %s: %v", want, err)
	}
	canon, _ := json.Marshal(w)
	if g := fieldsJSON(t, got); g != string(canon) {
		t.Errorf("fields =\n%s\nwant\n%s", g, canon)
	}
}

func claudeFigs() Figures {
	return Figures{Tokens: Tokens{Input: 2, Output: 4, CacheRead: 11382, CacheWrite: 18024}, Money: &Money{Amount: 0.00372082, Basis: BasisEstimate}}
}

func codexFigs() Figures {
	return Figures{Tokens: Tokens{Input: 7609, Output: 5, CacheRead: 12672}}
}

func copilotFigs() Figures {
	premium := 15.0
	return Figures{Tokens: Tokens{Input: 4, Output: 4, CacheWrite: 35732}, Money: &Money{Amount: 17.8756, Basis: BasisCredits, PremiumRequestCost: &premium}}
}

func ptr(f Figures) *Figures { return &f }

func TestExitFieldsClaudeShip(t *testing.T) {
	got := ExitFields("work", "claude", 0, ptr(claudeFigs()), nil)
	eq(t, got, `{"fleet":"work","harness":"claude",
		"tokens":{"input":2,"output":4,"reasoning":0,"cache_read":11382,"cache_write":18024},
		"money":{"amount":0.00372082,"basis":"estimate","incomplete":false}}`)
}

func TestExitFieldsCodexShipHasNullMoney(t *testing.T) {
	got := ExitFields("work", "codex", 0, ptr(codexFigs()), nil)
	eq(t, got, `{"fleet":"work","harness":"codex",
		"tokens":{"input":7609,"output":5,"reasoning":0,"cache_read":12672,"cache_write":0},
		"money":null}`)
}

func TestExitFieldsCopilotShipCarriesCreditsAndPremiumRequests(t *testing.T) {
	got := ExitFields("work", "copilot", 0, ptr(copilotFigs()), nil)
	eq(t, got, `{"fleet":"work","harness":"copilot",
		"tokens":{"input":4,"output":4,"reasoning":0,"cache_read":0,"cache_write":35732},
		"money":{"amount":17.8756,"basis":"credits","incomplete":false,"premium_requests":15}}`)
}

func TestExitFieldsMarksIncompleteMoney(t *testing.T) {
	f := claudeFigs()
	f.Money.Incomplete = true
	got := ExitFields("work", "claude", 0, &f, nil)
	if !contains(fieldsJSON(t, got), `"incomplete":true`) {
		t.Errorf("fields = %s, want incomplete true", fieldsJSON(t, got))
	}
}

func contains(s, sub string) bool { return strings.Contains(s, sub) }

func TestExitFieldsRoundOnlyWhenMoored(t *testing.T) {
	if s := fieldsJSON(t, ExitFields("work", "claude", 0, ptr(claudeFigs()), nil)); contains(s, `"round"`) {
		t.Errorf("round 0 wrote a round: %s", s)
	}
	if s := fieldsJSON(t, ExitFields("work", "claude", 3, ptr(claudeFigs()), nil)); !contains(s, `"round":3`) {
		t.Errorf("round 3 is missing: %s", s)
	}
}

func TestExitFieldsNoFiguresIsNullNotZero(t *testing.T) {
	got := ExitFields("work", "claude", 0, nil, nil)
	eq(t, got, `{"fleet":"work","harness":"claude","tokens":null,"money":null}`)
}

func TestExitFieldsCrewIsSummedAndListed(t *testing.T) {
	crew := []Boat{
		{Name: "scout", Harness: "claude", Figures: ptr(Figures{Tokens: Tokens{Input: 10, Output: 1, Reasoning: 2, CacheRead: 3, CacheWrite: 4}, Money: &Money{Amount: 0.5, Basis: BasisEstimate, Incomplete: true}})},
		{Name: "tester", Harness: "claude", Figures: ptr(Figures{Tokens: Tokens{Input: 20}, Money: &Money{Amount: 0.25, Basis: BasisEstimate}})},
	}
	own := claudeFigs()
	own.Money.Amount = 0.125 // binary fractions, so the sum is exact
	got := ExitFields("work", "claude", 0, &own, crew)
	eq(t, got, `{"fleet":"work","harness":"claude",
		"tokens":{"input":32,"output":5,"reasoning":2,"cache_read":11385,"cache_write":18028},
		"money":{"amount":0.875,"basis":"estimate","incomplete":true},
		"crew":[
		  {"name":"scout","harness":"claude","tokens":{"input":10,"output":1,"reasoning":2,"cache_read":3,"cache_write":4},"money":{"amount":0.5,"basis":"estimate","incomplete":true}},
		  {"name":"tester","harness":"claude","tokens":{"input":20,"output":0,"reasoning":0,"cache_read":0,"cache_write":0},"money":{"amount":0.25,"basis":"estimate","incomplete":false}}]}`)
}

func TestExitFieldsCrewWithNoFiguresIsListedWithNulls(t *testing.T) {
	crew := []Boat{{Name: "scout", Harness: "claude"}}
	got := ExitFields("work", "claude", 0, ptr(claudeFigs()), crew)
	eq(t, got, `{"fleet":"work","harness":"claude",
		"tokens":{"input":2,"output":4,"reasoning":0,"cache_read":11382,"cache_write":18024},
		"money":{"amount":0.00372082,"basis":"estimate","incomplete":false},
		"crew":[{"name":"scout","harness":"claude","tokens":null,"money":null}]}`)
}

// Money never sums across bases: the boat keeps its own figures under crew, and its tokens still count.
func TestExitFieldsCrewOfAnotherBasisAddsTokensNotMoney(t *testing.T) {
	crew := []Boat{{Name: "helper", Harness: "copilot", Figures: ptr(copilotFigs())}}
	got := ExitFields("work", "claude", 0, ptr(claudeFigs()), crew)
	eq(t, got, `{"fleet":"work","harness":"claude",
		"tokens":{"input":6,"output":8,"reasoning":0,"cache_read":11382,"cache_write":53756},
		"money":{"amount":0.00372082,"basis":"estimate","incomplete":false},
		"crew":[{"name":"helper","harness":"copilot",
		  "tokens":{"input":4,"output":4,"reasoning":0,"cache_read":0,"cache_write":35732},
		  "money":{"amount":17.8756,"basis":"credits","incomplete":false,"premium_requests":15}}]}`)
}

// A Codex crew boat reports no money, so it adds tokens to a Claude ship and leaves the estimate alone.
func TestExitFieldsCodexCrewAddsTokensOnly(t *testing.T) {
	crew := []Boat{{Name: "codey", Harness: "codex", Figures: ptr(codexFigs())}}
	got := ExitFields("work", "claude", 0, ptr(claudeFigs()), crew)
	s := fieldsJSON(t, got)
	if !contains(s, `"tokens":{"input":7611,"output":9,"reasoning":0,"cache_read":24054,"cache_write":18024}`) ||
		!contains(s, `"money":{"amount":0.00372082,"basis":"estimate","incomplete":false}`) ||
		!contains(s, `"name":"codey","harness":"codex","tokens":{"input":7609,"output":5,"reasoning":0,"cache_read":12672,"cache_write":0},"money":null`) {
		t.Errorf("fields = %s", s)
	}
}

// A Codex ship has no money of its own, so a crew boat's money is not promoted to the ship's.
func TestExitFieldsShipWithoutMoneyStaysNullWhateverItsCrewSpent(t *testing.T) {
	crew := []Boat{{Name: "helper", Harness: "claude", Figures: ptr(claudeFigs())}}
	var m map[string]any
	if err := json.Unmarshal([]byte(fieldsJSON(t, ExitFields("work", "codex", 0, ptr(codexFigs()), crew))), &m); err != nil {
		t.Fatal(err)
	}
	if v, has := m["money"]; !has || v != nil {
		t.Errorf("ship money = %v, want null", v)
	}
	boats, _ := m["crew"].([]any)
	if len(boats) != 1 || boats[0].(map[string]any)["money"] == nil {
		t.Errorf("crew = %v, want the boat listed with its own money", m["crew"])
	}
}

// Without its own figures the ship's totals are unknown, however much its crew used.
func TestExitFieldsShipWithoutFiguresIsNullEvenWithCrewFigures(t *testing.T) {
	crew := []Boat{{Name: "helper", Harness: "claude", Figures: ptr(claudeFigs())}}
	got := ExitFields("work", "claude", 0, nil, crew)
	s := fieldsJSON(t, got)
	if !contains(s, `"tokens":null,"money":null`) || !contains(s, `"name":"helper"`) {
		t.Errorf("fields = %s, want null totals and the boat still listed", s)
	}
}
