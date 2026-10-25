package launcher

import (
	"fmt"
	"slices"
	"strings"
	"unicode"
)

// subcommands are the names of each harness's subcommands, aliases and hidden
// commands included, on claude 2.1.310, codex-cli 0.163.0 and GitHub Copilot CLI
// 1.0.103. The visible ones are from `<harness> --help`; the hidden ones (not in
// --help) were found in the binaries and each confirmed by `<harness> <name> --help`. A bare --arg that equals one would run that
// subcommand in place of the session, and with it none of Admiral's hooks.
var subcommands = map[string][]string{
	"claude": {"agents", "attach", "auth", "auto-mode", "doctor", "gateway", "import", "install", "logs", "mcp",
		"plugin", "plugins", "purge", "respawn", "rm", "setup-token", "stop", "kill", "ultrareview", "update", "upgrade", "help",
		"remote-control", "rc", "remote", "project", "sandbox", "design-login"},
	"codex": {"agents", "exec", "e", "review", "login", "logout", "mcp", "plugin", "app-server", "remote-control", "app",
		"completion", "update", "doctor", "sandbox", "debug", "apply", "a", "resume", "queue", "archive", "delete",
		"migrate-rollouts", "unarchive", "fork", "cloud", "exec-server", "features", "help",
		"cloud-tasks", "tcp-tunnel", "stdio-to-uds", "responses-api-proxy", "execpolicy"},
	"copilot": {"app", "login", "help", "init", "config", "update", "version", "workflow", "sessions", "memories",
		"plugin", "mcp", "skill", "instruction", "lsp", "sandbox", "completion"},
}

// codexHookFeatures are the names codex knows its hooks feature by: `hooks` and
// the legacy alias `codex_hooks`.
var codexHookFeatures = []string{"hooks", "codex_hooks"}

// RefuseHookArgs vets the values of `admiral crew launch --arg` for a harness
// (#175). Admiral's hooks always hold, so a value that overrides or disables
// them, runs a subcommand or moves the session off this machine is refused, and
// the error names it. This is where each harness's hook-flag knowledge lives. A
// bare "--" is refused on every harness: it would end the flags before
// Admiral's prompt.
func RefuseHookArgs(harness string, args []string) error {
	for i, a := range args {
		if hasControl(a) {
			return fmt.Errorf("--arg %q is refused: it holds an ASCII control character", a)
		}
		next := ""
		if i+1 < len(args) {
			next = args[i+1]
		}
		if bad := hookArg(harness, a, next); bad != "" {
			return fmt.Errorf("--arg %q is refused: it would override or disable Admiral's hooks, run a subcommand or leave this machine", bad)
		}
	}
	return nil
}

// hookArg is what to name in the refusal when arg (followed by next, which a
// flag may take as its value) is one RefuseHookArgs refuses, else "".
func hookArg(harness, arg, next string) string {
	if arg == "--" || slices.Contains(subcommands[harness], arg) {
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
			if codexConfigRefused(next) {
				return arg + " " + next
			}
		case strings.HasPrefix(arg, "--config="):
			if codexConfigRefused(strings.TrimPrefix(arg, "--config=")) {
				return arg
			}
		case strings.HasPrefix(arg, "-c"): // -c<kv> and -c=<kv>
			if codexConfigRefused(strings.TrimPrefix(strings.TrimPrefix(arg, "-c"), "=")) {
				return arg
			}
		case arg == "--disable":
			if slices.Contains(codexHookFeatures, next) {
				return arg + " " + next
			}
		case strings.HasPrefix(arg, "--disable="):
			if slices.Contains(codexHookFeatures, strings.TrimPrefix(arg, "--disable=")) {
				return arg
			}
		case strings.HasPrefix(arg, "-p"), arg == "--profile", strings.HasPrefix(arg, "--profile="):
			// A profile file could switch hooks off.
			return arg
		case arg == "--remote", strings.HasPrefix(arg, "--remote="):
			// The session would run on a remote app server, not here.
			return arg
		}
	case "copilot":
		if arg == "--plugin-dir" || strings.HasPrefix(arg, "--plugin-dir=") ||
			arg == "--connect" || strings.HasPrefix(arg, "--connect=") || arg == "--acp" || strings.HasPrefix(arg, "--acp=") {
			return arg
		}
	}
	return ""
}

// codexConfigRefused reports whether a codex config override "key=value" can
// switch Admiral's hooks off: the key is the hooks table or below it, the whole
// features table or the hooks feature, or a profile (which layers a config file).
func codexConfigRefused(kv string) bool {
	key, _, _ := strings.Cut(kv, "=")
	key = configKey(key)
	switch {
	case key == "hooks", strings.HasPrefix(key, "hooks."),
		key == "features", key == "profile", key == "profiles", strings.HasPrefix(key, "profiles."):
		return true
	}
	feature, ok := strings.CutPrefix(key, "features.")
	return ok && slices.Contains(codexHookFeatures, feature)
}

// configKey is a codex config key as codex reads it. Codex trims the key like
// Rust's str::trim (every Unicode space) and reads quoted TOML segments, so
// spaces and quotes are dropped everywhere.
func configKey(key string) string {
	return strings.Map(func(r rune) rune {
		if unicode.IsSpace(r) || r == '"' || r == '\'' {
			return -1
		}
		return r
	}, key)
}
