package launcher

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// The hook bundle is rewritten at every start and shared by all fleets.

// The binary is found through the ship's PATH ($ADMIRAL_HOME/bin).
func hookCommand(event string) string { return "admiral hook " + event }

// statusLine replaces the engineer's own (and its padding and refreshInterval); the wrapper
// keeps Claude's JSON and runs the engineer's line itself.
func statusLine(flags string) map[string]any {
	return map[string]any{"type": "command", "command": "admiral statusline" + flags}
}

// MergeToolMatcher spells the case out: a matcher is a JavaScript regular expression,
// which has no (?i).
const MergeToolMatcher = "^mcp__.*([Mm][Ee][Rr][Gg][Ee]|[Ee][Nn][Qq][Uu][Ee][Uu][Ee])"

// Claude Code lets a call through when a PreToolUse hook times out or fails, so onFailure
// "block" (Claude Code 2.1.295 and later) turns both into a refusal.
func guardGroup(matcher string) map[string]any {
	return map[string]any{"matcher": matcher,
		"hooks": []any{map[string]any{"type": "command", "command": "admiral guard pretooluse", "timeout": 10, "onFailure": "block"}}}
}

// Format: https://code.claude.com/docs/en/hooks. Matching hooks of one event run in
// parallel, so the working PreToolUse hook skips AskUserQuestion, whose hook says needs_you.
func claudeSettings() any {
	group := func(matcher, event string) map[string]any {
		g := map[string]any{"hooks": []any{map[string]any{"type": "command", "command": hookCommand(event)}}}
		if matcher != "" {
			g["matcher"] = matcher
		}
		return g
	}
	return map[string]any{"statusLine": statusLine(""), "hooks": map[string]any{
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

// ChatAllow is all the chat may run: it runs under --permission-mode dontAsk.
var ChatAllow = []any{"Bash(admiral ships *)", "Bash(admiral outcomes *)", "Bash(admiral inbox *)",
	"Bash(admiral orders *)", "Bash(admiral chat ask *)", "Skill(admiralbot)"}

// No idle_prompt: it fires after a minute of quiet and would turn every idle chat into
// waiting. SessionStart has no matcher, since Claude Code would match it against the source.
func claudeChatSettings() any {
	group := func(matcher, event string) map[string]any {
		g := map[string]any{"hooks": []any{map[string]any{"type": "command", "command": hookCommand(event)}}}
		if matcher != "" {
			g["matcher"] = matcher
		}
		return g
	}
	return map[string]any{"statusLine": statusLine(" --chat"), "permissions": map[string]any{
		"allow":                               ChatAllow,
		"deny":                                []any{"Edit", "Write", "NotebookEdit"},
		"blockReadsOutsideWorkingDirectories": true,
	}, "hooks": map[string]any{
		"SessionStart":     []any{group("", "SessionStart")},
		"UserPromptSubmit": []any{group("", "UserPromptSubmit")},
		"PreToolUse":       []any{group("AskUserQuestion", "PreToolUse")},
		"PostToolUse":      []any{group("", "PostToolUse")},
		"Notification":     []any{group("permission_prompt", "Notification"), group("elicitation_dialog", "Notification")},
		"Stop":             []any{group("", "Stop")},
	}}
}

// https://docs.github.com/en/copilot/reference/copilot-cli-reference/cli-hooks-reference and
// https://docs.github.com/en/copilot/reference/copilot-cli-reference/cli-plugin-reference
// don't say what --plugin-dir expects, so loading a plugin from a directory is unconfirmed.
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

// https://learn.chatgpt.com/docs/hooks does not say how -c maps onto hook tables; the
// inline form is the same value as the config.toml tables.
var codexEvents = []string{"SessionStart", "PermissionRequest", "Stop", "UserPromptSubmit", "PostToolUse", "Interrupt", "SubagentStart", "SubagentStop"}

func codexFlags() []string {
	var out []string
	for _, e := range codexEvents {
		out = append(out, "-c", fmt.Sprintf(`hooks.%s=[{hooks=[{type="command",command="%s"}]}]`, e, hookCommand(e)))
	}
	return append(out, "-c", `tui.notification_method="bel"`)
}

// The link is replaced at every start, so it follows a rebuilt or moved binary.
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

func ClaudeSettingsPath(home string) string {
	return filepath.Join(home, "harness", "claude-settings.json")
}
func claudeChatSettingsPath(home string) string {
	return filepath.Join(home, "harness", "chat-claude-settings.json")
}
func copilotPluginDir(home string) string { return filepath.Join(home, "harness", "copilot") }
