package cli

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"github.com/jbuffin/admiral/internal/outcomes"
)

// guardReason is what the agent reads when the guard refuses. It names no
// command, so a refusal never repeats text the ship wrote.
const guardReason = "Autopilot ships leave reviews PENDING. The engineer submits them from the inbox."

// mergeReason is what the agent reads when the guard refuses a merge, or a write
// to the rules that gate one.
const mergeReason = "Autopilot ships merge only with gh pr merge --auto, using the panel's merge method, " +
	"on a pull request whose base branch requires an approving review. Any other merge, " +
	"and any change to those branch rules, is for the engineer."

// The rule codes a refusal carries into its guard_denied outcome line.
const (
	ruleGhPrReview    = "gh_pr_review"
	ruleGhAPIEvent    = "gh_api_review_event"
	ruleGhAPISubmit   = "gh_api_review_submit"
	ruleGraphqlReview = "graphql_review"
	ruleMcpReview     = "mcp_review"
	ruleUnparsed      = "unparsed"

	ruleGhPrMerge         = "gh_pr_merge"
	ruleGhAPIMerge        = "gh_api_merge"
	ruleGraphqlMerge      = "graphql_merge"
	ruleGhAPIGateWrite    = "gh_api_gate_write"
	ruleGraphqlGateWrite  = "graphql_gate_write"
	ruleMcpMerge          = "mcp_merge"
	ruleAutoMergeMethod   = "auto_merge_method"
	ruleAutoMergeSteered  = "auto_merge_steered"
	ruleAutoMergeUnproven = "auto_merge_unconfirmed"
)

// reasonFor is the reason a rule refuses with: the review reason for the review
// rules and anything unreadable, the merge reason for the rest.
func reasonFor(rule string) string {
	switch rule {
	case ruleGhPrReview, ruleGhAPIEvent, ruleGhAPISubmit, ruleGraphqlReview, ruleMcpReview, ruleUnparsed:
		return guardReason
	}
	return mergeReason
}

// GuardDenied is a refusal. main exits 2 on it and prints nothing more, because
// Guard has already written the reason to stderr, which Claude Code feeds back to
// the agent as it stands.
type GuardDenied struct{ Rule, Reason string }

func (e *GuardDenied) Error() string { return e.Reason }

// Guard is `admiral guard pretooluse`, the PreToolUse hook that keeps an
// autopilot ship from submitting a pull-request review as APPROVE or
// REQUEST_CHANGES (#115), and from merging a pull request other than with
// `gh pr merge --auto` under the rules of guard_merge.go (#120). It is internal like Hook, but unlike Hook it is
// meant to fail: a refusal exits 2 with the reason on stderr. Outside autopilot
// (ADMIRAL_AUTOPILOT not exactly 1) it does nothing at all.
func Guard(args []string, std IO) error {
	if len(args) == 0 || args[0] != "pretooluse" || os.Getenv("ADMIRAL_AUTOPILOT") != "1" {
		return nil
	}
	raw, _ := io.ReadAll(io.LimitReader(std.In, 1<<20))
	tool, rule := guardDecision(raw)
	if rule == "" {
		return nil
	}
	recordGuardDenied(tool, rule)
	reason := reasonFor(rule)
	_, _ = io.WriteString(std.Err, reason+"\n")
	return &GuardDenied{Rule: rule, Reason: reason}
}

// recordGuardDenied appends the guard_denied line of the ship named in the
// environment. The home comes from ADMIRAL_REPORT first, which Admiral sets and a
// fleet's env cannot override, and never from home.Resolve, which would create
// ~/.admiral. With no ship or no home, or a failed write, the refusal stands
// without a record and without a word on stderr.
func recordGuardDenied(tool, rule string) {
	ship, dir := os.Getenv("ADMIRAL_SHIP"), os.Getenv("ADMIRAL_HOME")
	if report := os.Getenv("ADMIRAL_REPORT"); report != "" {
		dir = filepath.Dir(filepath.Dir(report))
	}
	if ship == "" || dir == "" || dir == "." {
		return
	}
	fields := map[string]any{"rule": rule}
	if safeToken.MatchString(tool) {
		fields["tool"] = tool
	}
	w := outcomes.New(filepath.Join(dir, "outcomes"), func(string, ...any) {})
	w.Enqueue(outcomes.Record{Ship: ship, Event: "guard_denied", Fields: fields}, nil)
	w.Close()
}

// guardDecision is the tool name and the rule that refuses the payload, or no
// rule to allow it. A payload it cannot read is refused as unparsed.
func guardDecision(raw []byte) (tool, rule string) {
	var p struct {
		ToolName  string          `json:"tool_name"`
		ToolInput json.RawMessage `json:"tool_input"`
		Cwd       string          `json:"cwd"`
	}
	if json.Unmarshal(raw, &p) != nil || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return "", ruleUnparsed
	}
	switch {
	case p.ToolName == "Bash":
		var in struct {
			Command *string `json:"command"`
		}
		if json.Unmarshal(p.ToolInput, &in) != nil || in.Command == nil {
			return p.ToolName, ruleUnparsed
		}
		g := guardCheck{cwd: p.Cwd, st: &guardState{}}
		if rule := g.command(*in.Command, 0); rule != "" {
			return p.ToolName, rule
		}
		return p.ToolName, g.confirmMerges()
	case mcpMerges(p.ToolName):
		return p.ToolName, ruleMcpMerge
	case strings.Contains(p.ToolName, "pull_request_review"):
		var in struct {
			Event string `json:"event"`
		}
		if json.Unmarshal(p.ToolInput, &in) == nil && reviewEvent(in.Event) {
			return p.ToolName, ruleMcpReview
		}
	}
	return p.ToolName, ""
}

// reviewEvent says whether v is a review event an autopilot ship may not submit.
func reviewEvent(v string) bool {
	return strings.EqualFold(v, "APPROVE") || strings.EqualFold(v, "REQUEST_CHANGES")
}

var (
	// eventText is an event position in text a command pipes to gh api: "event"
	// (with escaped quotes, a jq literal or a here-string around it) then the value.
	eventText = regexp.MustCompile(`(?i)\bevent\W{0,6}(approve|request_changes)\b`)
	// eventExpansion is an event position whose value is an expansion, in the text
	// of a command line (a here-string, a heredoc) or in a script that was parsed again.
	eventExpansion = regexp.MustCompile("(?i)\\bevent\\W{0,6}(?:\\$|`|" + marker + ")")
	// gqlSlotExpands is the event or input slot of a mutation holding an expansion.
	gqlSlotExpands = regexp.MustCompile(`(?i)\b(?:event|input)\W{0,6}` + placeholder)
	// gqlBound is the event or input slot of a mutation bound to a variable.
	gqlBound = regexp.MustCompile(`(?i)\b(?:event|input)\s*:\s*[$` + placeholder + `]`)
	// gqlEvent is an event name anywhere in a mutation: `event : APPROVE`, a
	// variable's default, or a value the guard cannot pair up with its field.
	gqlEvent = regexp.MustCompile(`(?i)\b(approve|request_changes)\b`)
)

// guardCheck holds what resolving a command needs of the payload: the directory
// that relative files named on the command line are read from, and the state the
// walk over the command line builds up (see merge.go).
type guardCheck struct {
	cwd string
	st  *guardState
}

// ghSubcommand drops the flags that sit before gh's subcommand words (-R and
// --repo, --hostname with their values, and any other flag) until it reaches
// `pr review`, `pr merge` or `api`, after which the arguments are left as they are. gh
// accepts -R before `review` as well as before `pr`.
func ghSubcommand(args []string) []string {
	var words []string
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "-R" || a == "--repo" || a == "--hostname":
			i++
		case strings.HasPrefix(a, "-"):
		default:
			words = append(words, a)
			if words[0] == "api" || len(words) == 2 && words[0] == "pr" && (words[1] == "review" || words[1] == "merge") {
				return append(words, args[i+1:]...)
			}
		}
	}
	return words
}

// gh is the rule for a gh call: `pr review`, `pr merge` and `api` are checked,
// anything else is allowed, because no other gh command can submit a review or
// merge (`pr ready` only takes a pull request out of draft).
func (g guardCheck) gh(args []string, line *lineText) string {
	sub := ghSubcommand(args)
	switch {
	case len(sub) >= 2 && sub[0] == "pr" && sub[1] == "review":
		if prReviewSubmits(sub[2:]) {
			return ruleGhPrReview
		}
	case len(sub) >= 2 && sub[0] == "pr" && sub[1] == "merge":
		return g.prMerge(sub[2:], ghGlobalRepo(args))
	case len(sub) >= 2 && (sub[0] == "pr" && sub[1] == "checkout" || sub[0] == "repo" && sub[1] == "set-default"):
		g.st.steered = true
	case len(sub) >= 1 && sub[0] == "api":
		return g.api(sub[1:], line)
	}
	return ""
}

// prReviewSubmits follows pflag over the arguments of gh pr review: -b, -F and
// -R (and their long forms) take a value, which may start with a dash, a
// short cluster ends at a value-taking flag, and -- ends the flags. It reports
// --approve or --request-changes (-a, -r) in any form. A word the guard cannot
// read counts too (the PR, a word where a flag could stand, an unquoted flag
// value that could split into flags), unless a literal --comment or -c is also
// there: gh takes one event flag, so the others could not submit.
func prReviewSubmits(args []string) bool {
	unsure, comment := false, false
	value := func(i *int, attached string) {
		if attached == "" && *i+1 < len(args) {
			*i++
			attached = args[*i]
		}
		unsure = unsure || hasSplit(attached)
	}
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "--":
			i = len(args)
		case strings.HasPrefix(a, "--"):
			name, val, has := strings.Cut(a[2:], "=")
			switch name {
			case "approve", "request-changes":
				return true
			case "comment":
				comment = comment || !has || val != "false"
			case "body", "body-file", "repo":
				if has {
					unsure = unsure || hasSplit(val)
				} else {
					value(&i, "")
				}
			default:
				unsure = unsure || hasPlaceholder(name)
			}
		case len(a) > 1 && a[0] == '-':
		cluster:
			for j := 1; j < len(a) && a[j] != '='; j++ {
				switch {
				case a[j] == 'a' || a[j] == 'r':
					return true
				case a[j] == 'c':
					comment = true
				case a[j] == placeholder[0]:
					unsure = true
				case strings.IndexByte("bFR", a[j]) >= 0:
					value(&i, a[j+1:])
					break cluster
				}
			}
		default:
			unsure = unsure || hasPlaceholder(a)
		}
	}
	return unsure && !comment
}

// apiField is a -f or -F field of gh api; typed is -F, the one that reads @file.
type apiField struct {
	key, val string
	typed    bool
}

type apiArgs struct {
	positional []string
	fields     []apiField
	input      string
	hasInput   bool
	method     string // -X or --method; "" when not given
	fieldFlag  bool   // a -f or -F was given, which makes gh send a POST
}

// parseAPI splits gh api's arguments by pflag's rules; the flags that take a
// value are those of `gh api --help`, in attached forms too (-XPOST, --method=POST).
func parseAPI(args []string) (p apiArgs) {
	long := map[string]string{"method": "", "header": "", "raw-field": "f", "field": "F", "input": "i",
		"jq": "", "template": "", "preview": "", "hostname": "", "cache": ""}
	short := map[byte]string{'X': "method", 'H': "header", 'f': "raw-field", 'F': "field", 'q': "jq", 't': "template", 'p': "preview"}
	set := func(name, val string) {
		switch long[name] {
		case "f", "F":
			p.fieldFlag = true
			// A field with no = is dropped by gh, unless an expansion could hold one.
			if k, v, ok := strings.Cut(val, "="); ok || hasPlaceholder(val) {
				p.fields = append(p.fields, apiField{k, v, long[name] == "F"})
			}
		case "i":
			p.input, p.hasInput = val, true
		}
		if name == "method" {
			p.method = val
		}
	}
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "--":
			p.positional = append(p.positional, args[i+1:]...)
			return p
		case strings.HasPrefix(a, "--"):
			name, val, has := strings.Cut(a[2:], "=")
			if _, valued := long[name]; !valued {
				continue
			}
			if !has && i+1 < len(args) {
				i++
				val = args[i]
			}
			set(name, val)
		case len(a) > 1 && a[0] == '-':
			for j := 1; j < len(a); j++ {
				name, valued := short[a[j]]
				if !valued {
					continue
				}
				val := a[j+1:]
				if val == "" && i+1 < len(args) {
					i++
					val = args[i]
				}
				set(name, val)
				break
			}
		default:
			p.positional = append(p.positional, a)
		}
	}
	return p
}

// endpointSegments is the path of a gh api endpoint, less a host, an /api/v3
// prefix, a leading slash, and a query string or fragment.
func endpointSegments(endpoint string) []string {
	if i := strings.Index(endpoint, "://"); i >= 0 {
		endpoint = endpoint[i+3:]
		if j := strings.Index(endpoint, "/"); j >= 0 {
			endpoint = endpoint[j+1:]
		} else {
			endpoint = ""
		}
	}
	endpoint = endpoint[:strings.IndexAny(endpoint+"?", "?#")]
	endpoint = strings.TrimPrefix(strings.TrimPrefix(endpoint, "/"), "api/v3/")
	return strings.Split(strings.Trim(endpoint, "/"), "/")
}

// routeTails are the readings of a gh api path as repos/<o>/<r>/<tail>: an
// expansion in the owner position may span owner and repo ($REPO holding o/r), and
// one first segment may stand for repos/<o>/<r> ($BASE).
func routeTails(seg []string) (tails [][]string) {
	switch {
	case len(seg) >= 3 && seg[0] == "repos":
		tails = append(tails, seg[3:])
		if hasPlaceholder(seg[1]) {
			tails = append(tails, seg[2:])
		}
	case len(seg) >= 2 && seg[0] == "repositories":
		tails = append(tails, seg[2:])
	case hasPlaceholder(seg[0]):
		tails = append(tails, seg[1:])
	}
	return tails
}

// reviewRoute says which review endpoint the segments of a gh api path name:
// "reviews" for repos/<o>/<r>/pulls/<n>/reviews, "events" for the submit
// endpoint under it, "other" for a comments or review path that cannot submit,
// or "". An expansion matches a segment, but "other" needs its word spelled out,
// so an expansion cannot stand in for reviews or events there.
func reviewRoute(seg []string) string {
	is := func(s, lit string) bool { return hasPlaceholder(s) || s == lit }
	for _, t := range routeTails(seg) {
		switch {
		case len(t) == 3 && is(t[0], "pulls") && is(t[2], "reviews"):
			return "reviews"
		case len(t) == 5 && is(t[0], "pulls") && is(t[2], "reviews") && is(t[4], "events"):
			return "events"
		case len(t) >= 3 && len(t) <= 5 && (is(t[0], "pulls") || is(t[0], "issues")) && t[2] == "comments",
			len(t) >= 4 && len(t) <= 5 && is(t[0], "pulls") && t[2] == "reviews" && (len(t) == 4 || t[4] == "comments" || t[4] == "dismissals"):
			return "other"
		}
	}
	return ""
}

// api is the rule for gh api: the review rules, then the merge rules
// (guard_merge.go). The review rules cover the reviews endpoint with a submitting
// event, the submit endpoint, or GraphQL with a review mutation. An endpoint with an
// expansion that matches no route could be either, so an event that submits (or
// that the guard cannot read) refuses, and so does the text rule on the call.
func (g guardCheck) api(args []string, line *lineText) string {
	if rule := g.apiReview(args, line); rule != "" {
		return rule
	}
	return g.apiMerge(args, line)
}

func (g guardCheck) apiReview(args []string, line *lineText) string {
	p := parseAPI(args)
	if len(p.positional) == 0 {
		return ""
	}
	seg := endpointSegments(p.positional[0])
	if len(seg) == 2 && seg[0] == "api" && seg[1] == "graphql" {
		seg = seg[1:] // GHE serves GraphQL at /api/graphql
	}
	switch route := reviewRoute(seg); {
	case len(seg) == 1 && seg[0] == "graphql":
		if g.graphqlSubmits(p, line, strings.Join(args, " ")) {
			return ruleGraphqlReview
		}
	case route == "reviews":
		if g.eventSubmits(p, line) {
			return ruleGhAPIEvent
		}
	case route == "events":
		return ruleGhAPISubmit
	case route == "" && hasPlaceholder(p.positional[0]):
		if g.eventSubmits(p, line) {
			return ruleGhAPIEvent
		}
		if reviewEventText(strings.Join(args, " ")) {
			return ruleUnparsed
		}
	}
	return ""
}

// readFile reads a file named on a command line, relative to the payload's cwd,
// at most 1 MiB. Only a regular file is read: a pipe, a device or the guard's
// own stdin would give the guard nothing to judge.
func (g guardCheck) readFile(name string) (string, bool) {
	if name == "/dev/stdin" || strings.HasPrefix(name, "/dev/fd/") {
		return "", false
	}
	if !filepath.IsAbs(name) {
		name = filepath.Join(g.cwd, name)
	}
	if st, err := os.Stat(name); err != nil || !st.Mode().IsRegular() {
		return "", false
	}
	f, err := os.Open(name)
	if err != nil {
		return "", false
	}
	defer f.Close()
	b, err := io.ReadAll(io.LimitReader(f, 1<<20+1))
	return string(b), err == nil && len(b) <= 1<<20
}

// eventSubmits reports an APPROVE or REQUEST_CHANGES event in a gh api call to
// the reviews endpoint. A source it cannot read counts as one.
func (g guardCheck) eventSubmits(p apiArgs, line *lineText) bool {
	for _, f := range p.fields {
		if hasPlaceholder(f.key) {
			return true // it could be event
		}
		if f.key != "event" {
			continue
		}
		v := f.val
		if hasPlaceholder(v) {
			return true // a pending review has no event field at all
		}
		if f.typed && strings.HasPrefix(v, "@") {
			b, ok := g.readFile(v[1:])
			if v == "@-" || !ok {
				return true
			}
			v = strings.TrimSpace(b)
		}
		if reviewEvent(v) {
			return true
		}
	}
	if !p.hasInput {
		return false
	}
	if p.input == "-" {
		return eventText.MatchString(line.text) || eventExpansion.MatchString(line.text)
	}
	b, ok := g.readFile(p.input)
	var body struct {
		Event string `json:"event"`
	}
	return !ok || json.Unmarshal([]byte(b), &body) != nil || reviewEvent(body.Event)
}

// anyReviewEvent reports an APPROVE or REQUEST_CHANGES string anywhere in a
// decoded JSON value.
func anyReviewEvent(v any) bool {
	switch v := v.(type) {
	case string:
		return reviewEvent(v)
	case []any:
		return slices.ContainsFunc(v, anyReviewEvent)
	case map[string]any:
		for _, x := range v {
			if anyReviewEvent(x) {
				return true
			}
		}
	}
	return false
}

// graphqlSubmits reports a submitPullRequestReview or addPullRequestReview
// mutation with an APPROVE or REQUEST_CHANGES event, in its text or in a
// variable. A source it cannot read counts as one when the mutation is there,
// and so does a value with an expansion: beside a query that is not exact it
// could be anything, and beside a mutation it could be the event when the
// mutation binds its event or input to a variable (a variable that only names
// the pull request opens a PENDING review). An expansion in the event or input
// slot of the mutation itself refuses.
func (g guardCheck) graphqlSubmits(p apiArgs, line *lineText, own string) bool {
	var queries, vars []string
	unknownVar := false // a variable from stdin or a file that cannot be read
	eventVar := false   // an --input body has APPROVE or REQUEST_CHANGES among its variables
	for _, f := range p.fields {
		v := f.val
		if f.typed && strings.HasPrefix(v, "@") {
			if v == "@-" && f.key == "query" {
				queries = append(queries, line.text)
				continue
			}
			b, ok := g.readFile(v[1:])
			switch {
			case f.key == "query" && !ok:
				return true
			case !ok || v == "@-":
				unknownVar = true
				continue
			}
			v = b
		}
		if f.key == "query" {
			queries = append(queries, v)
		} else {
			vars = append(vars, strings.TrimSpace(v))
		}
	}
	if p.hasInput && p.input == "-" {
		queries = append(queries, line.text)
	} else if p.hasInput {
		b, ok := g.readFile(p.input)
		var body struct {
			Query     string `json:"query"`
			Variables any    `json:"variables"`
		}
		if !ok || json.Unmarshal([]byte(b), &body) != nil {
			return true
		}
		queries = append(queries, body.Query)
		eventVar = anyReviewEvent(body.Variables)
	}
	varsUnsure := unknownVar || eventVar || slices.ContainsFunc(vars, reviewEvent)
	varsExpand := slices.ContainsFunc(vars, hasPlaceholder)
	for _, q := range queries {
		mutation := strings.Contains(q, "submitPullRequestReview") || strings.Contains(q, "addPullRequestReview")
		switch {
		case hasPlaceholder(q) && !mutation:
			if varsUnsure || varsExpand || reviewEventText(own) {
				return true
			}
			if onlyExpansion(q) && line.wholeRule() != "" {
				return true // the query is only an expansion: it was set elsewhere on the line
			}
		case mutation && (gqlEvent.MatchString(q) || varsUnsure || gqlSlotExpands.MatchString(q) || varsExpand && gqlBound.MatchString(q)):
			return true
		}
	}
	return false
}
