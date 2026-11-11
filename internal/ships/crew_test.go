package ships

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/jbuffin/admiral/internal/sessionhost"
)

func TestCrewBoatsCarryRoleAndConversationFlagButNotThePath(t *testing.T) {
	boats := crewBoats([]sessionhost.Crew{
		{Name: "a", Harness: "claude", Role: "reviewer", Transcript: "/x/projects/p/s.jsonl"},
		{Name: "b", Harness: "claude"},
		{Name: "c", Harness: "codex", Transcript: "/x/sessions/2026/11/11/rollout.jsonl"},
		{Name: "d", Harness: "codex"},
		{Name: "e", Harness: "copilot", Transcript: "/x/session-state/s/events.jsonl"},
		{Name: "f", Harness: "copilot"},
	})
	want := []struct {
		role string
		conv bool
	}{{"reviewer", true}, {"", false}, {"", true}, {"", false}, {"", true}, {"", false}}
	for i, w := range want {
		if boats[i].Role != w.role || boats[i].Conversation != w.conv {
			t.Errorf("boat %s = role %q conversation %v, want %q %v", boats[i].Name, boats[i].Role, boats[i].Conversation, w.role, w.conv)
		}
	}
	b, _ := json.Marshal(boats)
	if strings.Contains(string(b), "jsonl") || strings.Contains(string(b), `"role":""`) {
		t.Errorf("wire = %s, want no transcript path and no empty role", b)
	}
}
