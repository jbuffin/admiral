// Tests that Claude ships, and only they, run `admiral ship-note` at session start.
package launcher

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jbuffin/admiral/internal/standing"
)

type hookGroup struct {
	Matcher *string `json:"matcher"`
	Hooks   []struct {
		Type    string `json:"type"`
		Command string `json:"command"`
	} `json:"hooks"`
}

func hookEvents(t *testing.T, raw []byte) map[string][]hookGroup {
	t.Helper()
	var s struct {
		Hooks map[string][]hookGroup `json:"hooks"`
	}
	if err := json.Unmarshal(raw, &s); err != nil {
		t.Fatal(err)
	}
	return s.Hooks
}

func settingsJSON(t *testing.T, v any) []byte {
	t.Helper()
	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func wantShipNoteAtSessionStart(t *testing.T, where string, raw []byte) {
	t.Helper()
	groups := hookEvents(t, raw)["SessionStart"]
	if len(groups) != 1 || len(groups[0].Hooks) != 1 {
		t.Fatalf("%s SessionStart = %+v; want one group with one hook", where, groups)
	}
	g := groups[0]
	if g.Matcher != nil {
		t.Errorf("%s SessionStart matcher = %q; want none, so every source runs it, compaction too", where, *g.Matcher)
	}
	if h := g.Hooks[0]; h.Type != "command" || h.Command != "admiral ship-note" {
		t.Errorf("%s SessionStart hook = %+v; want command admiral ship-note", where, h)
	}
	// admiral hook SessionStart would rebind the ship's transcript to a source other than launch.
	if strings.Contains(string(raw), "admiral hook SessionStart") {
		t.Errorf("%s runs admiral hook SessionStart", where)
	}
}

func TestClaudeShipSettingsRunTheShipNoteAtSessionStart(t *testing.T) {
	wantShipNoteAtSessionStart(t, "claudeSettings", settingsJSON(t, claudeSettings()))
}

func TestEveryClaudeShipSettingsFileRunsTheShipNote(t *testing.T) {
	home := t.TempDir()
	if err := WriteBundle(home); err != nil {
		t.Fatal(err)
	}
	bundled, err := os.ReadFile(ClaudeSettingsPath(home))
	if err != nil {
		t.Fatal(err)
	}
	wantShipNoteAtSessionStart(t, "claude-settings.json", bundled)

	path, err := writeClaudeRuled(home, nil)
	if err != nil {
		t.Fatal(err)
	}
	ruled, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	wantShipNoteAtSessionStart(t, filepath.Base(path), ruled)
}

func TestRestrictedShipSettingsHaveNoShipNote(t *testing.T) {
	for _, level := range []standing.Restricted{standing.Investigate, standing.DraftFix} {
		raw := settingsJSON(t, restrictedSettings(level, nestCheckout, "/home/x/admiral", nestTemp, nil))
		if groups, ok := hookEvents(t, raw)["SessionStart"]; ok {
			t.Errorf("restricted %v SessionStart = %+v; a restricted ship can't reach Admiral, so it gets no note", level, groups)
		}
		if strings.Contains(string(raw), "ship-note") {
			t.Errorf("restricted %v settings run ship-note", level)
		}
	}
}

func TestChatCodexAndCopilotBundlesGetNoShipNote(t *testing.T) {
	chat := settingsJSON(t, claudeChatSettings())
	groups := hookEvents(t, chat)["SessionStart"]
	if len(groups) != 1 || len(groups[0].Hooks) != 1 || groups[0].Hooks[0].Command != "admiral hook SessionStart" {
		t.Errorf("chat SessionStart = %+v; want only admiral hook SessionStart", groups)
	}
	for name, bundle := range map[string]string{
		"chat":    string(chat),
		"codex":   strings.Join(codexFlags(), " "),
		"copilot": string(settingsJSON(t, copilotHooks())),
	} {
		if strings.Contains(bundle, "ship-note") {
			t.Errorf("the %s bundle runs ship-note", name)
		}
	}
}
