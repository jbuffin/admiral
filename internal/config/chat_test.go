package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestParseChat(t *testing.T) {
	const fleets = `"version":1,"fleets":{"w":{"name":"W","claude":{}}}`

	cfg, err := Parse([]byte(`{` + fleets + `,"chat":{"model":"gpt","effort":"max"}}`))
	if err != nil {
		t.Fatalf("a bad model or effort must not fail the load: %v", err)
	}
	if want := (Chat{Fleet: "w", Model: "sonnet"}); cfg.Chat != want {
		t.Errorf("chat = %+v, want the defaults %+v", cfg.Chat, want)
	}

	if _, err := Parse([]byte(`{` + fleets + `,"chat":{"modle":"opus"}}`)); err == nil {
		t.Error("an unknown chat key must be refused")
	}
}

func TestSetChatRefusesWhatParseWouldReplace(t *testing.T) {
	const orig = `{"version":1,"fleets":{"w":{"name":"W","claude":{}}},"chat":{"fleet":"w","model":"opus"}}`
	for name, c := range map[string]Chat{
		"unknown model":  {Fleet: "w", Model: "gpt"},
		"unknown effort": {Fleet: "w", Model: "opus", Effort: "max"},
		"unknown fleet":  {Fleet: "gone", Model: "opus"},
	} {
		dir := t.TempDir()
		path := filepath.Join(dir, FileName)
		if err := os.WriteFile(path, []byte(orig), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := SetChat(dir, c); err == nil {
			t.Errorf("%s: SetChat(%+v) returned nil; Parse would silently replace it", name, c)
		}
		if got, _ := os.ReadFile(path); string(got) != orig {
			t.Errorf("%s: a refused SetChat changed config.json to %s", name, got)
		}
	}
}
