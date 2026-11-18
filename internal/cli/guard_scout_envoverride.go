package cli

import (
	"path/filepath"
	"slices"
	"strings"

	"mvdan.cc/sh/v3/syntax"

	"github.com/jbuffin/admiral/internal/launcher"
)

const ruleScoutEnvOverride = "scout_env_override"

// The variables a crew launch reads to know its caller is a scout: a scout's shell must not drop or change them.
var scoutModeVars = []string{launcher.ScoutEnv, "ADMIRAL_AUTOPILOT"}

func isModeVar(name string) bool { return slices.Contains(scoutModeVars, name) }

// scoutEnvOverrideRule refuses a command that unsets, clears or reassigns a scout's mode variables, anywhere in it
// (a pipe, a subshell, bash -c, eval). A command it cannot parse is left to the scout walker, which refuses it.
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
		}
		return rule == ""
	})
	return rule
}

// envOverrideInWords looks at each word that names a command able to change the environment of what follows it.
func envOverrideInWords(words []string, depth int) string {
	for i, w := range words {
		rest := words[i+1:]
		switch name := filepath.Base(w); name {
		case "unset":
			for _, a := range rest {
				if isModeVar(a) || hasPlaceholder(a) {
					return ruleScoutEnvOverride
				}
			}
		case "env":
			if envChanges(rest) {
				return ruleScoutEnvOverride
			}
		case "eval":
			if scoutEnvOverrideRule(strings.Join(rest, " "), depth+1) != "" {
				return ruleScoutEnvOverride
			}
		case "sh", "bash", "zsh", "dash", "ksh":
			for j, a := range rest {
				if strings.HasPrefix(a, "-") && !strings.HasPrefix(a, "--") && strings.Contains(a, "c") && j+1 < len(rest) {
					if scoutEnvOverrideRule(rest[j+1], depth+1) != "" {
						return ruleScoutEnvOverride
					}
					break
				}
			}
		}
	}
	return ""
}

// envChanges reports whether the flags and assignments after env drop or change a mode variable.
func envChanges(args []string) bool {
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "-i" || a == "--ignore-environment" || a == "-":
			return true
		case a == "-u" || a == "--unset":
			if i+1 >= len(args) || isModeVar(args[i+1]) || hasPlaceholder(args[i+1]) {
				return true
			}
			i++
		case strings.HasPrefix(a, "--unset="):
			if v := strings.TrimPrefix(a, "--unset="); isModeVar(v) || hasPlaceholder(v) {
				return true
			}
		case strings.HasPrefix(a, "-u") && !strings.HasPrefix(a, "--"):
			if v := a[2:]; isModeVar(v) || hasPlaceholder(v) {
				return true
			}
		case strings.HasPrefix(a, "-") && strings.Contains(a, "i") && !strings.HasPrefix(a, "--") && !strings.Contains(a, "="):
			return true
		case assignment.MatchString(a):
			if isModeVar(a[:strings.IndexByte(a, '=')]) {
				return true
			}
		case !strings.HasPrefix(a, "-"):
			return false // the command env runs
		}
	}
	return false
}
