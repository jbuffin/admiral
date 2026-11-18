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

// inside also compares by file identity: on a Mac other letter case or /System/Volumes/Data names the same checkout.
func inside(p, root string) bool {
	if p == root || strings.HasPrefix(p, root+"/") {
		return true
	}
	rootInfo, err := os.Stat(root)
	if err != nil {
		return true
	}
	for q := p; q != "/" && q != "."; q = filepath.Dir(q) {
		if fi, err := os.Stat(q); err == nil && os.SameFile(fi, rootInfo) {
			return true
		}
	}
	return false
}

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
	case "Bash", "Monitor":
		var in struct {
			Command *string `json:"command"`
		}
		if json.Unmarshal(p.ToolInput, &in) != nil || in.Command == nil {
			return p.ToolName, ruleScoutEnv, ""
		}
		if p.ToolName == "Monitor" && strings.TrimSpace(*in.Command) == "" {
			return p.ToolName, ruleScoutEnv, ""
		}
		if r := scoutEnvOverrideRule(*in.Command, 0); r != "" {
			return p.ToolName, r, ""
		}
		c := scoutCheck{root: root, cwd: p.Cwd, st: &scoutState{}}
		return p.ToolName, c.command(*in.Command, 0), ""
	}
	if strings.HasPrefix(p.ToolName, "mcp__") {
		if scoutMcpWrites(p.ToolName) {
			return p.ToolName, ruleScoutMcp, ""
		}
		return p.ToolName, "", ""
	}
	if !scoutHarmless[p.ToolName] {
		return p.ToolName, ruleScoutEnv, "" // a tool the policy has not seen could change something
	}
	return p.ToolName, "", ""
}

// scoutHarmless are the harness tools that only read, plan or start another session under the same hooks.
var scoutHarmless = map[string]bool{
	"Read": true, "Grep": true, "Glob": true, "LS": true, "WebFetch": true, "WebSearch": true, "TodoWrite": true,
	"Task": true, "Agent": true, "NotebookRead": true, "BashOutput": true, "KillShell": true, "KillBash": true,
	"TaskOutput": true, "TaskStop": true, "TaskList": true, "TaskGet": true, "ExitPlanMode": true, "EnterPlanMode": true,
	"AskUserQuestion": true, "ToolSearch": true, "Skill": true, "SendMessage": true,
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
	// A name in the environment that makes git or a shell run a program of its own.
	gitEnv, shellEnv bool
	renamed          bool // an alias table was assigned: a later word may be any command
}

var gitSafeEnv = []string{"GIT_TERMINAL_PROMPT", "GIT_OPTIONAL_LOCKS"}

// A pager or editor git starts runs whatever the variable names.
var gitProgramEnv = []string{"PAGER", "EDITOR", "VISUAL", "LESSOPEN", "LESS"}

// Where git finds its global config, whose core.fsmonitor or pager runs a program.
var gitConfigEnv = []string{"HOME", "XDG_CONFIG_HOME"}

func (st *scoutState) assigned(name string) {
	st.gitEnv = st.gitEnv || strings.HasPrefix(name, "GIT_") && !slices.Contains(gitSafeEnv, name) || slices.Contains(gitConfigEnv, name) || slices.Contains(gitProgramEnv, name)
	st.renamed = st.renamed || slices.Contains(aliasTables, name)
	st.shellEnv = st.shellEnv || slices.Contains(startupNames, name)
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
				switch cmd := s.Cmd.(type) {
				case *syntax.CallExpr:
					rule = c.call(cmd, s.Redirs, depth)
				case *syntax.DeclClause:
					for _, a := range cmd.Args {
						if a.Name != nil {
							c.st.assigned(a.Name.Value)
						}
					}
				}
			}
		}
		return rule == ""
	})
	return rule
}

// ansiEscaped finds a $'…' part with an escape in it: the parser keeps the text raw, so the word is not the one the shell runs.
func ansiEscaped(w *syntax.Word) bool {
	var has func(parts []syntax.WordPart) bool
	has = func(parts []syntax.WordPart) bool {
		for _, p := range parts {
			switch p := p.(type) {
			case *syntax.SglQuoted:
				if p.Dollar && strings.Contains(p.Value, `\`) {
					return true
				}
			case *syntax.DblQuoted:
				if has(p.Parts) {
					return true
				}
			}
		}
		return false
	}
	return has(w.Parts)
}

func (c scoutCheck) redirects(redirs []*syntax.Redirect) string {
	for _, r := range redirs {
		if r.Word != nil && ansiEscaped(r.Word) {
			return ruleScoutWrite
		}
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
	for _, a := range call.Assigns {
		if a.Name != nil {
			c.st.assigned(a.Name.Value)
		}
	}
	if c.st.renamed {
		return ruleScoutWrite
	}
	words := make([]string, len(call.Args))
	for i, w := range call.Args {
		if ansiEscaped(w) {
			return ruleScoutWrite
		}
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
		if slices.Contains(scoutRunners, w) {
			return ruleScoutRunner
		}
		if spec, ok := wrappers[w]; ok {
			if w == "xcrun" {
				args = longSingleDash(args, "-sdk", "-toolchain")
			}
			rest, script := skipWrapperFlags(spec, args)
			if script != "" {
				if r := c.command(script, depth+1); r != "" {
					return r
				}
			}
			for _, a := range args[:len(args)-len(rest)] {
				if assignment.MatchString(a) {
					c.st.assigned(a[:strings.IndexByte(a, '=')])
				}
			}
			if chdirFlag(w, args[:len(args)-len(rest)]) {
				c.st.moved = true
			}
			if slices.Contains(joinedWrappers, w) && len(rest) > 0 {
				return c.command(markWords.Replace(strings.Join(rest, " ")), depth+1)
			}
			if viaXargs && len(rest) == 0 && w != "xargs" && w != "parallel" {
				return ruleScoutWrite // the words from stdin become the command it runs
			}
			viaXargs = viaXargs || w == "xargs" || w == "parallel"
			words = rest
			continue
		}
		return c.program(w, args, redirs, depth, viaXargs)
	}
	return ""
}

// longSingleDash spells the flags xcrun takes with one dash as the long options the wrapper table knows.
func longSingleDash(args []string, names ...string) []string {
	out := slices.Clone(args)
	for i, a := range out {
		if slices.Contains(names, a) {
			out[i] = "-" + a
		}
	}
	return out
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
		return c.git(args, viaXargs)
	case strings.HasPrefix(w, "git-"):
		return c.git(append([]string{strings.TrimPrefix(w, "git-")}, args...), viaXargs) // git's own dashed binaries
	case w == "alias" || w == "unalias" || w == "hash" && hashRenames(args):
		return ruleScoutWrite
	case slices.Contains(httpClients, w):
		if httpSends(w, args) {
			return ruleScoutWrite
		}
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
		if c.anyWrites(c.targets(w, args)) || c.hardLinks(w, args) {
			return ruleScoutWrite
		}
	}
	return ""
}

var scoutWriters = map[string]bool{
	"rm": true, "rmdir": true, "unlink": true, "touch": true, "mkdir": true, "truncate": true,
	"chmod": true, "chown": true, "chgrp": true, "mv": true, "cp": true, "install": true, "ln": true,
	"rsync": true, "ditto": true, "tee": true, "dd": true, "sed": true, "perl": true, "patch": true,
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
		return append(operands(args, "tS", []string{"target-directory", "suffix"}), targetDir(w, args)...)
	case "install":
		if installsDirs(args) {
			return operands(args, "mogGSt", []string{"mode", "owner", "group", "suffix", "target-directory"})
		}
		return destination(w, args)
	case "cp", "ln", "rsync", "ditto":
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
		return inPlace(args, "iI", "efl", "expression", "file", "line-length")
	case "perl":
		return inPlace(args, "i", "eE", "", "", "")
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

// targetDir is the directory -t or --target-directory names, in any spelling GNU accepts
// (a cluster like -vt, -tDIR, an abbreviated --target). rsync's -t takes no value.
func targetDir(w string, args []string) []string {
	if w == "rsync" {
		return nil
	}
	for i, a := range args {
		switch {
		case a == "--":
			return nil
		case strings.HasPrefix(a, "--"):
			name, val, has := strings.Cut(a[2:], "=")
			if len(name) < 1 || !strings.HasPrefix("target-directory", name) {
				continue
			}
			switch {
			case has:
				return []string{val}
			case i+1 < len(args):
				return []string{args[i+1]}
			}
			return []string{placeholder}
		case len(a) > 1 && a[0] == '-':
			if j := strings.IndexByte(a, 't'); j > 0 {
				switch {
				case j+1 < len(a):
					return []string{a[j+1:]}
				case i+1 < len(args):
					return []string{args[i+1]}
				}
				return []string{placeholder}
			}
		}
	}
	return nil
}

// destination is the one path cp, install, ln and rsync write: -t's directory, else
// the last operand. ln with one operand links into the current directory.
func destination(w string, args []string) []string {
	if dir := targetDir(w, args); dir != nil {
		return dir
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
func inPlace(args []string, editing, valued, longValued, longValued2, longValued3 string) []string {
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
				case strings.IndexByte(editing, a[j]) >= 0:
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
	longName := func(name string) string {
		for _, l := range []string{"directory", "output", "input", "strip", "reject-file"} {
			if name != "" && strings.HasPrefix(l, name) && (len(name) > 1 || l == "output") {
				return l
			}
		}
		return ""
	}
	set := func(opt, v string) {
		switch opt {
		case "directory":
			base = v
		case "output", "reject-file":
			out = append(out, v)
		}
	}
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "--":
			if i+1 < len(args) {
				return append(out, patchFile(base, args[i+1]))
			}
		case strings.HasPrefix(a, "--"):
			name, v, has := strings.Cut(a[2:], "=")
			if opt := longName(name); opt != "" {
				if !has && i+1 < len(args) {
					i++
					v = args[i]
				}
				set(opt, v)
			}
		case len(a) > 1 && a[0] == '-':
			for j := 1; j < len(a); j++ {
				if strings.IndexByte("dioprFBVz", a[j]) < 0 {
					continue
				}
				v := a[j+1:]
				if v == "" && i+1 < len(args) {
					i++
					v = args[i]
				}
				switch a[j] {
				case 'd':
					set("directory", v)
				case 'o':
					set("output", v)
				case 'r':
					set("reject-file", v)
				}
				break
			}
		default:
			return append(out, patchFile(base, a))
		}
	}
	return append(out, base)
}

// patchFile is a file patch is told to patch, which -d puts under its directory.
func patchFile(base, name string) string {
	if filepath.IsAbs(name) {
		return name
	}
	return filepath.Join(base, name)
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

// shell reads the script a shell runs when the call shows it; a file the guard cannot read is refused,
// and so is one that does not exist yet, which a write earlier in the call may create.
func (c scoutCheck) shell(args []string, redirs []*syntax.Redirect, depth int) string {
	if c.st.shellEnv {
		return ruleScoutWrite // BASH_ENV, ENV or ZDOTDIR makes the shell run a file first
	}
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

// gitXargsReads are the reads with no option that writes a file or runs a program.
var gitXargsReads = []string{"status", "rev-parse", "cat-file", "ls-files", "ls-tree", "rev-list", "show-ref", "merge-base", "name-rev", "blame", "check-ignore", "describe"}

// gitDangerousLong are the long options of a read that write a file or run a program; git takes any unambiguous prefix.
var gitDangerousLong = []string{"output", "upload-pack", "receive-pack", "exec", "open-files-in-pager"}

// gitRunsAProgram finds an option that makes a read write a file or run a program, in any spelling git accepts.
func gitRunsAProgram(name string, args []string) bool {
	dangerous := gitDangerousLong
	if name == "fetch" {
		dangerous = append(slices.Clone(dangerous), "update-head-ok", "refmap", "negotiate-only")
	}
	for _, a := range args {
		switch {
		case a == "--":
			return false
		case strings.HasPrefix(a, "--"):
			opt, _, _ := strings.Cut(a[2:], "=")
			if opt != "" && slices.ContainsFunc(dangerous, func(d string) bool { return strings.HasPrefix(d, opt) || strings.HasPrefix(opt, d) }) {
				return true
			}
		case len(a) > 1 && a[0] == '-':
			if name == "grep" && strings.Contains(a, "O") || name == "fetch" && strings.Contains(a, "u") {
				return true
			}
		}
	}
	return false
}

// gitFetch refuses a refspec that writes a local ref: one with a destination or a leading plus.
func gitFetch(args []string) string {
	seenRemote := false
	for _, a := range args {
		if a == "--" || strings.HasPrefix(a, "-") {
			continue
		}
		if !seenRemote {
			seenRemote = true
			continue
		}
		if strings.Contains(a, ":") || strings.HasPrefix(a, "+") {
			return ruleScoutGit
		}
	}
	return ""
}

var gitSafeConfig = []string{"color.", "column.", "advice."}

func (c scoutCheck) git(args []string, viaXargs bool) string {
	if c.st.gitEnv {
		return ruleScoutGit
	}
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
		case a == "--namespace":
			i++
		case strings.HasPrefix(a, "--git-dir") || strings.HasPrefix(a, "--work-tree"):
			return ruleScoutGit // another repository's directory: GIT_DIR by another name
		case strings.HasPrefix(a, "--namespace="):
		case strings.HasPrefix(a, "--config-env") || strings.HasPrefix(a, "--exec-path") || a == "--list-cmds" || strings.HasPrefix(a, "-c") && len(a) > 2:
			return ruleScoutGit
		case strings.HasPrefix(a, "-"):
		default:
			goto sub
		}
	}
	if viaXargs {
		return ruleScoutGit // the subcommand is whatever stdin brings
	}
	return "" // git with no subcommand: a version or help flag
sub:
	name, rest := args[i], args[i+1:]
	if hasPlaceholder(name) {
		return ruleScoutGit
	}
	switch {
	case slices.Contains(gitReads, name):
		if viaXargs && !slices.Contains(gitXargsReads, name) || gitRunsAProgram(name, rest) {
			return ruleScoutGit // stdin can add any option to the call
		}
		if name == "fetch" {
			return gitFetch(rest)
		}
		return ""
	case name == "branch":
		return gitListing(rest, "-a -r -v -vv --all --remotes --verbose --show-current --no-color --color --column --no-column -i --ignore-case --abbrev --no-abbrev",
			"--contains --no-contains --merged --no-merged --points-at --sort --format")
	case name == "tag":
		return gitListing(rest, "-n --no-color --color --column --no-column -i --ignore-case",
			"--contains --no-contains --merged --no-merged --points-at --sort --format")
	case name == "remote":
		for len(rest) > 0 && (rest[0] == "-v" || rest[0] == "--verbose") {
			rest = rest[1:]
		}
		if len(rest) == 0 || rest[0] == "show" || rest[0] == "get-url" {
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
	if ghFlagBeforeVerb(args) {
		return ruleScoutGhPr // ghSubcommand skips the flag, so the verb it reads may be the flag's value
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

// ghFlagBeforeVerb finds a flag between the command group and its verb; -R and --hostname are the ones gh takes there.
func ghFlagBeforeVerb(args []string) bool {
	group := false
	if sub := ghSubcommand(args); len(sub) > 0 && sub[0] == "api" {
		return false
	}
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "-R" || a == "--repo" || a == "--hostname":
			i++
		case strings.HasPrefix(a, "--repo=") || strings.HasPrefix(a, "--hostname="):
		case strings.HasPrefix(a, "-"):
			if group {
				return true
			}
		case !group:
			group = true
		default:
			return false
		}
	}
	return false
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

var httpClients = []string{"curl", "wget", "http", "https", "xh", "xhs"}

var httpDataItem = regexp.MustCompile(`^[^=:@/\s]+(?::=|=[^=]|=$|@)`)

// httpSends finds a request that sends a body or uses a method other than GET or HEAD, to any host.
func httpSends(w string, args []string) bool {
	readMethod := func(m string) bool { return strings.EqualFold(m, "GET") || strings.EqualFold(m, "HEAD") }
	if w == "http" || w == "https" || w == "xh" || w == "xhs" {
		ignoresStdin := false
		for _, a := range args {
			switch {
			case a == "--ignore-stdin" || a == "-I":
				ignoresStdin = true
			case a == "--":
			case strings.HasPrefix(a, "--"):
				if name, _, _ := strings.Cut(a[2:], "="); slices.ContainsFunc([]string{"form", "json", "multipart", "raw", "data"}, func(l string) bool { return name != "" && strings.HasPrefix(l, name) }) {
					return true
				}
			case len(a) > 1 && a[0] == '-':
				if strings.ContainsAny(a, "fjxd") {
					return true
				}
			case a == strings.ToUpper(a) && isLetters(a) && !readMethod(a), httpDataItem.MatchString(a):
				return true
			}
		}
		return !ignoresStdin // httpie and xh send what stdin holds as the body
	}
	method := func(i int, inline string) bool {
		if inline == "" && i+1 < len(args) {
			inline = args[i+1]
		}
		return !readMethod(inline)
	}
outer:
	for i, a := range args {
		switch {
		case a == "--":
			return false
		case strings.HasPrefix(a, "--"):
			name, v, has := strings.Cut(a[2:], "=")
			hasPrefix := func(l string) bool { return name != "" && strings.HasPrefix(l, name) }
			switch {
			case name == "request" || name == "method":
				if !has {
					v = ""
				}
				if method(i, v) {
					return true
				}
			case slices.ContainsFunc([]string{"data", "form", "upload-file", "json", "post-data", "post-file", "body-data", "body-file", "config"}, hasPrefix) || strings.HasPrefix(name, "data") || strings.HasPrefix(name, "form") || strings.HasPrefix(name, "post") || strings.HasPrefix(name, "body") || strings.HasPrefix(name, "json"):
				return true
			}
		case len(a) > 1 && a[0] == '-':
			for j := 1; j < len(a); j++ {
				switch a[j] {
				case 'd', 'F', 'T', 'K':
					return true
				case 'X':
					if method(i, a[j+1:]) {
						return true
					}
					continue outer
				}
			}
		}
	}
	return false
}

func isLetters(s string) bool {
	return s != "" && strings.Trim(s, "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz") == ""
}

// installsDirs finds install -d or -D, which create directories from its operands.
func installsDirs(args []string) bool {
	for _, a := range args {
		switch {
		case a == "--":
			return false
		case strings.HasPrefix(a, "--"):
			if name, _, _ := strings.Cut(a[2:], "="); len(name) >= 3 && strings.HasPrefix("directory", name) {
				return true
			}
		case len(a) > 1 && a[0] == '-' && strings.ContainsAny(a, "dD"):
			return true
		}
	}
	return false
}

// hardLinks finds a link or copy that gives a checkout file a second name, which a later write outside the checkout changes through.
func (c scoutCheck) hardLinks(w string, args []string) bool {
	var link bool
	switch w {
	case "ln":
		link = true
		for _, a := range args {
			switch {
			case a == "--":
			case strings.HasPrefix(a, "--"):
				if name, _, _ := strings.Cut(a[2:], "="); len(name) >= 3 && strings.HasPrefix("symbolic", name) {
					link = false
				}
			case len(a) > 1 && a[0] == '-' && strings.Contains(a, "s"):
				link = false
			}
		}
	case "cp":
		for _, a := range args {
			switch {
			case a == "--":
			case strings.HasPrefix(a, "--"):
				if name, _, _ := strings.Cut(a[2:], "="); name != "" && strings.HasPrefix("link", name) {
					link = true
				}
			case len(a) > 1 && a[0] == '-' && strings.Contains(a, "l"):
				link = true
			}
		}
	case "rsync":
		for _, a := range args {
			if strings.HasPrefix(a, "--link") { // rsync resolves a relative link-dest against the destination, so any value counts
				return true
			}
		}
	}
	if !link {
		return false
	}
	pos := operands(args, "tS", []string{"suffix", "target-directory"})
	if targetDir(w, args) == nil && len(pos) > 1 {
		pos = pos[:len(pos)-1]
	}
	return c.anyWrites(pos)
}
