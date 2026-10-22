package cli

import (
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"regexp"
	"strings"
)

// Hook is `admiral hook <event>`, the command every harness hook runs (ADR
// 0013, 0014). It reads the harness's hook JSON on stdin and sets
// @admiral-state on $TMUX_PANE, with @admiral-reason for needs_you, or does
// nothing. It is internal, so no usage text names it, and it never fails: a
// harness treats a hook's exit code and output as its own signal.
func Hook(args []string, std IO) error {
	pane := os.Getenv("TMUX_PANE")
	if len(args) == 0 || pane == "" {
		return nil
	}
	raw, _ := io.ReadAll(io.LimitReader(std.In, 1<<20))
	var p hookPayload
	if json.Unmarshal(raw, &p) != nil {
		return nil
	}
	if state, reason := hookDecision(args[0], p); state != "" {
		_ = exec.Command("tmux", hookCall(pane, state, reason)...).Run()
	}
	return nil
}

// hookCall is the one tmux call that writes the reason and the state, reason
// first (for working too, as Host.setPane does) so the poll never sees
// needs_you without it. A notification's reason is written only if none is set
// (-o), so permission:Bash survives Claude's PermissionRequest and Notification
// in either order; -q keeps a set reason from making tmux skip the state write.
func hookCall(pane, state, reason string) []string {
	call := []string{"set", "-p"}
	switch {
	case reason == "":
		call = append(call, "-u", "-t", pane, "@admiral-reason")
	case strings.HasPrefix(reason, "notification"):
		call = append(call, "-o", "-q", "-t", pane, "@admiral-reason", reason)
	default:
		call = append(call, "-t", pane, "@admiral-reason", reason)
	}
	return append(call, ";", "set", "-p", "-t", pane, "@admiral-state", state)
}

// hookPayload holds the fields of Claude Code's, Codex's and Copilot's hook
// JSON that decide a state.
type hookPayload struct {
	ToolName         string `json:"tool_name"`         // Claude Code
	ToolNameCopilot  string `json:"toolName"`          // Copilot
	NotificationType string `json:"notification_type"` // Claude Code, Copilot
	AgentID          string `json:"agentId"`           // Copilot: set inside a subagent
	BackgroundTasks  []struct {
		Type   string `json:"type"`
		Status string `json:"status"`
	} `json:"background_tasks"` // Claude Code's Stop; Codex sends none
}

// hookDecision is the state an event sets and, for needs_you, the short code
// @admiral-reason holds with it; "" for no state. Claude Code and Codex share
// event names, so one rule serves both. Only a tool name or notification type
// is ever kept in a reason, and only as a safe token: nothing else of the
// payload is stored.
func hookDecision(event string, p hookPayload) (state, reason string) {
	switch event {
	case "PermissionRequest":
		return "needs_you", withToken("permission", p.ToolName)
	case "PreToolUse":
		if p.ToolName == "AskUserQuestion" {
			return "needs_you", "question"
		}
		return "working", ""
	case "UserPromptSubmit", "PostToolUse", "Interrupt", "postToolUse":
		return "working", ""
	case "Notification", "notification":
		if p.NotificationType == "permission_prompt" || p.NotificationType == "elicitation_dialog" {
			return "needs_you", withToken("notification", p.NotificationType)
		}
	case "Stop":
		// Only a subagent or an agent-team teammate holds the turn open; a
		// background shell (a Monitor) doesn't.
		for _, t := range p.BackgroundTasks {
			if t.Status == "running" && (t.Type == "subagent" || t.Type == "teammate") {
				return "", ""
			}
		}
		return "needs_you", "stop"
	case "preToolUse":
		if p.ToolNameCopilot == "ask_user" {
			return "needs_you", "question"
		}
	case "agentStop":
		if p.AgentID == "" {
			return "needs_you", "stop"
		}
	case "userPromptSubmitted":
		if p.AgentID == "" {
			return "working", ""
		}
	}
	return "", ""
}

var safeToken = regexp.MustCompile(`^[A-Za-z0-9_.:-]{1,64}$`)

// withToken is prefix:token, or the bare prefix when token isn't short and made
// of characters tmux and a format leave alone.
func withToken(prefix, token string) string {
	if safeToken.MatchString(token) {
		return prefix + ":" + token
	}
	return prefix
}
