package cli

import (
	"encoding/json"
	"regexp"
	"slices"
	"strings"

	"github.com/jbuffin/admiral/internal/launcher"
	"mvdan.cc/sh/v3/syntax"
)

// The merge half of the guard (#120, #261). An autopilot ship merges nothing: the
// guard refuses every gh pr merge that can merge, a change of a pull request's
// base, and a write to the rules that gate a merge. It is best effort: it reads
// the text of a Bash call and nothing else (see "Known gaps" in
// docs/agents/codebase.md).

// guardState is what the walk over one command line builds up: whether a cd
// moved the shell, whether a startup file could run, and the standard input the
// line shows.
type guardState struct {
	moved   bool // a cd, pushd or popd: relative files are not read
	startup bool // BASH_ENV, ENV or ZDOTDIR set on the line: a shell runs a startup file
	// stdin is the text on the standard input of the call being checked, when
	// the line shows it literally (stdinOK); feeders holds the literal output of
	// the statement on the left of each pipe.
	stdin   string
	stdinOK bool
	feeders map[*syntax.Stmt]string
}

// mcpMergeTool matches the MCP tools whose name has merge or enqueue in it: the
// matcher in the ship's settings, so a tool that reaches the guard is one this
// matches.
var mcpMergeTool = regexp.MustCompile(launcher.MergeToolMatcher)

func mcpMerges(tool string) bool {
	if !mcpMergeTool.MatchString(tool) {
		return false
	}
	name := tool[strings.LastIndex(tool, "__")+2:]
	return !slices.ContainsFunc([]string{"list_", "get_", "search_"}, func(p string) bool { return strings.HasPrefix(name, p) })
}

func authReadsToken(sub string, args []string) bool {
	return sub == "token" || sub == "git-credential" || sub == "status" && slices.ContainsFunc(args, func(a string) bool {
		if name, val, has := strings.Cut(a, "="); name == "--show-token" {
			on, ok := boolFlag(has, val)
			return on || !ok
		}
		return shortFlagHas(a, "t")
	})
}

// Text the guard could not parse is judged by what it mentions.
var (
	prMergeText    = regexp.MustCompile(`(?is)\bpr\b.*\bmerge\b`)
	mergeRouteText = regexp.MustCompile(`(?i)\bpulls/\S*merge|/merges\b`)
	gateRouteText  = regexp.MustCompile(`(?i)\brulesets\b|/protection\b`)
)

func mergeTextRule(text string) string {
	switch {
	case prMergeText.MatchString(text):
		return ruleGhPrMerge
	case mergeRouteText.MatchString(text):
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

// prMerge is the rule for gh pr merge, given the arguments after `merge`. An
// autopilot ship merges nothing, so every form that can merge is refused, --auto
// included. Only --disable-auto alone is allowed: it removes a merge and adds
// none, so not beside --auto, a method or --delete-branch. Its flags are followed as pflag does: -b, -F, -R, -t and -A (and their
// long forms) take a value, which may start with a dash, a short cluster ends at
// a value-taking flag, and -- ends the flags.
func (g guardCheck) prMerge(args []string) string {
	disable, admin := false, false
	other := false  // a flag that merges or deletes a branch beside --disable-auto
	unsure := false // a flag the guard cannot read, or a value that could split into flags
	value := func(i *int, attached string) string {
		if attached == "" && *i+1 < len(args) {
			*i++
			attached = args[*i]
		}
		return attached
	}
	boolean := func(has bool, val string) bool {
		on, ok := boolFlag(has, val)
		unsure = unsure || !ok // gh would refuse the flag or read it another way
		return on
	}
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "--":
			i = len(args)
		case strings.HasPrefix(a, "--"):
			name, val, has := strings.Cut(a[2:], "=")
			switch name {
			case "disable-auto":
				disable = boolean(has, val)
			case "admin":
				admin = boolean(has, val)
			case "auto", "squash", "merge", "rebase", "delete-branch":
				other = boolean(has, val) || other
			case "repo":
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
				case a[j] == placeholder[0]:
					unsure = true
				case strings.IndexByte("smrd", a[j]) >= 0:
					other = true
				case a[j] == 'R':
					value(&i, a[j+1:])
					break cluster
				case strings.IndexByte("bFtA", a[j]) >= 0:
					unsure = unsure || hasSplit(value(&i, a[j+1:]))
					break cluster
				}
			}
		}
	}
	if disable && !admin && !other && !unsure {
		return ""
	}
	return ruleGhPrMerge
}

// gh sends an implicit POST when it is given fields or an --input and no method.
func (p apiArgs) writes() bool {
	if p.method == "" {
		return p.fieldFlag || p.hasInput
	}
	return !strings.EqualFold(p.method, "GET") && !strings.EqualFold(p.method, "HEAD")
}

// apiMerge: an expansion in a segment matches any word there, and a method held
// in a variable is a write.
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
	// A word that could be -X, or an endpoint followed by more words, hides the
	// method and the route.
	if len(p.positional) > 1 && hasPlaceholder(p.positional[0]) {
		return ruleGhAPIMerge
	}
	if !p.writes() && !p.flagWord {
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
		case len(rest) == 0 && routeExtends(seg[:last], strings.EqualFold(p.method, "PATCH")):
			return ruleGhAPIMerge
		case len(rest) > 0 && slices.Contains([]string{"merge", "merges"}, rest[len(rest)-1]):
			return ruleGhAPIMerge
		case slices.Contains(rest, "protection") || slices.Contains(rest, "rulesets"):
			return ruleGhAPIGateWrite
		}
	}
	return ""
}

func (g guardCheck) retargets(p apiArgs, line *lineText) bool {
	for _, f := range p.fields {
		if f.key == "base" || hasPlaceholder(f.key) {
			return true
		}
	}
	if !p.hasInput {
		return false
	}
	// The body is decoded, so a \u escape can't hide the key; one that is not a
	// JSON object, or is not shown, is refused.
	b, ok := g.inputBody(p.input)
	body, ok2 := decodeBody(b)
	_, has := body["base"]
	return !ok || !ok2 || has
}

// routeExtends: under comments and the like an expansion would have to carry a
// "../" to get anywhere, and GitHub does not resolve one.
func routeExtends(prefix []string, patch bool) bool {
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
	case t[0] == "issues" && len(t) == 1:
		return !patch // issues/$N is an edit of an issue, a PATCH; another method could be anything
	case t[0] == "pulls":
		return len(t) == 2 || len(t) == 1 && !patch // pulls/$N is a PATCH of a pull request, or the merge route's PUT
	}
	return t[0] == "branches" || t[0] == "rulesets" || hasPlaceholder(t[0])
}

// The branch may hold slashes.
func protectionRoute(t []string) bool {
	if len(t) < 3 || t[0] != "branches" && !hasPlaceholder(t[0]) {
		return false
	}
	return slices.ContainsFunc(t[2:], func(s string) bool { return s == "protection" || hasPlaceholder(s) })
}

var (
	graphqlMerges = []string{"mergePullRequest", "enablePullRequestAutoMerge", "mergeBranch",
		"enqueuePullRequest", "dequeuePullRequest"}
	graphqlGates = []string{"createBranchProtectionRule", "updateBranchProtectionRule", "deleteBranchProtectionRule",
		"createRepositoryRuleset", "updateRepositoryRuleset", "deleteRepositoryRuleset"}
	graphqlRetargets = []string{"updatePullRequest"}
)

// A field call whose name holds an expansion could be any mutation.
var gqlNameExpands = regexp.MustCompile(`(?:^|[{}\s])\w*` + placeholder + `\w*\s*[({]`)

var gqlInputBound = regexp.MustCompile(`input\s*:\s*[$` + placeholder + `]`)

var gqlString = regexp.MustCompile(`"(?:[^"\\]|\\.)*"`)

// An expansion outside every string and not after a colon could be a field name,
// a keyword, a whole selection.
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

// graphqlUnreadable runs before the review rules so an unreadable body is not
// taken for a review.
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

// graphqlMutates counts a query the guard cannot read as a merge.
func (g guardCheck) graphqlMutates(p apiArgs, line *lineText) string {
	var queries []string
	for _, f := range p.fields {
		if f.key != "query" {
			continue
		}
		v := f.val
		if f.typed && strings.HasPrefix(v, "@") {
			b, ok := g.inputBody(v[1:]) // @- is stdin
			if !ok {
				return ruleGraphqlMerge
			}
			v = b
		}
		queries = append(queries, v)
	}
	if p.hasInput {
		b, ok := g.inputBody(p.input)
		body, ok2 := decodeBody(b)
		query, ok3 := bodyString(body, "query")
		var vars any
		if raw, has := body["variables"]; has && json.Unmarshal(raw, &vars) != nil || !ok || !ok2 || !ok3 {
			return ruleGraphqlMerge
		}
		text, _ := json.Marshal(vars)
		queries = append(queries, query, string(text))
	}
	expanded := false
	for _, q := range queries {
		if hasPlaceholder(q) {
			// An expansion can be a value that closes its string and adds a mutation.
			expanded = true
			if onlyExpansion(q) || strings.Contains(q, "mutation") || gqlOpaque(q) {
				return ruleGraphqlMerge
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
	// operationName picks which of several operations runs, which an expansion
	// could have added.
	if expanded && slices.ContainsFunc(p.fields, func(f apiField) bool { return f.key == "operationName" }) {
		return ruleGraphqlMerge
	}
	return ""
}
