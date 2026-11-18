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
		// Recorded on Claude Code 2.1.327: a background subagent's tool hooks carry agent_id and fire while the lead's question waits.
		{"PreToolUse inside a subagent", "PreToolUse", `{"tool_name":"Bash","agent_id":"a0f790cb4a518e0f9","agent_type":"general-purpose"}`, "working"},
		{"PostToolUse inside a subagent", "PostToolUse", `{"tool_name":"Bash","agent_id":"a0f790cb4a518e0f9","agent_type":"general-purpose"}`, "working"},
		{"UserPromptSubmit", "UserPromptSubmit", `{"prompt":"go"}`, "working"},
		{"PostToolUse whose tool_response is a string", "PostToolUse", `{"tool_name":"Read","tool_response":"file text"}`, "working"},
		{"codex Interrupt", "Interrupt", `{"turn_id":"01a15222-ed87"}`, "needs_you"},
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

// A subagent's tool hooks clear its own permission prompt answered in the terminal, but never the lead's question.
func TestSubagentToolHooksKeepOnlyTheLeadsQuestion(t *testing.T) {
	var p hookPayload
	if err := json.Unmarshal([]byte(`{"tool_name":"Bash","agent_id":"a0f790cb4a518e0f9","agent_type":"general-purpose"}`), &p); err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct{ name, reason, want string }{
		{"subagent PostToolUse after a subagent permission prompt clears needs_you", "permission:Bash", "working"},
		{"subagent PostToolUse after a notification prompt clears needs_you", "notification:permission_prompt", "working"},
		{"subagent PostToolUse while the lead asks a question", "question", ""},
		{"subagent PostToolUse while the lead's AskUserQuestion shows as a permission", "permission:AskUserQuestion", ""},
	} {
		if got := keepsQuestion(hookDecision("PostToolUse", p), c.reason).state; got != c.want {
			t.Errorf("%s: state = %q, want %q", c.name, got, c.want)
		}
	}
}
