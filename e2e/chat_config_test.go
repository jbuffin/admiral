package e2e

import (
	"encoding/json"
	"net/http"
	"os"
	"reflect"
	"testing"
)

// The `chat` config key (#119, u1): the real binary with a temporary
// ADMIRAL_HOME, read through GET /api/config and written through
// PUT /api/chat, with config.json read back from disk. The disk is compared as
// parsed JSON, not bytes, because the writer re-indents the file.

type chatKey struct {
	Fleet  string `json:"fleet"`
	Model  string `json:"model"`
	Effort string `json:"effort"`
}

func chatOf(t *testing.T, in *instance) chatKey {
	t.Helper()
	_, raw := fetchConfig(t, in)
	var b struct {
		Chat *chatKey `json:"chat"`
	}
	if err := json.Unmarshal([]byte(raw), &b); err != nil || b.Chat == nil {
		t.Fatalf("GET /api/config has no top-level `chat` object (err %v): %s", err, raw)
	}
	return *b.Chat
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

func putChat(t *testing.T, in *instance, body map[string]any) {
	t.Helper()
	if code, resp := send(t, http.MethodPut, in.LocalURL+"/api/chat", body); code != http.StatusOK {
		t.Fatalf("PUT /api/chat %v = %d, want 200: %s", body, code, resp)
	}
}

func TestChatConfig(t *testing.T) {
	t.Run("defaults, save and reset effort", func(t *testing.T) {
		home := newHome(t)
		writeConfig(t, home, `{"version":1,"favorite_skills":["review"],"fleets":{"work":{"name":"Work Fleet","claude":{}}}}`)
		in := startAdmiral(t, home, "127.0.0.1:0")

		// 1. No `chat` key: the first fleet with a Claude dock, sonnet, no effort. Nothing is written.
		if got, want := chatOf(t, in), (chatKey{Fleet: "work", Model: "sonnet"}); got != want {
			t.Errorf("default chat = %+v, want %+v", got, want)
		}
		if _, ok := configOnDisk(t, home)["chat"]; ok {
			t.Errorf("reading the defaults must not write `chat` to config.json")
		}

		// 2. Model and effort are saved; the rest of the file is unchanged.
		putChat(t, in, map[string]any{"fleet": "work", "model": "opus", "effort": "high"})
		want := map[string]any{
			"version":         float64(1),
			"favorite_skills": []any{"review"},
			"fleets":          map[string]any{"work": map[string]any{"name": "Work Fleet", "claude": map[string]any{}}},
			// The first start seeds the Orders source (seedSources); it is not the chat key's doing.
			"sources": map[string]any{"orders": map[string]any{"plugin": "freeform", "name": "Orders"}},
			"chat":    map[string]any{"fleet": "work", "model": "opus", "effort": "high"},
		}
		if got := configOnDisk(t, home); !reflect.DeepEqual(got, want) {
			t.Errorf("config.json after save = %v, want %v", got, want)
		}
		if got, w := chatOf(t, in), (chatKey{Fleet: "work", Model: "opus", Effort: "high"}); got != w {
			t.Errorf("GET /api/config chat after save = %+v, want %+v", got, w)
		}

		// 3. Effort back to Harness default (empty): the field is gone, fleet and model stay.
		putChat(t, in, map[string]any{"fleet": "work", "model": "opus", "effort": ""})
		want["chat"] = map[string]any{"fleet": "work", "model": "opus"}
		if got := configOnDisk(t, home); !reflect.DeepEqual(got, want) {
			t.Errorf("config.json after resetting effort = %v, want %v (no `effort` field)", got, want)
		}
		if got, w := chatOf(t, in), (chatKey{Fleet: "work", Model: "opus"}); got != w {
			t.Errorf("GET /api/config chat after reset = %+v, want %+v", got, w)
		}
	})

	t.Run("fleet defaults", func(t *testing.T) {
		// 4. No Claude dock anywhere: the first fleet.
		home := newHome(t)
		writeConfig(t, home, `{"version":1,"fleets":{"alpha":{"name":"Alpha","codex":{}},"beta":{"name":"Beta","copilot":{}}}}`)
		in := startAdmiral(t, home, "127.0.0.1:0")
		if got := chatOf(t, in).Fleet; got != "alpha" {
			t.Errorf("default fleet with no Claude dock = %q, want the first fleet, alpha", got)
		}

		// The first fleet *with* a Claude dock wins over an earlier one without.
		home = newHome(t)
		writeConfig(t, home, `{"version":1,"fleets":{"alpha":{"name":"Alpha","codex":{}},"beta":{"name":"Beta","claude":{}}}}`)
		in = startAdmiral(t, home, "127.0.0.1:0")
		if got := chatOf(t, in).Fleet; got != "beta" {
			t.Errorf("default fleet = %q, want beta, the first with a Claude dock", got)
		}

		// A chat.fleet naming a missing fleet never fails the load: the default shows instead.
		home = newHome(t)
		writeConfig(t, home, `{"version":1,"fleets":{"beta":{"name":"Beta","claude":{}}},"chat":{"fleet":"gone","model":"haiku"}}`)
		in = startAdmiral(t, home, "127.0.0.1:0")
		b, raw := fetchConfig(t, in)
		if b.Error != nil {
			t.Fatalf("a missing chat.fleet must not be a config error: %s", raw)
		}
		if got, w := chatOf(t, in), (chatKey{Fleet: "beta", Model: "haiku"}); got != w {
			t.Errorf("chat with a missing fleet = %+v, want %+v", got, w)
		}
	})
}
