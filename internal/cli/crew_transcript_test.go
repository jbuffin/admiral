package cli

import (
	"encoding/json"
	"reflect"
	"testing"
)

// The call that records a crew boat's transcript path (#175, ADR 0021): one `if`
// command that tmux runs only on a pane whose window has @admiral-crew-name, so a
// ship's pane and the chat never get the option. The path is untrusted, so it is
// recorded only when sessionhost.ValidTranscriptPath accepts it and it holds no
// character that would need quoting inside the nested command. Payloads go
// through encoding/json so the field tags are exercised.
func TestCrewTranscriptCall(t *testing.T) {
	const pane = "%7"
	call := func(path string) []string {
		return []string{"if", "-F", "-t", pane, "#{@admiral-crew-name}",
			"set -p -t " + pane + " @admiral-crew-transcript '" + path + "'"}
	}
	const withSpace = "/Users/me/My Projects/.claude/projects/x/abc.jsonl"
	jstr := func(s string) string { b, _ := json.Marshal(s); return string(b) }
	cases := []struct {
		name, event, payload string
		want                 []string
	}{
		// Claude: any event's transcript_path.
		{"Claude UserPromptSubmit", "UserPromptSubmit", `{"transcript_path":` + jstr(withSpace) + `}`, call(withSpace)},
		{"Claude PostToolUse", "PostToolUse", `{"tool_name":"Bash","transcript_path":"/a/b.jsonl"}`, call("/a/b.jsonl")},
		{"Claude Stop", "Stop", `{"background_tasks":[],"transcript_path":"/a/b.jsonl"}`, call("/a/b.jsonl")},
		// Codex always sends the key, possibly null.
		{"Codex with a path", "Stop", `{"turn_id":"t1","transcript_path":"/h/.codex/sessions/r.jsonl"}`, call("/h/.codex/sessions/r.jsonl")},
		{"Codex null", "Stop", `{"turn_id":"t1","transcript_path":null}`, nil},
		{"no path", "UserPromptSubmit", `{"prompt":"go"}`, nil},
		{"empty path", "UserPromptSubmit", `{"transcript_path":""}`, nil},
		// Copilot: transcriptPath, on a lead agentStop only.
		{"Copilot lead agentStop", "agentStop", `{"stopReason":"end_turn","transcriptPath":"/h/.copilot/s/events.jsonl"}`, call("/h/.copilot/s/events.jsonl")},
		{"Copilot subagent agentStop", "agentStop", `{"agentId":"788718ef","transcriptPath":"/h/.copilot/s/events.jsonl"}`, nil},
		{"Copilot subagentStart", "subagentStart", `{"agentId":"788718ef","transcriptPath":"/h/.copilot/s/events.jsonl"}`, nil},
		{"Copilot subagentStop", "subagentStop", `{"agentId":"788718ef","transcriptPath":"/h/.copilot/s/events.jsonl"}`, nil},
		{"Copilot lead userPromptSubmitted", "userPromptSubmitted", `{"prompt":"go","transcriptPath":"/h/.copilot/s/events.jsonl"}`, nil},
		{"Copilot lead agentStop with none", "agentStop", `{"stopReason":"end_turn"}`, nil},
		// ValidTranscriptPath's refusals.
		{"relative", "Stop", `{"transcript_path":"rel/x.jsonl"}`, nil},
		{"not .jsonl", "Stop", `{"transcript_path":"/a/b.txt"}`, nil},
		{"control character", "Stop", `{"transcript_path":"/a/b\u0001c.jsonl"}`, nil},
		{"newline", "Stop", `{"transcript_path":"/a/b\nc.jsonl"}`, nil},
		// The stricter crew check: each of these would need quoting in the nested command.
		{"single quote", "Stop", `{"transcript_path":` + jstr("/a/it's.jsonl") + `}`, nil},
		{"double quote", "Stop", `{"transcript_path":` + jstr(`/a/"x".jsonl`) + `}`, nil},
		{"backslash", "Stop", `{"transcript_path":` + jstr(`/a/b\c.jsonl`) + `}`, nil},
		{"backtick", "Stop", `{"transcript_path":` + jstr("/a/`x`.jsonl") + `}`, nil},
		{"dollar", "Stop", `{"transcript_path":` + jstr("/a/$HOME.jsonl") + `}`, nil},
		{"hash", "Stop", `{"transcript_path":` + jstr("/a/#{pane_id}.jsonl") + `}`, nil},
		{"semicolon", "Stop", `{"transcript_path":` + jstr("/a/b;kill-server.jsonl") + `}`, nil},
		{"open brace", "Stop", `{"transcript_path":` + jstr("/a/{b.jsonl") + `}`, nil},
		{"close brace", "Stop", `{"transcript_path":` + jstr("/a/b}.jsonl") + `}`, nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var p hookPayload
			if err := json.Unmarshal([]byte(c.payload), &p); err != nil {
				t.Fatal(err)
			}
			if got := crewTranscriptCall(pane, c.event, p); !reflect.DeepEqual(got, c.want) {
				t.Errorf("crewTranscriptCall(%s) = %q, want %q", c.event, got, c.want)
			}
		})
	}
}
