package launcher

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// The hook bundle (ADR 0013, 0007): every hook runs one command that records
// the ship's state in a pane option, which Admiral watches. It is rewritten at
// every start and shared by all fleets.

const (
	needsYou = "needs_you"
	working  = "working"
)

func hookCommand(state string) string { return "tmux set -p @admiral-state " + state }

// Claude Code: settings file passed with --settings.
// Format: https://code.claude.com/docs/en/hooks (event → matcher groups → handlers).
// Matching hooks of one event run in parallel, so the working PreToolUse hook
// matches every tool except AskUserQuestion, whose hook says needs_you.
func claudeSettings() any {
	cmd := func(state string) []any {
		return []any{map[string]any{"type": "command", "command": hookCommand(state)}}
	}
	group := func(matcher, state string) map[string]any {
		g := map[string]any{"hooks": cmd(state)}
		if matcher != "" {
			g["matcher"] = matcher
		}
		return g
	}
	return map[string]any{"hooks": map[string]any{
		"PermissionRequest": []any{group("", needsYou)},
		"PreToolUse": []any{
			group("AskUserQuestion", needsYou),
			group("^(?!AskUserQuestion$).*", working),
		},
		"Notification":     []any{group("permission_prompt", needsYou), group("elicitation_dialog", needsYou)},
		"Stop":             []any{group("", needsYou)},
		"UserPromptSubmit": []any{group("", working)},
		"PostToolUse":      []any{group("", working)},
	}}
}

// Copilot: a plugin folder with a manifest and hooks.json.
// hooks.json: https://docs.github.com/en/copilot/reference/copilot-cli-reference/cli-hooks-reference
// (version 1, camelCase events, `bash` command, regex `matcher` on the tool
// name or notification type). The plugin manifest's format is not in that
// page: plugin.json with name, description and version is the common shape and
// is unconfirmed against an official source.
func copilotHooks() any {
	entry := func(matcher, state string) map[string]any {
		e := map[string]any{"type": "command", "bash": hookCommand(state)}
		if matcher != "" {
			e["matcher"] = matcher
		}
		return e
	}
	return map[string]any{"version": 1, "hooks": map[string]any{
		"notification":        []any{entry("permission_prompt|elicitation_dialog", needsYou)},
		"preToolUse":          []any{entry("ask_user", needsYou)},
		"agentStop":           []any{entry("", needsYou)},
		"userPromptSubmitted": []any{entry("", working)},
		"postToolUse":         []any{entry("", working)},
	}}
}

// Codex takes its hooks as -c flags, one per event, in the TOML of
// https://learn.chatgpt.com/docs/hooks ([[hooks.Event]] with a nested
// [[hooks.Event.hooks]] of type "command"), written inline. That page does not
// say how -c maps onto hook tables; the inline form below is the same value as
// the config.toml tables.
var codexEvents = []struct{ event, state string }{
	{"PermissionRequest", needsYou}, {"Stop", needsYou},
	{"UserPromptSubmit", working}, {"PostToolUse", working}, {"Interrupt", working},
}

func codexFlags() []string {
	var out []string
	for _, e := range codexEvents {
		out = append(out, "-c", fmt.Sprintf(`hooks.%s=[{hooks=[{type="command",command="%s"}]}]`, e.event, hookCommand(e.state)))
	}
	return append(out, "-c", `tui.notification_method="bel"`)
}

// WriteBundle rewrites the Claude Code settings file and the Copilot plugin
// folder under dir/harness.
func WriteBundle(dir string) error {
	root := filepath.Join(dir, "harness")
	files := map[string]any{
		filepath.Join(root, "claude-settings.json"): claudeSettings(),
		filepath.Join(root, "copilot", "plugin.json"): map[string]any{"name": "admiral",
			"description": "Tells Admiral when a ship needs you", "version": "1.0.0"},
		filepath.Join(root, "copilot", "hooks.json"): copilotHooks(),
	}
	for p, v := range files {
		if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
			return err
		}
		raw, err := json.MarshalIndent(v, "", "  ")
		if err != nil {
			return err
		}
		if err := os.WriteFile(p, append(raw, '\n'), 0o600); err != nil {
			return err
		}
	}
	return nil
}

func claudeSettingsPath(home string) string {
	return filepath.Join(home, "harness", "claude-settings.json")
}
func copilotPluginDir(home string) string { return filepath.Join(home, "harness", "copilot") }
