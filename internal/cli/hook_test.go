package cli

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/jbuffin/admiral/internal/sessionhost"
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
			if got := hookDecision(c.event, p).reason; got != c.want {
				t.Errorf("hookDecision(%s) reason = %q, want %q", c.event, got, c.want)
			}
		})
	}
}

// @admiral-subagents after an event (ADR 0014, #72 stories 21-23), on the
// payloads recorded in docs/research/crew-boats.md (trimmed). A list is
// written "id:type" for a short fixture; the start of a kept entry is t0.
func TestHookSubagentList(t *testing.T) {
	const t0 = "2026-10-24T18:00:00Z"
	now := time.Date(2026, 10, 24, 18, 30, 0, 0, time.UTC)
	const t1 = "2026-10-24T18:30:00Z"
	list := func(entries ...string) []sessionhost.Subagent {
		var out []sessionhost.Subagent
		for _, e := range entries {
			id, typ, _ := strings.Cut(e, ":")
			out = append(out, sessionhost.Subagent{ID: id, Type: typ, Start: t0})
		}
		return out
	}
	with := func(l []sessionhost.Subagent, id, typ string) []sessionhost.Subagent {
		return append(l, sessionhost.Subagent{ID: id, Type: typ, Start: t1})
	}
	var full []sessionhost.Subagent
	for i := 0; i < 64; i++ {
		full = append(full, sessionhost.Subagent{ID: "s" + strings.Repeat("x", i), Type: "t", Start: t0})
	}
	const (
		claudeStart = `{"hook_event_name":"SubagentStart","agent_id":"a0f790cb4a518e0f9","agent_type":"Explore"}`
		claudeStop  = `{"hook_event_name":"SubagentStop","agent_id":"a0f790cb4a518e0f9","agent_type":"Explore","background_tasks":[{"id":"a0f790cb4a518e0f9","type":"subagent","status":"running","agent_type":"Explore"}]}`
		claudeInt   = `{"hook_event_name":"SubagentStop","agent_id":"af0aff5a46b69da59","agent_type":"","background_tasks":[]}`
		codexStart  = `{"hook_event_name":"SubagentStart","agent_id":"01a15222-3773-7723-a517-989315c87c35","agent_type":"default","turn_id":"01a15222-3793","model":"gpt-6-luna"}`
		codexStop   = `{"hook_event_name":"SubagentStop","agent_id":"01a15222-3773-7723-a517-989315c87c35","agent_type":"default","stop_hook_active":false,"last_assistant_message":"alpha"}`
		copStart    = `{"agentName":"general-purpose","agentId":"788718ef-39f6-45b1-ade6-7cb742bc6958","agentType":"general-purpose","parentAgentId":"796c5372-28bb-496c-bb5b-f9e223ac6ff8"}`
		copStop     = `{"agentId":"788718ef-39f6-45b1-ade6-7cb742bc6958","agentType":"general-purpose","agentName":"general-purpose","response":"done","stopReason":"end_turn","parentAgentId":"796c5372-28bb-496c-bb5b-f9e223ac6ff8"}`
	)
	const (
		cID = "a0f790cb4a518e0f9"
		xID = "01a15222-3773-7723-a517-989315c87c35"
		pID = "788718ef-39f6-45b1-ade6-7cb742bc6958"
	)
	cases := []struct {
		name, harness, event, payload string
		cur, want                     []sessionhost.Subagent
	}{
		// Claude Code.
		{"claude start adds", "claude", "SubagentStart", claudeStart, nil, with(nil, cID, "Explore")},
		{"claude start for a listed id adds nothing and keeps its start", "claude", "SubagentStart", claudeStart, list(cID + ":Explore"), list(cID + ":Explore")},
		{"claude start keeps the others", "claude", "SubagentStart", claudeStart, list("b1:Plan"), with(list("b1:Plan"), cID, "Explore")},
		{"claude stop of a listed id removes it despite its own background_tasks", "claude", "SubagentStop", claudeStop, list(cID+":Explore", "b1:Plan"), list("b1:Plan")},
		{"claude internal agent stop (unknown id, no type) leaves the list", "claude", "SubagentStop", claudeInt, list(cID + ":Explore"), list(cID + ":Explore")},
		{"claude Stop replaces the list from running subagents, keeping known starts", "claude", "Stop",
			`{"hook_event_name":"Stop","background_tasks":[{"id":"` + cID + `","type":"subagent","status":"running","agent_type":"Explore"},{"id":"c2","type":"subagent","status":"running","agent_type":"Plan"},{"id":"sh1","type":"shell","status":"running","command":"sleep 99"},{"id":"d3","type":"subagent","status":"completed","agent_type":"Plan"}],"session_crons":[]}`,
			list("gone:Plan", cID+":Explore"), with(list(cID+":Explore"), "c2", "Plan")},
		{"claude Stop lists a repeated id once", "claude", "Stop",
			`{"background_tasks":[{"id":"c2","type":"subagent","status":"running","agent_type":"Plan"},{"id":"c2","type":"teammate","status":"running","agent_type":"Plan"}]}`, nil, with(nil, "c2", "Plan")},
		{"claude Stop keeps a running teammate", "claude", "Stop",
			`{"background_tasks":[{"id":"m1","type":"teammate","status":"running","agent_type":"general-purpose"}]}`, nil, with(nil, "m1", "general-purpose")},
		{"claude Stop with only a shell empties the list", "claude", "Stop",
			`{"background_tasks":[{"id":"sh1","type":"shell","status":"running"}]}`, list(cID + ":Explore"), nil},
		{"claude Stop with background_tasks [] empties the list", "claude", "Stop", `{"last_assistant_message":"alpha","background_tasks":[],"session_crons":[]}`, list(cID + ":Explore"), nil},
		{"unknown harness: background_tasks [] is Claude's Stop and empties", "", "Stop", `{"background_tasks":[]}`, list(cID + ":Explore"), nil},
		{"unknown harness: a Stop with no background_tasks is Codex's and leaves the list", "", "Stop", `{"turn_id":"01a15222-ed87"}`, list(cID + ":Explore"), list(cID + ":Explore")},
		// Codex.
		{"codex start adds", "codex", "SubagentStart", codexStart, list("b1:default"), with(list("b1:default"), xID, "default")},
		{"codex start for a listed id adds nothing", "codex", "SubagentStart", codexStart, list(xID + ":default"), list(xID + ":default")},
		{"codex stop of a listed id removes it", "codex", "SubagentStop", codexStop, list(xID+":default", "b1:default"), list("b1:default")},
		{"codex stop of an unknown id leaves the list", "codex", "SubagentStop", codexStop, list("b1:default"), list("b1:default")},
		{"codex Stop leaves the list alone", "codex", "Stop", `{"turn_id":"01a15222-ed87","last_assistant_message":"done"}`, list(xID + ":default"), list(xID + ":default")},
		// Copilot.
		{"copilot subagentStart adds", "copilot", "subagentStart", copStart, nil, with(nil, pID, "general-purpose")},
		{"copilot subagentStart for a listed id adds nothing", "copilot", "subagentStart", copStart, list(pID + ":general-purpose"), list(pID + ":general-purpose")},
		{"copilot subagentStop of a listed id removes it", "copilot", "subagentStop", copStop, list(pID+":general-purpose", "b1:t"), list("b1:t")},
		{"copilot subagentStop of an unknown id leaves the list", "copilot", "subagentStop", copStop, list("b1:t"), list("b1:t")},
		{"copilot lead agentStop clears all", "copilot", "agentStop", `{"stopReason":"end_turn","stop_hook_active":false}`, list(pID+":general-purpose", "b1:t"), nil},
		{"copilot subagent agentStop leaves the list", "copilot", "agentStop", `{"stopReason":"end_turn","agentId":"` + pID + `","agentType":"general-purpose","parentAgentId":"796c5372"}`, list(pID + ":general-purpose"), list(pID + ":general-purpose")},
		{"copilot lead userPromptSubmitted clears all", "copilot", "userPromptSubmitted", `{"prompt":"go"}`, list(pID + ":general-purpose"), nil},
		{"copilot subagent userPromptSubmitted leaves the list", "copilot", "userPromptSubmitted", `{"prompt":"x","agentId":"` + pID + `"}`, list(pID + ":general-purpose"), list(pID + ":general-purpose")},
		// What reaches tmux.
		{"an unsafe id is dropped", "claude", "SubagentStart", `{"agent_id":"x;kill-server","agent_type":"Explore"}`, list("b1:Plan"), list("b1:Plan")},
		{"an id with a tmux format is dropped", "codex", "SubagentStart", `{"agent_id":"#{pane_id}","agent_type":"default"}`, nil, nil},
		{"an unsafe type is stored as empty", "claude", "SubagentStart", `{"agent_id":"ok1","agent_type":"Ex plore"}`, nil, with(nil, "ok1", "")},
		{"an unsafe copilot type is stored as empty", "copilot", "subagentStart", `{"agentId":"ok1","agentType":"a\nb"}`, nil, with(nil, "ok1", "")},
		{"an unsafe id in a Stop's background_tasks is dropped", "claude", "Stop",
			`{"background_tasks":[{"id":"bad id","type":"subagent","status":"running","agent_type":"Plan"},{"id":"c2","type":"subagent","status":"running","agent_type":"x y"}]}`, nil, with(nil, "c2", "")},
		{"the list is capped at 64", "claude", "SubagentStart", claudeStart, full, full},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var p hookPayload
			if err := json.Unmarshal([]byte(c.payload), &p); err != nil {
				t.Fatal(err)
			}
			got := subagentList(c.event, p, c.harness, c.cur, now)
			if len(got) == 0 && len(c.want) == 0 {
				return
			}
			if !reflect.DeepEqual(got, c.want) {
				t.Errorf("subagentList(%s, %s) = %+v, want %+v", c.harness, c.event, got, c.want)
			}
		})
	}
}

// The lead rule (#72 story 12, #152 AC3): a lead's Stop waits while a crew
// boat works. The facts come from tmux; the decision is pure.
func TestHookLeadRule(t *testing.T) {
	stop := hookWrite{state: "needs_you", reason: "stop"}
	const (
		claudeStop     = `{"hook_event_name":"Stop","background_tasks":[],"session_crons":[]}`
		claudeStopTask = `{"hook_event_name":"Stop","background_tasks":[{"id":"a0f790cb4a518e0f9","type":"subagent","status":"running","agent_type":"Explore"}]}`
		codexStop      = `{"hook_event_name":"Stop","turn_id":"01a15222-ed87","last_assistant_message":"done"}`
		copLead        = `{"stopReason":"end_turn","stop_hook_active":false}`
		copSub         = `{"stopReason":"end_turn","agentId":"788718ef","agentType":"general-purpose","parentAgentId":"796c5372"}`
	)
	working, idle := crewFacts{CrewWorking: true}, crewFacts{}
	crew := crewFacts{ThisIsCrew: true}
	crewWithOthers := crewFacts{ThisIsCrew: true, CrewWorking: true}
	failed := crewFacts{CrewReadFailed: true, CrewWorking: true}
	cases := []struct {
		name, event, payload string
		f                    crewFacts
		want                 hookWrite
	}{
		{"claude lead Stop with crew working writes no state", "Stop", claudeStop, working, hookWrite{}},
		{"codex lead Stop with crew working writes no state", "Stop", codexStop, working, hookWrite{}},
		{"copilot lead agentStop with crew working writes no state", "agentStop", copLead, working, hookWrite{}},
		{"claude lead Stop with no crew working is needs_you stop", "Stop", claudeStop, idle, stop},
		{"codex lead Stop with no crew working is needs_you stop", "Stop", codexStop, idle, stop},
		{"copilot lead agentStop with no crew working is needs_you stop", "agentStop", copLead, idle, stop},
		{"a crew boat's own Stop is needs_you with the others idle", "Stop", claudeStop, crew, stop},
		{"a crew boat's own Stop is needs_you whatever the other crew do", "Stop", codexStop, crewWithOthers, stop},
		{"a crew boat's own agentStop is needs_you whatever the other crew do", "agentStop", copLead, crewWithOthers, stop},
		{"a failed crew read gives needs_you stop", "Stop", claudeStop, failed, stop},
		{"claude's background_tasks rule still applies with no crew", "Stop", claudeStopTask, idle, hookWrite{}},
		{"claude's background_tasks rule still applies to a crew boat", "Stop", claudeStopTask, crew, hookWrite{}},
		{"a copilot subagent's agentStop writes nothing", "agentStop", copSub, idle, hookWrite{}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var p hookPayload
			if err := json.Unmarshal([]byte(c.payload), &p); err != nil {
				t.Fatal(err)
			}
			if got := leadDecision(c.event, p, c.f); got != c.want {
				t.Errorf("leadDecision(%s, %+v) = %+v, want %+v", c.event, c.f, got, c.want)
			}
		})
	}
}

// Whether a crew pane holds its lead's Stop: live with its own state working,
// or live with no hook yet inside the 10 s launch window (ADR 0007).
func TestHookCrewPaneWorking(t *testing.T) {
	now := time.Date(2026, 10, 24, 18, 30, 0, 0, time.UTC)
	ago := func(d time.Duration) string { return now.Add(-d).Format(time.RFC3339) }
	cases := []struct {
		name string
		f    crewPaneFact
		want bool
	}{
		{"a live pane working", crewPaneFact{State: "working", HasActivity: true, Launched: ago(time.Minute)}, true},
		{"a live pane that needs you", crewPaneFact{State: "needs_you", HasActivity: true, Launched: ago(time.Minute)}, false},
		{"a dead pane keeps its working state and doesn't count", crewPaneFact{Dead: true, State: "working", HasActivity: true, Launched: ago(time.Minute)}, false},
		{"a dead pane inside its launch window doesn't count", crewPaneFact{Dead: true, Launched: ago(2 * time.Second)}, false},
		{"no activity yet inside the launch window", crewPaneFact{Launched: ago(3 * time.Second)}, true},
		{"no activity past the launch window", crewPaneFact{Launched: ago(11 * time.Second)}, false},
		{"a hook already stamped, no state, inside the window", crewPaneFact{HasActivity: true, Launched: ago(3 * time.Second)}, false},
		{"an unreadable launch time doesn't count", crewPaneFact{Launched: "soon"}, false},
		{"no launch time doesn't count", crewPaneFact{}, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := crewPaneWorking(c.f, now); got != c.want {
				t.Errorf("crewPaneWorking(%+v) = %v, want %v", c.f, got, c.want)
			}
		})
	}
}
