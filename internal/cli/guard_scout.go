package cli

import (
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"github.com/jbuffin/admiral/internal/launcher"
	"github.com/jbuffin/admiral/internal/probes"
	"mvdan.cc/sh/v3/syntax"
)

// One reason for every scout rule, so a refusal never repeats text the ship wrote.
const scoutReason = "Scout ships only look: this one may not change the repository, its pull requests or the files in its checkout."

const (
	ruleScoutEnv   = "scout_env"
	ruleScoutGit   = "scout_git"
	ruleScoutGhPr  = "scout_gh_pr"
	ruleScoutGhAPI = "scout_gh_api"
	ruleScoutGql   = "scout_graphql"
	ruleScoutMcp   = "scout_mcp"
	ruleScoutEdit  = "scout_edit"
	ruleScoutWrite = "scout_shell_write"
)

func isScoutRule(rule string) bool { return strings.HasPrefix(rule, "scout_") }

// scoutRoot is the checkout the ship may not change: absolute, clean, not /, and
// resolved through symlinks so a path under /var and one under /private/var compare equal.
func scoutRoot(env string) (string, bool) {
	if env == "" || !filepath.IsAbs(env) || filepath.Clean(env) != env || env == "/" {
		return "", false
	}
	root, ok := resolvePath(env)
	return root, ok && root != "/"
}

// resolvePath follows symlinks component by component, so a ".." after a link climbs
// out of the link's target. A component that does not exist is kept as written.
func resolvePath(p string) (string, bool) {
	if !filepath.IsAbs(p) {
		return "", false
	}
	queue := strings.Split(p, "/")
	cur, links := "/", 0
	for len(queue) > 0 {
		comp := queue[0]
		queue = queue[1:]
		switch comp {
		case "", ".":
			continue
		case "..":
			cur = filepath.Dir(cur)
			continue
		}
		next := filepath.Join(cur, comp)
		fi, err := os.Lstat(next)
		switch {
		case errors.Is(err, fs.ErrNotExist):
			cur = next
		case err != nil:
			return "", false
		case fi.Mode()&fs.ModeSymlink != 0:
			if links++; links > 40 {
				return "", false
			}
			dest, err := os.Readlink(next)
			if err != nil {
				return "", false
			}
			if filepath.IsAbs(dest) {
				cur = "/"
			}
			queue = append(strings.Split(dest, "/"), queue...)
		default:
			cur = next
		}
	}
	return cur, true
}

func inside(p, root string) bool { return p == root || strings.HasPrefix(p, root+"/") }

// scoutDecision judges one hook call for a scout ship. It refuses a payload it cannot read.
func scoutDecision(raw []byte, root string) (tool, rule, target string) {
	var p struct {
		ToolName  string          `json:"tool_name"`
		ToolInput json.RawMessage `json:"tool_input"`
		Cwd       string          `json:"cwd"`
	}
	if json.Unmarshal(raw, &p) != nil || p.ToolName == "" {
		return "", ruleScoutEnv, ""
	}
	switch p.ToolName {
	case "Edit", "Write", "MultiEdit", "NotebookEdit":
		rule, target = editTarget(p.ToolName, p.ToolInput, root)
		return p.ToolName, rule, target
	case "Bash":
		var in struct {
			Command *string `json:"command"`
		}
		if json.Unmarshal(p.ToolInput, &in) != nil || in.Command == nil {
			return p.ToolName, ruleScoutEnv, ""
		}
		c := scoutCheck{root: root, cwd: p.Cwd, st: &scoutState{}}
		return p.ToolName, c.command(*in.Command, 0), ""
	}
	if strings.HasPrefix(p.ToolName, "mcp__") && scoutMcpWrites(p.ToolName) {
		return p.ToolName, ruleScoutMcp, ""
	}
	return p.ToolName, "", ""
}

var scoutMcpTool = regexp.MustCompile(launcher.ScoutMCPMatcher)

func scoutMcpWrites(tool string) bool {
	if !scoutMcpTool.MatchString(tool) {
		return false
	}
	name := tool[strings.LastIndex(tool, "__")+2:]
	if strings.HasSuffix(name, "_read") {
		return false
	}
	return !slices.ContainsFunc([]string{"list_", "get_", "search_"}, func(p string) bool { return strings.HasPrefix(name, p) })
}

func editTarget(tool string, input json.RawMessage, root string) (rule, target string) {
	var in map[string]json.RawMessage
	if json.Unmarshal(input, &in) != nil {
		return ruleScoutEdit, ""
	}
	key := "file_path"
	if tool == "NotebookEdit" {
		key = "notebook_path"
	}
	var path string
	if json.Unmarshal(in[key], &path) != nil || !filepath.IsAbs(path) {
		return ruleScoutEdit, ""
	}
	res, ok := resolvePath(path)
	if !ok || inside(res, root) {
		return ruleScoutEdit, res
	}
	return "", res
}

// scoutProbe notes a Write the guard is asked about while a capability probe's
// variables are valid, and a refusal of the very file the probe asked for.
func scoutProbe(tool, rule, target string) {
	nonce, dir := os.Getenv(probes.NonceEnv), os.Getenv(probes.DirEnv)
	if tool != "Write" || !probes.ValidEnv(dir, nonce) {
		return
	}
	_ = os.WriteFile(probes.ScoutSeenPath(dir, nonce), nil, 0o600)
	if rule == "" || target == "" {
		return
	}
	root, ok := scoutRoot(os.Getenv(launcher.ScoutEnv))
	if !ok {
		return
	}
	if want, ok := resolvePath(probes.ScoutFilePath(root, nonce)); ok && want == target {
		_ = os.WriteFile(probes.ScoutDeniedPath(dir, nonce), nil, 0o600)
	}
}

type scoutState struct {
	moved bool // a cd, pushd, popd, env -C or sudo -D: relative paths are not under cwd
}

type scoutCheck struct {
	root, cwd string
	st        *scoutState
}

// command refuses text the bash parser cannot read: a scout never falls back to a guess.
func (c scoutCheck) command(text string, depth int) string {
	if depth > maxShellDepth {
		return ruleScoutWrite
	}
	file, err := syntax.NewParser(syntax.Variant(syntax.LangBash)).Parse(strings.NewReader(text), "")
	if err != nil {
		return ruleScoutWrite
	}
	rule := ""
	syntax.Walk(file, func(n syntax.Node) bool {
		if s, ok := n.(*syntax.Stmt); ok && rule == "" {
			if rule = c.redirects(s.Redirs); rule == "" {
				if call, ok := s.Cmd.(*syntax.CallExpr); ok {
					rule = c.call(call, s.Redirs, depth)
				}
			}
		}
		return rule == ""
	})
	return rule
}

func (c scoutCheck) redirects(redirs []*syntax.Redirect) string {
	for _, r := range redirs {
		switch r.Op {
		case syntax.RdrOut, syntax.AppOut, syntax.ClbOut, syntax.RdrAll, syntax.AppAll, syntax.RdrInOut:
		case syntax.DplOut:
			if t := wordText(r.Word); t == "-" || isDigits(t) {
				continue
			}
		default:
			continue
		}
		if c.writes(wordText(r.Word)) {
			return ruleScoutWrite
		}
	}
	return ""
}

func isDigits(s string) bool {
	return s != "" && strings.Trim(s, "0123456789") == ""
}

// writes reports whether changing path could change the checkout; a path it cannot
// place counts as a change.
func (c scoutCheck) writes(path string) bool {
	switch path {
	case "/dev/null", "/dev/stdout", "/dev/stderr":
		return false
	}
	if path == "" || strings.ContainsAny(path, placeholder+splitMark+"*?[{~") {
		return true
	}
	if !filepath.IsAbs(path) {
		if c.st.moved || !filepath.IsAbs(c.cwd) {
			return true
		}
		path = filepath.Join(c.cwd, path)
	}
	res, ok := resolvePath(path)
	return !ok || inside(res, c.root)
}

func (c scoutCheck) anyWrites(paths []string) bool {
	return slices.ContainsFunc(paths, c.writes)
}

func (c scoutCheck) call(call *syntax.CallExpr, redirs []*syntax.Redirect, depth int) string {
	words := make([]string, len(call.Args))
	for i, w := range call.Args {
		words[i] = wordText(w)
	}
	return c.words(words, redirs, depth, false)
}

func (c scoutCheck) words(words []string, redirs []*syntax.Redirect, depth int, viaXargs bool) string {
	for len(words) > 0 {
		if hasPlaceholder(words[0]) {
			return ruleScoutWrite // a command held in a variable
		}
		w, args := strings.TrimPrefix(filepath.Base(words[0]), "="), words[1:]
		if spec, ok := wrappers[w]; ok {
			rest, script := skipWrapperFlags(spec, args)
			if script != "" {
				if r := c.command(script, depth+1); r != "" {
					return r
				}
			}
			if chdirFlag(w, args[:len(args)-len(rest)]) {
				c.st.moved = true
			}
			if slices.Contains(joinedWrappers, w) && len(rest) > 0 {
				return c.command(markWords.Replace(strings.Join(rest, " ")), depth+1)
			}
			viaXargs = viaXargs || w == "xargs"
			words = rest
			continue
		}
		return c.program(w, args, redirs, depth, viaXargs)
	}
	return ""
}

// chdirFlag finds the flag of a wrapper that runs its command in another directory.
func chdirFlag(wrapper string, flags []string) bool {
	for _, f := range flags {
		switch {
		case f == "--chdir" || strings.HasPrefix(f, "--chdir="):
			return true
		case wrapper == "env" && strings.HasPrefix(f, "-C"):
			return true
		case wrapper == "sudo" && strings.HasPrefix(f, "-D"):
			return true
		}
	}
	return false
}

func (c scoutCheck) program(w string, args []string, redirs []*syntax.Redirect, depth int, viaXargs bool) string {
	if viaXargs && scoutWriters[w] {
		return ruleScoutWrite // it adds the words it reads from stdin
	}
	switch {
	case w == "cd" || w == "pushd" || w == "popd":
		c.st.moved = true
	case w == "git":
		return c.git(args)
	case w == "gh":
		if viaXargs {
			return ruleScoutGhPr
		}
		return c.gh(args)
	case w == "find":
		return c.find(args, redirs, depth)
	case slices.Contains(shells, w):
		if viaXargs {
			return ruleScoutWrite
		}
		return c.shell(args, redirs, depth)
	case w == "eval":
		return c.command(markWords.Replace(strings.Join(args, " ")), depth+1)
	case w == "source" || w == ".":
		if viaXargs || len(args) == 0 {
			return ruleScoutWrite
		}
		return c.scriptFile(args[0], depth)
	case w == "trap":
		if len(args) > 0 && args[0] == "--" {
			args = args[1:]
		}
		if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
			return c.command(markWords.Replace(args[0]), depth+1)
		}
	default:
		if c.anyWrites(c.targets(w, args)) {
			return ruleScoutWrite
		}
	}
	return ""
}

var scoutWriters = map[string]bool{
	"rm": true, "rmdir": true, "unlink": true, "touch": true, "mkdir": true, "truncate": true,
	"chmod": true, "chown": true, "chgrp": true, "mv": true, "cp": true, "install": true, "ln": true,
	"rsync": true, "tee": true, "dd": true, "sed": true, "perl": true, "patch": true,
}

// targets are the paths a command changes, or too many of them when its flags are
// not known: an over-reach only refuses more.
func (c scoutCheck) targets(w string, args []string) []string {
	switch w {
	case "rm", "rmdir", "unlink":
		return operands(args, "", nil)
	case "touch":
		return operands(args, "tdr", []string{"date", "reference"})
	case "mkdir":
		return operands(args, "m", []string{"mode"})
	case "truncate":
		return operands(args, "sr", []string{"size", "reference"})
	case "chmod", "chown", "chgrp":
		if pos := operands(args, "", []string{"reference"}); len(pos) > 0 {
			return pos[1:]
		}
	case "tee":
		return operands(args, "", nil)
	case "mv":
		return operands(args, "tS", []string{"target-directory", "suffix"})
	case "cp", "install", "ln", "rsync":
		return destination(w, args)
	case "dd":
		var out []string
		for _, a := range args {
			if v, ok := strings.CutPrefix(a, "of="); ok {
				out = append(out, v)
			}
		}
		return out
	case "sed":
		return inPlace(args, "efl", "expression", "file", "line-length")
	case "perl":
		return inPlace(args, "eE", "", "", "")
	case "patch":
		return patchTargets(args, c.cwd)
	}
	return nil
}

// operands is the words that are not flags, with the value of a flag that takes one
// left out. A word after -- is an operand.
func operands(args []string, short string, long []string) []string {
	var out []string
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "--":
			return append(out, args[i+1:]...)
		case strings.HasPrefix(a, "--"):
			if name, _, has := strings.Cut(a[2:], "="); !has && slices.Contains(long, name) {
				i++
			}
		case len(a) > 1 && a[0] == '-':
			for j := 1; j < len(a); j++ {
				if strings.IndexByte(short, a[j]) >= 0 {
					if j == len(a)-1 {
						i++
					}
					break
				}
			}
		default:
			out = append(out, a)
		}
	}
	return out
}

// destination is the one path cp, install, ln and rsync write: -t's directory, else
// the last operand. ln with one operand links into the current directory.
func destination(w string, args []string) []string {
	for i, a := range args {
		switch {
		case a == "--":
		case a == "-t" || a == "--target-directory":
			if i+1 < len(args) {
				return []string{args[i+1]}
			}
			return []string{placeholder}
		case strings.HasPrefix(a, "--target-directory="):
			return []string{strings.TrimPrefix(a, "--target-directory=")}
		}
	}
	pos := operands(args, "tS", []string{"suffix"})
	switch {
	case len(pos) == 0:
		return nil
	case len(pos) == 1 && w == "ln":
		return []string{"."}
	}
	return pos[len(pos)-1:]
}

// inPlace is the files sed or perl edit when given -i, and none otherwise. The
// script is the first operand unless a flag gave it.
func inPlace(args []string, valued, longValued, longValued2, longValued3 string) []string {
	edits, scripted := false, false
	var pos []string
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "--":
			pos = append(pos, args[i+1:]...)
			i = len(args)
		case strings.HasPrefix(a, "--"):
			name, _, has := strings.Cut(a[2:], "=")
			switch name {
			case "in-place":
				edits = true
			case longValued, longValued2:
				scripted = scripted || name == longValued || name == longValued2
				if !has {
					i++
				}
			case longValued3:
				if !has {
					i++
				}
			}
		case len(a) > 1 && a[0] == '-':
		cluster:
			for j := 1; j < len(a); j++ {
				switch {
				case a[j] == 'i':
					edits = true
					break cluster
				case strings.IndexByte(valued, a[j]) >= 0:
					if a[j] == 'e' || a[j] == 'E' || a[j] == 'f' {
						scripted = true
					}
					if j == len(a)-1 {
						i++
					}
					break cluster
				}
			}
		default:
			pos = append(pos, a)
		}
	}
	if !edits {
		return nil
	}
	if !scripted && len(pos) > 0 {
		pos = pos[1:]
	}
	return pos
}

func patchTargets(args []string, cwd string) []string {
	base := cwd
	var out []string
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "-d" || a == "--directory":
			if i++; i < len(args) {
				base = args[i]
			}
		case strings.HasPrefix(a, "--directory="):
			base = strings.TrimPrefix(a, "--directory=")
		case a == "-o" || a == "--output":
			if i++; i < len(args) {
				out = append(out, args[i])
			}
		case a == "-i" || a == "-p" || a == "-r" || a == "-F" || a == "-B" || a == "-V" || a == "-z" || a == "--input" || a == "--strip" || a == "--reject-file":
			i++
		case strings.HasPrefix(a, "-"):
		default:
			return append(out, a)
		}
	}
	return append(out, base)
}

func (c scoutCheck) find(args []string, redirs []*syntax.Redirect, depth int) string {
	var roots []string
	for _, a := range args {
		if strings.HasPrefix(a, "-") && len(roots) > 0 || a == "(" || a == "!" {
			break
		}
		if !strings.HasPrefix(a, "-") {
			roots = append(roots, a)
		}
	}
	if len(roots) == 0 {
		roots = []string{"."}
	}
	for i := 0; i < len(args); i++ {
		switch a := args[i]; {
		case a == "-delete" || strings.HasPrefix(a, "-fprint") || a == "-fls":
			return ruleScoutWrite
		case slices.Contains([]string{"-exec", "-execdir", "-ok", "-okdir"}, a):
			end := i + 1
			for end < len(args) && args[end] != ";" && args[end] != "+" {
				end++
			}
			for _, root := range roots {
				sub := make([]string, 0, end-i-1)
				for _, w := range args[i+1 : end] {
					sub = append(sub, strings.ReplaceAll(w, "{}", root))
				}
				if r := c.words(sub, redirs, depth, false); r != "" {
					return r
				}
			}
			i = end
		}
	}
	return ""
}

// shell reads the script a shell runs when the call shows it; a file that does not
// exist cannot run, and any other file the guard cannot read is refused.
func (c scoutCheck) shell(args []string, redirs []*syntax.Redirect, depth int) string {
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
	switch {
	case cFlag && i < len(args):
		return c.command(markWords.Replace(args[i]), depth+1)
	case cFlag:
		return ruleScoutWrite
	case i < len(args):
		return c.scriptFile(args[i], depth)
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
		default:
			continue
		}
		found = true
		if rule := c.command(markWords.Replace(body), depth+1); rule != "" {
			return rule
		}
	}
	if !found {
		return ruleScoutWrite // the script comes down a pipe or from the shell's own stdin
	}
	return ""
}

func (c scoutCheck) scriptFile(name string, depth int) string {
	if hasPlaceholder(name) {
		return ruleScoutWrite
	}
	if !filepath.IsAbs(name) {
		if c.st.moved || !filepath.IsAbs(c.cwd) {
			return ruleScoutWrite
		}
		name = filepath.Join(c.cwd, name)
	}
	if _, err := os.Stat(name); errors.Is(err, fs.ErrNotExist) {
		return ""
	}
	text, ok := guardCheck{cwd: c.cwd, st: &guardState{}}.readFile(name)
	if !ok {
		return ruleScoutWrite
	}
	return c.command(text, depth+1)
}

// gitReads are the subcommands that only look; any other is a change or could be an alias.
var gitReads = []string{
	"status", "log", "show", "diff", "blame", "grep", "ls-files", "ls-tree", "rev-parse", "rev-list", "cat-file",
	"describe", "shortlog", "fetch", "diff-tree", "diff-index", "diff-files", "merge-base", "show-ref",
	"for-each-ref", "ls-remote", "name-rev", "count-objects", "cherry", "whatchanged", "version", "help",
	"check-ignore", "var", "verify-commit", "verify-tag", "show-branch", "range-diff", "annotate",
}

var gitSafeConfig = []string{"color.", "column.", "advice."}

func (c scoutCheck) git(args []string) string {
	dir := c.cwd
	i := 0
	for ; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "-C":
			i++
			if i >= len(args) || hasPlaceholder(args[i]) {
				dir = ""
			} else if filepath.IsAbs(args[i]) {
				dir = args[i]
			} else if dir != "" {
				dir = filepath.Join(dir, args[i])
			}
		case a == "-c":
			i++
			if i >= len(args) || !slices.ContainsFunc(gitSafeConfig, func(p string) bool { return strings.HasPrefix(args[i], p) }) {
				return ruleScoutGit
			}
		case a == "--git-dir" || a == "--work-tree" || a == "--namespace":
			i++
		case strings.HasPrefix(a, "--git-dir=") || strings.HasPrefix(a, "--work-tree=") || strings.HasPrefix(a, "--namespace="):
		case strings.HasPrefix(a, "--config-env") || strings.HasPrefix(a, "--exec-path") || a == "--list-cmds" || strings.HasPrefix(a, "-c") && len(a) > 2:
			return ruleScoutGit
		case strings.HasPrefix(a, "-"):
		default:
			goto sub
		}
	}
	return "" // git with no subcommand: a version or help flag
sub:
	name, rest := args[i], args[i+1:]
	if hasPlaceholder(name) {
		return ruleScoutGit
	}
	switch {
	case slices.Contains(gitReads, name):
		if slices.ContainsFunc(rest, func(a string) bool {
			return strings.HasPrefix(a, "--output") || strings.HasPrefix(a, "--upload-pack") || strings.HasPrefix(a, "--receive-pack") ||
				name == "grep" && (a == "-O" || strings.HasPrefix(a, "-O") || strings.HasPrefix(a, "--open-files-in-pager"))
		}) {
			return ruleScoutGit
		}
		return ""
	case name == "branch":
		return gitListing(rest, "-a -r -v -vv --all --remotes --verbose --show-current --no-color --color --column --no-column -i --ignore-case --abbrev --no-abbrev",
			"--contains --no-contains --merged --no-merged --points-at --sort --format")
	case name == "tag":
		return gitListing(rest, "-n --no-color --color --column --no-column -i --ignore-case",
			"--contains --no-contains --merged --no-merged --points-at --sort --format")
	case name == "remote":
		if len(rest) == 0 || slices.Contains([]string{"-v", "--verbose", "show", "get-url"}, rest[0]) && !slices.ContainsFunc(rest[1:], func(a string) bool { return a == "add" }) {
			return ""
		}
	case name == "config":
		read := slices.ContainsFunc(rest, func(a string) bool {
			return strings.HasPrefix(a, "--get") || a == "-l" || a == "--list"
		})
		write := slices.ContainsFunc(rest, func(a string) bool {
			return slices.Contains([]string{"--add", "--replace-all", "--unset", "--unset-all", "--edit", "-e", "--rename-section", "--remove-section", "--set"}, a)
		})
		if read && !write {
			return ""
		}
	case name == "stash":
		if len(rest) > 0 && (rest[0] == "list" || rest[0] == "show") {
			return ""
		}
	case name == "worktree":
		if len(rest) > 0 && rest[0] == "list" {
			return ""
		}
	case name == "clone":
		return c.gitClone(rest, dir)
	}
	return ruleScoutGit
}

// gitListing allows a branch or tag call that only lists: its flags are known and a
// name is allowed only beside --list or -l, as a pattern.
func gitListing(args []string, flags, valued string) string {
	list, names := false, false
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch name, _, has := strings.Cut(a, "="); {
		case a == "--list" || a == "-l":
			list = true
		case strings.HasPrefix(a, "--") && strings.Contains(" "+valued+" ", " "+name+" "):
			if !has {
				i++
			}
		case strings.Contains(" "+flags+" ", " "+name+" "):
		case strings.HasPrefix(a, "-"):
			return ruleScoutGit
		default:
			names = true
		}
	}
	if names && !list {
		return ruleScoutGit
	}
	return ""
}

// gitClone writes a new directory: it is fine only where the guard can see it is
// outside the checkout.
func (c scoutCheck) gitClone(args []string, dir string) string {
	var pos []string
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "--":
			pos = append(pos, args[i+1:]...)
			i = len(args)
		case slices.Contains([]string{"-b", "--branch", "-o", "--origin", "--depth", "--reference", "--template", "-c", "--config", "-j", "--jobs", "--filter", "--separate-git-dir", "-u", "--upload-pack"}, a):
			i++
		case strings.HasPrefix(a, "-"):
		default:
			pos = append(pos, a)
		}
	}
	if slices.ContainsFunc(args, func(a string) bool {
		return a == "--separate-git-dir" || strings.HasPrefix(a, "--separate-git-dir=") || a == "-u" || strings.HasPrefix(a, "--upload-pack")
	}) {
		return ruleScoutGit
	}
	if len(pos) == 0 {
		return ruleScoutGit
	}
	if len(pos) == 1 {
		if dir == "" {
			return ruleScoutGit
		}
		return c.cloneInto(dir)
	}
	dest := pos[1]
	if !filepath.IsAbs(dest) {
		if dir == "" || c.st.moved {
			return ruleScoutGit
		}
		dest = filepath.Join(dir, dest)
	}
	return c.cloneInto(dest)
}

func (c scoutCheck) cloneInto(path string) string {
	if strings.ContainsAny(path, placeholder+splitMark+"*?[{~") || c.st.moved {
		return ruleScoutGit
	}
	if res, ok := resolvePath(path); !ok || inside(res, c.root) {
		return ruleScoutGit
	}
	return ""
}

// gh: a pull request is only read, an API call is a GET, a GraphQL call a query.
func (c scoutCheck) gh(args []string) string {
	sub := ghSubcommand(args)
	if len(sub) == 0 {
		return ""
	}
	readVerb := func() bool {
		return len(sub) >= 2 && slices.Contains([]string{"view", "list", "status", "diff", "checks"}, sub[1])
	}
	switch sub[0] {
	case "api":
		return c.ghAPI(sub[1:])
	case "pr":
		if readVerb() && !hasPlaceholder(sub[1]) {
			return ""
		}
		return ruleScoutGhPr
	case "alias", "extension", "ext":
		return ruleScoutGhPr
	case "issue", "release", "repo", "gist", "label", "workflow", "run", "ruleset", "secret", "variable", "project", "discussion", "codespace", "cache", "ssh-key", "gpg-key", "org", "agent-task", "attestation", "environment":
		if readVerb() {
			return ""
		}
		return ruleScoutGhPr
	}
	if !ghCommands[sub[0]] {
		return ruleScoutGhPr
	}
	return ""
}

var graphqlMutation = regexp.MustCompile(`(?i)\bmutation\b`)

func (c scoutCheck) ghAPI(args []string) string {
	p := parseAPI(args)
	if p.flagWord || hasPlaceholder(p.method) {
		return ruleScoutGhAPI
	}
	if len(p.positional) == 0 {
		return ""
	}
	seg := endpointSegments(p.positional[0])
	if len(seg) == 2 && seg[0] == "api" && seg[1] == "graphql" {
		seg = seg[1:]
	}
	if len(seg) == 1 && seg[0] == "graphql" {
		return c.graphqlRule(p)
	}
	method := strings.ToUpper(p.method)
	if p.hasInput || method == "" && p.fieldFlag || method != "" && method != "GET" && method != "HEAD" {
		return ruleScoutGhAPI
	}
	return ""
}

func (c scoutCheck) graphqlRule(p apiArgs) string {
	g := guardCheck{cwd: c.cwd, st: &guardState{moved: c.st.moved}}
	var queries []string
	for _, f := range p.fields {
		if f.key != "query" {
			continue
		}
		v := f.val
		if f.typed && strings.HasPrefix(v, "@") {
			b, ok := g.readFile(v[1:])
			if v == "@-" || !ok {
				return ruleScoutGql
			}
			v = b
		}
		queries = append(queries, v)
	}
	if p.hasInput {
		b, ok := g.inputBody(p.input)
		body, ok2 := decodeBody(b)
		query, ok3 := bodyString(body, "query")
		if !ok || !ok2 || !ok3 {
			return ruleScoutGql
		}
		queries = append(queries, query)
	}
	for _, q := range queries {
		if hasPlaceholder(q) || graphqlMutation.MatchString(q) {
			return ruleScoutGql
		}
	}
	return ""
}
