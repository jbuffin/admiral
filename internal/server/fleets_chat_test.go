package server_test

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

const chatFleetsFixture = `{
	"version": 1,
	"fleets": {
		"work": {"name": "Work", "search_roots": ["/srv/work"], "claude": {}, "codex": {}},
		"home": {"name": "Home", "search_roots": ["/srv/home"], "claude": {}}
	}
}`

func (f *fleetFixture) chatOf(key string) (out struct {
	Harness          string `json:"harness"`
	Model            string `json:"model"`
	Effort           string `json:"effort"`
	ContextThreshold any    `json:"context_threshold"`
	On               bool   `json:"on"`
}) {
	f.t.Helper()
	_, body := f.do(http.MethodGet, "/api/config", nil)
	var v struct {
		Fleets []struct {
			Key  string `json:"key"`
			Chat *struct {
				Harness          string `json:"harness"`
				Model            string `json:"model"`
				Effort           string `json:"effort"`
				ContextThreshold any    `json:"context_threshold"`
				On               bool   `json:"on"`
			} `json:"chat"`
		} `json:"fleets"`
	}
	if err := json.Unmarshal([]byte(body), &v); err != nil {
		f.t.Fatal(err)
	}
	for _, fl := range v.Fleets {
		if fl.Key == key && fl.Chat != nil {
			return *fl.Chat
		}
	}
	f.t.Fatalf("GET /api/config has no chat for fleet %s: %s", key, body)
	return out
}

func TestFleetSave_ChatSettingsRoundTripAndTouchOnlyThatFleet(t *testing.T) {
	f := newFleetFixture(t, chatFleetsFixture)
	code, body := f.do(http.MethodPut, "/api/fleets/work", map[string]any{
		"chat": map[string]any{"harness": "claude", "model": "opus", "effort": "high", "context_threshold": 300000},
	})
	if code != http.StatusOK {
		t.Fatalf("PUT chat = %d %s, want 200", code, body)
	}
	g := f.chatOf("work") // read at once: the save reloads before it answers
	if g.Harness != "claude" || g.Model != "opus" || g.Effort != "high" || g.ContextThreshold != float64(300000) || !g.On {
		t.Errorf("work chat = %+v, want claude, opus, high, 300000, on", g)
	}
	if h := f.chatOf("home"); h.Model != "sonnet" || h.ContextThreshold != float64(150000) {
		t.Errorf("home chat = %+v, want the defaults: a save names one fleet", h)
	}
	var disk struct {
		Fleets map[string]map[string]json.RawMessage `json:"fleets"`
	}
	if err := json.Unmarshal([]byte(f.file()), &disk); err != nil {
		t.Fatal(err)
	}
	if _, ok := disk.Fleets["work"]["chat"]; !ok {
		t.Errorf("config.json has no chat under fleet work:\n%s", f.file())
	}
	if _, ok := disk.Fleets["home"]["chat"]; ok {
		t.Errorf("config.json gave fleet home a chat key it never asked for:\n%s", f.file())
	}

	// A save of other fields leaves the chat settings as stored.
	if code, body := f.do(http.MethodPut, "/api/fleets/work", map[string]any{"owners": []string{"acme"}}); code != http.StatusOK {
		t.Fatalf("PUT owners = %d %s, want 200", code, body)
	}
	if g := f.chatOf("work"); g.Model != "opus" {
		t.Errorf("work chat after an owners save = %+v, want opus kept", g)
	}

	// An empty effort is the harness default, and "never" is a threshold.
	if code, body := f.do(http.MethodPut, "/api/fleets/work", map[string]any{
		"chat": map[string]any{"harness": "claude", "model": "opus", "effort": "", "context_threshold": "never"},
	}); code != http.StatusOK {
		t.Fatalf("PUT chat with no effort = %d %s, want 200", code, body)
	}
	if g := f.chatOf("work"); g.Effort != "" || g.ContextThreshold != "never" {
		t.Errorf("work chat = %+v, want the harness-default effort and never", g)
	}
	if strings.Contains(f.file(), `"effort"`) {
		t.Errorf("an empty effort must leave the key out of config.json:\n%s", f.file())
	}
}

func TestFleetSave_ChatValuesParseWouldReplaceAreRefused(t *testing.T) {
	for name, chat := range map[string]map[string]any{
		"an unknown model":     {"model": "gpt"},
		"an unknown effort":    {"effort": "max"},
		"a threshold off list": {"context_threshold": 200000},
		"a word as threshold":  {"context_threshold": "sometimes"},
		"an unproven harness":  {"harness": "codex"},
		"an unknown key":       {"modle": "opus"},
	} {
		f := newFleetFixture(t, chatFleetsFixture)
		code, body := f.do(http.MethodPut, "/api/fleets/work", map[string]any{"chat": chat})
		if code != http.StatusBadRequest || !strings.Contains(body, "chat") {
			t.Errorf("PUT with %s = %d %s, want 400 naming chat", name, code, body)
		}
		if f.file() != chatFleetsFixture {
			t.Errorf("PUT with %s changed config.json", name)
		}
	}
}

func (f *fleetFixture) notifyOf(key string) bool {
	f.t.Helper()
	_, body := f.do(http.MethodGet, "/api/config", nil)
	var v struct {
		Fleets []struct {
			Key    string `json:"key"`
			Notify *bool  `json:"notify"`
		} `json:"fleets"`
	}
	if err := json.Unmarshal([]byte(body), &v); err != nil {
		f.t.Fatal(err)
	}
	for _, fl := range v.Fleets {
		if fl.Key == key && fl.Notify != nil {
			return *fl.Notify
		}
	}
	f.t.Fatalf("GET /api/config has no notify for fleet %s: %s", key, body)
	return false
}

func TestFleetSave_NotifyRoundTripsAndTouchesOnlyThatFleet(t *testing.T) {
	f := newFleetFixture(t, chatFleetsFixture)
	if !f.notifyOf("work") || !f.notifyOf("home") {
		t.Fatal("notify is off for a fleet that never set it, want on by default")
	}
	if code, body := f.do(http.MethodPut, "/api/fleets/work", map[string]any{"notify": false}); code != http.StatusOK {
		t.Fatalf("PUT notify false = %d %s, want 200", code, body)
	}
	if f.notifyOf("work") || !f.notifyOf("home") {
		t.Errorf("after turning work off: work=%v home=%v, want work off and home on", f.notifyOf("work"), f.notifyOf("home"))
	}
	var disk struct {
		Fleets map[string]map[string]json.RawMessage `json:"fleets"`
	}
	if err := json.Unmarshal([]byte(f.file()), &disk); err != nil {
		t.Fatal(err)
	}
	if string(disk.Fleets["work"]["notify"]) != "false" {
		t.Errorf("config.json has notify %q under work, want false:\n%s", disk.Fleets["work"]["notify"], f.file())
	}
	if _, ok := disk.Fleets["home"]["notify"]; ok {
		t.Errorf("config.json gave fleet home a notify key it never asked for:\n%s", f.file())
	}

	// A save of other fields leaves Notify as stored.
	if code, body := f.do(http.MethodPut, "/api/fleets/work", map[string]any{"owners": []string{"acme"}}); code != http.StatusOK {
		t.Fatalf("PUT owners = %d %s", code, body)
	}
	if f.notifyOf("work") {
		t.Error("saving owners turned work's Notify back on")
	}
	if code, body := f.do(http.MethodPut, "/api/fleets/work", map[string]any{"notify": true}); code != http.StatusOK || !f.notifyOf("work") {
		t.Errorf("PUT notify true = %d %s, want 200 and on", code, body)
	}
}

func TestFleetSave_NotifyRefusesANonBoolean(t *testing.T) {
	f := newFleetFixture(t, chatFleetsFixture)
	for _, bad := range []any{"no", 0, []bool{false}} {
		code, body := f.do(http.MethodPut, "/api/fleets/work", map[string]any{"notify": bad})
		if code != http.StatusBadRequest {
			t.Errorf("PUT notify %v = %d %s, want 400", bad, code, body)
		}
	}
	if !f.notifyOf("work") {
		t.Error("a refused save changed Notify")
	}
}
