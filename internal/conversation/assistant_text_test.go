package conversation_test

import (
	"encoding/json"
	"fmt"
	"testing"

	"github.com/jbuffin/admiral/internal/conversation"
)

// A table, an indented nested list and a fence whose body has pipes, blank lines and leading spaces.
const tableAndFence = "Results:\n\n| name | n |\n|:-----|--:|\n| a \\| b | 1 |\n| `x|y` | 22 |\n\n- top\n    - nested\n\n```go\nfunc main() {\n\n\tfmt.Println(\"a | b\")\n    // four spaces\n}\n```\n\nDone."

func jsonString(t *testing.T, s string) string {
	t.Helper()
	b, err := json.Marshal(s)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func assistantTexts(items []conversation.Item) []string {
	var out []string
	for _, it := range items {
		if it.Kind == conversation.KindAssistant {
			out = append(out, it.Text)
		}
	}
	return out
}

func wantTableAndFence(t *testing.T, items []conversation.Item) {
	t.Helper()
	got := assistantTexts(items)
	if len(got) != 1 || got[0] != tableAndFence {
		t.Errorf("assistant text = %q, want it unchanged: %q", got, tableAndFence)
	}
}

func TestClaudeAssistantTextComesThroughUnchanged(t *testing.T) {
	line := `{"type":"assistant","uuid":"t1","message":{"content":[{"type":"text","text":` + jsonString(t, tableAndFence) + `}]}}` + "\n"
	wantTableAndFence(t, read(t, line))
}

func TestCodexRolloutAssistantTextComesThroughUnchanged(t *testing.T) {
	line := `{"timestamp":"2026-11-11T20:21:00.179Z","ordinal":15,"type":"event_msg","payload":{"type":"item_completed","thread_id":"th","turn_id":"tu",` +
		`"item":{"type":"AgentMessage","id":"msg_1","content":[{"type":"Text","text":` + jsonString(t, tableAndFence) + `}],"phase":"final_answer"}}}` + "\n"
	wantTableAndFence(t, readCodex(t, line))
}

func TestCodexLiveAssistantTextComesThroughUnchanged(t *testing.T) {
	live := conversation.NewLive(conversation.Options{}, liveThread)
	feed(live, fmt.Sprintf(`{"method":"item/completed","params":{"threadId":%q,"turnId":"t","item":{"type":"agentMessage","id":"msg_t","text":%s,"phase":"final_answer"}}}`,
		liveThread, jsonString(t, tableAndFence)))
	wantTableAndFence(t, live.Items())
}

func TestCopilotAssistantTextComesThroughUnchanged(t *testing.T) {
	wantTableAndFence(t, readCop(t, copilotTurn("show me", tableAndFence)))
}
