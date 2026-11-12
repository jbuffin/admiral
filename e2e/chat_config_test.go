package e2e

import (
	"encoding/json"
	"net/http"
	"os"
	"reflect"
	"testing"
)

// The disk is compared as parsed JSON, not bytes, because the writer re-indents
// the file.

type fleetChat struct {
	Harness          string `json:"harness"`
	Model            string `json:"model"`
	Effort           string `json:"effort"`
	ContextThreshold any    `json:"context_threshold"`
	On               bool   `json:"on"`
	OffReason        string `json:"off_reason"`
}

func chatsOf(t *testing.T, in *instance) map[string]fleetChat {
	t.Helper()
	_, raw := fetchConfig(t, in)
	var b struct {
		Chat   json.RawMessage `json:"chat"`
		Fleets []struct {
			Key  string    `json:"key"`
			Chat fleetChat `json:"chat"`
		} `json:"fleets"`
	}
	if err := json.Unmarshal([]byte(raw), &b); err != nil {
		t.Fatal(err)
	}
	if b.Chat != nil {
		t.Errorf("GET /api/config still has a top-level chat: %s", b.Chat)
	}
	out := map[string]fleetChat{}
	for _, f := range b.Fleets {
		out[f.Key] = f.Chat
	}
	return out
}

func configOnDisk(t *testing.T, home string) map[string]any {
	t.Helper()
	data, err := os.ReadFile(configPath(home))
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatalf("config.json is not JSON: %v\n%s", err, data)
	}
	return m
}

func putFleetChat(t *testing.T, in *instance, fleet string, chat map[string]any) {
	t.Helper()
	if code, resp := send(t, http.MethodPut, in.LocalURL+"/api/fleets/"+fleet, map[string]any{"chat": chat}); code != http.StatusOK {
		t.Fatalf("PUT /api/fleets/%s chat %v = %d, want 200: %s", fleet, chat, code, resp)
	}
}

func TestChatConfigSaveLeavesTheRestOfTheFileAndThereIsNoChatRoute(t *testing.T) {
	t.Parallel()
	home := newHome(t)
	writeConfig(t, home, `{"version":1,"favorite_skills":["review"],"fleets":{"work":{"name":"Work Fleet","claude":{}},"away":{"name":"Away","codex":{}}},"sources":{}}`)
	in := startAdmiral(t, home, "127.0.0.1:0")

	putFleetChat(t, in, "work", map[string]any{"model": "opus"})
	want := map[string]any{
		"version":         float64(1),
		"favorite_skills": []any{"review"},
		"fleets": map[string]any{
			"work": map[string]any{"name": "Work Fleet", "claude": map[string]any{}, "chat": map[string]any{"model": "opus"}},
			"away": map[string]any{"name": "Away", "codex": map[string]any{}},
		},
		"sources": map[string]any{},
	}
	if got := configOnDisk(t, home); !reflect.DeepEqual(got, want) {
		t.Errorf("config.json after save = %v, want %v", got, want)
	}
	if code, _ := send(t, http.MethodPut, in.LocalURL+"/api/chat", map[string]any{"model": "opus"}); code != http.StatusNotFound {
		t.Errorf("PUT /api/chat = %d, want 404: the top-level chat settings are gone", code)
	}
}
