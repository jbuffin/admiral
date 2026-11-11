package cli

import (
	"encoding/json"
	"testing"
)

// The state each harness event writes on the ship's pane; "" writes none.
func TestHookState(t *testing.T) {
	for _, c := range []struct{ name, event, payload, want string }{
		{"claude Stop with a running subagent", "Stop", `{"background_tasks":[{"type":"subagent","status":"running"}]}`, ""},
		{"claude Stop with a running teammate", "Stop", `{"background_tasks":[{"type":"teammate","status":"running"}]}`, ""},
		{"claude Stop with only a running shell", "Stop", `{"background_tasks":[{"type":"shell","status":"running"}]}`, "needs_you"},
		{"claude Stop with a finished subagent", "Stop", `{"background_tasks":[{"type":"subagent","status":"completed"}]}`, "needs_you"},
		{"codex Stop", "Stop", `{"turn_id":"01a15222-ed87","last_assistant_message":"done"}`, "needs_you"},
		{"PermissionRequest", "PermissionRequest", `{"tool_name":"Bash"}`, "needs_you"},
		{"AskUserQuestion", "PreToolUse", `{"tool_name":"AskUserQuestion"}`, "needs_you"},
		{"PreToolUse of another tool", "PreToolUse", `{"tool_name":"Bash"}`, "working"},
		{"UserPromptSubmit", "UserPromptSubmit", `{"prompt":"go"}`, "working"},
		{"PostToolUse whose tool_response is a string", "PostToolUse", `{"tool_name":"Read","tool_response":"file text"}`, "working"},
		{"codex Interrupt", "Interrupt", `{"turn_id":"01a15222-ed87"}`, "working"},
		{"permission_prompt", "Notification", `{"notification_type":"permission_prompt"}`, "needs_you"},
		{"copilot agentStop of the lead", "agentStop", `{"stopReason":"end_turn"}`, "needs_you"},
		{"copilot userPromptSubmitted of a subagent", "userPromptSubmitted", `{"prompt":"go","agentId":"788718ef"}`, ""},
		{"copilot userPromptSubmitted of the lead", "userPromptSubmitted", `{"prompt":"go"}`, "working"},
		{"copilot ask_user", "preToolUse", `{"toolName":"ask_user"}`, "needs_you"},
	} {
		var p hookPayload
		if err := json.Unmarshal([]byte(c.payload), &p); err != nil {
			t.Fatal(err)
		}
		if got := hookDecision(c.event, p).state; got != c.want {
			t.Errorf("%s: state = %q, want %q", c.name, got, c.want)
		}
	}
}
