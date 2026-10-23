package cli

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"
)

// The merge half of `admiral guard pretooluse` (#120). On an autopilot ship the
// guard refuses every merge but `gh pr merge --auto` with the panel's method
// (ADMIRAL_AUTO_MERGE), on a PR whose base branch GitHub confirms requires an
// approving review, and refuses writes that lower that gate. The check against
// GitHub is a seam these tests replace:
//
//	var approvalLookup func(ctx context.Context, cwd string, pr []string) (bool, error)
//
// pr holds what `gh pr view` is given to resolve the PR: its argument, if the
// command has one, then "-R" and the repository, if it names one (--repo=o/r
// becomes "-R", "o/r"). cwd is the payload's cwd. The answer is whether the base
// branch requires at least one approving review; an error means not confirmed.
// How the lookup asks GitHub is tested in e2e/guard_merge_test.go.

// The rule codes of the merge refusals, one per kind.
const (
	wantRuleGhPrMerge         = "gh_pr_merge"
	wantRuleGhAPIMerge        = "gh_api_merge"
	wantRuleGraphqlMerge      = "graphql_merge"
	wantRuleGhAPIGate         = "gh_api_gate_write"
	wantRuleGraphqlGate       = "graphql_gate_write"
	wantRuleMcpMerge          = "mcp_merge"
	wantRuleAutoMergeMethod   = "auto_merge_method"
	wantRuleAutoMergeSteered  = "auto_merge_steered"
	wantRuleAutoMergeUnproven = "auto_merge_unconfirmed"
)

// fakeLookup replaces approvalLookup for a test and records what it was asked.
type fakeLookup struct {
	ok    bool
	err   error
	calls [][]string
	cwds  []string
}

func (f *fakeLookup) use(t *testing.T) {
	t.Helper()
	old := approvalLookup
	t.Cleanup(func() { approvalLookup = old })
	approvalLookup = func(ctx context.Context, cwd string, pr []string) (bool, error) {
		f.calls = append(f.calls, slices.Clone(pr))
		f.cwds = append(f.cwds, cwd)
		return f.ok, f.err
	}
}

// runMergeGuard runs Guard on an autopilot ship whose panel merges with method
// (ADMIRAL_AUTO_MERGE unset when empty), with the lookup answering look.
func runMergeGuard(t *testing.T, c guardTestCase, method string, look *fakeLookup) *GuardDenied {
	t.Helper()
	t.Setenv("ADMIRAL_AUTO_MERGE", method)
	look.use(t)
	payload := c.payload(t)
	err, out, _ := runGuardTest(t, payload, "1")
	if out != "" {
		t.Errorf("stdout %q, want nothing: Claude reads a hook's stdout", out)
	}
	if err == nil {
		return nil
	}
	var denied *GuardDenied
	if !errors.As(err, &denied) {
		t.Fatalf("Guard returned %v, want nil or a *GuardDenied\npayload %s", err, payload)
	}
	return denied
}

// A command that is refused whatever the panel's method and whatever GitHub
// says. The lookup says the base requires approval and the method is squash, so
// only the rule can refuse it.
func TestGuardRefusesMergesOnAnAutopilotShip(t *testing.T) {
	b := func(name, cmd, want string) guardTestCase {
		return guardTestCase{name: name, tool: "Bash", input: bashInput(cmd), want: want}
	}
	mcp := func(name, tool string, input map[string]any, want string) guardTestCase {
		return guardTestCase{name: name, tool: tool, input: input, want: want}
	}
	file := func(name, cmd string, files map[string]string, want string) guardTestCase {
		return guardTestCase{name: name, tool: "Bash", input: bashInput(cmd), files: files, want: want}
	}
	const (
		mergeRoute = "repos/o/r/pulls/12/merge"
		protection = "repos/o/r/branches/main/protection"
	)
	gql := func(name, mutation, want string) guardTestCase {
		return b(name, `gh api graphql -f query='mutation { `+mutation+` }'`, want)
	}
	cases := []guardTestCase{
		// gh pr merge without --auto, or with --admin.
		b("direct merge", `gh pr merge 12`, wantRuleGhPrMerge),
		b("direct squash", `gh pr merge 12 --squash`, wantRuleGhPrMerge),
		b("direct -s", `gh pr merge 12 -s`, wantRuleGhPrMerge),
		b("direct merge commit", `gh pr merge 12 --merge`, wantRuleGhPrMerge),
		b("direct rebase", `gh pr merge 12 --rebase --delete-branch`, wantRuleGhPrMerge),
		b("direct merge of the current branch", `gh pr merge`, wantRuleGhPrMerge),
		b("a merge-queue enqueue is a direct merge", `gh pr merge 12 --squash --match-head-commit abc123`, wantRuleGhPrMerge),
		b("--auto=false is a direct merge", `gh pr merge 12 --auto=false -s`, wantRuleGhPrMerge),
		b("--admin beside --auto", `gh pr merge 12 --auto --admin -s`, wantRuleGhPrMerge),
		b("--admin before the number", `gh pr merge --admin 12 --auto -s`, wantRuleGhPrMerge),
		b("--admin=true", `gh pr merge 12 --auto -s --admin=true`, wantRuleGhPrMerge),
		b("-R before merge", `gh pr merge 12 -R o/r -s`, wantRuleGhPrMerge),
		b("-R between pr and merge", `gh pr -R o/r merge 12 -s`, wantRuleGhPrMerge),
		b("direct merge after &&", `gh pr ready 12 && gh pr merge 12 -s`, wantRuleGhPrMerge),
		b("direct merge in bash -c", `bash -c "gh pr merge 12 --squash"`, wantRuleGhPrMerge),
		b("direct merge in a substitution", `echo $(gh pr merge 12 --squash)`, wantRuleGhPrMerge),
		b("direct merge by absolute path", `/opt/homebrew/bin/gh pr merge 12 -s`, wantRuleGhPrMerge),
		b("a program held in a variable", `$GH pr merge 12 --squash`, wantRuleGhPrMerge),

		// gh api merge routes. Only a write merges: GET asks whether it merged.
		b("api PUT pulls merge", `gh api -X PUT `+mergeRoute, wantRuleGhAPIMerge),
		b("api --method PUT with a field", `gh api --method PUT `+mergeRoute+` -f merge_method=squash`, wantRuleGhAPIMerge),
		b("api attached -XPUT and a leading slash", `gh api -XPUT /`+mergeRoute, wantRuleGhAPIMerge),
		b("api full URL", `gh api -X PUT https://api.github.com/`+mergeRoute, wantRuleGhAPIMerge),
		b("api on a GHE host", `gh api --hostname ghe.example.com -X PUT `+mergeRoute, wantRuleGhAPIMerge),
		b("api query string", `gh api -X PUT "`+mergeRoute+`?x=1"`, wantRuleGhAPIMerge),
		b("api expansion in the number", `gh api -X PUT repos/o/r/pulls/$N/merge`, wantRuleGhAPIMerge),
		b("api expansion in the repository", `gh api -X PUT repos/$REPO/pulls/12/merge`, wantRuleGhAPIMerge),
		b("api method held in a variable", `gh api -X "$M" `+mergeRoute, wantRuleGhAPIMerge),
		b("api merge with a field and no -X is an implicit POST [D]", `gh api `+mergeRoute+` -f merge_method=squash`, wantRuleGhAPIMerge),
		b("api POST merges", `gh api -X POST repos/o/r/merges -f base=main -f head=topic`, wantRuleGhAPIMerge),
		b("api merges with fields and no -X", `gh api repos/o/r/merges -f base=main -f head=topic`, wantRuleGhAPIMerge),
		b("api merges with -F", `gh api repos/o/r/merges -F base=main -F head=topic`, wantRuleGhAPIMerge),
		file("api merges with --input", `gh api repos/o/r/merges --input m.json`, map[string]string{"m.json": `{"base":"main","head":"topic"}`}, wantRuleGhAPIMerge),
		b("api expansion spanning the merge segment", `gh api -X PUT repos/o/r/pulls/$N`, wantRuleGhAPIMerge),
		b("api expansion before a merge segment", `gh api -X PUT "$P/merge"`, wantRuleGhAPIMerge),
		b("api expansion spanning the protection segment", `gh api -X DELETE repos/o/r/branches/$B`, wantRuleGhAPIMerge),
		b("api merge in a pipeline", `echo '{}' | gh api -X PUT `+mergeRoute+` --input -`, wantRuleGhAPIMerge),

		// GraphQL merges.
		gql("graphql mergePullRequest", `mergePullRequest(input: {pullRequestId: "X"}) { clientMutationId }`, wantRuleGraphqlMerge),
		gql("graphql enablePullRequestAutoMerge", `enablePullRequestAutoMerge(input: {pullRequestId: "X", mergeMethod: SQUASH}) { clientMutationId }`, wantRuleGraphqlMerge),
		gql("graphql mergeBranch", `mergeBranch(input: {repositoryId: "X", base: "main", head: "topic"}) { clientMutationId }`, wantRuleGraphqlMerge),
		file("graphql mutation read from a file", `gh api graphql -F query=@q.graphql`,
			map[string]string{"q.graphql": `mutation { mergePullRequest(input: {pullRequestId: "X"}) { clientMutationId } }`}, wantRuleGraphqlMerge),
		file("graphql mutation in an --input body", `gh api graphql --input q.json`,
			map[string]string{"q.json": `{"query":"mutation($id: ID!) { enablePullRequestAutoMerge(input: {pullRequestId: $id}) { clientMutationId } }","variables":{"id":"X"}}`}, wantRuleGraphqlMerge),
		b("graphql mutation read from stdin", "gh api graphql -F query=@- <<'EOF'\nmutation { mergePullRequest(input: {pullRequestId: \"X\"}) { clientMutationId } }\nEOF", wantRuleGraphqlMerge),

		// Writes to the gate the approval check relies on (A2): a write is any
		// method but GET and HEAD, and gh's implicit POST counts.
		b("protection PUT", `gh api -X PUT `+protection+` --input p.json`, wantRuleGhAPIGate),
		b("protection DELETE", `gh api -X DELETE `+protection, wantRuleGhAPIGate),
		b("required reviews PATCH", `gh api -X PATCH `+protection+`/required_pull_request_reviews -F required_approving_review_count=0`, wantRuleGhAPIGate),
		b("required reviews DELETE", `gh api -X DELETE `+protection+`/required_pull_request_reviews`, wantRuleGhAPIGate),
		b("protection with a field and no -X", `gh api `+protection+` -f enforce_admins=false`, wantRuleGhAPIGate),
		b("protection with --raw-field and no -X", `gh api `+protection+` --raw-field enforce_admins=false`, wantRuleGhAPIGate),
		b("protection with --field and no -X", `gh api `+protection+` --field enforce_admins=false`, wantRuleGhAPIGate),
		file("protection with --input and no -X", `gh api `+protection+` --input p.json`, map[string]string{"p.json": `{}`}, wantRuleGhAPIGate),
		b("protection of a branch with a slash", `gh api -X DELETE repos/o/r/branches/release%2F1/protection`, wantRuleGhAPIGate),
		b("protection with the method in a variable", `gh api -X "$M" `+protection, wantRuleGhAPIGate),
		b("protection with an expansion in the branch", `gh api -X DELETE repos/o/r/branches/$B/protection`, wantRuleGhAPIGate),
		b("ruleset POST", `gh api -X POST repos/o/r/rulesets --input r.json`, wantRuleGhAPIGate),
		b("ruleset PUT", `gh api -X PUT repos/o/r/rulesets/5 --input r.json`, wantRuleGhAPIGate),
		b("ruleset PATCH", `gh api -X PATCH repos/o/r/rulesets/5 -f enforcement=disabled`, wantRuleGhAPIGate),
		b("ruleset DELETE", `gh api -X DELETE repos/o/r/rulesets/5`, wantRuleGhAPIGate),
		b("ruleset with a field and no -X", `gh api repos/o/r/rulesets/5 -f enforcement=disabled`, wantRuleGhAPIGate),
		b("ruleset with the method in a variable", `gh api --method "$M" repos/o/r/rulesets/5`, wantRuleGhAPIGate),
		b("org ruleset POST", `gh api -X POST orgs/acme/rulesets --input r.json`, wantRuleGhAPIGate),
		b("org ruleset PUT", `gh api -X PUT orgs/acme/rulesets/5 --input r.json`, wantRuleGhAPIGate),
		b("org ruleset DELETE", `gh api -X DELETE orgs/acme/rulesets/5`, wantRuleGhAPIGate),
		b("ruleset on a GHE host", `gh api --hostname ghe.example.com -X DELETE repos/o/r/rulesets/5`, wantRuleGhAPIGate),
		gql("graphql createBranchProtectionRule", `createBranchProtectionRule(input: {repositoryId: "X", pattern: "main"}) { clientMutationId }`, wantRuleGraphqlGate),
		gql("graphql updateBranchProtectionRule", `updateBranchProtectionRule(input: {branchProtectionRuleId: "X", requiredApprovingReviewCount: 0}) { clientMutationId }`, wantRuleGraphqlGate),
		gql("graphql deleteBranchProtectionRule", `deleteBranchProtectionRule(input: {branchProtectionRuleId: "X"}) { clientMutationId }`, wantRuleGraphqlGate),
		gql("graphql createRepositoryRuleset", `createRepositoryRuleset(input: {sourceId: "X", name: "n", enforcement: ACTIVE}) { clientMutationId }`, wantRuleGraphqlGate),
		gql("graphql updateRepositoryRuleset", `updateRepositoryRuleset(input: {repositoryRulesetId: "X", enforcement: DISABLED}) { clientMutationId }`, wantRuleGraphqlGate),
		gql("graphql deleteRepositoryRuleset", `deleteRepositoryRuleset(input: {repositoryRulesetId: "X"}) { clientMutationId }`, wantRuleGraphqlGate),

		// MCP tools that merge or enable auto-merge.
		mcp("mcp merge_pull_request", "mcp__github__merge_pull_request", map[string]any{"pullNumber": 12}, wantRuleMcpMerge),
		mcp("mcp enable_pull_request_auto_merge", "mcp__github__enable_pull_request_auto_merge", map[string]any{"pullNumber": 12}, wantRuleMcpMerge),
		mcp("mcp enable_auto_merge", "mcp__github__enable_auto_merge", map[string]any{"pullNumber": 12}, wantRuleMcpMerge),
		mcp("mcp merge_pull_request of another server", "mcp__acme_git__merge_pull_request", nil, wantRuleMcpMerge),
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			for _, method := range []string{"squash", ""} {
				look := &fakeLookup{ok: true}
				denied := runMergeGuard(t, c, method, look)
				switch {
				case denied == nil:
					t.Errorf("Guard allowed it with ADMIRAL_AUTO_MERGE=%q, want a refusal for rule %s", method, c.want)
				case denied.Rule != c.want:
					t.Errorf("with ADMIRAL_AUTO_MERGE=%q the rule = %q, want %q", method, denied.Rule, c.want)
				}
				if len(look.calls) != 0 {
					t.Errorf("the lookup ran %d time(s) for a command the rule refuses outright, want 0: it must not reach GitHub", len(look.calls))
				}
			}
		})
	}
}

// What a merge, a ready-for-review and a read of the gate are allowed to do on
// an autopilot ship, with a panel whose method is squash and a lookup that
// confirms the gate: the rules must not catch them.
func TestGuardAllowsWhatIsNotAMerge(t *testing.T) {
	b := func(name, cmd string) guardTestCase {
		return guardTestCase{name: name, tool: "Bash", input: bashInput(cmd), want: wantAllowed}
	}
	mcp := func(name, tool string) guardTestCase {
		return guardTestCase{name: name, tool: tool, input: map[string]any{"pullNumber": 12}, want: wantAllowed}
	}
	cases := []guardTestCase{
		b("C5 gh pr ready", `gh pr ready`),
		b("C5 gh pr ready with a number", `gh pr ready 12`),
		b("C5 gh pr ready with -R", `gh pr ready 12 -R o/r`),
		b("C5 gh pr ready after &&", `gh pr checks 12 && gh pr ready 12`),
		b("gh pr merge --disable-auto", `gh pr merge 12 --disable-auto`),
		b("gh pr merge --disable-auto of the current branch", `gh pr merge --disable-auto`),
		b("gh pr view", `gh pr view 12 --json state`),
		b("gh pr create --draft", `gh pr create --draft --title x --body y`),
		b("gh pr checks", `gh pr checks 12`),
		b("gh pr comment", `gh pr comment 12 -b "merge this"`),
		b("gh pr list with a merged filter", `gh pr list --state merged`),
		b("git merge is not a gh merge", `git merge main`),
		b("the word merge in an echo", `echo gh pr merge 12`),
		b("api merged check (GET)", `gh api repos/o/r/pulls/12/merge`),
		b("api merged check with -X GET", `gh api -X GET repos/o/r/pulls/12/merge`),
		b("api comment on a pull request", `gh api -X POST repos/o/r/issues/12/comments -f body=x`),
		b("api PATCH a pull request title", `gh api -X PATCH repos/o/r/pulls/12 -f title=x`),
		b("api update-branch", `gh api -X PUT repos/o/r/pulls/12/update-branch`),
		b("api read the rules of a branch", `gh api repos/o/r/rules/branches/main`),
		b("api read protection", `gh api repos/o/r/branches/main/protection`),
		b("api read protection with -X GET", `gh api -X GET repos/o/r/branches/main/protection`),
		b("api read protection with HEAD", `gh api -X HEAD repos/o/r/branches/main/protection`),
		b("api list rulesets", `gh api repos/o/r/rulesets`),
		b("api read a ruleset with -X GET", `gh api -X GET repos/o/r/rulesets/5`),
		b("api list org rulesets", `gh api orgs/acme/rulesets`),
		b("api read a branch", `gh api repos/o/r/branches/main`),
		b("graphql disablePullRequestAutoMerge", `gh api graphql -f query='mutation { disablePullRequestAutoMerge(input: {pullRequestId: "X"}) { clientMutationId } }'`),
		b("graphql query of protection and rulesets", `gh api graphql -f query='query { repository(owner: "o", name: "r") { branchProtectionRules(first: 5) { nodes { requiresApprovingReviews } } rulesets(first: 5) { nodes { name } } } }'`),
		mcp("mcp list_merged_pull_requests", "mcp__github__list_merged_pull_requests"),
		mcp("mcp get_pull_request", "mcp__github__get_pull_request"),
		mcp("mcp list_pull_requests", "mcp__github__list_pull_requests"),
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			look := &fakeLookup{ok: true}
			if denied := runMergeGuard(t, c, "squash", look); denied != nil {
				t.Errorf("Guard refused with rule %s, want it allowed", denied.Rule)
			}
		})
	}
	// gh pr ready and --disable-auto need no panel method at all.
	for _, cmd := range []string{`gh pr ready 12`, `gh pr merge 12 --disable-auto`} {
		look := &fakeLookup{err: errors.New("no network")}
		if denied := runMergeGuard(t, guardTestCase{tool: "Bash", input: bashInput(cmd)}, "", look); denied != nil {
			t.Errorf("%q with no ADMIRAL_AUTO_MERGE: refused with rule %s, want it allowed", cmd, denied.Rule)
		}
		if len(look.calls) != 0 {
			t.Errorf("%q asked GitHub, want no lookup", cmd)
		}
	}
}

// Off the autopilot, none of the merge rules applies.
func TestGuardMergeRulesAreOffOutsideAutopilot(t *testing.T) {
	for _, cmd := range []string{`gh pr merge 12 --squash --admin`, `gh api -X PUT repos/o/r/pulls/12/merge`,
		`gh api -X DELETE repos/o/r/rulesets/5`, `gh pr merge 12 --auto -s`} {
		look := &fakeLookup{err: errors.New("no network")}
		look.use(t)
		c := guardTestCase{tool: "Bash", input: bashInput(cmd)}
		if err, out, errb := runGuardTest(t, c.payload(t), ""); err != nil || out != "" || errb != "" {
			t.Errorf("%q: Guard = %v, stdout %q, stderr %q, want nil and no output", cmd, err, out, errb)
		}
		if len(look.calls) != 0 {
			t.Errorf("%q asked GitHub off the autopilot", cmd)
		}
	}
	look := &fakeLookup{ok: true}
	look.use(t)
	c := guardTestCase{tool: "mcp__github__merge_pull_request", input: map[string]any{"pullNumber": 12}}
	if err, _, _ := runGuardTest(t, c.payload(t), "0"); err != nil {
		t.Errorf("an MCP merge with ADMIRAL_AUTOPILOT=0: Guard = %v, want nil", err)
	}
}

// gh pr merge --auto (C7): allowed only with the panel's method, nothing steering
// it to another PR or repository, and a base branch GitHub confirms requires an
// approving review.
func TestGuardAutoMerge(t *testing.T) {
	type row struct {
		name, cmd, method string
		look              fakeLookup
		want              string
	}
	yes, no := fakeLookup{ok: true}, fakeLookup{}
	failed := fakeLookup{err: errors.New("gh: HTTP 403")}
	failedYes := fakeLookup{ok: true, err: errors.New("timed out")}
	rows := []row{
		// Allowed: the method named is the panel's, GitHub confirms.
		{"C7 squash", `gh pr merge 12 --auto --squash`, "squash", yes, wantAllowed},
		{"C7 squash short", `gh pr merge 12 --auto -s`, "squash", yes, wantAllowed},
		{"C7 flags in the other order", `gh pr merge 12 --squash --auto`, "squash", yes, wantAllowed},
		{"C7 merge commit", `gh pr merge 12 --auto --merge`, "merge", yes, wantAllowed},
		{"C7 merge commit short", `gh pr merge 12 --auto -m`, "merge", yes, wantAllowed},
		{"C7 rebase", `gh pr merge 12 --auto --rebase`, "rebase", yes, wantAllowed},
		{"C7 rebase short", `gh pr merge 12 --auto -r`, "rebase", yes, wantAllowed},
		{"C7 the current branch (no PR argument)", `gh pr merge --auto -s`, "squash", yes, wantAllowed},
		{"C7 a branch name", `gh pr merge topic --auto -s`, "squash", yes, wantAllowed},
		{"C7 a URL", `gh pr merge https://github.com/o/r/pull/12 --auto -s`, "squash", yes, wantAllowed},
		{"C7 with -R", `gh pr merge 12 --auto -s -R o/r`, "squash", yes, wantAllowed},
		{"C7 with --repo", `gh pr merge 12 --auto -s --repo o/r`, "squash", yes, wantAllowed},
		{"C7 --match-head-commit is harmless", `gh pr merge 12 --auto -s --match-head-commit abc123`, "squash", yes, wantAllowed},
		{"C7 --delete-branch is harmless", `gh pr merge 12 --auto -s --delete-branch`, "squash", yes, wantAllowed},
		{"C7 a body that looks like a flag", `gh pr merge 12 --auto -s -b "--rebase -m"`, "squash", yes, wantAllowed},
		{"C7 after ready", `gh pr ready 12 && gh pr merge 12 --auto -s`, "squash", yes, wantAllowed},
		{"C7 a later cd does not steer it", `gh pr merge 12 --auto -s && cd /tmp`, "squash", yes, wantAllowed},
		{"a direct merge after a good auto-merge", `gh pr merge 12 --auto -s; gh pr merge 13 --squash`, "squash", yes, wantRuleGhPrMerge},
		{"C7 a git command that does not steer", `git status && gh pr merge 12 --auto -s`, "squash", yes, wantAllowed},

		// Refused: the method.
		{"no method in the environment", `gh pr merge 12 --auto -s`, "", yes, wantRuleAutoMergeMethod},
		{"an invalid method in the environment", `gh pr merge 12 --auto -s`, "ff", yes, wantRuleAutoMergeMethod},
		{"a method of another case", `gh pr merge 12 --auto -s`, "SQUASH", yes, wantRuleAutoMergeMethod},
		{"no method flag", `gh pr merge 12 --auto`, "squash", yes, wantRuleAutoMergeMethod},
		{"no method flag, other flags", `gh pr merge 12 --auto --delete-branch`, "squash", yes, wantRuleAutoMergeMethod},
		{"another method (long)", `gh pr merge 12 --auto --rebase`, "squash", yes, wantRuleAutoMergeMethod},
		{"another method (short -m)", `gh pr merge 12 --auto -m`, "squash", yes, wantRuleAutoMergeMethod},
		{"another method (short -r)", `gh pr merge 12 --auto -r`, "squash", yes, wantRuleAutoMergeMethod},
		{"two methods", `gh pr merge 12 --auto --squash --rebase`, "squash", yes, wantRuleAutoMergeMethod},
		{"the method flag only in a body", `gh pr merge 12 --auto -b "-s"`, "squash", yes, wantRuleAutoMergeMethod},

		// Refused: something steers it to another PR or repository.
		{"inline GH_REPO", `GH_REPO=x/y gh pr merge 12 --auto -s`, "squash", yes, wantRuleAutoMergeSteered},
		{"inline GH_HOST", `GH_HOST=ghe.example.com gh pr merge 12 --auto -s`, "squash", yes, wantRuleAutoMergeSteered},
		{"GH_REPO through env", `env GH_REPO=x/y gh pr merge 12 --auto -s`, "squash", yes, wantRuleAutoMergeSteered},
		{"GH_REPO exported earlier [D]", `export GH_REPO=x/y; gh pr merge 12 --auto -s`, "squash", yes, wantRuleAutoMergeSteered},
		{"cd first", `cd /other/checkout && gh pr merge --auto -s`, "squash", yes, wantRuleAutoMergeSteered},
		{"cd on the line before", "cd /other/checkout\ngh pr merge --auto -s", "squash", yes, wantRuleAutoMergeSteered},
		{"git checkout first", `git checkout other && gh pr merge --auto -s`, "squash", yes, wantRuleAutoMergeSteered},
		{"git switch first", `git switch other && gh pr merge --auto -s`, "squash", yes, wantRuleAutoMergeSteered},
		{"git remote first", `git remote set-url origin https://github.com/x/y && gh pr merge --auto -s`, "squash", yes, wantRuleAutoMergeSteered},
		{"an expansion as the PR", `gh pr merge $PR --auto -s`, "squash", yes, wantRuleAutoMergeSteered},
		{"a substitution as the PR", `gh pr merge "$(gh pr list --json number -q '.[0].number')" --auto -s`, "squash", yes, wantRuleAutoMergeSteered},
		{"an expansion inside the PR", `gh pr merge "topic-$N" --auto -s`, "squash", yes, wantRuleAutoMergeSteered},
		{"an expansion as -R", `gh pr merge 12 --auto -s -R "$REPO"`, "squash", yes, wantRuleAutoMergeSteered},
		{"an expansion as --repo=", `gh pr merge 12 --auto -s --repo=$REPO`, "squash", yes, wantRuleAutoMergeSteered},
		{"env -C", `env -C /other/checkout gh pr merge --auto -s`, "squash", yes, wantRuleAutoMergeSteered},
		{"sudo --chdir", `sudo --chdir=/other/checkout gh pr merge --auto -s`, "squash", yes, wantRuleAutoMergeSteered},
		{"env -u GH_REPO", `env -u GH_REPO gh pr merge 12 --auto -s`, "squash", yes, wantRuleAutoMergeSteered},
		{"inline GIT_DIR", `GIT_DIR=/other/.git gh pr merge --auto -s`, "squash", yes, wantRuleAutoMergeSteered},
		{"xargs adds the PR", `echo 12 | xargs gh pr merge --auto -s`, "squash", yes, wantRuleAutoMergeSteered},
		{"arguments held in a variable", `gh pr merge $ARGS --auto -s`, "squash", yes, wantRuleAutoMergeSteered},

		// Refused: GitHub does not confirm that the base requires a review.
		{"the base does not require a review", `gh pr merge 12 --auto -s`, "squash", no, wantRuleAutoMergeUnproven},
		{"the lookup failed", `gh pr merge 12 --auto -s`, "squash", failed, wantRuleAutoMergeUnproven},
		{"the lookup failed after an answer", `gh pr merge 12 --auto -s`, "squash", failedYes, wantRuleAutoMergeUnproven},
		{"the lookup failed (current branch)", `gh pr merge --auto -s`, "squash", failed, wantRuleAutoMergeUnproven},
	}
	for _, r := range rows {
		t.Run(r.name, func(t *testing.T) {
			look := r.look
			denied := runMergeGuard(t, guardTestCase{tool: "Bash", input: bashInput(r.cmd)}, r.method, &look)
			switch {
			case r.want == wantAllowed && denied != nil:
				t.Errorf("Guard refused with rule %s, want it allowed", denied.Rule)
			case r.want != wantAllowed && denied == nil:
				t.Errorf("Guard allowed it, want a refusal for rule %s", r.want)
			case r.want != wantAllowed && denied.Rule != r.want:
				t.Errorf("rule = %q, want %q", denied.Rule, r.want)
			}
			if r.want == wantAllowed && len(look.calls) != 1 {
				t.Errorf("the lookup ran %d time(s), want 1", len(look.calls))
			}
			if r.want == wantRuleAutoMergeMethod || r.want == wantRuleAutoMergeSteered {
				if len(look.calls) != 0 {
					t.Errorf("the lookup ran for a command refused before it, want none: it must not resolve a PR the command steers elsewhere")
				}
			}
		})
	}
}

// The lookup gets what gh pr view needs to find the same PR the merge names, in
// the payload's cwd, and a deadline that fits in the hook's 10 s.
func TestGuardAutoMergeLookupArguments(t *testing.T) {
	rows := []struct {
		name, cmd string
		want      []string
	}{
		{"a PR number", `gh pr merge 12 --auto -s`, []string{"12"}},
		{"no PR argument", `gh pr merge --auto -s`, nil},
		{"a branch", `gh pr merge topic --auto -s`, []string{"topic"}},
		{"a URL", `gh pr merge https://github.com/o/r/pull/12 --auto -s`, []string{"https://github.com/o/r/pull/12"}},
		{"-R", `gh pr merge 12 --auto -s -R o/r`, []string{"12", "-R", "o/r"}},
		{"--repo with a space", `gh pr merge 12 --auto -s --repo o/r`, []string{"12", "-R", "o/r"}},
		{"--repo=", `gh pr merge 12 --auto -s --repo=o/r`, []string{"12", "-R", "o/r"}},
		{"-R attached before merge", `gh pr -Racme/other merge 12 --auto -s`, []string{"12", "-R", "acme/other"}},
		{"-R and no PR argument", `gh pr merge -R o/r --auto -s`, []string{"-R", "o/r"}},
		{"flags before the PR", `gh pr merge --auto -s --delete-branch 12`, []string{"12"}},
		{"a body is not a PR", `gh pr merge 12 --auto -s -b "some text"`, []string{"12"}},
	}
	for _, r := range rows {
		t.Run(r.name, func(t *testing.T) {
			t.Setenv("ADMIRAL_AUTO_MERGE", "squash")
			var calls [][]string
			var cwd string
			var deadline time.Time
			old := approvalLookup
			t.Cleanup(func() { approvalLookup = old })
			approvalLookup = func(ctx context.Context, dir string, pr []string) (bool, error) {
				calls, cwd = append(calls, slices.Clone(pr)), dir
				deadline, _ = ctx.Deadline()
				return true, nil
			}
			start := time.Now()
			dir := t.TempDir()
			payload, _ := json.Marshal(map[string]any{"hook_event_name": "PreToolUse", "tool_name": "Bash",
				"tool_input": map[string]any{"command": r.cmd}, "cwd": dir})
			if err, _, _ := runGuardTest(t, string(payload), "1"); err != nil {
				t.Fatalf("Guard refused: %v, want it allowed", err)
			}
			if len(calls) != 1 || !slices.Equal(calls[0], r.want) && (len(calls[0]) > 0 || len(r.want) > 0) {
				t.Errorf("the lookup was given %q, want one call with %q", calls, r.want)
			}
			if cwd != dir {
				t.Errorf("the lookup ran in %q, want the payload's cwd %q", cwd, dir)
			}
			if deadline.IsZero() || deadline.Sub(start) > 8*time.Second {
				t.Errorf("the lookup's deadline is %v after the start (zero means none), want one inside 8 s: the hook times out at 10 s and a timed-out hook must not be what refuses", deadline.Sub(start))
			}
		})
	}
}

// The merge refusals say why in words of their own and keep the review reason
// for reviews. Neither repeats the command.
func TestGuardMergeReason(t *testing.T) {
	c := guardTestCase{tool: "Bash", input: bashInput("gh pr merge 12 --squash --body " + "xyzzy-distinct-text")}
	look := &fakeLookup{ok: true}
	t.Setenv("ADMIRAL_AUTO_MERGE", "squash")
	look.use(t)
	err, out, errb := runGuardTest(t, c.payload(t), "1")
	var denied *GuardDenied
	if !errors.As(err, &denied) || denied.Rule != wantRuleGhPrMerge {
		t.Fatalf("Guard = %v, want a *GuardDenied for rule %s", err, wantRuleGhPrMerge)
	}
	if denied.Reason == "" || denied.Reason == wantGuardReason || !strings.Contains(strings.ToLower(denied.Reason), "merge") {
		t.Errorf("Reason = %q, want a reason about merging, not the review one", denied.Reason)
	}
	if strings.TrimSuffix(errb, "\n") != denied.Reason || out != "" {
		t.Errorf("stderr %q, stdout %q, want the reason alone on stderr", errb, out)
	}
	if strings.Contains(errb, "xyzzy") {
		t.Errorf("the refusal repeats the command: %q", errb)
	}
	// The merge rules give the merge reason; a review rule keeps the review one.
	for cmd, wantReason := range map[string]string{
		`gh api -X PUT repos/o/r/pulls/12/merge`:             denied.Reason,
		`gh pr merge 12 --auto`:                              denied.Reason,
		`gh pr review 12 --approve`:                          wantGuardReason,
		`gh api repos/o/r/pulls/12/reviews -f event=APPROVE`: wantGuardReason,
	} {
		err, _, _ := runGuardTest(t, guardTestCase{tool: "Bash", input: bashInput(cmd)}.payload(t), "1")
		if !errors.As(err, &denied) || denied.Reason != wantReason {
			t.Errorf("%q: refused with %v, want the reason %q", cmd, err, wantReason)
		}
	}
	// A write to the gate gets some reason of its own, not the review one [D].
	err, _, _ = runGuardTest(t, guardTestCase{tool: "Bash", input: bashInput(`gh api -X DELETE repos/o/r/rulesets/5`)}.payload(t), "1")
	if !errors.As(err, &denied) || denied.Reason == "" || denied.Reason == wantGuardReason {
		t.Errorf("a gate write was refused with %v, want a reason that is not the review one", err)
	}
}
