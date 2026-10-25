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
// and a double-dash name takes it after "=". valueFits, when set, vets each
// value against the dock's args. dockUnset refuses the flag when the dock's args
// already set it, for a flag whose repeat may replace the dock's value.
type flagRule struct {
	names     []string
	arity     arity
	valueFits func(value string, dock []string) bool
	dockUnset bool
}

// AutopilotHarnesses are the harnesses `admiral guard` covers, the only ones a
// caller with ADMIRAL_AUTOPILOT=1 may launch as crew. Each needs a table in
// autopilotRules. Adding codex or copilot here is not enough: settle the copilot
// flags noted below, and check the table against the harness's own parser.
var AutopilotHarnesses = []string{"claude"}

// autopilotRules is each harness's allowlist (#175): the flags a caller with
// ADMIRAL_AUTOPILOT=1 may pass. Claude's flags select a model or effort, deny
// tools, or keep the dock's permission mode or stricter. Codex's and copilot's
// are unreachable until AutopilotHarnesses names them. Copilot's --add-dir (file
// access) and --available-tools (an allowlist of tools, which a crew list may
// widen past a dock's) are the engineer's decision but can loosen the dock, so
// settle them before copilot is covered.
var autopilotRules = map[string][]flagRule{
	"claude": {
		{names: []string{"--model"}, arity: arityOne, valueFits: notDashLed},
		{names: []string{"--advisor"}, arity: arityOne, valueFits: notDashLed},
		{names: []string{"--effort"}, arity: arityOne, valueFits: notDashLed},
		// Whether a repeat adds to the dock's list or replaces it is unverified.
		{names: []string{"--disallowedTools"}, arity: arityMany, dockUnset: true},
		{names: []string{"--permission-mode"}, arity: arityOne, valueFits: noLooserMode},
	},
	"codex": {
		{names: []string{"-m", "--model"}, arity: arityOne},
		{names: []string{"-s", "--sandbox"}, arity: arityOne, valueFits: func(v string, _ []string) bool { return v == "read-only" }},
		{names: []string{"-c", "--config"}, arity: arityOne, valueFits: func(v string, _ []string) bool {
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

// notDashLed reports whether a value is safe to hand a claude flag that takes
// the next arg whatever it is: one that starts with "-" would read as a flag if
// claude's parsing ever changed.
func notDashLed(value string, _ []string) bool {
	return !strings.HasPrefix(value, "-")
}

// modeRank orders claude's permission modes from strictest to loosest. default
// and manual are one rank: manual is default's current spelling.
var modeRank = map[string]int{"plan": 0, "dontAsk": 1, "default": 2, "manual": 2, "acceptEdits": 3, "auto": 4, "bypassPermissions": 5}

// strictestRank is plan's: what a dock with a mode this table doesn't know allows.
const strictestRank = 0

// noLooserMode reports whether a crew --permission-mode is no looser than the
// dock's. A dock with no mode runs as default. Claude 2.1.310 doesn't take
// default as a value, so a crew boat can't say it; it is only the baseline.
func noLooserMode(mode string, dock []string) bool {
	rank, known := modeRank[mode]
	if !known || mode == "default" {
		return false
	}
	base := modeRank["default"]
	if m := dockMode(dock); m != "" {
		var known bool
		if base, known = modeRank[m]; !known {
			base = strictestRank
		}
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
		if rule.dockUnset && dockSets(dockArgs, rule.names) {
			return refuseArg(a, "the dock already sets it")
		}
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
			if rule.valueFits != nil && !rule.valueFits(v, dockArgs) {
				if joined {
					return refuseArg(a, "that value is not allowed")
				}
				return refuseArg(a+" "+v, "that value is not allowed")
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

// dockSets reports whether the dock's args set one of the flags, in either spelling.
func dockSets(dock, names []string) bool {
	for _, a := range dock {
		for _, n := range names {
			if a == n || strings.HasPrefix(a, n+"=") {
				return true
			}
		}
	}
	return false
}
