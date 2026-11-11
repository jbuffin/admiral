package config_test

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jbuffin/admiral/internal/config"
)

const oldConfig = `{
  "version": 1,
  "x_future": {"keep": [1, 2, {"deep": true}]},
  "favorite_skills": ["tdd"],
  "fleets": {
    "zulu": {"name": "Zulu", "search_roots": ["/srv/code"], "claude": {}},
    "alpha": {"name": "Alpha", "search_roots": ["/srv/code"], "claude": {}}
  },
  "sources": {
    "tracker": {"plugin": "testplug", "name": "Tracker", "settings": {"token": "decoy-secret-0a1b2c3d"}},
    "github": {"plugin": "github", "name": "GitHub"},
    "notes": {"plugin": "freeform", "name": "Notes"}
  },
  "autopilot": {"max_ships": 2, "resume_source": {"source": "notes", "panel": "orders"}}
}
`

func migrateDir(t *testing.T, cfg string) string {
	t.Helper()
	dir := t.TempDir()
	writeFile(t, dir, cfg)
	return dir
}

func readText(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestMigrateGivesEverySourceTheFirstFleetAndDropsFreeform(t *testing.T) {
	dir := migrateDir(t, oldConfig)
	orders := filepath.Join(dir, "data", "notes", "orders.json")
	if err := os.MkdirAll(filepath.Dir(orders), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(orders, []byte(`[{"id":"o1","text":"Fix the flaky login test\nand keep the second line"},{"id":"o2","text":"Bump the linter"}]`), 0o600); err != nil {
		t.Fatal(err)
	}

	m, err := config.Migrate(dir, "1.2.3")
	if err != nil || m == nil {
		t.Fatalf("Migrate = %v, %v; want a migration", m, err)
	}
	if got := readText(t, filepath.Join(dir, "config.json.pre-1.2.3")); got != oldConfig {
		t.Errorf("the backup is not the old file byte for byte:\n%s", got)
	}

	cfg := decode(t, filepath.Join(dir, "config.json"))
	var sources map[string]map[string]any
	if err := json.Unmarshal(cfg["sources"], &sources); err != nil {
		t.Fatal(err)
	}
	if len(sources) != 2 || sources["tracker"]["fleet"] != "zulu" || sources["github"]["fleet"] != "zulu" {
		t.Errorf("sources = %v, want tracker and github on zulu, notes gone", sources)
	}
	if strings.Contains(string(cfg["autopilot"]), "resume_source") || !strings.Contains(string(cfg["autopilot"]), "max_ships") {
		t.Errorf("autopilot = %s, want resume_source cleared and max_ships kept", cfg["autopilot"])
	}
	for _, k := range []string{"x_future", "favorite_skills", "fleets"} {
		if cfg[k] == nil {
			t.Errorf("%s was dropped", k)
		}
	}
	if _, err := os.Stat(orders); err != nil {
		t.Errorf("the freeform source's orders file must stay on disk: %v", err)
	}

	title, body := m.Notice()
	if title == "" {
		t.Error("the notice has no title")
	}
	for _, want := range []string{"config.json.pre-1.2.3", "Fix the flaky login test\n    and keep the second line", "Bump the linter", "tracker", "Zulu", "notes"} {
		if !strings.Contains(body, want) {
			t.Errorf("notice body lacks %q:\n%s", want, body)
		}
	}
	if strings.Contains(body, "decoy-secret") {
		t.Errorf("the notice copied a source setting:\n%s", body)
	}

	if again, err := config.Migrate(dir, "1.2.4"); again != nil || err != nil {
		t.Errorf("a second Migrate = %v, %v; want nothing to do", again, err)
	}
}

func TestMigrateWithNoFleetsRemovesTheSources(t *testing.T) {
	old := `{"version":1,"fleets":{},"sources":{"tracker":{"plugin":"testplug","name":"Tracker"}}}`
	dir := migrateDir(t, old)
	m, err := config.Migrate(dir, "1.0.0")
	if err != nil || m == nil {
		t.Fatalf("Migrate = %v, %v; want a migration", m, err)
	}
	if got := string(decode(t, filepath.Join(dir, "config.json"))["sources"]); got != "{}" {
		t.Errorf("sources = %s, want none", got)
	}
	if _, body := m.Notice(); !strings.Contains(body, "tracker") {
		t.Errorf("notice body does not name the removed source:\n%s", body)
	}
}

func TestMigrateLeavesAnOtherwiseInvalidConfig(t *testing.T) {
	bad := `{"version":1,"fleets":[],"sources":{"notes":{"plugin":"freeform"}}}`
	dir := migrateDir(t, bad)
	if m, err := config.Migrate(dir, "1.0.0"); m != nil || err != nil {
		t.Fatalf("Migrate = %v, %v; want nothing done", m, err)
	}
	if got := readText(t, filepath.Join(dir, "config.json")); got != bad {
		t.Errorf("an invalid config was rewritten: %q", got)
	}
	if b, _ := filepath.Glob(filepath.Join(dir, "config.json.pre-*")); len(b) != 0 {
		t.Errorf("a backup was written: %v", b)
	}
}

func TestReadConfigRefusesAnUnmigratedConfig(t *testing.T) {
	dir := migrateDir(t, oldConfig)
	if _, err := config.ReadConfig(dir); !errors.Is(err, config.ErrNeedsMigration) {
		t.Fatalf("ReadConfig = %v, want ErrNeedsMigration", err)
	}
	if !strings.Contains(config.ErrNeedsMigration.Error(), "start Admiral once") {
		t.Errorf("the refusal must tell the engineer what to do: %v", config.ErrNeedsMigration)
	}
	if got := readText(t, filepath.Join(dir, "config.json")); got != oldConfig {
		t.Errorf("ReadConfig changed config.json:\n%s", got)
	}
}
