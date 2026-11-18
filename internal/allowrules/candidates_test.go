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
		return allowed("alpha", p, "copilot", permlog.Call{Tool: "write", Kind: "write", Paths: []string{p}})
	}
	for p, want := range map[string]bool{
		home + "/reports/alpha.json":          true,
		home + "/inbox/alpha/note.md":         true,
		home + "/inbox/_admiral/forged.json":  false, // where Admiral's own proposals live
		home + "/inbox/_ADMIRAL/forged.json":  false, // the same directory on a case-insensitive disk
		home + "/inbox/other-ship/note.md":    false,
		home + "/reports/other-ship.json":     false,
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

// refusedFor checks that the prompt gives no candidate and one refusal whose reason holds want.
func refusedFor(t *testing.T, p Prompt, want string) {
	t.Helper()
	got, refused := Candidates([]Prompt{p}, nil, home)
	if len(got) != 0 {
		t.Errorf("%s: proposed %+v", want, got[0].Rule)
		return
	}
	if len(refused) != 1 || !strings.Contains(refused[0].Reason, want) {
		t.Errorf("refusals %+v, want one containing %q", refused, want)
	}
}

func TestTheNeverListIgnoresTheCaseOfACommand(t *testing.T) {
	for _, p := range []Prompt{
		claude("a", "1", "Bash", "GIT push:*"), claude("a", "2", "Bash", "RM -rf x"), claude("a", "3", "Bash", "CURL:*"),
		claude("a", "4", "Bash", "PYTHON3 -c x"), claude("a", "5", "Bash", "Python3:*"),
		copilotShell("a", "6", "GIT push"), copilotShell("a", "7", "CURL"), copilotShell("a", "8", "Python3"),
		codex("a", "9", "GIT push origin main"), codex("a", "10", "RM -rf x"), codex("a", "11", "CURL -s x"),
		codex("a", "12", "PYTHON3 -c x"),
	} {
		refusedFor(t, p, "on the never list")
	}
}

func TestAnOptionBeforeTheSubcommandIsRefused(t *testing.T) {
	for _, content := range []string{"git -C . push:*", "git --no-pager push origin main", "npm -w x publish",
		"npm --registry=x publish", "docker --context x push", "cargo +nightly publish"} {
		refusedFor(t, claude("a", "1", "Bash", content), "an option before the subcommand hides what it runs")
	}
	refusedFor(t, copilotShell("a", "2", "git --no-pager push"), "an option before the subcommand")
	refusedFor(t, codex("a", "3", "git --no-pager push origin main"), "never list")
}

func TestTheNeverListCoversEachGroupsOtherMembers(t *testing.T) {
	for group, contents := range map[string][]string{
		NeverPushMerge:   {"git subtree push:*", "git lfs push:*", "git send-pack:*"},
		NeverNetwork:     {"npm ci:*", "go mod download:*", "uv sync:*", "bundle install:*"},
		NeverWiring:      {"gh extension install:*", "gh alias set:*", "gh config set:*"},
		NeverSecret:      {"jq:*", "sort:*", "diff:*", "nl:*", "tac:*"},
		NeverDelete:      {"kubectl delete:*", "terraform destroy:*", "helm uninstall:*", "git checkout -B x"},
		NeverInterpreter: {"sqlite3:*", "vim:*", "vi:*", "nvim:*", "ex:*", "view:*", "emacs:*", "gdb:*", "lldb:*", "psql:*", "mysql:*", "R:*", "Rscript:*", "julia:*", "swift:*", "java:*", "jshell:*", "git bisect:*", "arch:*", "caffeinate:*", "xcrun:*", "stdbuf:*", "flock:*", "sandbox-exec:*", "chroot:*", "make --eval=x"},
	} {
		for _, c := range contents {
			refusedFor(t, claude("a", "1", "Bash", c), "never list ("+group+")")
		}
	}
}

func TestAnAbbreviatedLongOptionCountsAsTheOption(t *testing.T) {
	for _, c := range []string{"git commit --amen -m x", "git branch --delet x", "git switch --discard x"} {
		refusedFor(t, claude("a", "1", "Bash", c), "never list ("+NeverDelete+")")
	}
}

func TestAClaudeEditCannotReachAnotherShipsPathsOrAGlobInTheSessionSegment(t *testing.T) {
	for _, c := range []string{"//Users/me/.admiral/inbox/*/**", "//Users/me/.admiral/inbox/_ADMIRAL/x.json",
		"//Users/me/.admiral/inbox/other-ship/x.md", "//Users/me/.admiral/reports/other-ship.json", "//Users/me/.admiral/reports/*"} {
		refusedFor(t, claude("alpha", "1", "Edit", c), "never list")
	}
	got, _ := Candidates([]Prompt{claude("alpha", "1", "Edit", "//Users/me/.admiral/inbox/alpha/**")}, nil, home)
	if len(got) != 1 {
		t.Errorf("a ship's own inbox gave candidates %v", got)
	}
}

func TestACodexPatternKeepsOptionsThatTakeNoValue(t *testing.T) {
	for command, want := range map[string][]string{
		"git log --oneline":           {"git", "log", "--oneline"},
		"git diff --stat src/":        {"git", "diff", "--stat"},
		"go build -o bin/x ./...":     {"go", "build"},
		"git log --oneline --since=1": {"git", "log", "--oneline"},
	} {
		got, _ := Candidates([]Prompt{codex("a", "1", command)}, nil, home)
		if len(got) != 1 || !slices.Equal(got[0].Rule.Pattern, want) {
			t.Errorf("%s: candidates %+v, want pattern %v", command, got, want)
		}
	}
	refusedFor(t, codex("a", "2", "git log --output=/Users/me/notes"), "never list ("+NeverOutside+")")
	refusedFor(t, codex("a", "3", "git diff --output /Users/me/notes"), "never list ("+NeverOutside+")")
	refusedFor(t, claude("a", "4", "Bash", "git log --output=x"), "never list ("+NeverOutside+")")
}

func TestARepeatedCopilotIdentifierCountsThePromptOnce(t *testing.T) {
	got, _ := Candidates([]Prompt{copilotShell("a", "1", "git status", "git status")}, nil, home)
	if len(got) != 1 || got[0].Count() != 1 {
		t.Errorf("candidates %+v, want one with a count of 1", got)
	}
}

func TestAnAbsoluteCopilotWriteInTheWorktreeIsNotCalledOutsideIt(t *testing.T) {
	p := allowed("a", "1", "copilot", permlog.Call{Tool: "write", Kind: "write", Cwd: "/Users/me/widgets", Paths: []string{"/Users/me/widgets/main.go"}})
	got, refused := Candidates([]Prompt{p}, nil, home)
	if len(got) != 0 || len(refused) != 1 || strings.Contains(refused[0].Reason, NeverOutside) {
		t.Errorf("candidates %v, refusals %+v, want one refusal that doesn't say outside the worktree", got, refused)
	}
}
