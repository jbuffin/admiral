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

func TestChatConfig(t *testing.T) {
	t.Parallel()
	home := newHome(t)
	writeConfig(t, home, `{"version":1,"favorite_skills":["review"],"fleets":{"work":{"name":"Work Fleet","claude":{}},"away":{"name":"Away","codex":{}}},"sources":{}}`)
	in := startAdmiral(t, home, "127.0.0.1:0")

	// No chat key: the defaults show and nothing is written; a fleet without Claude Code is off with a reason.
	chats := chatsOf(t, in)
	if got := chats["work"]; got.Harness != "claude" || got.Model != "sonnet" || got.Effort != "" || got.ContextThreshold != float64(150000) || !got.On {
		t.Errorf("default chat = %+v, want claude, sonnet, harness-default effort, 150000, on", got)
	}
	if got := chats["away"]; got.On || got.OffReason == "" {
		t.Errorf("a fleet with no Claude Code dock = %+v, want off with a reason", got)
	}
	if _, ok := configOnDisk(t, home)["chat"]; ok {
		t.Errorf("reading the defaults must not write a top-level `chat` to config.json")
	}

	// The settings are saved under the fleet; the rest of the file is unchanged.
	putFleetChat(t, in, "work", map[string]any{"harness": "claude", "model": "opus", "effort": "high", "context_threshold": 300000})
	want := map[string]any{
		"version":         float64(1),
		"favorite_skills": []any{"review"},
		"fleets": map[string]any{
			"work": map[string]any{"name": "Work Fleet", "claude": map[string]any{},
				"chat": map[string]any{"harness": "claude", "model": "opus", "effort": "high", "context_threshold": float64(300000)}},
			"away": map[string]any{"name": "Away", "codex": map[string]any{}},
		},
		"sources": map[string]any{},
	}
	if got := configOnDisk(t, home); !reflect.DeepEqual(got, want) {
		t.Errorf("config.json after save = %v, want %v", got, want)
	}
	if got := chatsOf(t, in)["work"]; got.Model != "opus" || got.Effort != "high" || got.ContextThreshold != float64(300000) {
		t.Errorf("GET /api/config chat after save = %+v, want opus, high, 300000", got)
	}
}

func TestChatConfigRefusals(t *testing.T) {
	t.Parallel()
	home := newHome(t)
	writeConfig(t, home, `{"version":1,"fleets":{"work":{"name":"Work","claude":{},"codex":{}}}}`)
	in := startAdmiral(t, home, "127.0.0.1:0")
	for _, chat := range []map[string]any{
		{"model": "gpt"},
		{"effort": "max"},
		{"context_threshold": 200000},
		{"harness": "codex"},
	} {
		if code, _ := send(t, http.MethodPut, in.LocalURL+"/api/fleets/work", map[string]any{"chat": chat}); code != http.StatusBadRequest {
			t.Errorf("PUT /api/fleets/work chat %v = %d, want 400", chat, code)
		}
	}
	if code, _ := send(t, http.MethodPut, in.LocalURL+"/api/chat", map[string]any{"fleet": "work", "model": "opus", "effort": ""}); code != http.StatusNotFound {
		t.Errorf("PUT /api/chat = %d, want 404: the top-level chat settings are gone", code)
	}
	if _, ok := configOnDisk(t, home)["chat"]; ok {
		t.Error("a refused PUT must not write `chat`")
	}
	var fleets map[string]map[string]json.RawMessage
	raw, _ := json.Marshal(configOnDisk(t, home)["fleets"])
	_ = json.Unmarshal(raw, &fleets)
	if _, ok := fleets["work"]["chat"]; ok {
		t.Error("a refused PUT wrote chat under the fleet")
	}
}
