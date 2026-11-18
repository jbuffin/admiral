package allowrules

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"github.com/jbuffin/admiral/internal/permlog"
	"github.com/jbuffin/admiral/internal/standing"
)

const home = "/Users/me/.admiral"

func allowed(ship, id, harness string, call permlog.Call) Prompt {
	return Prompt{Record: permlog.Record{ID: id, Ship: ship, Harness: harness, Fleet: "work", Repo: "acme/widgets",
		Call: call, Answer: permlog.Allow, By: permlog.Person, Via: permlog.Terminal}}
}

// claude is a Bash or file prompt carrying the one rule Claude Code suggested for it.
func claude(ship, id, tool, content string) Prompt {
	sug, _ := json.Marshal([]map[string]any{{"type": "addRules", "behavior": "allow", "destination": "localSettings",
		"rules": []map[string]string{{"toolName": tool, "ruleContent": content}}}})
	return allowed(ship, id, "claude", permlog.Call{Tool: tool, Kind: "shell", Suggestions: sug})
}

func codex(ship, id, command string) Prompt {
	return allowed(ship, id, "codex", permlog.Call{Tool: "Bash", Kind: "shell", Command: command})
}

func copilotShell(ship, id string, identifiers ...string) Prompt {
	return allowed(ship, id, "copilot", permlog.Call{Tool: "shell", Kind: "shell", Identifiers: identifiers})
}

func TestTheNeverListRefusesEachGroup(t *testing.T) {
	cases := []struct {
		group  string
		prompt Prompt
	}{
		// Copilot's shell(gh pr) also allows gh pr merge, so the identifier is refused though the prompt was a create.
		{NeverPushMerge, copilotShell("a", "1", "gh pr")},
		{NeverGHAPI, codex("a", "2", `/bin/zsh -lc 'gh api -X POST repos/acme/widgets/labels -f name=bug'`)},
		{NeverPublish, claude("a", "3", "Bash", "npm publish:*")},
		{NeverInterpreter, codex("a", "4", `/bin/zsh -lc "python3 -c 'print(1)'"`)},
		{NeverDelete, claude("a", "5", "Bash", "git branch -D old-work")},
		{NeverNetwork, claude("a", "6", "WebFetch", "domain:example.com")},
		{NeverSecret, claude("a", "7", "Bash", "security find-generic-password:*")},
		{NeverOutside, claude("a", "8", "Edit", "//Users/me/other-repo/**")},
		{NeverWiring, codex("a", "9", "chmod +x scripts/release.sh")},
	}
	for _, c := range cases {
		t.Run(c.group, func(t *testing.T) {
			got, refused := Candidates([]Prompt{c.prompt}, nil, home)
			if len(got) != 0 {
				t.Fatalf("proposed %+v", got[0].Rule)
			}
			if len(refused) != 1 || !strings.Contains(refused[0].Reason, "never list ("+c.group+")") {
				t.Fatalf("refusals %+v, want one from the %q group", refused, c.group)
			}
		})
	}
}

func TestOnlyAPersonsAllowFromATrustedLaunchIsProposed(t *testing.T) {
	ok := claude("a", "1", "Bash", "gh pr view:*")
	denied := ok
	denied.Answer = permlog.Deny
	timedOut := ok
	timedOut.Answer, timedOut.By = permlog.Deny, permlog.Timeout
	reviewer := ok
	reviewer.By, reviewer.Via = permlog.Reviewer, ""
	restricted := ok
	restricted.Restricted = true
	t2 := ok
	t2.Tier = standing.T2
	t1 := ok
	t1.Tier = standing.T1

	for name, p := range map[string]Prompt{"denied": denied, "timed out": timedOut, "allowed by the reviewer": reviewer,
		"restricted ship": restricted, "T2 launch": t2} {
		if got, refused := Candidates([]Prompt{p}, nil, home); len(got) != 0 || len(refused) != 1 || refused[0].Reason == "" {
			t.Errorf("%s: candidates %v, refusals %v; want none and one refusal with a reason", name, got, refused)
		}
	}
	for name, p := range map[string]Prompt{"the engineer's launch": ok, "T1 launch": t1} {
		got, _ := Candidates([]Prompt{p}, nil, home)
		want := Rule{Harness: "claude", Repo: "acme/widgets", Rule: "Bash(gh pr view:*)"}
		if len(got) != 1 || !got[0].Rule.Same(want) {
			t.Errorf("%s: candidates %v, want %v", name, got, want)
		}
	}
}

func TestRulesAlreadyAllowedOrSkippedAreNotProposed(t *testing.T) {
	view := Rule{Harness: "claude", Repo: "acme/widgets", Rule: "Bash(gh pr view:*)"}
	checks := Rule{Harness: "claude", Repo: "acme/widgets", Rule: "Bash(gh pr checks:*)"}
	fleets := map[string]Fleet{"work": {Rules: []Rule{view}, Skipped: []Rule{checks}}}
	got, refused := Candidates([]Prompt{
		claude("a", "1", "Bash", "gh pr view:*"),
		claude("a", "2", "Bash", "gh pr checks:*"),
	}, fleets, home)
	if len(got) != 0 {
		t.Fatalf("proposed %v", got)
	}
	var reasons []string
	for _, r := range refused {
		reasons = append(reasons, r.Reason)
	}
	if !slices.Equal(reasons, []string{"already allowed", "the engineer skipped it"}) {
		t.Errorf("refusals %q", reasons)
	}
	// Another fleet's rules and skips don't count.
	if got, _ := Candidates([]Prompt{claude("a", "1", "Bash", "gh pr view:*")}, map[string]Fleet{"home": fleets["work"]}, home); len(got) != 1 {
		t.Errorf("a rule only another fleet holds gave candidates %v", got)
	}
}

func TestTheSamePromptTwiceIsOneCandidateWithACountOfTwo(t *testing.T) {
	got, _ := Candidates([]Prompt{
		codex("alpha", "1", `/bin/zsh -lc 'gh pr view 7 --json title'`),
		codex("beta", "1", `gh pr view 12`),
		codex("alpha", "1", `/bin/zsh -lc 'gh pr view 7 --json title'`), // the same log line read again
	}, nil, home)
	if len(got) != 1 || got[0].Count() != 2 || !slices.Equal(got[0].Rule.Pattern, []string{"gh", "pr", "view"}) {
		t.Fatalf("candidates %+v, want one gh pr view rule with a count of 2", got)
	}
	if got[0].Rule.Repo != "" || got[0].Fleet != "work" {
		t.Errorf("a Codex rule is %+v in fleet %q, want no repo in fleet work", got[0].Rule, got[0].Fleet)
	}
}

func TestACompoundCopilotCommandProposesEachSafeIdentifier(t *testing.T) {
	got, refused := Candidates([]Prompt{copilotShell("a", "1", "npm test", "git push")}, nil, home)
	if len(got) != 1 || got[0].Rule.Rule != "shell(npm test)" {
		t.Fatalf("candidates %+v, want shell(npm test)", got)
	}
	if len(refused) != 1 || refused[0].Rule.Rule != "shell(git push)" {
		t.Errorf("refusals %+v, want shell(git push)", refused)
	}
}

func TestWritesReachOnlyTheWorktreeAndAdmiralsReportAndInbox(t *testing.T) {
	write := func(p string) Prompt {
		return allowed("a", p, "copilot", permlog.Call{Tool: "write", Kind: "write", Paths: []string{p}})
	}
	for p, want := range map[string]bool{
		home + "/reports/alpha.md":            true,
		home + "/inbox/alpha/note.md":         true,
		home + "/inbox/_admiral/forged.json":  false, // where Admiral's own proposals live
		home + "/allow-rules.json":            false,
		"/Users/me/other-repo/main.go":        false,
		home + "/reports/../allow-rules.json": false,
	} {
		got, _ := Candidates([]Prompt{write(p)}, nil, home)
		if (len(got) == 1) != want {
			t.Errorf("write to %s: candidates %v, want proposed=%v", p, got, want)
		}
	}
	if got, _ := Candidates([]Prompt{claude("a", "1", "Edit", "docs/**")}, nil, home); len(got) != 1 {
		t.Errorf("an edit inside the worktree gave candidates %v", got)
	}
}

func TestAWholeToolRuleIsNeverProposed(t *testing.T) {
	for _, p := range []Prompt{claude("a", "1", "Bash", ""), claude("a", "2", "Edit", "**")} {
		if got, refused := Candidates([]Prompt{p}, nil, home); len(got) != 0 || len(refused) != 1 {
			t.Errorf("%s: candidates %v, refusals %v", p.Call.Suggestions, got, refused)
		}
	}
}
