package config_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jbuffin/admiral/internal/config"
)

// chatSettings is a fleet's `chat` object in GET /api/config, read as JSON so the test sees the wire shape.
type chatSettings struct {
	Harness          string   `json:"harness"`
	Model            string   `json:"model"`
	Effort           string   `json:"effort"`
	ContextThreshold any      `json:"context_threshold"`
	On               bool     `json:"on"`
	OffReason        string   `json:"off_reason"`
	Harnesses        []string `json:"harnesses"`
}

func chatViews(t *testing.T, m *config.Manager) map[string]chatSettings {
	t.Helper()
	raw, err := json.Marshal(m.View())
	if err != nil {
		t.Fatal(err)
	}
	var v struct {
		Chat   json.RawMessage `json:"chat"`
		Fleets []struct {
			Key  string       `json:"key"`
			Chat chatSettings `json:"chat"`
		} `json:"fleets"`
	}
	if err := json.Unmarshal(raw, &v); err != nil {
		t.Fatal(err)
	}
	if v.Chat != nil {
		t.Errorf("the view still has a top-level chat: %s", v.Chat)
	}
	out := map[string]chatSettings{}
	for _, f := range v.Fleets {
		out[f.Key] = f.Chat
	}
	return out
}

func TestFleetChatDefaults(t *testing.T) {
	m, _ := openView(t, withFleets(`"w":{"name":"W","claude":{}}`))
	got := chatViews(t, m)["w"]
	if got.Harness != "claude" || got.Model != "sonnet" || got.Effort != "" || got.ContextThreshold != float64(150000) || !got.On || got.OffReason != "" {
		t.Errorf("a fleet with no chat key = %+v, want claude, sonnet, harness-default effort, 150000, on", got)
	}
	if len(got.Harnesses) != 1 || got.Harnesses[0] != "claude" {
		t.Errorf("harnesses offered = %v, want only claude: its fence is the one proven", got.Harnesses)
	}
}

func TestFleetChatSettingsParse(t *testing.T) {
	m, _ := openView(t, withFleets(`
		"w":{"name":"W","claude":{},"chat":{"harness":"claude","model":"opus","effort":"high","context_threshold":300000}},
		"v":{"name":"V","claude":{},"chat":{"context_threshold":"never"}}`))
	views := chatViews(t, m)
	if g := views["w"]; g.Harness != "claude" || g.Model != "opus" || g.Effort != "high" || g.ContextThreshold != float64(300000) {
		t.Errorf("w chat = %+v, want claude, opus, high, 300000", g)
	}
	if g := views["v"]; g.ContextThreshold != "never" || g.Model != "sonnet" {
		t.Errorf("v chat = %+v, want threshold never and the default model", g)
	}
}

func TestFleetChatBadValuesFallBackToDefaultsAndUnknownKeysAreRefused(t *testing.T) {
	m, _ := openView(t, withFleets(`"w":{"name":"W","claude":{},"chat":{"model":"gpt","effort":"max","context_threshold":123}}`))
	if g := chatViews(t, m)["w"]; g.Model != "sonnet" || g.Effort != "" || g.ContextThreshold != float64(150000) {
		t.Errorf("bad values must not fail the load; chat = %+v, want the defaults", g)
	}
	if _, err := config.Parse([]byte(withFleets(`"w":{"name":"W","claude":{},"chat":{"modle":"opus"}}`))); err == nil || !strings.Contains(err.Error(), "fleets.w.chat.modle") {
		t.Errorf("an unknown chat key: err = %v, want one naming fleets.w.chat.modle", err)
	}
}

func TestFleetChatIsOffWithAReasonThatSaysWhy(t *testing.T) {
	m, _ := openView(t, withFleets(`
		"noclaude":{"name":"No Claude","codex":{}},
		"unproven":{"name":"Unproven","claude":{},"codex":{},"chat":{"harness":"codex"}},
		"fine":{"name":"Fine","claude":{}}`))
	views := chatViews(t, m)
	no, unproven, fine := views["noclaude"], views["unproven"], views["fine"]
	if no.On || !strings.Contains(no.OffReason, "Claude Code") {
		t.Errorf("a fleet with no Claude Code dock: %+v, want off with a reason naming Claude Code", no)
	}
	if unproven.On || unproven.OffReason == "" {
		t.Errorf("a fleet whose chosen harness is not proven: %+v, want off with a reason", unproven)
	}
	if unproven.OffReason == no.OffReason {
		t.Errorf("both off reasons read %q; a person must be able to tell a missing dock from an unproven harness", no.OffReason)
	}
	if !fine.On || fine.OffReason != "" {
		t.Errorf("a fleet with a Claude Code dock: %+v, want on with no reason", fine)
	}
}

const oldChatConfig = `{
  "version": 1,
  "x_future": {"keep": [1, 2]},
  "fleets": {
    "alpha": {"name": "Alpha", "codex": {}},
    "beta": {"name": "Beta", "claude": {}},
    "gamma": {"name": "Gamma", "claude": {}}
  },
  "chat": {"fleet": "gamma", "model": "opus", "effort": "high"}
}
`

func fleetChatOnDisk(t *testing.T, dir string) (top map[string]json.RawMessage, fleets map[string]map[string]json.RawMessage) {
	t.Helper()
	if err := json.Unmarshal([]byte(readText(t, filepath.Join(dir, "config.json"))), &top); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(top["fleets"], &fleets); err != nil {
		t.Fatal(err)
	}
	return top, fleets
}

func TestMigrateMovesTheOldChatKeyOntoItsFleet(t *testing.T) {
	dir := migrateDir(t, oldChatConfig)
	if _, err := config.ReadConfig(dir); err == nil {
		t.Fatal("ReadConfig of a config with a top-level chat key succeeded; commands must refuse until the server migrates")
	}

	m, err := config.Migrate(dir, "1.2.3")
	if err != nil || m == nil {
		t.Fatalf("Migrate = %v, %v; want a migration for a config whose only old shape is chat", m, err)
	}
	if got := readText(t, filepath.Join(dir, "config.json.pre-1.2.3")); got != oldChatConfig {
		t.Errorf("the backup is not the old file byte for byte:\n%s", got)
	}
	top, _ := fleetChatOnDisk(t, dir)
	if _, ok := top["chat"]; ok {
		t.Error("the top-level chat key is still in config.json")
	}
	if _, ok := top["x_future"]; !ok {
		t.Error("the migration dropped an unknown top-level key")
	}

	cfg, err := config.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(cfg.Close)
	views := chatViews(t, cfg)
	if g := views["gamma"]; g.Model != "opus" || g.Effort != "high" || g.Harness != "claude" || !g.On {
		t.Errorf("gamma (the chat.fleet) = %+v, want opus, high, on", g)
	}
	for _, key := range []string{"alpha", "beta"} {
		if g := views[key]; g.Model != "sonnet" || g.Effort != "" || g.ContextThreshold != float64(150000) {
			t.Errorf("%s = %+v, want the defaults", key, g)
		}
	}

	title, body := m.Notice()
	if !strings.Contains(title+body, "chat") {
		t.Errorf("the notice %q / %q does not name the chat", title, body)
	}
	if again, err := config.Migrate(dir, "1.2.3"); again != nil || err != nil {
		t.Errorf("a second Migrate = %v, %v; want nothing to do", again, err)
	}
}

func TestMigrateSendsAChatForAMissingFleetToTheFirstClaudeFleet(t *testing.T) {
	dir := migrateDir(t, `{"version":1,"fleets":{"alpha":{"name":"Alpha","codex":{}},"beta":{"name":"Beta","claude":{}}},"chat":{"fleet":"gone","model":"haiku"}}`)
	if m, err := config.Migrate(dir, "1.2.3"); err != nil || m == nil {
		t.Fatalf("Migrate = %v, %v; want a migration", m, err)
	}
	cfg, err := config.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(cfg.Close)
	views := chatViews(t, cfg)
	if views["beta"].Model != "haiku" || views["alpha"].Model != "sonnet" {
		t.Errorf("beta = %+v, alpha = %+v; want haiku on beta, the first fleet with a Claude Code dock, and the default on alpha", views["beta"], views["alpha"])
	}
}

func TestMigrateLeavesAConfigWithPerFleetChatAlone(t *testing.T) {
	dir := migrateDir(t, withFleets(`"w":{"name":"W","claude":{},"chat":{"model":"opus"}}`))
	if m, err := config.Migrate(dir, "1.2.3"); m != nil || err != nil {
		t.Errorf("Migrate = %v, %v; want nothing to do", m, err)
	}
	if bk, _ := filepath.Glob(filepath.Join(dir, "config.json.pre-*")); len(bk) != 0 {
		t.Errorf("backups = %v, want none", bk)
	}
	if _, err := os.Stat(filepath.Join(dir, "config.json")); err != nil {
		t.Fatal(err)
	}
}
