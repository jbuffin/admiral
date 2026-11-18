package cli

import (
	"path/filepath"
	"slices"
	"strings"

	"mvdan.cc/sh/v3/syntax"

	"github.com/jbuffin/admiral/internal/launcher"
)

const (
	ruleScoutEnvOverride = "scout_env_override"
	ruleScoutCrewShip    = "scout_crew_ship"
	ruleScoutRunner      = "scout_command_runner"
)

// The variables a crew launch and the ship's tmux reach depend on: a scout's shell must not drop or change them.
var scoutModeVars = []string{launcher.ScoutEnv, "ADMIRAL_AUTOPILOT", "ADMIRAL_SHIP", "TMUX", "TMUX_PANE"}

func isModeVar(name string) bool { return slices.Contains(scoutModeVars, name) }

// namesModeVar is true for a mode variable, and for a word the guard cannot read (an expansion could be one).
func namesModeVar(w string) bool { return isModeVar(w) || hasPlaceholder(w) }

// scoutRunners start another program the guard does not parse, from a server, a schedule or a script: a scout
// that may not change anything must not hand them a command. nohup is a parsed wrapper, so it is not here.
var scoutRunners = []string{"tmux", "screen", "at", "batch", "launchctl", "crontab", "osascript", "script"}

// scoutEnvOverrideRule refuses a command that unsets, clears or reassigns a scout's mode variables, or reaches
// another ship's crew, anywhere in it (a pipe, a subshell, bash -c, eval). A command it cannot parse is left to
// the scout walker, which refuses it.
func scoutEnvOverrideRule(text string, depth int) string {
	if depth > maxShellDepth {
		return ruleScoutEnvOverride
	}
	file, err := syntax.NewParser(syntax.Variant(syntax.LangBash)).Parse(strings.NewReader(text), "")
	if err != nil {
		return ""
	}
	rule := ""
	syntax.Walk(file, func(n syntax.Node) bool {
		switch x := n.(type) {
		case *syntax.CallExpr:
			for _, a := range x.Assigns {
				if a.Name != nil && isModeVar(a.Name.Value) {
					rule = ruleScoutEnvOverride
				}
			}
			words := make([]string, len(x.Args))
			for i, w := range x.Args {
				words[i] = wordText(w)
			}
			if rule == "" {
				rule = envOverrideInWords(words, depth)
			}
		case *syntax.DeclClause:
			for _, a := range x.Args {
				if a.Name != nil && isModeVar(a.Name.Value) {
					rule = ruleScoutEnvOverride
				}
			}
		case *syntax.ForClause:
			switch l := x.Loop.(type) {
			case *syntax.WordIter:
				if l.Name != nil && isModeVar(l.Name.Value) {
					rule = ruleScoutEnvOverride
				}
			case *syntax.CStyleLoop:
				if arithNamesModeVar(l.Init) || arithNamesModeVar(l.Cond) || arithNamesModeVar(l.Post) {
					rule = ruleScoutEnvOverride
				}
			}
		case *syntax.LetClause:
			for _, e := range x.Exprs {
				if arithNamesModeVar(e) {
					rule = ruleScoutEnvOverride
				}
			}
		case *syntax.ArithmCmd:
			if arithNamesModeVar(x.X) {
				rule = ruleScoutEnvOverride
			}
		case *syntax.ArithmExp:
			if arithNamesModeVar(x.X) {
				rule = ruleScoutEnvOverride
			}
		}
		return rule == ""
	})
	return rule
}

// arithNamesModeVar is true when an arithmetic expression mentions a mode variable at all: assignments, ++ and
// op-assignments cannot be told apart cheaply, and a scout has no use for the arithmetic of these names.
func arithNamesModeVar(e syntax.ArithmExpr) bool {
	if e == nil {
		return false
	}
	found := false
	syntax.Walk(e, func(n syntax.Node) bool {
		switch x := n.(type) {
		case *syntax.Lit:
			if isModeVar(x.Value) {
				found = true
			}
		case *syntax.ParamExp:
			if x.Param != nil && isModeVar(x.Param.Value) {
				found = true
			}
		}
		return !found
	})
	return found
}

// envOverrideInWords looks at each word that names a command able to change the environment of what follows it.
func envOverrideInWords(words []string, depth int) string {
	for i, w := range words {
		rest := words[i+1:]
		switch name := filepath.Base(w); {
		case name == "unset", name == "read", name == "mapfile", name == "readarray", name == "getopts":
			if slices.ContainsFunc(rest, namesModeVar) {
				return ruleScoutEnvOverride
			}
		case name == "printf":
			for j, a := range rest {
				if a == "-v" && j+1 < len(rest) && namesModeVar(rest[j+1]) || strings.HasPrefix(a, "-v") && namesModeVar(a[2:]) {
					return ruleScoutEnvOverride
				}
			}
		case name == "let":
			for _, a := range rest {
				if namesModeVar(letTarget(a)) {
					return ruleScoutEnvOverride
				}
			}
		case name == "exec":
			if execClears(rest) {
				return ruleScoutEnvOverride
			}
		case name == "env":
			if r := envChanges(rest, depth); r != "" {
				return r
			}
		case name == "eval":
			if r := scoutEnvOverrideRule(strings.Join(rest, " "), depth+1); r != "" {
				return r
			}
		case name == "admiral":
			if crewWithShip(rest) {
				return ruleScoutCrewShip
			}
		case slices.Contains(shells, name):
			for j, a := range rest {
				if strings.HasPrefix(a, "-") && !strings.HasPrefix(a, "--") && strings.Contains(a, "c") && j+1 < len(rest) {
					if r := scoutEnvOverrideRule(rest[j+1], depth+1); r != "" {
						return r
					}
					break
				}
			}
		}
	}
	return ""
}

// letTarget is the variable a `let` argument assigns, or the whole word when it is not a name.
func letTarget(a string) string {
	end := 0
	for end < len(a) && (a[end] == '_' || a[end] >= 'A' && a[end] <= 'Z' || a[end] >= 'a' && a[end] <= 'z' || end > 0 && a[end] >= '0' && a[end] <= '9') {
		end++
	}
	if end == 0 {
		return a
	}
	return a[:end]
}

// execClears is true for exec -c, alone or in a cluster: it starts the program with an empty environment.
func execClears(args []string) bool {
	for i := 0; i < len(args); i++ {
		a := args[i]
		if a == "--" || len(a) < 2 || a[0] != '-' {
			return false
		}
		for _, f := range a[1:] {
			if f == 'c' {
				return true
			}
			if f == 'a' {
				i++ // its operand is a name
				break
			}
		}
	}
	return false
}

// crewWithShip is true for `admiral crew … --ship`: a scout reaches no other ship's crew.
func crewWithShip(args []string) bool {
	if !slices.Contains(args, "crew") {
		return false
	}
	return slices.ContainsFunc(args, func(a string) bool {
		return a == "--ship" || strings.HasPrefix(a, "--ship=") || a == "-ship" || strings.HasPrefix(a, "-ship=")
	})
}

// envChanges returns the rule for flags and assignments after env that drop or change a mode variable, or that
// the guard does not know.
func envChanges(args []string, depth int) string {
	spec := wrappers["env"]
	value := func(i int, inline string) (string, int) {
		if inline != "" {
			return inline, i
		}
		if i+1 < len(args) {
			return args[i+1], i + 1
		}
		return "", i
	}
	i := 0
	for ; i < len(args); i++ {
		a := args[i]
		if a == "--" {
			i++
			break
		}
		if a == "-" {
			return ruleScoutEnvOverride
		}
		if len(a) < 2 || a[0] != '-' {
			break
		}
		if a[1] == '-' {
			name, v, has := strings.Cut(a[2:], "=")
			switch {
			case name == "ignore-environment":
				return ruleScoutEnvOverride
			case name == "unset":
				if !has {
					v, i = value(i, "")
				}
				if v == "" || namesModeVar(v) {
					return ruleScoutEnvOverride
				}
			case name == "chdir" || name == "split-string":
				if !has {
					v, i = value(i, "")
				}
				if name == "split-string" {
					if r := scoutEnvOverrideRule(v, depth+1); r != "" {
						return r
					}
				}
			case name == "null" || name == "debug":
			default:
				return ruleScoutEnvOverride // a flag the guard does not know
			}
			continue
		}
		for j := 1; j < len(a); j++ {
			c := a[j]
			switch {
			case c == 'i':
				return ruleScoutEnvOverride
			case strings.IndexByte(spec.short, c) >= 0:
				var v string
				v, i = value(i, a[j+1:])
				switch c {
				case 'u':
					if v == "" || namesModeVar(v) {
						return ruleScoutEnvOverride
					}
				case 'S':
					if r := scoutEnvOverrideRule(v, depth+1); r != "" {
						return r
					}
				}
				j = len(a) // the operand took the rest of the cluster
			case c == '0' || c == 'v':
			default:
				return ruleScoutEnvOverride
			}
		}
	}
	for ; i < len(args) && assignment.MatchString(args[i]); i++ {
		if isModeVar(args[i][:strings.IndexByte(args[i], '=')]) {
			return ruleScoutEnvOverride
		}
	}
	return ""
}
