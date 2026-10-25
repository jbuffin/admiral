package launcher

import (
	"fmt"
	"strings"
)

// RefuseHookArgs vets the values of `admiral crew launch --arg` for a harness
// (#175). Admiral's hooks always hold, so a value that overrides or disables
// them is refused, autopilot or not, and the error names it. This is the one
// place that knows each harness's hook flags. A bare "--" is refused on every
// harness: it would end the flags before Admiral's prompt.
func RefuseHookArgs(harness string, args []string) error {
	for i, a := range args {
		next := ""
		if i+1 < len(args) {
			next = args[i+1]
		}
		if bad := hookArg(harness, a, next); bad != "" {
			return fmt.Errorf("--arg %s is refused: it would override or disable Admiral's hooks", bad)
		}
	}
	return nil
}

// hookArg is what to name in the refusal when arg (followed by next, which a
// flag may take as its value) overrides or disables the hooks, else "".
func hookArg(harness, arg, next string) string {
	if arg == "--" {
		return arg
	}
	switch harness {
	case "claude":
		if arg == "--settings" || strings.HasPrefix(arg, "--settings=") || arg == "--bare" || arg == "--safe-mode" {
			return arg
		}
	case "codex":
		switch {
		case arg == "-c" || arg == "--config":
			if hooksConfig(next) {
				return arg + " " + next
			}
		case strings.HasPrefix(arg, "--config="):
			if hooksConfig(strings.TrimPrefix(arg, "--config=")) {
				return arg
			}
		case strings.HasPrefix(arg, "-c"): // -c<kv> and -c=<kv>
			if hooksConfig(strings.TrimPrefix(strings.TrimPrefix(arg, "-c"), "=")) {
				return arg
			}
		case arg == "--disable":
			if hooksKey(next) {
				return arg + " " + next
			}
		case strings.HasPrefix(arg, "--disable="):
			if hooksKey(strings.TrimPrefix(arg, "--disable=")) {
				return arg
			}
		case strings.HasPrefix(arg, "-p"), arg == "--profile", strings.HasPrefix(arg, "--profile="):
			// A profile file could switch hooks off.
			return arg
		}
	case "copilot":
		if arg == "--plugin-dir" || strings.HasPrefix(arg, "--plugin-dir=") {
			return arg
		}
	}
	return ""
}

// hooksConfig reports whether a codex config override "key=value" sets the hooks
// table or a key under it, or turns the hooks feature off.
func hooksConfig(kv string) bool {
	key, _, _ := strings.Cut(kv, "=")
	return hooksKey(key)
}

// hooksKey reports whether a codex config key is the hooks table, below it, or
// features.hooks, ignoring spaces and quotes.
func hooksKey(key string) bool {
	key = strings.NewReplacer(`"`, "", `'`, "", " ", "", "\t", "").Replace(key)
	return key == "hooks" || strings.HasPrefix(key, "hooks.") || key == "features.hooks"
}
