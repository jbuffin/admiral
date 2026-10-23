package cli

import (
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"mvdan.cc/sh/v3/syntax"
)

var assignment = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*=`)

// placeholder stands in a word for each expansion in it ($VAR, $(…), $((…)),
// <(…)): the word keeps its literal parts, and hasPlaceholder says it is not
// exact.
const placeholder = "\x00"

// splitMark follows a placeholder that came from an expansion outside quotes: the
// shell splits its value into words, so it could be several arguments.
const splitMark = "\x01"

// marker takes the place of a placeholder in a script that is parsed again. It is
// a plain word, so it survives quoting that would turn `$_` into text; wordText
// maps it back.
const marker = "__admiral_x__"

func hasPlaceholder(s string) bool { return strings.Contains(s, placeholder) }
func hasSplit(s string) bool       { return strings.Contains(s, splitMark) }

// lineText is a command line and what the text rule says of parts of it. The rule
// runs on the smallest text a fallback is about, and never twice on the same
// text: a call that sits inside a span already checked is skipped, because that
// span holds all its words.
type lineText struct {
	text                   string
	stmtFrom, stmtTo       int // the top-level statement being walked
	stmtRule               string
	stmtDone               bool
	coveredFrom, coveredTo int // the span the text rule last ran on
	whole                  string
	wholeDone              bool
}

func (l *lineText) enter(s *syntax.Stmt) {
	l.stmtFrom, l.stmtTo = l.offsets(s)
	l.stmtDone = false
}

// offsets is where a node sits in the text.
func (l *lineText) offsets(n syntax.Node) (from, to int) {
	to = min(int(n.End().Offset()), len(l.text))
	return min(int(n.Pos().Offset()), to), to
}

// wholeRule is the long-spelling text rule (review and merge) on the whole line, for a judgment with
// no text of its own: the program, script, file or query was set on another part
// of the line (a variable, a file written earlier). Short flags are not matched here.
func (l *lineText) wholeRule() string {
	if !l.wholeDone {
		if reviewEventText(l.text) {
			l.whole = ruleUnparsed
		} else {
			l.whole = mergeTextRule(l.text)
		}
		l.wholeDone = true
	}
	return l.whole
}

// onlyExpansion says whether a word or script is nothing but expansions and
// whitespace, so its content is set elsewhere on the line.
func onlyExpansion(s string) bool { return strings.Trim(s, placeholder+splitMark+" \t\r\n") == "" }

// stmt is the text rule on the top-level statement being walked: where a shell
// reads its script from a pipe or a file, the script is not in the call itself.
// A statement that has none of it falls back to the whole line.
func (l *lineText) stmt() string {
	if !l.stmtDone {
		l.stmtRule, l.stmtDone = textRule(l.text[l.stmtFrom:l.stmtTo]), true
	}
	if l.stmtRule != "" {
		return l.stmtRule
	}
	return l.wholeRule()
}

// span is the text rule on one call, for a program the guard cannot name.
func (l *lineText) span(n syntax.Node) string {
	from, to := l.offsets(n)
	if from >= l.coveredFrom && to <= l.coveredTo {
		return ""
	}
	l.coveredFrom, l.coveredTo = from, to
	return textRule(l.text[from:to])
}

// maxShellDepth is how deep a script inside a script (sh -c, eval, a heredoc
// fed to a shell) is followed. Real command lines nest two or three deep, and
// a cap keeps a crafted one from costing more than a refusal.
const maxShellDepth = 8

// command is the rule that refuses a shell command line, or "". It parses the
// line and looks at every simple command in it, wherever it sits (lists,
// pipelines, loops, functions, substitutions). When the line does not parse as
// bash, it still looks at the statements that did, then at what zsh makes of
// it, and then applies the text rule.
func (g guardCheck) command(text string, depth int) string {
	if depth > maxShellDepth {
		return ruleUnparsed
	}
	line := &lineText{text: text}
	rule := ""
	visit := func(n syntax.Node) bool {
		if s, ok := n.(*syntax.Stmt); ok && rule == "" {
			switch cmd := s.Cmd.(type) {
			case *syntax.CallExpr:
				rule = g.call(cmd, s.Redirs, line, depth)
			case *syntax.ForClause:
				if it, ok := cmd.Loop.(*syntax.WordIter); ok && slices.Contains(steerNames, it.Name.Value) {
					g.st.steered = true
				}
			case *syntax.DeclClause:
				for _, a := range cmd.Args {
					if a.Name != nil && steersGh(a.Name.Value+"=") {
						g.st.steered = true
					}
				}
			}
		}
		return rule == ""
	}
	walk := func(lang syntax.LangVariant) error {
		return syntax.NewParser(syntax.Variant(lang)).Stmts(strings.NewReader(text), func(s *syntax.Stmt) bool {
			line.enter(s)
			syntax.Walk(s, visit)
			return rule == ""
		})
	}
	if walk(syntax.LangBash) == nil {
		// The parser reads a trailing backslash as a continuation; the rules call it unparseable.
		if trail := len(text) - len(strings.TrimRight(text, `\`)); trail%2 == 1 {
			if r := textRule(text); r != "" {
				return r
			}
		}
		return rule
	}
	if rule == "" {
		_ = walk(syntax.LangZsh)
	}
	if rule == "" {
		rule = textRule(text)
	}
	return rule
}

// shortFlag is a short flag word whose letters include a or r (-a, -r, -ab),
// which in a line about a review is --approve or --request-changes.
var shortFlag = regexp.MustCompile(`(?:^|[^\w-])-[A-Za-z]*[ar][A-Za-z]*(?:=\S*)?(?:[^\w-]|$)`)

// textRule is the fallback for a command the guard cannot read: it refuses text
// that mentions a review and an event that submits one, in a long or short
// spelling, and text that mentions a merge or a write to the gate (mergeTextRule).
func textRule(text string) string {
	if r := mergeTextRule(text); r != "" {
		return r
	}
	if reviewEventText(text) || strings.Contains(strings.ToLower(text), "review") && shortFlag.MatchString(text) {
		return ruleUnparsed
	}
	return ""
}

// reviewEventText says whether text mentions a review and an event that submits
// one, in a long spelling.
func reviewEventText(text string) bool {
	t := strings.ToLower(text)
	if !strings.Contains(t, "review") {
		return false
	}
	for _, w := range []string{"approve", "request_changes", "request-changes"} {
		if strings.Contains(t, w) {
			return true
		}
	}
	return false
}

// wrapper is a command that runs the command after its flags: short lists the
// letters of the flags that take a value, long their long names, positionals the
// words (a duration) that come before the command, and script the flag whose
// value is itself a command line.
type wrapper struct {
	short       string
	long        []string
	positionals int
	script      string
}

var wrappers = map[string]wrapper{
	"env":        {"uCSPLU", []string{"unset", "chdir", "split-string"}, 0, "S"},
	"command":    {},
	"exec":       {"a", nil, 0, ""},
	"nohup":      {},
	"time":       {"of", []string{"output", "format"}, 0, ""},
	"timeout":    {"sk", []string{"signal", "kill-after"}, 1, ""},
	"nice":       {"n", []string{"adjustment"}, 0, ""},
	"sudo":       {"ugCDhprtUT", []string{"user", "group", "chdir", "chroot", "host", "prompt", "role", "type", "other-user", "command-timeout"}, 0, ""},
	"stdbuf":     {"ioe", []string{"input", "output", "error"}, 0, ""},
	"caffeinate": {"tw", nil, 0, ""},
	"xargs":      {"ILnPsdEaJRS", []string{"replace", "max-lines", "max-args", "max-procs", "max-chars", "delimiter", "eof", "arg-file"}, 0, ""},
	// zsh precommand modifiers.
	"noglob": {}, "nocorrect": {}, "builtin": {}, "-": {}, "repeat": {positionals: 1},
}

// shells run a script given with -c, or read one from stdin.
var shells = []string{"sh", "bash", "zsh", "dash", "ksh", "fish"}

// call is the rule for one simple command: gh after assignments and wrappers, a
// shell given a script, or eval. A program word with an expansion is gh when
// what follows looks like `pr … review` or `api`, and is otherwise unknown,
// which gets the text rule on the call.
func (g guardCheck) call(c *syntax.CallExpr, redirs []*syntax.Redirect, line *lineText, depth int) string {
	words := make([]string, len(c.Args))
	for i, w := range c.Args {
		words[i] = wordText(w)
	}
	for _, a := range c.Assigns {
		if a.Name == nil {
			continue
		}
		if slices.Contains(aliasTables, a.Name.Value) {
			return ruleShellAlias
		}
		if steersGh(a.Name.Value + "=") {
			g.st.steered = true
		}
	}
	for len(words) > 0 {
		if hasPlaceholder(words[0]) {
			if sub := ghSubcommand(words[1:]); len(sub) >= 1 && slices.Contains([]string{"api", "alias", "extension", "ext", "auth"}, sub[0]) || len(sub) >= 2 && sub[0] == "pr" && (sub[1] == "review" || sub[1] == "merge" || sub[1] == "edit") {
				return g.gh(words[1:], line)
			}
			if r := line.span(c); r != "" || !onlyExpansion(words[0]) {
				return r
			}
			return line.wholeRule()
		}
		w, args := strings.TrimPrefix(filepath.Base(words[0]), "="), words[1:] // =gh is zsh's path lookup
		if spec, ok := wrappers[w]; ok {
			var script string
			args, script = skipWrapperFlags(spec, args)
			if script != "" {
				if r := g.command(script, depth+1); r != "" {
					return r
				}
			}
			if slices.ContainsFunc(words[1:len(words)-len(args)], steersWrapper) || w == "xargs" {
				g.st.steered = true // xargs adds the words it reads from stdin
			}
			words = args
			continue
		}
		if steersBuiltin(w, args, line.text[min(int(c.Pos().Offset()), len(line.text)):min(int(c.End().Offset()), len(line.text))]) {
			g.st.steered = true
		}
		switch {
		case w == "cd" || w == "pushd" || w == "popd":
			g.st.steered = true
		case w == "alias" || w == "unalias" || w == "hash" && hashRenames(args):
			return ruleShellAlias
		case w == "source" || w == ".":
			return ruleShellScript
		case w == "git":
			if gitSteers(args) {
				g.st.steered = true
			}
		case w == "gh":
			return g.gh(args, line)
		case slices.Contains(shells, w):
			return g.shell(c, args, redirs, line, depth)
		case w == "eval":
			return g.script(c, strings.Join(args, " "), line, depth)
		case w == "trap":
			if len(args) > 0 && args[0] == "--" {
				args = args[1:]
			}
			if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
				return g.script(c, args[0], line, depth) // the handler is a script
			}
		}
		return ""
	}
	return ""
}

// aliasTables are the shell variables that define names for commands: zsh's
// aliases, functions and commands, and bash's BASH_ALIASES and BASH_CMDS.
var aliasTables = []string{"aliases", "galiases", "saliases", "functions", "commands", "BASH_ALIASES", "BASH_CMDS"}

// hashRenames says whether a hash call binds a name to a path (bash's -p, zsh's
// name=path), which runs another program under that name.
func hashRenames(args []string) bool {
	return slices.ContainsFunc(args, func(a string) bool {
		return strings.Contains(a, "=") || len(a) > 1 && a[0] == '-' && a[1] != '-' && strings.Contains(a, "p")
	})
}

var markWords = strings.NewReplacer(placeholder+splitMark, marker, placeholder, marker)

// script is the rule for a script a shell or eval is given. Its expansions are
// words the shell would have put there, so it is parsed with a marker for each,
// which keeps its meaning whatever quotes surround it; a script that is nothing
// but an expansion is unknown, which gets the text rule on the call.
func (g guardCheck) script(c *syntax.CallExpr, text string, line *lineText, depth int) string {
	if onlyExpansion(text) {
		if r := line.span(c); r != "" {
			return r
		}
		if r := line.wholeRule(); r != "" {
			return r
		}
		return ruleShellScript // the script is built or read somewhere the guard cannot see
	}
	return g.command(markWords.Replace(text), depth+1)
}

// skipWrapperFlags drops a wrapper's flags (read letter by letter, a letter that
// takes a value taking the rest of its cluster or the next word), the
// assignments after them and the positionals it expects, and returns the command
// that follows. It also returns the command line a script flag held (env -S).
func skipWrapperFlags(spec wrapper, args []string) (rest []string, script string) {
	value := func(inline string) string {
		if inline != "" {
			return inline
		}
		if len(args) > 0 {
			v := args[0]
			args = args[1:]
			return v
		}
		return ""
	}
	for len(args) > 0 {
		a := args[0]
		if a == "--" {
			args = args[1:]
			break
		}
		if len(a) < 2 || a[0] != '-' {
			break
		}
		args = args[1:]
		if a[1] == '-' {
			name, v, has := strings.Cut(a[2:], "=")
			if slices.Contains(spec.long, name) {
				if !has {
					v = value("")
				}
				if spec.script != "" && name == "split-string" {
					script = v
				}
			}
			continue
		}
		for j := 1; j < len(a); j++ {
			if strings.IndexByte(spec.short, a[j]) >= 0 {
				v := value(a[j+1:])
				if spec.script != "" && a[j] == spec.script[0] {
					script = v
				}
				break
			}
		}
	}
	for len(args) > 0 && assignment.MatchString(args[0]) {
		args = args[1:]
	}
	for i := 0; i < spec.positionals && len(args) > 0; i++ {
		args = args[1:]
	}
	return args, script
}

// shell is the rule for a shell. Its options are skipped, with the values of
// -o, -O (and +o, +O) and --rcfile; once an option cluster held c, the script
// is the first word after them. Without -c the script is a heredoc or
// here-string on the command, or else unknown (a pipe, a file), which gets the
// text rule on the statement.
func (g guardCheck) shell(c *syntax.CallExpr, args []string, redirs []*syntax.Redirect, line *lineText, depth int) string {
	cFlag := false
	i := 0
	for ; i < len(args); i++ {
		a := args[i]
		if a == "--" {
			i++
			break
		}
		if len(a) < 2 || a[0] != '-' && a[0] != '+' {
			break
		}
		switch {
		case a == "--rcfile" || a == "--init-file":
			i++
		case a[1] != '-':
			cFlag = cFlag || a[0] == '-' && strings.Contains(a, "c")
			i += strings.Count(a, "o") + strings.Count(a, "O")
		}
	}
	if cFlag {
		if i >= len(args) {
			return line.stmt()
		}
		return g.script(c, args[i], line, depth)
	}
	if i < len(args) {
		return ruleShellScript // a script file: the guard is not shown what is in it
	}
	found := false
	for _, r := range redirs {
		var body string
		switch r.Op {
		case syntax.Hdoc, syntax.DashHdoc:
			if r.Hdoc == nil {
				continue
			}
			body = heredocText(r)
		case syntax.WordHdoc:
			body = wordText(r.Word)
		case syntax.RdrIn, syntax.RdrInOut:
			return ruleShellScript // sh < file
		default:
			continue
		}
		found = true
		if rule := g.script(c, body, line, depth); rule != "" {
			return rule
		}
	}
	if found {
		return ""
	}
	// Nothing on the command shows the script: it comes down a pipe, or the shell
	// reads its own stdin.
	if r := line.stmt(); r != "" {
		return r
	}
	return ruleShellScript
}

// wordText is the text of a word with its quoting resolved and a placeholder for
// each expansion in it, followed by a split mark when the expansion is unquoted.
func wordText(w *syntax.Word) string { return textOf(w, false, false) }

// heredocText is the body of a heredoc as the shell reads it: an unquoted
// delimiter makes the body a double-quoted string, and a quoted one (`'EOF'`,
// `"EOF"`, `\EOF`) leaves every character, backslashes too, as it stands.
func heredocText(r *syntax.Redirect) string {
	raw := false
	for _, p := range r.Word.Parts {
		if l, ok := p.(*syntax.Lit); !ok || strings.Contains(l.Value, `\`) {
			raw = true
		}
	}
	return textOf(r.Hdoc, true, raw)
}

// textOf is wordText for a word read inside double quotes (quoted) or with no
// escapes resolved (raw). A marker from an earlier parse of the same script
// goes back to a placeholder.
func textOf(w *syntax.Word, quoted, raw bool) string {
	var b strings.Builder
	var add func(parts []syntax.WordPart, quoted bool)
	add = func(parts []syntax.WordPart, quoted bool) {
		unmark := placeholder
		if !quoted {
			unmark += splitMark
		}
		for _, p := range parts {
			switch p := p.(type) {
			case *syntax.Lit:
				v := p.Value
				if !raw {
					v = unescape(v, quoted)
				}
				b.WriteString(strings.ReplaceAll(v, marker, unmark))
			case *syntax.SglQuoted:
				b.WriteString(strings.ReplaceAll(p.Value, marker, placeholder+splitMark))
			case *syntax.DblQuoted:
				add(p.Parts, true)
			default:
				b.WriteString(unmark)
			}
		}
	}
	add(w.Parts, quoted)
	return b.String()
}

// unescape resolves the backslashes of a literal: any escape outside quotes, and
// \" \\ \$ \` inside double quotes. A backslash before a newline is a line
// continuation.
func unescape(s string, quoted bool) string {
	if !strings.Contains(s, `\`) {
		return s
	}
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] == '\\' && i+1 < len(s) {
			if next := s[i+1]; next == '\n' {
				i++
				continue
			} else if !quoted || strings.IndexByte("\"\\$`", next) >= 0 {
				i++
			}
		}
		b.WriteByte(s[i])
	}
	return b.String()
}
