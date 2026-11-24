package cli

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/jbuffin/admiral/internal/sessionhost"
)

const (
	agT0  = "2026-10-24T18:00:00Z" // an entry's start
	agT1  = "2026-10-24T18:10:00Z" // an earlier end
	agNow = "2026-10-24T18:30:00Z"
)

// ent writes one stored entry as JSON, leaving out what is empty.
func ent(id, typ, kind, start, end string) string {
	s := fmt.Sprintf(`{"id":%q,"type":%q,"start":%q`, id, typ, start)
	if kind != "" {
		s += fmt.Sprintf(`,"kind":%q`, kind)
	}
	if end != "" {
		s += fmt.Sprintf(`,"end":%q`, end)
	}
	return s + "}"
}

func arr(entries ...string) string { return "[" + strings.Join(entries, ",") + "]" }

// asObjects reads entries through JSON so a test compares them as plain maps.
func asObjects(t *testing.T, v any) []map[string]string {
	t.Helper()
	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	out := []map[string]string{}
	if string(raw) == "null" {
		return out
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatal(err)
	}
	return out
}

func entries(t *testing.T, listJSON string) []sessionhost.Subagent {
	t.Helper()
	var l []sessionhost.Subagent
	if err := json.Unmarshal([]byte(listJSON), &l); err != nil {
		t.Fatal(err)
	}
	return l
}

func runList(t *testing.T, event, payload, harness string, cur []sessionhost.Subagent) []map[string]string {
	t.Helper()
	var p hookPayload
	if err := json.Unmarshal([]byte(payload), &p); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 10, 24, 18, 30, 0, 0, time.UTC)
	return asObjects(t, subagentList(event, p, harness, cur, now))
}

func TestHookAgentsList(t *testing.T) {
	const (
		cID = "a0f790cb4a518e0f9"
		pID = "788718ef-39f6-45b1-ade6-7cb742bc6958"
	)
	running := ent(cID, "Explore", "subagent", agT0, "")
	finished := ent("old1", "Plan", "subagent", agT0, agT1)
	stopJSON := func(tasks ...string) string { return `{"background_tasks":[` + strings.Join(tasks, ",") + `]}` }
	task := func(id, typ, status string) string {
		return fmt.Sprintf(`{"id":%q,"type":%q,"status":%q,"agent_type":"Plan"}`, id, typ, status)
	}
	cases := []struct {
		name, harness, event, payload string
		cur, want                     string
	}{
		// A stop marks the entry finished and keeps it.
		{"claude SubagentStop marks the listed entry finished", "claude", "SubagentStop",
			`{"agent_id":"` + cID + `","agent_type":"Explore","background_tasks":[]}`,
			arr(running, ent("b1", "Plan", "subagent", agT0, "")), arr(ent(cID, "Explore", "subagent", agT0, agNow), ent("b1", "Plan", "subagent", agT0, ""))},
		{"claude SubagentStop of an id the list never saw leaves it", "claude", "SubagentStop",
			`{"agent_id":"af0aff5a46b69da59","agent_type":"","background_tasks":[]}`, arr(running), arr(running)},
		{"copilot subagentStop marks the listed entry finished", "copilot", "subagentStop",
			`{"agentId":"` + pID + `","agentType":"general-purpose","stopReason":"end_turn"}`,
			arr(ent(pID, "general-purpose", "subagent", agT0, "")), arr(ent(pID, "general-purpose", "subagent", agT0, agNow))},
		{"a legacy entry with no kind is marked finished as it is", "claude", "SubagentStop",
			`{"agent_id":"` + cID + `","agent_type":"Explore"}`, arr(ent(cID, "Explore", "", agT0, "")), arr(ent(cID, "Explore", "", agT0, agNow))},
		// Starts carry their kind.
		{"claude SubagentStart adds a running subagent", "claude", "SubagentStart",
			`{"agent_id":"` + cID + `","agent_type":"Explore"}`, arr(finished), arr(finished, ent(cID, "Explore", "subagent", agNow, ""))},
		{"copilot subagentStart adds a running subagent", "copilot", "subagentStart",
			`{"agentId":"` + pID + `","agentType":"general-purpose"}`, "[]", arr(ent(pID, "general-purpose", "subagent", agNow, ""))},
		// Copilot's lead turn ends: everything running is finished.
		{"copilot lead agentStop finishes every running entry and keeps the end of a finished one", "copilot", "agentStop",
			`{"stopReason":"end_turn"}`, arr(running, finished), arr(ent(cID, "Explore", "subagent", agT0, agNow), finished)},
		{"copilot lead userPromptSubmitted finishes every running entry", "copilot", "userPromptSubmitted",
			`{"prompt":"go"}`, arr(running), arr(ent(cID, "Explore", "subagent", agT0, agNow))},
		{"copilot subagent agentStop changes nothing", "copilot", "agentStop",
			`{"stopReason":"end_turn","agentId":"` + pID + `","parentAgentId":"796c5372"}`, arr(running), arr(running)},
		// Claude's Stop reconciles the list with background_tasks.
		{"claude Stop finishes a running entry the payload no longer lists", "claude", "Stop",
			stopJSON(task("b1", "subagent", "running")), arr(running, ent("b1", "Plan", "subagent", agT0, "")), arr(ent(cID, "Explore", "subagent", agT0, agNow), ent("b1", "Plan", "subagent", agT0, ""))},
		{"claude Stop finishes an entry whose status is no longer running", "claude", "Stop",
			stopJSON(task(cID, "subagent", "completed")), arr(running), arr(ent(cID, "Explore", "subagent", agT0, agNow))},
		{"claude Stop treats any other status as not running", "claude", "Stop",
			stopJSON(task(cID, "subagent", "killed")), arr(running), arr(ent(cID, "Explore", "subagent", agT0, agNow))},
		{"claude Stop keeps a finished entry and its end", "claude", "Stop", stopJSON(), arr(finished), arr(finished)},
		{"claude Stop with an empty background_tasks finishes everything running", "claude", "Stop", stopJSON(), arr(running), arr(ent(cID, "Explore", "subagent", agT0, agNow))},
		{"claude Stop adds an unseen running subagent", "claude", "Stop",
			stopJSON(task("c2", "subagent", "running")), arr(finished), arr(finished, ent("c2", "Plan", "subagent", agNow, ""))},
		{"claude Stop adds an unseen running teammate as a teammate", "claude", "Stop",
			stopJSON(task("m1", "teammate", "running")), "[]", arr(ent("m1", "Plan", "teammate", agNow, ""))},
		{"claude Stop ignores an unseen subagent that is not running", "claude", "Stop",
			stopJSON(task("d3", "subagent", "completed"), task("d4", "teammate", "completed")), "[]", "[]"},
		// Background shells and Monitors.
		{"claude Stop lists a running shell, with no type", "claude", "Stop",
			stopJSON(`{"id":"bdevsrv1","type":"shell","status":"running"}`), "[]", arr(ent("bdevsrv1", "", "shell", agNow, ""))},
		{"claude Stop adds an unseen finished shell as finished, started and ended now", "claude", "Stop",
			stopJSON(`{"id":"bdone001","type":"shell","status":"completed"}`), "[]", arr(ent("bdone001", "", "shell", agNow, agNow))},
		{"claude Stop finishes a listed shell that completed", "claude", "Stop",
			stopJSON(`{"id":"bdevsrv1","type":"shell","status":"completed"}`), arr(ent("bdevsrv1", "", "shell", agT0, "")), arr(ent("bdevsrv1", "", "shell", agT0, agNow))},
		{"claude Stop keeps a listed Monitor a Monitor while it runs", "claude", "Stop",
			stopJSON(`{"id":"bwatch01","type":"shell","status":"running"}`), arr(ent("bwatch01", "", "monitor", agT0, "")), arr(ent("bwatch01", "", "monitor", agT0, ""))},
		{"claude Stop finishes a listed Monitor that is gone", "claude", "Stop",
			stopJSON(), arr(ent("bwatch01", "", "monitor", agT0, "")), arr(ent("bwatch01", "", "monitor", agT0, agNow))},
		{"claude Stop stores nothing but id, kind and times of a shell", "claude", "Stop",
			`{"background_tasks":[{"id":"bdevsrv1","type":"shell","status":"running","command":"sleep 99","description":"dev server","prompt":"secret plan","output":"ready"}]}`,
			"[]", arr(ent("bdevsrv1", "", "shell", agNow, ""))},
		{"claude Stop with an unsafe shell id lists nothing", "claude", "Stop",
			stopJSON(`{"id":"b d;x","type":"shell","status":"running"}`), "[]", "[]"},
		{"Stop with no harness and a present background_tasks finishes a running entry", "", "Stop",
			stopJSON(), arr(running), arr(ent(cID, "Explore", "subagent", agT0, agNow))},
		{"codex Stop leaves finished entries and running ones alone", "codex", "Stop", `{"turn_id":"01a15222-ed87"}`, arr(running, finished), arr(running, finished)},
		// A Monitor's PostToolUse.
		{"PostToolUse of Monitor adds a running monitor", "claude", "PostToolUse",
			`{"tool_name":"Monitor","tool_input":{"command":"watch.sh"},"tool_response":{"taskId":"bwatch01","timeoutMs":1800000,"persistent":false}}`, "[]", arr(ent("bwatch01", "", "monitor", agNow, ""))},
		{"PostToolUse of a persistent Monitor adds it too", "claude", "PostToolUse",
			`{"tool_name":"Monitor","tool_response":{"taskId":"bwatch02","persistent":true}}`, arr(running), arr(running, ent("bwatch02", "", "monitor", agNow, ""))},
		{"PostToolUse of a Monitor already listed adds nothing", "claude", "PostToolUse",
			`{"tool_name":"Monitor","tool_response":{"taskId":"bwatch01"}}`, arr(ent("bwatch01", "", "monitor", agT0, "")), arr(ent("bwatch01", "", "monitor", agT0, ""))},
		{"PostToolUse of a Monitor with an unsafe task id adds nothing", "claude", "PostToolUse",
			`{"tool_name":"Monitor","tool_response":{"taskId":"b w;x"}}`, "[]", "[]"},
		{"PostToolUse of another tool adds nothing", "claude", "PostToolUse",
			`{"tool_name":"Bash","tool_response":{"taskId":"bwatch01"}}`, "[]", "[]"},
		// A new lead session clears the list.
		{"copilot sessionStart new clears", "copilot", "sessionStart", `{"source":"new"}`, arr(running, finished), "[]"},
		{"copilot sessionStart resume keeps", "copilot", "sessionStart", `{"source":"resume"}`, arr(running, finished), arr(running, finished)},
		{"ShipSessionStart startup clears", "claude", "ShipSessionStart", `{"source":"startup"}`, arr(running, finished), "[]"},
		{"ShipSessionStart clear clears", "claude", "ShipSessionStart", `{"source":"clear"}`, arr(running, finished), "[]"},
		{"ShipSessionStart resume keeps", "claude", "ShipSessionStart", `{"source":"resume"}`, arr(running, finished), arr(running, finished)},
		{"ShipSessionStart compact keeps", "claude", "ShipSessionStart", `{"source":"compact"}`, arr(running, finished), arr(running, finished)},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := runList(t, c.event, c.payload, c.harness, entries(t, c.cur))
			var want []map[string]string
			if err := json.Unmarshal([]byte(c.want), &want); err != nil {
				t.Fatal(err)
			}
			if len(got) == 0 && len(want) == 0 {
				return
			}
			if !reflect.DeepEqual(got, want) {
				t.Errorf("subagentList(%s, %s) =\n %v\nwant\n %v", c.harness, c.event, got, want)
			}
		})
	}
}

func fullList(t *testing.T, running, finished int) []sessionhost.Subagent {
	t.Helper()
	var parts []string
	for i := 0; i < running; i++ {
		parts = append(parts, ent(fmt.Sprintf("r%d", i), "Plan", "subagent", agT0, ""))
	}
	for i := 0; i < finished; i++ {
		// f0 ended first.
		end := time.Date(2026, 10, 24, 18, 10+i, 0, 0, time.UTC).Format(time.RFC3339)
		parts = append(parts, ent(fmt.Sprintf("f%d", i), "Plan", "subagent", agT0, end))
	}
	return entries(t, arr(parts...))
}

func TestHookAgentsCapDropsTheOldestFinished(t *testing.T) {
	ids := func(l []map[string]string) map[string]bool {
		m := map[string]bool{}
		for _, a := range l {
			m[a["id"]] = true
		}
		return m
	}
	t.Run("a new subagent on a full list drops the earliest-ended finished entry", func(t *testing.T) {
		got := runList(t, "SubagentStart", `{"agent_id":"new1","agent_type":"Plan"}`, "claude", fullList(t, 62, 2))
		have := ids(got)
		if len(got) != sessionhost.MaxSubagents || !have["new1"] || have["f0"] || !have["f1"] || !have["r0"] || !have["r61"] {
			t.Errorf("list = %v; want 64 entries: new1 and f1 kept, f0 (ended first) dropped, all running kept", got)
		}
	})
	t.Run("a new shell from a Stop on a full list drops the earliest-ended finished entry", func(t *testing.T) {
		got := runList(t, "Stop", `{"background_tasks":[{"id":"bnew0001","type":"shell","status":"running"}]}`, "claude", fullList(t, 0, 64))
		have := ids(got)
		if len(got) != sessionhost.MaxSubagents || !have["bnew0001"] || have["f0"] || !have["f1"] {
			t.Errorf("list = %v; want 64 entries with bnew0001 added and f0 dropped", got)
		}
	})
	t.Run("a new Monitor on a full list drops the earliest-ended finished entry", func(t *testing.T) {
		got := runList(t, "PostToolUse", `{"tool_name":"Monitor","tool_response":{"taskId":"bwatch09"}}`, "claude", fullList(t, 63, 1))
		have := ids(got)
		if len(got) != sessionhost.MaxSubagents || !have["bwatch09"] || have["f0"] {
			t.Errorf("list = %v; want bwatch09 added and f0 dropped", got)
		}
	})
	t.Run("a full list of running entries refuses the new one", func(t *testing.T) {
		got := runList(t, "SubagentStart", `{"agent_id":"new1","agent_type":"Plan"}`, "claude", fullList(t, 64, 0))
		if len(got) != sessionhost.MaxSubagents || ids(got)["new1"] {
			t.Errorf("list has %d entries, new1 listed = %v; want 64 running ones and no new1", len(got), ids(got)["new1"])
		}
		// The list is finished-aware, so a running-only list must still be refused through a Monitor too.
		got = runList(t, "PostToolUse", `{"tool_name":"Monitor","tool_response":{"taskId":"bwatch09"}}`, "claude", fullList(t, 64, 0))
		if ids(got)["bwatch09"] {
			t.Errorf("bwatch09 was listed on a list of 64 running entries")
		}
	})
}

// ShipSessionStart must not rebind the transcript (SessionStart's job, which does not run on a ship); like every admiral hook it still stamps the activity.
func TestSubagentCallClearsOnANewLeadSession(t *testing.T) {
	unset := sessionhost.UnsetPaneCall("%1", sessionhost.SubagentsOption)
	now := time.Date(2026, 10, 24, 18, 30, 0, 0, time.UTC)
	for _, c := range []struct{ event, payload string }{
		{"ShipSessionStart", `{"source":"startup"}`},
		{"ShipSessionStart", `{"source":"clear"}`},
		{"sessionStart", `{"source":"new"}`},
	} {
		t.Run(c.event+" "+c.payload, func(t *testing.T) {
			var p hookPayload
			if err := json.Unmarshal([]byte(c.payload), &p); err != nil {
				t.Fatal(err)
			}
			if got := subagentCall("%1", c.event, p, now); !reflect.DeepEqual(got, unset) {
				t.Errorf("subagentCall = %v, want the call that unsets the list: %v", got, unset)
			}
		})
	}
}
