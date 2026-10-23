package cli

import (
	"context"
	"encoding/json"
	"errors"
	"net/url"
	"os"
	"os/exec"
	"regexp"
	"slices"
	"strings"
	"time"
)

// The merge half of the guard (#120). On an autopilot ship the only way to merge
// is `gh pr merge --auto` with the panel's method (ADMIRAL_AUTO_MERGE), on a pull
// request whose base branch GitHub confirms requires an approving review. The
// guard refuses every other merge, and a write to the rules that gate one,
// because the ship's token could undo the check.

// guardState is what the walk over one command line builds up: whether something
// earlier on the line could point gh at another repository or branch than the one
// the guard resolves, and the pull requests whose base branch is still to be
// confirmed. The confirmation waits until the whole line has been read, so a
// refusal that needs no network never reaches GitHub.
type guardState struct {
	steered bool
	lookups [][]string
}

// steersGh says whether a word is an assignment of the variables that choose gh's
// repository or host.
func steersGh(word string) bool {
	return strings.HasPrefix(word, "GH_REPO=") || strings.HasPrefix(word, "GH_HOST=")
}

// gitSteers says whether a git command changes the branch, the remotes or the
// config that gh reads the repository and pull request from.
func gitSteers(args []string) bool {
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "-C" || a == "-c" || a == "--git-dir" || a == "--work-tree" || a == "--namespace":
			i++
		case strings.HasPrefix(a, "-"):
		default:
			return slices.Contains([]string{"checkout", "switch", "remote", "config", "worktree"}, a)
		}
	}
	return false
}

// ghGlobalRepo is the -R or --repo given before gh's subcommand words, which
// ghSubcommand drops (`gh pr -R o/r merge`).
func ghGlobalRepo(args []string) (repo string) {
	words := 0
	for i := 0; i < len(args) && words < 2; i++ {
		a := args[i]
		switch {
		case a == "-R" || a == "--repo":
			if i+1 < len(args) {
				i++
				repo = args[i]
			}
		case strings.HasPrefix(a, "--repo="):
			repo = a[len("--repo="):]
		case a == "--hostname":
			i++
		case !strings.HasPrefix(a, "-"):
			words++
		}
	}
	return repo
}

// mcpMerges says whether an MCP tool merges a pull request or enables its
// auto-merge, by name.
func mcpMerges(tool string) bool {
	for _, w := range []string{"merge_pull_request", "enable_pull_request_auto_merge", "enable_auto_merge"} {
		if strings.Contains(tool, w) {
			return true
		}
	}
	return false
}

// prMerge is the rule for gh pr merge, given the arguments after `merge` and the
// repository named before it. Its flags are followed as pflag does: -b, -F, -R,
// -t and -A (and their long forms) take a value, which may start with a dash, a
// short cluster ends at a value-taking flag, and -- ends the flags. Only --auto
// with the panel's method merges, and only once the base branch is confirmed;
// --disable-auto only removes a merge.
func (g guardCheck) prMerge(args []string, repo string) string {
	var pr []string // positional words
	auto, disable, admin := false, false, false
	var methods []string
	unsure := false // a flag the guard cannot read, or a value that could split into flags
	repoUnsure := hasPlaceholder(repo)
	value := func(i *int, attached string) string {
		if attached == "" && *i+1 < len(args) {
			*i++
			attached = args[*i]
		}
		return attached
	}
	boolean := func(has bool, val string) bool { return !has || val != "false" }
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "--":
			pr = append(pr, args[i+1:]...)
			i = len(args)
		case strings.HasPrefix(a, "--"):
			name, val, has := strings.Cut(a[2:], "=")
			switch name {
			case "auto":
				auto = boolean(has, val)
			case "disable-auto":
				disable = boolean(has, val)
			case "admin":
				admin = boolean(has, val)
			case "squash", "merge", "rebase":
				if boolean(has, val) {
					methods = append(methods, name)
				}
			case "repo":
				repo = val
				if !has {
					repo = value(&i, "")
				}
				repoUnsure = repoUnsure || hasPlaceholder(repo)
			case "body", "body-file", "subject", "author-email", "match-head-commit":
				if !has {
					val = value(&i, "")
				}
				unsure = unsure || hasSplit(val)
			default:
				unsure = unsure || hasPlaceholder(name)
			}
		case len(a) > 1 && a[0] == '-':
		cluster:
			for j := 1; j < len(a) && a[j] != '='; j++ {
				switch {
				case a[j] == 's':
					methods = append(methods, "squash")
				case a[j] == 'm':
					methods = append(methods, "merge")
				case a[j] == 'r':
					methods = append(methods, "rebase")
				case a[j] == placeholder[0]:
					unsure = true
				case a[j] == 'R':
					repo = value(&i, a[j+1:])
					repoUnsure = repoUnsure || hasPlaceholder(repo)
					break cluster
				case strings.IndexByte("bFtA", a[j]) >= 0:
					unsure = unsure || hasSplit(value(&i, a[j+1:]))
					break cluster
				}
			}
		default:
			pr = append(pr, a)
		}
	}
	switch {
	case disable && !admin:
		return ""
	case admin || !auto || unsure:
		return ruleGhPrMerge
	}
	want := os.Getenv("ADMIRAL_AUTO_MERGE")
	if want != "squash" && want != "merge" && want != "rebase" || len(methods) != 1 || methods[0] != want {
		return ruleAutoMergeMethod
	}
	if g.st.steered || repoUnsure || len(pr) > 1 || len(pr) == 1 && (hasPlaceholder(pr[0]) || pr[0] == "") {
		return ruleAutoMergeSteered
	}
	// The lookup is given what gh pr view needs to find the pull request the merge names.
	var view []string
	view = append(view, pr...)
	if repo != "" {
		view = append(view, "-R", repo)
	}
	g.st.lookups = append(g.st.lookups, view)
	return ""
}

// confirmMerges asks GitHub, once the whole line is read, whether the base branch
// of each pull request it auto-merges requires an approving review.
func (g guardCheck) confirmMerges() string {
	if len(g.st.lookups) == 0 {
		return ""
	}
	ctx, cancel := context.WithTimeout(context.Background(), lookupTimeout)
	defer cancel()
	for _, pr := range g.st.lookups {
		if ok, err := approvalLookup(ctx, g.cwd, pr); err != nil || !ok {
			return ruleAutoMergeUnproven
		}
	}
	return ""
}

// apiWrites says whether a gh api call is a write: any method but GET and HEAD,
// or gh's implicit POST when it is given fields or an --input and no method.
func (p apiArgs) writes() bool {
	if p.method == "" {
		return p.fieldFlag || p.hasInput
	}
	return !strings.EqualFold(p.method, "GET") && !strings.EqualFold(p.method, "HEAD")
}

// apiMerge is the rule for a gh api call that merges (the merge and merges
// routes, GraphQL mutations that merge or enable auto-merge) or that writes to
// the gate approvals rely on (rulesets and branch protection, in REST and
// GraphQL). An expansion in a segment matches any word there; a method held in a
// variable is a write.
func (g guardCheck) apiMerge(args []string, line *lineText) string {
	p := parseAPI(args)
	if len(p.positional) == 0 {
		return ""
	}
	seg := endpointSegments(p.positional[0])
	if len(seg) == 2 && seg[0] == "api" && seg[1] == "graphql" {
		seg = seg[1:] // GHE serves GraphQL at /api/graphql
	}
	if len(seg) == 1 && seg[0] == "graphql" {
		return g.graphqlMutates(p, line)
	}
	if !p.writes() {
		return ""
	}
	if len(seg) == 1 && hasPlaceholder(seg[0]) && onlyExpansion(seg[0]) {
		return ruleGhAPIMerge // the whole endpoint is set elsewhere: it could be a merge
	}
	is := func(s, lit string) bool { return hasPlaceholder(s) || s == lit }
	for _, t := range routeTails(seg) {
		switch {
		case len(t) == 3 && is(t[0], "pulls") && is(t[2], "merge"), len(t) == 1 && is(t[0], "merges"):
			return ruleGhAPIMerge
		case len(t) >= 1 && is(t[0], "rulesets"), protectionRoute(t):
			return ruleGhAPIGateWrite
		}
	}
	if len(seg) >= 3 && is(seg[0], "orgs") && is(seg[2], "rulesets") {
		return ruleGhAPIGateWrite
	}
	return ""
}

// protectionRoute says whether the tail of a repository path is
// branches/<branch>/protection…; the branch may hold slashes.
func protectionRoute(t []string) bool {
	if len(t) < 3 || t[0] != "branches" && !hasPlaceholder(t[0]) {
		return false
	}
	return slices.ContainsFunc(t[2:], func(s string) bool { return s == "protection" || hasPlaceholder(s) })
}

// The GraphQL mutations that merge or enable auto-merge, and those that write to
// branch protection rules or rulesets.
var (
	graphqlMerges = []string{"mergePullRequest", "enablePullRequestAutoMerge", "mergeBranch"}
	graphqlGates  = []string{"createBranchProtectionRule", "updateBranchProtectionRule", "deleteBranchProtectionRule",
		"createRepositoryRuleset", "updateRepositoryRuleset", "deleteRepositoryRuleset"}
)

// graphqlMutates is the rule for a gh api graphql call that names one of those
// mutations in its query, a file or stdin it reads, or an --input body. A query
// the guard cannot read counts as a merge, and so does a field call named by an
// expansion. A query that is only an expansion is judged by the whole line.
func (g guardCheck) graphqlMutates(p apiArgs, line *lineText) string {
	var queries []string
	for _, f := range p.fields {
		if f.key != "query" {
			continue
		}
		v := f.val
		if f.typed && strings.HasPrefix(v, "@") {
			if v == "@-" {
				queries = append(queries, line.text)
				continue
			}
			b, ok := g.readFile(v[1:])
			if !ok {
				return ruleGraphqlMerge
			}
			v = b
		}
		queries = append(queries, v)
	}
	switch {
	case p.hasInput && p.input == "-":
		queries = append(queries, line.text)
	case p.hasInput:
		b, ok := g.readFile(p.input)
		var body struct {
			Query string `json:"query"`
		}
		if !ok || json.Unmarshal([]byte(b), &body) != nil {
			return ruleGraphqlMerge
		}
		queries = append(queries, body.Query)
	}
	for _, q := range queries {
		if onlyExpansion(q) && hasPlaceholder(q) {
			q = line.text // the query was set elsewhere on the line
		}
		switch {
		case slices.ContainsFunc(graphqlMerges, func(m string) bool { return strings.Contains(q, m) }), gqlNameExpands.MatchString(q):
			return ruleGraphqlMerge
		case slices.ContainsFunc(graphqlGates, func(m string) bool { return strings.Contains(q, m) }):
			return ruleGraphqlGateWrite
		}
	}
	return ""
}

// gqlNameExpands is a field call whose name holds an expansion, which could be
// any mutation.
var gqlNameExpands = regexp.MustCompile(`[{}\s]\w*` + placeholder + `\w*\s*[({]`)

// lookupTimeout bounds the whole check against GitHub: the hook times out at
// 10 s, and a hook that times out must not be what refuses.
const lookupTimeout = 7 * time.Second

// approvalLookup says whether the base branch of the pull request that gh pr view
// finds from pr (its argument, then -R and a repository, as the merge names them)
// requires at least one approving review. An error means not confirmed. It is a
// variable so tests run without GitHub.
var approvalLookup = ghApprovalLookup

// ghApprovalLookup resolves the pull request with gh pr view, then reads the
// rules that apply to its base branch and, failing that, its classic branch
// protection, with plain GETs.
func ghApprovalLookup(ctx context.Context, cwd string, pr []string) (bool, error) {
	run := func(args ...string) ([]byte, error) {
		cmd := exec.CommandContext(ctx, "gh", args...)
		cmd.Dir = cwd
		cmd.WaitDelay = 500 * time.Millisecond
		return cmd.Output()
	}
	out, err := run(append(append([]string{"pr", "view"}, pr...), "--json", "url,baseRefName")...)
	if err != nil {
		return false, err
	}
	var view struct {
		URL         string `json:"url"`
		BaseRefName string `json:"baseRefName"`
	}
	if err := json.Unmarshal(out, &view); err != nil {
		return false, err
	}
	u, err := url.Parse(view.URL)
	if err != nil || view.BaseRefName == "" {
		return false, errors.New("gh pr view gave no base branch")
	}
	path := strings.Split(strings.Trim(u.Path, "/"), "/")
	if len(path) < 2 || path[0] == "" || path[1] == "" {
		return false, errors.New("gh pr view gave no repository")
	}
	get := func(endpoint string) ([]byte, error) {
		args := []string{"api", "-X", "GET"}
		if u.Host != "" && u.Host != "github.com" {
			args = append(args, "--hostname", u.Host)
		}
		return run(append(args, endpoint)...)
	}
	repo := "repos/" + url.PathEscape(path[0]) + "/" + url.PathEscape(path[1])
	base := strings.Join(mapSegments(strings.Split(view.BaseRefName, "/"), url.PathEscape), "/")

	if out, err := get(repo + "/rules/branches/" + base); err == nil {
		var rules []struct {
			Type       string `json:"type"`
			Parameters struct {
				Count int `json:"required_approving_review_count"`
			} `json:"parameters"`
		}
		if json.Unmarshal(out, &rules) == nil {
			for _, r := range rules {
				if r.Type == "pull_request" && r.Parameters.Count >= 1 {
					return true, nil
				}
			}
		}
	}
	out, err = get(repo + "/branches/" + base + "/protection")
	if err != nil {
		return false, err
	}
	var prot struct {
		Reviews struct {
			Count int `json:"required_approving_review_count"`
		} `json:"required_pull_request_reviews"`
	}
	if err := json.Unmarshal(out, &prot); err != nil {
		return false, err
	}
	return prot.Reviews.Count >= 1, nil
}

func mapSegments(s []string, f func(string) string) []string {
	out := make([]string, len(s))
	for i, v := range s {
		out[i] = f(v)
	}
	return out
}
