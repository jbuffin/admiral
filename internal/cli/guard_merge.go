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
	"strconv"
	"strings"
	"time"

	"github.com/jbuffin/admiral/internal/launcher"
)

// The merge half of the guard (#120). On an autopilot ship the only way to merge
// is `gh pr merge --auto` with the panel's method (ADMIRAL_AUTO_MERGE), on a pull
// request given as one full URL whose base branch GitHub confirms requires fresh
// approving reviews that nobody on the ship's token can bypass. The guard
// refuses every other merge, a change of a pull request's base, and a write to
// the rules that gate a merge, because the ship's token could undo the check.
// It is best effort: it reads the text of a Bash call and nothing else (see
// "Known gaps" in docs/agents/codebase.md).

// guardState is what the walk over one command line builds up: whether anything
// on the line could point gh at another repository or host, or change what it
// runs as, and the pull requests whose base branch is still to be confirmed.
// The confirmation waits until the whole line has been read, so a refusal that
// needs no network never reaches GitHub, and steering anywhere on the line
// counts, wherever the shell would run it.
type guardState struct {
	steered bool
	lookups [][]string
}

// steerNames are the variables that choose the repository, the host, the git
// directory or the credentials gh runs with.
var steerNames = []string{"GH_REPO", "GH_HOST", "GIT_DIR", "GIT_WORK_TREE"}

// steersGh says whether a word is an assignment of one of steerNames.
func steersGh(word string) bool {
	for _, v := range steerNames {
		if strings.HasPrefix(word, v+"=") {
			return true
		}
	}
	return false
}

// steersWrapper says whether a word a wrapper (env, sudo, xargs…) consumed changes
// where or how gh runs: a variable it sets, a directory it moves to, or an
// environment it clears or unsets. The lookup runs in the payload's cwd and
// environment, so any of them could point gh at another pull request.
func steersWrapper(word string) bool {
	switch {
	case steersGh(word), word == "-u", word == "-i", word == "--ignore-environment",
		strings.HasPrefix(word, "-C"), strings.HasPrefix(word, "-D"),
		strings.HasPrefix(word, "--chdir"), strings.HasPrefix(word, "--unset"):
		return true
	}
	return false
}

// steeringBuiltins assign a variable by an argument that names it.
var steeringBuiltins = []string{"read", "mapfile", "readarray", "getopts", "printf", ":", "declare", "typeset", "local", "export", "readonly"}

// steersBuiltin says whether a builtin call (its source text) assigns one of
// steerNames, or turns on allexport so that any assignment is exported.
func steersBuiltin(word string, args []string, src string) bool {
	if word == "set" {
		return slices.ContainsFunc(args, func(a string) bool {
			return a == "allexport" || len(a) > 1 && a[0] == '-' && a[1] != '-' && strings.Contains(a, "a")
		})
	}
	return slices.Contains(steeringBuiltins, word) && slices.ContainsFunc(steerNames, func(n string) bool { return strings.Contains(src, n) })
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
		case strings.HasPrefix(a, "-R") && len(a) > 2:
			repo = a[2:]
		case a == "--hostname":
			i++
		case !strings.HasPrefix(a, "-"):
			words++
		}
	}
	return repo
}

// mcpMergeTool matches the MCP tools whose name has merge or enqueue in it: the
// matcher in the ship's settings, so a tool that reaches the guard is one this
// matches.
var mcpMergeTool = regexp.MustCompile(launcher.MergeToolMatcher)

// mcpMerges says whether an MCP tool could merge or enqueue a pull request: every
// tool the matcher selects, bar those whose last name segment says they only read.
func mcpMerges(tool string) bool {
	if !mcpMergeTool.MatchString(tool) {
		return false
	}
	name := tool[strings.LastIndex(tool, "__")+2:]
	return !slices.ContainsFunc([]string{"list_", "get_", "search_"}, func(p string) bool { return strings.HasPrefix(name, p) })
}

// tokenRead is a call that reads the GitHub token: `gh auth token`, or a mention
// of the variables gh takes one from.
var tokenRead = regexp.MustCompile(`\b(?:GH_TOKEN|GITHUB_TOKEN|GH_ENTERPRISE_TOKEN|GITHUB_ENTERPRISE_TOKEN)\b|\bgh\b[^\n;|&]*\bauth\s+token\b`)

// Text the guard could not parse is judged by what it mentions: a merge, a route
// that merges, or a rule that gates one. mergeTextRule is the merge counterpart
// of the review text rule.
var (
	prMergeText   = regexp.MustCompile(`(?i)\bpr\b[^\n;|&]*\bmerge\b`)
	mergeRouteTxt = regexp.MustCompile(`(?i)\bpulls/\S*merge|/merges\b`)
	gateRouteText = regexp.MustCompile(`(?i)\brulesets\b|/protection\b`)
)

// mergeTextRule is the rule that refuses text which mentions a merge or a write
// to the gate, in a long spelling, or "".
func mergeTextRule(text string) string {
	switch {
	case prMergeText.MatchString(text):
		return ruleGhPrMerge
	case mergeRouteTxt.MatchString(text):
		return ruleGhAPIMerge
	case slices.ContainsFunc(graphqlMerges, func(m string) bool { return strings.Contains(text, m) }):
		return ruleGraphqlMerge
	case gateRouteText.MatchString(text):
		return ruleGhAPIGateWrite
	case slices.ContainsFunc(graphqlGates, func(m string) bool { return strings.Contains(text, m) }):
		return ruleGraphqlGateWrite
	case slices.ContainsFunc(graphqlRetargets, func(m string) bool { return strings.Contains(text, m) }):
		return rulePrBaseChange
	}
	return ""
}

// prURL is the one way a pull request is named to gh pr merge --auto: a full
// https URL. gh takes the repository and host from such an argument and from
// nothing else (cli/cli pkg/cmd/pr/shared/finder.go: Find sets the base repo from
// ParseURL and calls baseRepoFn, which reads GH_REPO, -R and the cwd's remotes,
// only when there is none), so the PR the lookup resolves is the PR gh merges.
var prURL = regexp.MustCompile(`^https://[A-Za-z0-9.-]+/[A-Za-z0-9._-]+/[A-Za-z0-9._-]+/pull/[0-9]+$`)

// prEditRetargets says whether gh pr edit sets the base (-B, --base), or has a
// flag or value it cannot read. It follows pflag over the flags that take a value
// (-F -R -a -b -i -l -m -p -r -t and their long forms), which may start with a dash.
func prEditRetargets(args []string) bool {
	long := []string{"body", "body-file", "title", "add-assignee", "remove-assignee", "add-label", "remove-label",
		"add-project", "remove-project", "add-reviewer", "remove-reviewer", "milestone", "repo"}
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "--":
			return false
		case strings.HasPrefix(a, "--"):
			name, val, has := strings.Cut(a[2:], "=")
			switch {
			case name == "base" || hasPlaceholder(name):
				return true
			case slices.Contains(long, name):
				if !has && i+1 < len(args) {
					i++
					val = args[i]
				}
				if hasSplit(val) {
					return true
				}
			}
		case len(a) > 1 && a[0] == '-':
		cluster:
			for j := 1; j < len(a); j++ {
				switch {
				case a[j] == 'B' || a[j] == placeholder[0]:
					return true
				case strings.IndexByte("FRabilmprt", a[j]) >= 0:
					val := a[j+1:]
					if val == "" && i+1 < len(args) {
						i++
						val = args[i]
					}
					if hasSplit(val) {
						return true
					}
					break cluster
				}
			}
		case hasPlaceholder(a):
			return true
		}
	}
	return false
}

// prMerge is the rule for gh pr merge, given the arguments after `merge` and the
// repository named before it. Its flags are followed as pflag does: -b, -F, -R,
// -t and -A (and their long forms) take a value, which may start with a dash, a
// short cluster ends at a value-taking flag, and -- ends the flags. Only --auto
// with the panel's method merges, on one full PR URL, and only once the base
// branch is confirmed; --disable-auto only removes a merge.
func (g guardCheck) prMerge(args []string, repo string) string {
	var pr []string // positional words
	auto, disable, admin := false, false, false
	var methods []string
	unsure := false // a flag the guard cannot read, or a value that could split into flags
	repoGiven := repo != ""
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
				repoGiven = true
				if !has {
					value(&i, "")
				}
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
					repoGiven = true
					value(&i, a[j+1:])
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
	// One full URL, and no repository beside it: nothing but the URL decides which
	// pull request gh merges, and the lookup resolves that same URL.
	if repoGiven || len(pr) != 1 || !prURL.MatchString(pr[0]) {
		return ruleAutoMergeSteered
	}
	g.st.lookups = append(g.st.lookups, []string{pr[0]})
	return ""
}

// confirmMerges decides the auto-merges of the line once all of it is read: a
// line that steers gh anywhere is refused, and otherwise GitHub is asked whether
// the base branch of each pull request keeps its approvals fresh and unbypassed.
func (g guardCheck) confirmMerges() string {
	if len(g.st.lookups) == 0 {
		return ""
	}
	if g.st.steered {
		return ruleAutoMergeSteered
	}
	ctx, cancel := context.WithTimeout(context.Background(), lookupTimeout)
	defer cancel()
	for _, pr := range g.st.lookups {
		if ok, err := approvalLookup(ctx, g.cwd, pr); err != nil || !ok {
			return ruleAutoMergeUnconfirmed
		}
	}
	return ""
}

// writes says whether a gh api call is a write: any method but GET and HEAD, or
// gh's implicit POST when it is given fields or an --input and no method.
func (p apiArgs) writes() bool {
	if p.method == "" {
		return p.fieldFlag || p.hasInput
	}
	return !strings.EqualFold(p.method, "GET") && !strings.EqualFold(p.method, "HEAD")
}

// apiMerge is the rule for a gh api call that merges (the merge and merges
// routes, GraphQL mutations that merge or enable auto-merge), that changes a
// pull request's base, or that writes to the gate approvals rely on (rulesets and
// branch protection, in REST and GraphQL). An expansion in a segment matches any
// word there; a method held in a variable is a write.
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
		case len(t) == 2 && is(t[0], "pulls") && g.retargets(p, line):
			return rulePrBaseChange
		}
	}
	if len(seg) >= 3 && is(seg[0], "orgs") && is(seg[2], "rulesets") {
		return ruleGhAPIGateWrite
	}
	// An expansion can span segments ($P holding 12/merge). Past the last one the
	// path must be plainly something else; at the end, what comes before must be a
	// prefix no merge or gate route extends.
	last := -1
	for i, s := range seg {
		if hasPlaceholder(s) {
			last = i
		}
	}
	if last >= 0 {
		rest := seg[last+1:]
		switch {
		case len(rest) == 0 && routeExtends(seg[:last]):
			return ruleGhAPIMerge
		case len(rest) > 0 && slices.Contains([]string{"merge", "merges"}, rest[len(rest)-1]):
			return ruleGhAPIMerge
		case slices.Contains(rest, "protection") || slices.Contains(rest, "rulesets"):
			return ruleGhAPIGateWrite
		}
	}
	return ""
}

// retargets says whether a write to a pull request may set its base: a field
// named base (or one the guard cannot name), or an --input body it cannot read or
// that has a base in it.
func (g guardCheck) retargets(p apiArgs, line *lineText) bool {
	for _, f := range p.fields {
		if f.key == "base" || hasPlaceholder(f.key) {
			return true
		}
	}
	if !p.hasInput {
		return false
	}
	if p.input == "-" {
		return strings.Contains(line.text, "base")
	}
	b, ok := g.readFile(p.input)
	return !ok || strings.Contains(b, "base")
}

// routeExtends says whether an expansion after this literal prefix of a path
// could complete a merge or gate route: the root, repos/<o>/<r> and what is above
// it, pulls/<n>, anything under branches, orgs/<o>. Under issues, comments and
// the like an expansion would have to hide a "../" to get anywhere, which the
// path normalisation already resolves.
func routeExtends(prefix []string) bool {
	if len(prefix) == 0 || hasPlaceholder(prefix[0]) {
		return true
	}
	var t []string
	switch prefix[0] {
	case "repos":
		if len(prefix) < 3 {
			return true
		}
		t = prefix[3:]
	case "orgs", "repositories":
		if len(prefix) < 2 || prefix[0] == "orgs" && len(prefix) == 2 {
			return true
		}
		t = prefix[2:]
	default:
		return false
	}
	switch {
	case len(t) == 0:
		return true
	case t[0] == "pulls":
		return len(t) == 2
	}
	return t[0] == "branches" || t[0] == "rulesets" || hasPlaceholder(t[0])
}

// protectionRoute says whether the tail of a repository path is
// branches/<branch>/protection…; the branch may hold slashes.
func protectionRoute(t []string) bool {
	if len(t) < 3 || t[0] != "branches" && !hasPlaceholder(t[0]) {
		return false
	}
	return slices.ContainsFunc(t[2:], func(s string) bool { return s == "protection" || hasPlaceholder(s) })
}

// The GraphQL mutations that merge or enable auto-merge, those that write to
// branch protection rules or rulesets, and the one that can change a pull
// request's base.
var (
	graphqlMerges = []string{"mergePullRequest", "enablePullRequestAutoMerge", "mergeBranch",
		"enqueuePullRequest", "dequeuePullRequest"}
	graphqlGates = []string{"createBranchProtectionRule", "updateBranchProtectionRule", "deleteBranchProtectionRule",
		"createRepositoryRuleset", "updateRepositoryRuleset", "deleteRepositoryRuleset"}
	graphqlRetargets = []string{"updatePullRequest"}
)

// gqlNameExpands is a field call whose name holds an expansion, which could be
// any mutation.
var gqlNameExpands = regexp.MustCompile(`(?:^|[{}\s])\w*` + placeholder + `\w*\s*[({]`)

// gqlInputBound is a mutation input held in a variable the guard does not see
// spelled out.
var gqlInputBound = regexp.MustCompile(`input\s*:\s*[$` + placeholder + `]`)

// gqlString is a GraphQL string literal.
var gqlString = regexp.MustCompile(`"(?:[^"\\]|\\.)*"`)

// gqlOpaque says whether an expansion in a query sits where it could be more than
// a value: outside every string, and not as the value after a colon. It could then
// be a field name, a keyword, a whole selection.
func gqlOpaque(q string) bool {
	q = gqlString.ReplaceAllString(q, `""`)
	for i := 0; i < len(q); i++ {
		if q[i] != placeholder[0] {
			continue
		}
		prev := strings.TrimRight(q[:i], " \t\r\n"+splitMark)
		if !strings.HasSuffix(prev, ":") {
			return true
		}
	}
	return false
}

// plainQuery is a literal query operation, the only text an expansion in a
// GraphQL query is allowed beside.
var plainQuery = regexp.MustCompile(`\bquery\b[^"']*\{`)

// graphqlUnreadable says whether a gh api graphql call names a query file or an
// --input body that cannot be read, which is refused before the review rules so
// that it is not taken for a review.
func (g guardCheck) graphqlUnreadable(args []string) bool {
	p := parseAPI(args)
	if len(p.positional) == 0 {
		return false
	}
	if seg := endpointSegments(p.positional[0]); len(seg) != 1 || seg[0] != "graphql" && !(len(seg) == 2 && seg[0] == "api" && seg[1] == "graphql") {
		return false
	}
	for _, f := range p.fields {
		if f.key == "query" && f.typed && strings.HasPrefix(f.val, "@") && f.val != "@-" {
			if _, ok := g.readFile(f.val[1:]); !ok {
				return true
			}
		}
	}
	if p.hasInput && p.input != "-" {
		_, ok := g.readFile(p.input)
		return !ok
	}
	return false
}

// graphqlMutates is the rule for a gh api graphql call that names one of those
// mutations in its query, a file or stdin it reads, or an --input body. A query
// the guard cannot read counts as a merge: a file it cannot open, stdin that is
// not shown on the line (a pipe from a file), a field call named by an
// expansion, an expansion beside the word mutation, or a query that is only an
// expansion unless the line itself shows a plain query operation and no
// mutation.
func (g guardCheck) graphqlMutates(p apiArgs, line *lineText) string {
	var queries []string
	stmt := line.text[min(line.stmtFrom, len(line.text)):min(line.stmtTo, len(line.text))]
	stdin := func() bool { // text for stdin is on the statement, not read from elsewhere
		return strings.Contains(stmt, "<<") || strings.Contains(stmt, "echo ") || strings.Contains(stmt, "printf ")
	}
	for _, f := range p.fields {
		if f.key != "query" {
			continue
		}
		v := f.val
		if f.typed && strings.HasPrefix(v, "@") {
			if v == "@-" {
				if !stdin() {
					return ruleGraphqlMerge
				}
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
		if !stdin() {
			return ruleGraphqlMerge
		}
		queries = append(queries, line.text)
	case p.hasInput:
		b, ok := g.readFile(p.input)
		var body struct {
			Query string `json:"query"`
		}
		if !ok || json.Unmarshal([]byte(b), &body) != nil {
			return ruleGraphqlMerge
		}
		queries = append(queries, body.Query, b)
	}
	for _, q := range queries {
		if hasPlaceholder(q) {
			if !onlyExpansion(q) && gqlOpaque(q) {
				return ruleGraphqlMerge
			}
			if onlyExpansion(q) {
				// The query was set elsewhere on the line: it must be shown there, as a
				// plain query, and not through a substitution in this statement.
				if strings.Contains(stmt, "$(") || strings.Contains(stmt, "`") || strings.Contains(stmt, "<(") ||
					!plainQuery.MatchString(line.text) || strings.Contains(line.text, "mutation") {
					return ruleGraphqlMerge
				}
				q = line.text
			}
		}
		switch {
		case slices.ContainsFunc(graphqlMerges, func(m string) bool { return strings.Contains(q, m) }), gqlNameExpands.MatchString(q):
			return ruleGraphqlMerge
		case slices.ContainsFunc(graphqlGates, func(m string) bool { return strings.Contains(q, m) }):
			return ruleGraphqlGateWrite
		case strings.Contains(q, "updatePullRequest") &&
			(strings.Contains(line.text, "baseRefName") || strings.Contains(q, "baseRefName") || gqlInputBound.MatchString(q) || hasPlaceholder(q)):
			return rulePrBaseChange
		}
	}
	return ""
}

// lookupTimeout bounds the whole check against GitHub: the hook times out at
// 10 s, and a hook that times out must not be what refuses.
const lookupTimeout = 7 * time.Second

// approvalLookup says whether the base branch of the pull request that gh pr view
// finds from pr (the PR's full URL) keeps its required approving reviews fresh
// and out of reach of the ship's token. An error means not confirmed. It is a
// variable so tests run without GitHub.
var approvalLookup = ghApprovalLookup

// rulesetRule is a rule of rules/branches/<branch>.
type rulesetRule struct {
	Type       string `json:"type"`
	Parameters struct {
		Count        int  `json:"required_approving_review_count"`
		DismissStale bool `json:"dismiss_stale_reviews_on_push"`
		LastPush     bool `json:"require_last_push_approval"`
	} `json:"parameters"`
	SourceType string `json:"ruleset_source_type"`
	Source     string `json:"ruleset_source"`
	ID         int    `json:"ruleset_id"`
}

// ghApprovalLookup resolves the pull request with gh pr view, then reads the
// rules that apply to its base branch and, failing that, its classic branch
// protection, with plain GETs. It confirms only a rule that requires an approving
// review, dismisses stale reviews on a push or requires approval of the last push,
// and that the token's user cannot bypass: a ruleset whose current_user_can_bypass
// is "never", or classic protection that enforces admins and has no bypass
// allowance.
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
	segs := strings.Split(view.BaseRefName, "/")
	for i, s := range segs {
		segs[i] = url.PathEscape(s)
	}
	base := strings.Join(segs, "/")

	if out, err := get(repo + "/rules/branches/" + base); err == nil {
		var rules []rulesetRule
		if json.Unmarshal(out, &rules) == nil {
			for _, r := range rules {
				if r.Type != "pull_request" || r.Parameters.Count < 1 || !r.Parameters.DismissStale && !r.Parameters.LastPush {
					continue
				}
				if never, err := rulesetUnbypassable(get, repo, r); err == nil && never {
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
			Count        int  `json:"required_approving_review_count"`
			DismissStale bool `json:"dismiss_stale_reviews"`
			LastPush     bool `json:"require_last_push_approval"`
			Bypass       struct {
				Users []json.RawMessage `json:"users"`
				Teams []json.RawMessage `json:"teams"`
				Apps  []json.RawMessage `json:"apps"`
			} `json:"bypass_pull_request_allowances"`
		} `json:"required_pull_request_reviews"`
		EnforceAdmins struct {
			Enabled bool `json:"enabled"`
		} `json:"enforce_admins"`
	}
	if err := json.Unmarshal(out, &prot); err != nil {
		return false, err
	}
	r := prot.Reviews
	return r.Count >= 1 && (r.DismissStale || r.LastPush) && prot.EnforceAdmins.Enabled &&
		len(r.Bypass.Users)+len(r.Bypass.Teams)+len(r.Bypass.Apps) == 0, nil
}

// rulesetUnbypassable reads the ruleset a rule came from and says whether it is
// active and the token's user can never bypass it.
func rulesetUnbypassable(get func(string) ([]byte, error), repo string, r rulesetRule) (bool, error) {
	var endpoint string
	switch r.SourceType {
	case "Repository":
		endpoint = repo + "/rulesets/" + strconv.Itoa(r.ID)
	case "Organization":
		endpoint = "orgs/" + url.PathEscape(r.Source) + "/rulesets/" + strconv.Itoa(r.ID)
	default:
		return false, errors.New("unknown ruleset source")
	}
	if r.ID == 0 {
		return false, errors.New("rule has no ruleset id")
	}
	out, err := get(endpoint)
	if err != nil {
		return false, err
	}
	var rs struct {
		Enforcement        string `json:"enforcement"`
		CurrentUserCanByps string `json:"current_user_can_bypass"`
	}
	if err := json.Unmarshal(out, &rs); err != nil {
		return false, err
	}
	return rs.Enforcement == "active" && rs.CurrentUserCanByps == "never", nil
}
