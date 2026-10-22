package cli

import (
	"encoding/json"
	"strings"
	"testing"
)

// The reason hookDecision returns (#113) is the code @admiral-reason holds
// with needs_you: a short word, plus the tool name or notification type when
// the payload's is a safe token. Payloads go through encoding/json so the
// field tags are exercised.
func TestHookReason(t *testing.T) {
	long64 := strings.Repeat("a", 64)
	cases := []struct {
		name, event, payload, want string
	}{
		// Claude Code and Codex share event names.
		{"permission names the tool", "PermissionRequest", `{"tool_name":"Bash","tool_input":{"command":"rm -rf x"}}`, "permission:Bash"},
		{"permission keeps an MCP tool name", "PermissionRequest", `{"tool_name":"mcp__srv__tool"}`, "permission:mcp__srv__tool"},
		{"permission keeps a 64-character name", "PermissionRequest", `{"tool_name":"` + long64 + `"}`, "permission:" + long64},
		{"permission drops a 65-character name", "PermissionRequest", `{"tool_name":"` + long64 + `a"}`, "permission"},
		{"permission with no tool", "PermissionRequest", `{}`, "permission"},
		{"permission drops a name with a space", "PermissionRequest", `{"tool_name":"Bash ls"}`, "permission"},
		{"permission drops a name with a semicolon", "PermissionRequest", `{"tool_name":"x;kill-server"}`, "permission"},
		{"permission drops a name with a newline", "PermissionRequest", `{"tool_name":"a\nb"}`, "permission"},
		{"permission drops a tmux format", "PermissionRequest", `{"tool_name":"#{pane_id}"}`, "permission"},
		{"AskUserQuestion is a question", "PreToolUse", `{"tool_name":"AskUserQuestion","tool_input":{"questions":[{"question":"Delete prod?"}]}}`, "question"},
		{"PreToolUse of Bash sets no reason", "PreToolUse", `{"tool_name":"Bash"}`, ""},
		{"permission_prompt", "Notification", `{"notification_type":"permission_prompt","message":"Claude needs your permission"}`, "notification:permission_prompt"},
		{"elicitation_dialog", "Notification", `{"notification_type":"elicitation_dialog"}`, "notification:elicitation_dialog"},
		{"a notification that is not needs you", "Notification", `{"notification_type":"idle_prompt"}`, ""},
		{"Stop", "Stop", `{"background_tasks":[]}`, "stop"},
		{"Codex Stop", "Stop", `{"turn_id":"01a15222-ed87","last_assistant_message":"done"}`, "stop"},
		{"Stop with a running subagent sets nothing", "Stop", `{"background_tasks":[{"type":"subagent","status":"running"}]}`, ""},
		{"Stop with only a running shell", "Stop", `{"background_tasks":[{"type":"shell","status":"running"}]}`, "stop"},
		{"a working event", "UserPromptSubmit", `{"prompt":"go"}`, ""},
		{"an unknown event", "NoSuchEvent", `{}`, ""},
		// Copilot.
		{"copilot ask_user is a question", "preToolUse", `{"toolName":"ask_user","toolArgs":{"question":"which?"}}`, "question"},
		{"copilot other tool", "preToolUse", `{"toolName":"bash"}`, ""},
		{"copilot agentStop of the lead", "agentStop", `{"stopReason":"end_turn"}`, "stop"},
		{"copilot agentStop of a subagent", "agentStop", `{"agentId":"788718ef","stopReason":"end_turn"}`, ""},
		{"copilot permission_prompt", "notification", `{"notification_type":"permission_prompt"}`, "notification:permission_prompt"},
		{"copilot notification with a bad type sets no state", "notification", `{"notification_type":"elicitation dialog"}`, ""},
		{"copilot working event", "userPromptSubmitted", `{"prompt":"go"}`, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var p hookPayload
			if err := json.Unmarshal([]byte(c.payload), &p); err != nil {
				t.Fatal(err)
			}
			if _, got, _ := hookDecision(c.event, p); got != c.want {
				t.Errorf("hookDecision(%s) reason = %q, want %q", c.event, got, c.want)
			}
		})
	}
}
