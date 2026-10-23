package e2e

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// `admiral guard pretooluse` and `gh pr merge --auto` (#120): the built binary
// with the fake gh. The rules are tested in internal/cli with the lookup
// replaced; here the lookup is real, so what is tested is how it asks GitHub and
// how it reads the answer: gh pr view for the PR, then the branch's rules, then
// its classic protection, each as a GET.

// requireMergeRefusal wants exit 2 and a reason that is about merging: not the
// review reason, and not empty.
func (r guardResult) requireMergeRefusal(t *testing.T) {
	t.Helper()
	reason := strings.TrimSuffix(r.stderr, "\n")
	if r.code != 2 || r.stdout != "" || reason == "" || reason == guardReason || !strings.Contains(strings.ToLower(reason), "merge") {
		t.Errorf("exit %d, stdout %q, stderr %q, want exit 2 and a reason about merging on stderr alone", r.code, r.stdout, r.stderr)
	}
}

// restRaw canns a REST GET of the fake gh with a body that need not be JSON: status 0 is 200.
func (g *fakeGH) restRaw(key, endpoint string, status int, body string) {
	g.t.Helper()
	if err := os.MkdirAll(filepath.Join(g.dir, "rest"), 0o755); err != nil {
		g.t.Fatal(err)
	}
	g.write("rest/"+key+".endpoint", endpoint)
	if status != 0 {
		g.write("rest/"+key+".status", itoa(status))
	}
	g.write("rest/"+key+".json", body)
}

// argvLines are the argv lines (arguments joined by a space) of every call the
// fake recorded, in no particular order, less the --version probes.
func (g *fakeGH) argvLines() (lines []string) {
	g.t.Helper()
	dirs, _ := filepath.Glob(filepath.Join(g.dir, "calls", "call.*"))
	for _, d := range dirs {
		b, err := os.ReadFile(filepath.Join(d, "argv"))
		if err != nil {
			g.t.Fatal(err)
		}
		lines = append(lines, strings.Join(strings.Split(strings.TrimSuffix(string(b), "\x00"), "\x00"), " "))
	}
	return lines
}

const (
	prViewWidgets = `{"url":"https://github.com/acme/widgets/pull/12","baseRefName":"main"}`
	rulesEndpoint = "repos/acme/widgets/rules/branches/main"
	rsEndpoint    = "repos/acme/widgets/rulesets/7"
	protEndpoint  = "repos/acme/widgets/branches/main/protection"
	mergeCmd      = "gh pr merge " + prURL + " --auto --squash"
)

// hasCall says whether one recorded call holds every one of the parts.
func hasCall(calls []string, parts ...string) bool {
	for _, c := range calls {
		all := true
		for _, p := range parts {
			all = all && strings.Contains(c, p)
		}
		if all {
			return true
		}
	}
	return false
}

// pullRequestRule is one rule of rules/branches/main as GitHub prints it: its
// approvals, and whether it dismisses stale reviews on a push or requires
// approval of the last push.
func pullRequestRule(count string, dismiss, lastPush bool) string {
	return `{"type":"pull_request","ruleset_source_type":"Repository","ruleset_source":"acme/widgets","ruleset_id":7,` +
		`"parameters":{"required_approving_review_count":` + count + `,"dismiss_stale_reviews_on_push":` + strconv.FormatBool(dismiss) +
		`,"require_last_push_approval":` + strconv.FormatBool(lastPush) + `}}`
}

func rulesJSON(rules ...string) string { return "[" + strings.Join(rules, ",") + "]" }

// rulesetJSON is GET repos/acme/widgets/rulesets/7.
func rulesetJSON(enforcement, canBypass string) string {
	return `{"id":7,"enforcement":"` + enforcement + `","current_user_can_bypass":"` + canBypass + `"}`
}

// protectionJSON is classic protection of main.
func protectionJSON(count string, dismiss, enforceAdmins bool, bypass string) string {
	return `{"url":"x","enforce_admins":{"enabled":` + strconv.FormatBool(enforceAdmins) + `},"required_pull_request_reviews":` +
		`{"required_approving_review_count":` + count + `,"dismiss_stale_reviews":` + strconv.FormatBool(dismiss) +
		`,"bypass_pull_request_allowances":` + bypass + `}}`
}

// The lookup proves GitHub is the gate (D8, D9): the rules of the base branch
// first, one pull_request rule with an approving review that goes stale on a push
// (or needs the last push approved) in a ruleset the token's user can never
// bypass; then classic protection with the same, enforced on admins; anything
// else, or any failure, refuses.
func TestGuardAutoMergeLookupAgainstGitHub(t *testing.T) {
	type answer struct {
		status int
		body   string
	}
	missing := answer{404, `{"message":"Not Found","status":"404"}`}
	forbidden := answer{403, `{"message":"Forbidden"}`}
	never := answer{0, rulesetJSON("active", "never")}
	fresh := pullRequestRule("1", true, false)
	fullProt := answer{0, protectionJSON("1", true, true, `{"users":[],"teams":[],"apps":[]}`)}
	rows := []struct {
		name         string
		rules, rs    answer
		prot         answer
		prView       string // "" is prViewWidgets
		prViewFail   string
		prViewBroken bool // no base to ask about: the lookup stops after gh pr view
		wantAllowed  bool
		wantRSRead   bool // the ruleset was read
		wantProtRead bool // the classic protection was asked for
	}{
		{name: "one approval, stale reviews dismissed, never bypassed", rules: answer{0, rulesJSON(fresh)}, rs: never, prot: missing, wantAllowed: true, wantRSRead: true},
		{name: "two approvals", rules: answer{0, rulesJSON(pullRequestRule("2", true, false))}, rs: never, prot: missing, wantAllowed: true, wantRSRead: true},
		{name: "last-push approval instead of dismissal", rules: answer{0, rulesJSON(pullRequestRule("1", false, true))}, rs: never, prot: missing, wantAllowed: true, wantRSRead: true},
		{name: "the rule that qualifies counts among several", rules: answer{0, rulesJSON(pullRequestRule("0", true, true), pullRequestRule("1", false, false), fresh)}, rs: never, prot: missing, wantAllowed: true, wantRSRead: true},
		{name: "no freshness setting", rules: answer{0, rulesJSON(pullRequestRule("1", false, false))}, rs: never, prot: missing, wantProtRead: true},
		{name: "no approvals required", rules: answer{0, rulesJSON(pullRequestRule("0", true, true))}, rs: never, prot: missing, wantProtRead: true},
		{name: "a ruleset the user can bypass", rules: answer{0, rulesJSON(fresh)}, rs: answer{0, rulesetJSON("active", "always")}, prot: missing, wantRSRead: true, wantProtRead: true},
		{name: "a ruleset the user can bypass with a pull request", rules: answer{0, rulesJSON(fresh)}, rs: answer{0, rulesetJSON("active", "pull_request_only")}, prot: missing, wantRSRead: true, wantProtRead: true},
		{name: "a ruleset that is not active", rules: answer{0, rulesJSON(fresh)}, rs: answer{0, rulesetJSON("evaluate", "never")}, prot: missing, wantRSRead: true, wantProtRead: true},
		{name: "a ruleset with no bypass field", rules: answer{0, rulesJSON(fresh)}, rs: answer{0, `{"enforcement":"active"}`}, prot: missing, wantRSRead: true, wantProtRead: true},
		{name: "the ruleset read fails", rules: answer{0, rulesJSON(fresh)}, rs: missing, prot: missing, wantRSRead: true, wantProtRead: true},
		{name: "the ruleset read is not JSON", rules: answer{0, rulesJSON(fresh)}, rs: answer{0, `<html>`}, prot: missing, wantRSRead: true, wantProtRead: true},
		{name: "a rule with no ruleset id", rules: answer{0, `[{"type":"pull_request","ruleset_source_type":"Repository","parameters":{"required_approving_review_count":1,"dismiss_stale_reviews_on_push":true}}]`}, rs: never, prot: missing, wantProtRead: true},
		{name: "no rules, protection qualifies", rules: answer{0, `[]`}, prot: fullProt, wantAllowed: true, wantProtRead: true},
		{name: "rules fall short, protection qualifies", rules: answer{0, rulesJSON(pullRequestRule("1", false, false))}, rs: never, prot: fullProt, wantAllowed: true, wantProtRead: true},
		{name: "no rules and no protection", rules: answer{0, `[]`}, prot: missing, wantProtRead: true},
		{name: "protection without freshness", rules: answer{0, `[]`}, prot: answer{0, protectionJSON("1", false, true, `{}`)}, wantProtRead: true},
		{name: "protection that does not enforce admins", rules: answer{0, `[]`}, prot: answer{0, protectionJSON("1", true, false, `{}`)}, wantProtRead: true},
		{name: "protection with a bypass allowance", rules: answer{0, `[]`}, prot: answer{0, protectionJSON("1", true, true, `{"users":[{"login":"x"}],"teams":[],"apps":[]}`)}, wantProtRead: true},
		{name: "protection requires no approval", rules: answer{0, `[]`}, prot: answer{0, protectionJSON("0", true, true, `{}`)}, wantProtRead: true},
		{name: "protection without required reviews", rules: answer{0, `[]`}, prot: answer{0, `{"url":"x","enforce_admins":{"enabled":true}}`}, wantProtRead: true},
		{name: "rules forbidden, no protection", rules: forbidden, prot: missing, wantProtRead: true},
		{name: "rules forbidden, protection forbidden", rules: forbidden, prot: forbidden, wantProtRead: true},
		{name: "rules that are not JSON, no protection", rules: answer{0, `<html>`}, prot: missing, wantProtRead: true},
		{name: "rules that are not a list, no protection", rules: answer{0, `{"message":"x"}`}, prot: missing, wantProtRead: true},
		{name: "protection that is not JSON", rules: answer{0, `[]`}, prot: answer{0, `<html>`}, wantProtRead: true},
		{name: "the PR cannot be resolved", rules: answer{0, rulesJSON(fresh)}, rs: never, prot: missing, prViewFail: "no pull requests found", prViewBroken: true},
		{name: "the PR answer is not JSON", rules: answer{0, rulesJSON(fresh)}, rs: never, prot: missing, prView: `no`, prViewBroken: true},
		{name: "the PR answer has no base", rules: answer{0, rulesJSON(fresh)}, rs: never, prot: missing, prView: `{"url":"https://github.com/acme/widgets/pull/12"}`, prViewBroken: true},
	}
	for _, r := range rows {
		t.Run(r.name, func(t *testing.T) {
			w := newWorld(t)
			g := w.newFakeGH()
			switch {
			case r.prViewFail != "":
				g.write("pr_view.fail", r.prViewFail+"\n")
			case r.prView != "":
				g.write("pr_view.json", r.prView)
			default:
				g.write("pr_view.json", prViewWidgets)
			}
			g.restRaw("rules", rulesEndpoint, r.rules.status, r.rules.body)
			g.restRaw("ruleset", rsEndpoint, r.rs.status, r.rs.body)
			g.restRaw("protection", protEndpoint, r.prot.status, r.prot.body)

			res := w.runGuardCmd(guardPayload(mergeCmd), shipEnv(t.TempDir(), "saltpaw-ii", "ADMIRAL_AUTOPILOT=1", "ADMIRAL_AUTO_MERGE=squash")...)
			if r.wantAllowed {
				res.requireQuiet(t)
			} else {
				res.requireMergeRefusal(t)
			}
			calls := g.argvLines()
			if !hasCall(calls, "pr view", prURL, "--json url,baseRefName") {
				t.Errorf("the lookup never ran gh pr view %s --json url,baseRefName; calls:\n%s", prURL, strings.Join(calls, "\n"))
			}
			if !r.prViewBroken {
				if !hasCall(calls, "api", "-X GET", rulesEndpoint) {
					t.Errorf("the lookup never GET %s; calls:\n%s", rulesEndpoint, strings.Join(calls, "\n"))
				}
				if got := hasCall(calls, "api", "-X GET", rsEndpoint); got != r.wantRSRead {
					t.Errorf("the lookup read the ruleset = %v, want %v; calls:\n%s", got, r.wantRSRead, strings.Join(calls, "\n"))
				}
				if got := hasCall(calls, "api", "-X GET", protEndpoint); got != r.wantProtRead {
					t.Errorf("the lookup asked for the classic protection = %v, want %v; calls:\n%s", got, r.wantProtRead, strings.Join(calls, "\n"))
				}
			}
			for _, c := range g.argvLines() {
				if strings.Contains(c, "api") && (strings.Contains(c, " -f ") || strings.Contains(c, " -F ") || strings.Contains(c, "--input")) {
					t.Errorf("the lookup sent a body: %q, want plain GETs", c)
				}
				if strings.Contains(c, "api") && strings.Contains(c, "-X") && !strings.Contains(c, "-X GET") {
					t.Errorf("the lookup used a method but GET: %q", c)
				}
			}
		})
	}
}

// The owner, repo and base come from what gh pr view returns for the URL the
// command names, and the REST calls carry them in the endpoint.
func TestGuardAutoMergeLookupUsesTheResolvedPR(t *testing.T) {
	w := newWorld(t)
	g := w.newFakeGH()
	const ghe = "https://ghe.example.com/acme/widgets/pull/12"
	g.write("pr_view.json", `{"url":"`+ghe+`","baseRefName":"release/1"}`)
	g.restRaw("rules", "repos/acme/widgets/rules/branches/release/1", 0, rulesJSON(pullRequestRule("1", true, false)))
	g.restRaw("ruleset", rsEndpoint, 0, rulesetJSON("active", "never"))
	res := w.runGuardCmd(guardPayload("gh pr merge "+ghe+" --auto --squash"), shipEnv(t.TempDir(), "saltpaw-ii", "ADMIRAL_AUTOPILOT=1", "ADMIRAL_AUTO_MERGE=squash")...)
	res.requireQuiet(t)
	calls := g.argvLines()
	if !hasCall(calls, "pr view", ghe, "--json url,baseRefName") {
		t.Errorf("gh pr view was not given the PR's URL; calls:\n%s", strings.Join(calls, "\n"))
	}
	if !hasCall(calls, "api", "-X GET", "--hostname ghe.example.com", "repos/acme/widgets/rules/branches/release/1") {
		t.Errorf("the rules of the resolved base were not read on the PR's host; calls:\n%s", strings.Join(calls, "\n"))
	}
	if !hasCall(calls, "api", "-X GET", "--hostname ghe.example.com", rsEndpoint) {
		t.Errorf("the ruleset was not read on the PR's host; calls:\n%s", strings.Join(calls, "\n"))
	}
	for _, c := range calls {
		if strings.HasPrefix(c, "api") && strings.Contains(c, "-R") {
			t.Errorf("the REST call %q carries -R, want the owner and repo in the endpoint", c)
		}
	}
}

// A lookup that hangs is cut off inside the hook's 10 s, and the call is refused
// rather than waited for: Claude Code would otherwise end the hook with
// onFailure block only after the full ten seconds, with the gh left running.
func TestGuardAutoMergeLookupTimesOut(t *testing.T) {
	w := newWorld(t)
	g := w.newFakeGH()
	g.write("pr_view.json", prViewWidgets)
	g.write("pr_view.sleep", "30\n")
	g.restRaw("rules", rulesEndpoint, 0, rulesJSON(pullRequestRule("1", true, false)))
	start := time.Now()
	res := w.runGuardCmd(guardPayload(mergeCmd), shipEnv(t.TempDir(), "saltpaw-ii", "ADMIRAL_AUTOPILOT=1", "ADMIRAL_AUTO_MERGE=squash")...)
	took := time.Since(start)
	res.requireMergeRefusal(t)
	if took > 9*time.Second {
		t.Errorf("the guard took %v on a hung gh, want it to refuse inside the hook's 10 s timeout (about 7 s)", took)
	}
}

// The guard end to end: the refusals the rules make without GitHub exit 2 with
// the merge reason and leave a guard_denied line with the rule; the allowed
// ones are silent. gh pr ready needs nothing from GitHub.
func TestGuardCmdMergeRefusalsAndAllowances(t *testing.T) {
	w := newWorld(t)
	g := w.newFakeGH()
	g.write("pr_view.json", prViewWidgets)
	g.restRaw("rules", rulesEndpoint, 0, rulesJSON(pullRequestRule("1", true, false)))
	g.restRaw("ruleset", rsEndpoint, 0, rulesetJSON("active", "never"))
	home := t.TempDir()
	env := shipEnv(home, "saltpaw-ii", "ADMIRAL_AUTOPILOT=1", "ADMIRAL_AUTO_MERGE=squash")

	for _, cmd := range []string{"gh pr ready 12", "gh pr merge 12 --disable-auto", mergeCmd} {
		w.runGuardCmd(guardPayload(cmd), env...).requireQuiet(t)
	}
	for cmd, rule := range map[string]string{
		"gh pr merge 12 --squash":                         "gh_pr_merge",
		"gh api -X PUT repos/acme/widgets/pulls/12/merge": "gh_api_merge",
		"gh pr merge " + prURL + " --auto --rebase":       "auto_merge_method",
		"echo 'gh pr merge 5 --admin' | bash":             "gh_pr_merge",
		"gh pr merge 12 --auto --squash":                  "auto_merge_steered",
	} {
		w.runGuardCmd(guardPayload(cmd), env...).requireMergeRefusal(t)
		found := false
		for _, l := range outcomeLines(t, home) {
			found = found || strings.Contains(l, `"rule":"`+rule+`"`)
		}
		if !found {
			t.Errorf("%q: the outcome log holds no guard_denied line with rule %s:\n%s", cmd, rule, strings.Join(outcomeLines(t, home), "\n"))
		}
	}
	// A write to the gate: refused with a reason that is not the review one [D].
	gate := w.runGuardCmd(guardPayload("gh api -X DELETE repos/acme/widgets/rulesets/5"), env...)
	if reason := strings.TrimSuffix(gate.stderr, "\n"); gate.code != 2 || reason == "" || reason == guardReason {
		t.Errorf("a ruleset delete: exit %d, stderr %q, want exit 2 and a reason that is not the review one", gate.code, gate.stderr)
	}
	// A merge tool of the GitHub MCP server.
	mcp := `{"hook_event_name":"PreToolUse","tool_name":"mcp__github__merge_pull_request","tool_input":{"pullNumber":12},"cwd":"/"}`
	w.runGuardCmd(mcp, env...).requireMergeRefusal(t)
	// A ship launched by hand, with ADMIRAL_AUTOPILOT unset, may merge.
	w.runGuardCmd(guardPayload("gh pr merge 12 --squash --admin"), shipEnv(home, "saltpaw-ii", "ADMIRAL_AUTO_MERGE=squash")...).requireQuiet(t)
}
