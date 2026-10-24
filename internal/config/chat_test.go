package config

import "testing"

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
