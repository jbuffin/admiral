package launcher

import (
	"fmt"
	"strings"
)

// arity is how many values a flag takes from the --args after it.
type arity int

const (
	arityOne  arity = iota // exactly one, even if it starts with "-" (the harness does the same)
	arityMany              // one or more: the following args up to the next that starts with "-"
	arityAny               // zero or more, the same way
)

// flagRule is one flag on a harness's autopilot allowlist. names holds every
// spelling; a single-dash name also takes its value joined (-cKEY=V, -c=KEY=V),
// and a double-dash name takes it after "=". ok, when set, vets each value
// against the dock's args.
type flagRule struct {
	names []string
	arity arity
	ok    func(value string, dock []string) bool
}

// autopilotRules is each harness's allowlist (#175): the flags a caller with
// ADMIRAL_AUTOPILOT=1 may pass, none of which loosens the dock. Only claude's
// is reachable today, since LaunchCrew refuses the other harnesses first.
var autopilotRules = map[string][]flagRule{
	"claude": {
		{names: []string{"--model"}, arity: arityOne},
		{names: []string{"--advisor"}, arity: arityOne},
		{names: []string{"--effort"}, arity: arityOne},
		{names: []string{"--disallowedTools"}, arity: arityMany},
		{names: []string{"--permission-mode"}, arity: arityOne, ok: noLooserMode},
	},
	"codex": {
		{names: []string{"-m", "--model"}, arity: arityOne},
		{names: []string{"-s", "--sandbox"}, arity: arityOne, ok: func(v string, _ []string) bool { return v == "read-only" }},
		{names: []string{"-c", "--config"}, arity: arityOne, ok: func(v string, _ []string) bool {
			key, _, _ := strings.Cut(v, "=")
			return configKey(key) == "model_reasoning_effort"
		}},
	},
	"copilot": {
		{names: []string{"--model"}, arity: arityOne},
		{names: []string{"--reasoning-effort"}, arity: arityOne},
		{names: []string{"--add-dir"}, arity: arityOne},
		{names: []string{"--available-tools"}, arity: arityAny},
		{names: []string{"--deny-tool"}, arity: arityAny},
	},
}

// modeRank orders claude's permission modes from strictest to loosest. default
// and manual are one rank: manual is default's current spelling.
var modeRank = map[string]int{"plan": 0, "dontAsk": 1, "default": 2, "manual": 2, "acceptEdits": 3, "auto": 4, "bypassPermissions": 5}

// noLooserMode reports whether a --permission-mode is no looser than the dock's.
// A dock with no mode runs as default; one with an unknown mode only takes plan.
func noLooserMode(mode string, dock []string) bool {
	rank, known := modeRank[mode]
	if !known {
		return false
	}
	base := modeRank["default"]
	if m := dockMode(dock); m != "" {
		base = modeRank[m] // unknown: 0
	}
	return rank <= base
}

// dockMode is the last --permission-mode in the dock's args, or "".
func dockMode(dock []string) string {
	mode := ""
	for i, a := range dock {
		switch {
		case a == "--permission-mode" && i+1 < len(dock):
			mode = dock[i+1]
		case strings.HasPrefix(a, "--permission-mode="):
			mode = strings.TrimPrefix(a, "--permission-mode=")
		}
	}
	return mode
}

// AutopilotArgs vets the values of `admiral crew launch --arg` against the
// per-harness allowlist for a caller with ADMIRAL_AUTOPILOT=1 (#175): the
// autopilot args allowlist. dockArgs are the dock's own args for the harness; a
// --permission-mode may be no looser than the dock's. The error names the first
// value that doesn't fit.
func AutopilotArgs(harness string, dockArgs []string, args []string) error {
	rules, ok := autopilotRules[harness]
	if !ok {
		return fmt.Errorf("--arg is refused: there is no autopilot args allowlist for %s", harness)
	}
	for i := 0; i < len(args); {
		a := args[i]
		rule, value, joined := matchRule(rules, a)
		if rule == nil {
			return refuseArg(a, "it is not on the autopilot args allowlist")
		}
		i++
		var values []string
		if joined {
			values = []string{value}
		} else {
			switch rule.arity {
			case arityOne:
				if i == len(args) {
					return refuseArg(a, "it has no value")
				}
				values, i = args[i:i+1], i+1
			default:
				start := i
				for i < len(args) && !strings.HasPrefix(args[i], "-") {
					i++
				}
				values = args[start:i]
				if rule.arity == arityMany && len(values) == 0 {
					return refuseArg(a, "it has no value")
				}
			}
		}
		for _, v := range values {
			if rule.ok != nil && !rule.ok(v, dockArgs) {
				return refuseArg(a+" "+v, "that value could loosen the dock")
			}
		}
	}
	return nil
}

// matchRule finds the rule arg names. For a flag with its value joined it
// returns the value and joined; a bare flag returns joined false.
func matchRule(rules []flagRule, arg string) (r *flagRule, value string, joined bool) {
	for i := range rules {
		for _, n := range rules[i].names {
			switch {
			case arg == n:
				return &rules[i], "", false
			case strings.HasPrefix(n, "--") && strings.HasPrefix(arg, n+"="):
				return &rules[i], strings.TrimPrefix(arg, n+"="), true
			case !strings.HasPrefix(n, "--") && strings.HasPrefix(arg, n):
				return &rules[i], strings.TrimPrefix(strings.TrimPrefix(arg, n), "="), true
			}
		}
	}
	return nil, "", false
}

func refuseArg(arg, why string) error {
	return fmt.Errorf("--arg %s is refused: %s", arg, why)
}
