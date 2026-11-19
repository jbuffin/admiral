package inbox

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/jbuffin/admiral/internal/allowrules"
	"github.com/jbuffin/admiral/internal/outcomes"
	"github.com/jbuffin/admiral/internal/permlog"
)

var proposedRule = allowrules.Rule{Harness: "claude", Repo: "acme/widgets", Rule: "Bash(gh pr view:*)"}

func candidate(ids ...string) []allowrules.Candidate {
	c := allowrules.Candidate{Fleet: "work", Rule: proposedRule}
	for _, id := range ids {
		c.Prompts = append(c.Prompts, permlog.Record{Ship: "saltpaw", ID: id, At: "2026-11-11T10:00:00Z"})
	}
	return []allowrules.Candidate{c}
}

func proposalItems(t *testing.T, home string) []AllowRulesObject {
	t.Helper()
	var out []AllowRulesObject
	for _, it := range scan(home) {
		if o, ok := it.AllowRulesObject(); ok {
			out = append(out, o)
		}
	}
	return out
}

func TestRulesLandOnANewItemWhenTheOpenOneIsAnsweredJustBeforeTheWrite(t *testing.T) {
	home := t.TempDir()
	if err := WriteAllowRuleProposals(home, "work", candidate("p1"), nil); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(home, "inbox", AdmiralSession)
	answered := ""
	beforeProposalWrite = func(dir, id string) {
		beforeProposalWrite = nil
		answered = id
		if err := os.WriteFile(filepath.Join(dir, answerName(id)), []byte(`{"version":1}`), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	t.Cleanup(func() { beforeProposalWrite = nil })

	if err := WriteAllowRuleProposals(home, "work", candidate("p2"), nil); err != nil {
		t.Fatal(err)
	}
	if answered == "" {
		t.Fatal("the seam never ran")
	}
	open := proposalItems(t, home) // an answered item is not listed
	if len(open) != 1 || len(open[0].Cards) != 1 || open[0].Cards[0].Count != 1 || open[0].Cards[0].Prompts[0].ID != "p2" {
		t.Fatalf("open items = %+v, want one new item holding the round's p2 only", open)
	}
	b, err := os.ReadFile(filepath.Join(dir, answered+".json"))
	if err != nil || len(b) == 0 {
		t.Fatalf("the answered item is unreadable: %v", err)
	}
}

func TestACardPastThePromptCapKeepsTheNewestAndTheTrueCount(t *testing.T) {
	home := t.TempDir()
	var ids []string
	for i := 0; i < maxCardPrompts+5; i++ {
		ids = append(ids, fmt.Sprintf("p%02d", i))
	}
	if err := WriteAllowRuleProposals(home, "work", candidate(ids...), nil); err != nil {
		t.Fatal(err)
	}
	if err := WriteAllowRuleProposals(home, "work", candidate("p98", "p99"), nil); err != nil {
		t.Fatal(err)
	}
	items := proposalItems(t, home)
	if len(items) != 1 || len(items[0].Cards) != 1 {
		t.Fatalf("items = %+v, want one item with one card", items)
	}
	c := items[0].Cards[0]
	if c.Count != maxCardPrompts+7 || len(c.Prompts) != maxCardPrompts {
		t.Fatalf("count %d with %d prompts, want %d counted and %d kept", c.Count, len(c.Prompts), maxCardPrompts+7, maxCardPrompts)
	}
	if last := c.Prompts[len(c.Prompts)-1].ID; last != "p99" || c.Prompts[0].ID != "p07" {
		t.Errorf("kept prompts run %s to %s, want the newest 20 (p07 to p99)", c.Prompts[0].ID, last)
	}
}

func TestAnAnswerOrRemoveInTheAdmiralInboxHoldsTheWritersLock(t *testing.T) {
	s := &Service{Home: t.TempDir()}
	dir := filepath.Join(s.Home, "inbox", AdmiralSession)
	if err := WriteAllowRuleProposals(s.Home, "work", candidate("p1"), nil); err != nil {
		t.Fatal(err)
	}
	items := proposalItems(t, s.Home)
	_ = items
	entries, _ := os.ReadDir(dir)
	id := entries[0].Name()[:len(entries[0].Name())-len(".json")]
	held := false
	_ = s.writeAnswered(AdmiralSession, id, KindAllowRules, func(Item) (any, []outcomes.Record, error) {
		if proposalMu.TryLock() {
			proposalMu.Unlock()
		} else {
			held = true
		}
		return map[string]any{"version": 1, "id": id, "kind": KindAllowRules}, nil, nil
	})
	if !held {
		t.Error("an answer ran without the lock the proposal writer takes, so the two can interleave")
	}
}
