package allowrules

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

var prView = Rule{Harness: "claude", Repo: "acme/widgets", Rule: "Bash(gh pr view:*)"}

func TestARuleIsKeptOnceAndRemoved(t *testing.T) {
	s := Open(t.TempDir())
	for range 2 {
		if err := s.Add("work", prView); err != nil {
			t.Fatal(err)
		}
	}
	if got, _ := s.For("work", "claude", "acme/widgets"); len(got) != 1 || !got[0].Same(prView) || got[0].Added.IsZero() {
		t.Fatalf("after adding a rule twice the store holds %v, want it once with the time it was added", got)
	}
	if got, _ := s.For("home", "claude", "acme/widgets"); len(got) != 0 {
		t.Errorf("another fleet gets %v, want no rules", got)
	}
	if err := s.Remove("work", prView); err != nil {
		t.Fatal(err)
	}
	if got, _ := s.List("work"); len(got.Rules) != 0 {
		t.Errorf("after Remove the fleet holds %v", got.Rules)
	}
}

func TestASkipIsKeptApartFromTheRules(t *testing.T) {
	s := Open(t.TempDir())
	var changed []string
	s.OnChange = func(fleet string) { changed = append(changed, fleet) }
	if err := s.Skip("work", prView); err != nil {
		t.Fatal(err)
	}
	got, _ := s.List("work")
	if len(got.Rules) != 0 || len(got.Skipped) != 1 || !got.Skipped[0].Same(prView) {
		t.Fatalf("after Skip the fleet holds %+v, want the rule skipped and not allowed", got)
	}
	if err := s.Unskip("work", prView); err != nil {
		t.Fatal(err)
	}
	if got, _ := s.List("work"); len(got.Skipped) != 0 {
		t.Errorf("after Unskip the fleet still skips %v", got.Skipped)
	}
	if len(changed) != 0 {
		t.Errorf("a skip changed the rules of %v, want no rule change", changed)
	}
	if err := s.Add("work", prView); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(changed, []string{"work"}) {
		t.Errorf("adding a rule reported changes to %v, want [work]", changed)
	}
}

func TestACorruptStoreIsNeverRewritten(t *testing.T) {
	home := t.TempDir()
	path := filepath.Join(home, FileName)
	if err := os.WriteFile(path, []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := Open(home).Add("work", prView); err == nil {
		t.Error("Add on a corrupt store succeeded, want an error")
	}
	if b, _ := os.ReadFile(path); string(b) != "{not json" {
		t.Errorf("the corrupt store now reads %q, want it left for the engineer", b)
	}
}

func TestAMisshapenRuleIsRefused(t *testing.T) {
	for name, r := range map[string]Rule{
		"a Claude rule with no repo":      {Harness: "claude", Rule: "Bash(make check)"},
		"a Codex rule scoped to a repo":   {Harness: "codex", Repo: "acme/widgets", Pattern: []string{"make"}},
		"a rule with a control character": {Harness: "claude", Repo: "acme/widgets", Rule: "Bash(make\ncheck)"},
		"an unknown harness":              {Harness: "gemini", Repo: "acme/widgets", Rule: "x"},
	} {
		if err := Open(t.TempDir()).Add("work", r); err == nil {
			t.Errorf("%s was added, want it refused", name)
		}
	}
}

func TestCodexRulesQuoteEachToken(t *testing.T) {
	got := CodexRules([]Rule{{Harness: "codex", Pattern: []string{"echo", `a "b" \c`}}, {Harness: "codex", Pattern: []string{"echo", `a "b" \c`}}})
	want := "# Admiral's allow rules. Admiral rewrites this file whenever they change, so an edit here is lost.\n" +
		`prefix_rule(pattern=["echo", "a \"b\" \\c"], decision="allow")` + "\n"
	if got != want {
		t.Errorf("CodexRules =\n%s\nwant\n%s", got, want)
	}
}
