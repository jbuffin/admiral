package e2e

import (
	"os"
	"path/filepath"
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
	protEndpoint  = "repos/acme/widgets/branches/main/protection"
	mergeCmd      = "gh pr merge 12 --auto --squash"
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

func rulesJSON(counts ...string) string {
	var rules []string
	for _, c := range counts {
		rules = append(rules, `{"type":"pull_request","parameters":{"required_approving_review_count":`+c+`,"dismiss_stale_reviews_on_push":false}}`)
	}
	return "[" + strings.Join(rules, ",") + "]"
}

func protectionJSON(count string) string {
	return `{"url":"x","required_pull_request_reviews":{"required_approving_review_count":` + count + `}}`
}

// The lookup reads GitHub's answer the way C7 says: the rules of the base
// branch first (the largest count of the pull_request rules), then classic
// protection, and anything it cannot confirm refuses.
func TestGuardAutoMergeLookupAgainstGitHub(t *testing.T) {
	type answer struct {
		status int
		body   string
	}
	missing := answer{404, `{"message":"Not Found","status":"404"}`}
	rows := []struct {
		name         string
		rules, prot  answer
		prView       string // "" is prViewWidgets
		prViewFail   string
		prViewBroken bool // no base to ask about: the lookup stops after gh pr view
		wantAllowed  bool
		wantProtRead bool // the classic protection was asked for
	}{
		{name: "rules require one approval", rules: answer{0, rulesJSON("1")}, prot: missing, wantAllowed: true},
		{name: "rules require two approvals", rules: answer{0, rulesJSON("2")}, prot: missing, wantAllowed: true},
		{name: "the largest of several pull_request rules counts", rules: answer{0, rulesJSON("0", "2")}, prot: missing, wantAllowed: true},
		{name: "no rules, protection requires one", rules: answer{0, `[]`}, prot: answer{0, protectionJSON("1")}, wantAllowed: true, wantProtRead: true},
		{name: "rules ask for none, protection requires one", rules: answer{0, rulesJSON("0")}, prot: answer{0, protectionJSON("1")}, wantAllowed: true, wantProtRead: true},
		{name: "no rules and no protection", rules: answer{0, `[]`}, prot: missing, wantProtRead: true},
		{name: "rules ask for none and there is no protection", rules: answer{0, rulesJSON("0")}, prot: missing, wantProtRead: true},
		{name: "rules without a pull_request rule", rules: answer{0, `[{"type":"required_status_checks","parameters":{}}]`}, prot: missing, wantProtRead: true},
		{name: "protection requires none", rules: answer{0, `[]`}, prot: answer{0, protectionJSON("0")}, wantProtRead: true},
		{name: "protection without required reviews", rules: answer{0, `[]`}, prot: answer{0, `{"url":"x","enforce_admins":{"enabled":true}}`}, wantProtRead: true},
		{name: "rules forbidden, no protection", rules: answer{403, `{"message":"Forbidden"}`}, prot: missing, wantProtRead: true},
		{name: "rules forbidden, protection forbidden", rules: answer{403, `{"message":"Forbidden"}`}, prot: answer{403, `{"message":"Forbidden"}`}, wantProtRead: true},
		{name: "rules that are not JSON, no protection", rules: answer{0, `<html>`}, prot: missing, wantProtRead: true},
		{name: "rules that are not a list, no protection", rules: answer{0, `{"message":"x"}`}, prot: missing, wantProtRead: true},
		{name: "protection that is not JSON", rules: answer{0, `[]`}, prot: answer{0, `<html>`}, wantProtRead: true},
		{name: "the PR cannot be resolved", rules: answer{0, rulesJSON("1")}, prot: missing, prViewFail: "no pull requests found", prViewBroken: true},
		{name: "the PR answer is not JSON", rules: answer{0, rulesJSON("1")}, prot: missing, prView: `no`, prViewBroken: true},
		{name: "the PR answer has no base", rules: answer{0, rulesJSON("1")}, prot: missing, prView: `{"url":"https://github.com/acme/widgets/pull/12"}`, prViewBroken: true},
		{name: "a stacked PR whose base asks for none", rules: answer{0, rulesJSON("0")}, prot: missing, prView: `{"url":"https://github.com/acme/widgets/pull/12","baseRefName":"main"}`, wantProtRead: true},
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
			g.restRaw("protection", protEndpoint, r.prot.status, r.prot.body)

			res := w.runGuardCmd(guardPayload(mergeCmd), shipEnv(t.TempDir(), "saltpaw-ii", "ADMIRAL_AUTOPILOT=1", "ADMIRAL_AUTO_MERGE=squash")...)
			if r.wantAllowed {
				res.requireQuiet(t)
			} else {
				res.requireMergeRefusal(t)
			}
			calls := g.argvLines()
			if !hasCall(calls, "pr view", "12", "--json url,baseRefName") {
				t.Errorf("the lookup never ran gh pr view 12 --json url,baseRefName; calls:\n%s", strings.Join(calls, "\n"))
			}
			if !r.prViewBroken {
				if !hasCall(calls, "api", "-X GET", rulesEndpoint) {
					t.Errorf("the lookup never GET %s; calls:\n%s", rulesEndpoint, strings.Join(calls, "\n"))
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

// The owner, repo and base come from what gh pr view returns, not from the
// command: a -R in the command is handed to gh pr view and nothing else.
func TestGuardAutoMergeLookupUsesTheResolvedPR(t *testing.T) {
	w := newWorld(t)
	g := w.newFakeGH()
	g.write("pr_view.json", `{"url":"https://github.com/acme/widgets/pull/12","baseRefName":"release"}`)
	g.restRaw("rules", "repos/acme/widgets/rules/branches/release", 0, rulesJSON("1"))
	res := w.runGuardCmd(guardPayload("gh pr merge 12 --auto --squash -R acme/widgets"), shipEnv(t.TempDir(), "saltpaw-ii", "ADMIRAL_AUTOPILOT=1", "ADMIRAL_AUTO_MERGE=squash")...)
	res.requireQuiet(t)
	calls := g.argvLines()
	if !hasCall(calls, "pr view", "12", "-R acme/widgets", "--json url,baseRefName") {
		t.Errorf("gh pr view was not given the PR and -R; calls:\n%s", strings.Join(calls, "\n"))
	}
	if !hasCall(calls, "api", "-X GET", "repos/acme/widgets/rules/branches/release") {
		t.Errorf("the rules of the resolved base were not read; calls:\n%s", strings.Join(calls, "\n"))
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
	g.restRaw("rules", rulesEndpoint, 0, rulesJSON("1"))
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
	g.restRaw("rules", rulesEndpoint, 0, rulesJSON("1"))
	home := t.TempDir()
	env := shipEnv(home, "saltpaw-ii", "ADMIRAL_AUTOPILOT=1", "ADMIRAL_AUTO_MERGE=squash")

	for _, cmd := range []string{"gh pr ready 12", "gh pr merge 12 --disable-auto", mergeCmd} {
		w.runGuardCmd(guardPayload(cmd), env...).requireQuiet(t)
	}
	for cmd, rule := range map[string]string{
		"gh pr merge 12 --squash":                         "gh_pr_merge",
		"gh api -X PUT repos/acme/widgets/pulls/12/merge": "gh_api_merge",
		"gh pr merge 12 --auto --rebase":                  "auto_merge_method",
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
