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

// hookCommand is the same for every harness: `admiral hook` works the state out
// from the event and the payload, and finds the binary through the ship's PATH
// ($ADMIRAL_HOME/bin).
func hookCommand(event string) string { return "admiral hook " + event }

// MergeToolMatcher is the PreToolUse matcher for MCP tools that could merge or
// enqueue a pull request: any mcp__ tool with merge or enqueue in its name, in
// any case. It spells the case out because a matcher is a JavaScript regular
// expression, which has no (?i). `admiral guard pretooluse` refuses every tool
// this matches, bar the read-only ones, so the two agree by construction.
const MergeToolMatcher = "^mcp__.*([Mm][Ee][Rr][Gg][Ee]|[Ee][Nn][Qq][Uu][Ee][Uu][Ee])"

// guardGroup runs the review guard (#115) on the tool calls its matcher names. The
// guard acts only on an autopilot ship, so every ship's settings can carry it.
// Claude Code lets a call through when a PreToolUse command hook times out or
// fails, so the handler has a short timeout and onFailure "block" (Claude Code
// 2.1.295 and later) turns both into a refusal.
func guardGroup(matcher string) map[string]any {
	return map[string]any{"matcher": matcher,
		"hooks": []any{map[string]any{"type": "command", "command": "admiral guard pretooluse", "timeout": 10, "onFailure": "block"}}}
}

// Claude Code: settings file passed with --settings.
// Format: https://code.claude.com/docs/en/hooks (event → matcher groups → handlers).
// Matching hooks of one event run in parallel, so the working PreToolUse hook
// matches every tool except AskUserQuestion, whose hook says needs_you.
func claudeSettings() any {
	group := func(matcher, event string) map[string]any {
		g := map[string]any{"hooks": []any{map[string]any{"type": "command", "command": hookCommand(event)}}}
		if matcher != "" {
			g["matcher"] = matcher
		}
		return g
	}
	return map[string]any{"hooks": map[string]any{
		"PermissionRequest": []any{group("", "PermissionRequest")},
		"PreToolUse": []any{
			group("AskUserQuestion", "PreToolUse"),
			group("^(?!AskUserQuestion$).*", "PreToolUse"),
			guardGroup("Bash"),
			guardGroup("^mcp__.*pull_request_review.*$"),
			guardGroup(MergeToolMatcher),
		},
		"Notification":     []any{group("permission_prompt", "Notification"), group("elicitation_dialog", "Notification")},
		"Stop":             []any{group("", "Stop")},
		"UserPromptSubmit": []any{group("", "UserPromptSubmit")},
		"PostToolUse":      []any{group("", "PostToolUse")},
		"SubagentStart":    []any{group("", "SubagentStart")},
		"SubagentStop":     []any{group("", "SubagentStop")},
	}}
}

// claudeChatSettings is the chat session's bundle (ADR 0019). It registers only
// what the chat needs: SessionStart (the transcript's path), the events that set
// working, idle and waiting, and nothing else. Not idle_prompt, which fires after
// a minute of quiet and would turn every idle chat into waiting; not
// SubagentStart/SubagentStop or the guards, which would write crew and guard
// facts on the chat's pane. SessionStart has no matcher, since Claude Code would
// match it against the source. PostToolUse takes the chat out of waiting once the
// engineer has answered in the terminal.
func claudeChatSettings() any {
	group := func(matcher, event string) map[string]any {
		g := map[string]any{"hooks": []any{map[string]any{"type": "command", "command": hookCommand(event)}}}
		if matcher != "" {
			g["matcher"] = matcher
		}
		return g
	}
	return map[string]any{"hooks": map[string]any{
		"SessionStart":     []any{group("", "SessionStart")},
		"UserPromptSubmit": []any{group("", "UserPromptSubmit")},
		"PreToolUse":       []any{group("AskUserQuestion", "PreToolUse")},
		"PostToolUse":      []any{group("", "PostToolUse")},
		"Notification":     []any{group("permission_prompt", "Notification"), group("elicitation_dialog", "Notification")},
		"Stop":             []any{group("", "Stop")},
	}}
}

// Copilot: a plugin folder with a manifest and hooks.json.
// hooks.json: https://docs.github.com/en/copilot/reference/copilot-cli-reference/cli-hooks-reference
// (version 1, camelCase events, `bash` command, regex `matcher` on the tool
// name or notification type). The plugin layout is the legacy one of
// https://docs.github.com/en/copilot/reference/copilot-cli-reference/cli-plugin-reference:
// plugin.json at the plugin root (only `name` is required) and hooks.json
// beside it. That page does not say what --plugin-dir expects, so loading a
// plugin from a directory is unconfirmed against a real Copilot.
func copilotHooks() any {
	entry := func(matcher, event string) map[string]any {
		e := map[string]any{"type": "command", "bash": hookCommand(event)}
		if matcher != "" {
			e["matcher"] = matcher
		}
		return e
	}
	return map[string]any{"version": 1, "hooks": map[string]any{
		"notification":        []any{entry("permission_prompt|elicitation_dialog", "notification")},
		"preToolUse":          []any{entry("ask_user", "preToolUse")},
		"agentStop":           []any{entry("", "agentStop")},
		"userPromptSubmitted": []any{entry("", "userPromptSubmitted")},
		"postToolUse":         []any{entry("", "postToolUse")},
		"subagentStart":       []any{entry("", "subagentStart")},
		"subagentStop":        []any{entry("", "subagentStop")},
	}}
}

// Codex takes its hooks as -c flags, one per event, in the TOML of
// https://learn.chatgpt.com/docs/hooks ([[hooks.Event]] with a nested
// [[hooks.Event.hooks]] of type "command"), written inline. That page does not
// say how -c maps onto hook tables; the inline form below is the same value as
// the config.toml tables.
var codexEvents = []string{"PermissionRequest", "Stop", "UserPromptSubmit", "PostToolUse", "Interrupt", "SubagentStart", "SubagentStop"}

func codexFlags() []string {
	var out []string
	for _, e := range codexEvents {
		out = append(out, "-c", fmt.Sprintf(`hooks.%s=[{hooks=[{type="command",command="%s"}]}]`, e, hookCommand(e)))
	}
	return append(out, "-c", `tui.notification_method="bel"`)
}

// LinkBinary points dir/bin/admiral at the running binary, which hooks reach
// through the ship's PATH. The link is replaced at every start, so it follows
// a rebuilt or moved binary.
func LinkBinary(dir, exe string) error {
	bin := filepath.Join(dir, "bin")
	if err := os.MkdirAll(bin, 0o700); err != nil {
		return err
	}
	link := filepath.Join(bin, "admiral")
	tmp := link + ".new"
	_ = os.Remove(tmp)
	if err := os.Symlink(exe, tmp); err != nil {
		return err
	}
	return os.Rename(tmp, link)
}

// WriteBundle rewrites the Claude Code settings file and the Copilot plugin
// folder under dir/harness.
func WriteBundle(dir string) error {
	root := filepath.Join(dir, "harness")
	files := map[string]any{
		filepath.Join(root, "claude-settings.json"):      claudeSettings(),
		filepath.Join(root, "chat-claude-settings.json"): claudeChatSettings(),
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
func claudeChatSettingsPath(home string) string {
	return filepath.Join(home, "harness", "chat-claude-settings.json")
}
func copilotPluginDir(home string) string { return filepath.Join(home, "harness", "copilot") }
