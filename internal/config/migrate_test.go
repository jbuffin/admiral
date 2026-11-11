package config_test

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
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
	if len(sources) != 3 || sources["tracker"]["fleet"] != "zulu" || sources["github"]["fleet"] != "zulu" || sources["github-alpha"]["fleet"] != "alpha" {
		t.Errorf("sources = %v, want tracker and github on zulu, github-alpha on alpha, notes gone", sources)
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

const migrateVersion = "9.9.9-test"

const ghFleets = `"fleets":{
  "a":{"name":"Alpha","search_roots":["/srv/a"],"claude":{}},
  "b":{"name":"Bravo","search_roots":["/srv/b"],"claude":{}}}`

// oldGitHub is a plugin source that already has a fleet but no views.
const oldGitHub = `"github":{"plugin":"github","name":"GitHub","fleet":"a",
  "settings":{"host":"ghe.example.com","account":"work","hotLabels":["p0"],"refreshMinutes":10,
    "panels":[{"title":"Bugs","query":"label:bug"},{"title":"Assigned to me","query":"assignee:@me"}]},
  "panel_order":["bugs","my-prs"],
  "autopilot":{"bugs":{"enabled":true,"fleet":"a","harness":"claude","skill":{"kind":"none"}}}}`

func migrateConfig(sources string) string {
	return `{"version":1,` + ghFleets + `,"sources":{` + sources + `},
  "autopilot":{"resume_source":{"source":"github","panel":"bugs"}}}
`
}

func readSources(t *testing.T, dir string) (map[string]map[string]any, map[string]any) {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(dir, "config.json"))
	if err != nil {
		t.Fatal(err)
	}
	var cfg struct {
		Sources   map[string]map[string]any `json:"sources"`
		Autopilot map[string]any            `json:"autopilot"`
	}
	if err := json.Unmarshal(b, &cfg); err != nil {
		t.Fatalf("config.json is not JSON after the migration: %v\n%s", err, b)
	}
	if _, err := config.Parse(b); err != nil {
		t.Errorf("the migrated config.json does not parse: %v\n%s", err, b)
	}
	return cfg.Sources, cfg.Autopilot
}

func settingsOf(t *testing.T, s map[string]any) map[string]any {
	t.Helper()
	m, ok := s["settings"].(map[string]any)
	if !ok {
		t.Fatalf("source has no settings object: %v", s)
	}
	return m
}

func wantJSON(t *testing.T, what string, got any, want string) {
	t.Helper()
	var w any
	if err := json.Unmarshal([]byte(want), &w); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, w) {
		gb, _ := json.Marshal(got)
		t.Errorf("%s = %s, want %s", what, gb, want)
	}
}

func TestMigrateGitHubPluginSource(t *testing.T) {
	dir := migrateDir(t, migrateConfig(oldGitHub))
	m, err := config.Migrate(dir, migrateVersion)
	if err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	if m == nil {
		t.Fatal("Migrate returned nil for a plugin GitHub source that already has a fleet")
	}
	srcs, ap := readSources(t, dir)
	views := `[{"title":"Bugs","query":"label:bug"},{"title":"Assigned to me","query":"assignee:@me"}]`

	a := srcs["github"]
	if a == nil || a["fleet"] != "a" || a["plugin"] != "github" || a["name"] != "GitHub" {
		t.Fatalf("source github = %v, want it kept on fleet a", a)
	}
	as := settingsOf(t, a)
	wantJSON(t, "github settings.views (entries kept, even one equal to a default)", as["views"], views)
	if _, has := as["panels"]; has {
		t.Errorf("github settings still hold panels: %v", as)
	}
	wantJSON(t, "github settings carried over", map[string]any{"host": as["host"], "account": as["account"], "hotLabels": as["hotLabels"], "refreshMinutes": as["refreshMinutes"]},
		`{"host":"ghe.example.com","account":"work","hotLabels":["p0"],"refreshMinutes":10}`)
	wantJSON(t, "github panel_order", a["panel_order"], `["bugs","my-prs"]`)
	wantJSON(t, "github autopilot", a["autopilot"], `{"bugs":{"enabled":true,"fleet":"a","harness":"claude","skill":{"kind":"none"}}}`)

	b := srcs["github-b"]
	if b == nil {
		t.Fatalf("sources = %v, want a github-b source for fleet b", keysOfSources(srcs))
	}
	if b["fleet"] != "b" || b["plugin"] != "github" {
		t.Errorf("github-b = %v, want plugin github on fleet b", b)
	}
	bs := settingsOf(t, b)
	wantJSON(t, "github-b settings", bs, `{"views":`+views+`,"host":"ghe.example.com","account":"work","hotLabels":["p0"],"refreshMinutes":10}`)
	wantJSON(t, "github-b panel_order", b["panel_order"], `["bugs","my-prs"]`)
	if _, has := b["autopilot"]; has {
		t.Errorf("github-b has autopilot %v: only the fleet that had the plugin keeps it", b["autopilot"])
	}
	wantJSON(t, "autopilot.resume_source", ap["resume_source"], `{"source":"github","panel":"bugs"}`)

	// The notice: GitHub only, so it doesn't open with the fleet move.
	_, body := m.Notice()
	if strings.Contains(body, "moved your sources into fleets") {
		t.Errorf("a GitHub-only migration opens with the fleet move:\n%s", body)
	}
	if strings.Contains(body, "went to the default fleet for now") {
		t.Errorf("the notice still says GitHub went to the default fleet:\n%s", body)
	}
	for _, want := range []string{m.Backup, "GitHub", "built in", "view", "Assigned to me", "Review requested", "My PRs"} {
		if !strings.Contains(body, want) {
			t.Errorf("notice lacks %q:\n%s", want, body)
		}
	}
	if strings.Contains(strings.ToLower(body), "panel") {
		t.Errorf("the notice says panel, the word is view:\n%s", body)
	}

	// A second migrate is a no-op, and a deleted GitHub source stays deleted.
	before, _ := os.ReadFile(filepath.Join(dir, "config.json"))
	if m2, err := config.Migrate(dir, migrateVersion); err != nil || m2 != nil {
		t.Fatalf("second Migrate = %+v, %v, want nil, nil", m2, err)
	}
	after, _ := os.ReadFile(filepath.Join(dir, "config.json"))
	if string(before) != string(after) {
		t.Errorf("a second Migrate rewrote config.json:\n%s", after)
	}
}

func keysOfSources(m map[string]map[string]any) []string {
	var k []string
	for key := range m {
		k = append(k, key)
	}
	return k
}

func TestMigrateGitHubSourceWithNoSettings(t *testing.T) {
	dir := migrateDir(t, migrateConfig(`"github":{"plugin":"github","name":"GitHub","fleet":"a"}`))
	m, err := config.Migrate(dir, migrateVersion)
	if err != nil || m == nil {
		t.Fatalf("Migrate = %+v, %v, want a migration", m, err)
	}
	srcs, _ := readSources(t, dir)
	wantJSON(t, "github settings.views", settingsOf(t, srcs["github"])["views"], `[]`)
	b := srcs["github-b"]
	if b == nil || b["fleet"] != "b" {
		t.Fatalf("sources = %v, want github-b on fleet b", keysOfSources(srcs))
	}
	wantJSON(t, "github-b settings.views", settingsOf(t, b)["views"], `[]`)
	_, body := m.Notice()
	if strings.Contains(body, "moved your sources into fleets") || strings.Contains(body, "default views now show") {
		t.Errorf("notice for a source with no custom views:\n%s", body)
	}
}

func TestMigrateGitHubKeyTakenAndOtherFleetCovered(t *testing.T) {
	t.Run("the new key avoids a taken one", func(t *testing.T) {
		taken := `"github-b":{"plugin":"jira","name":"Jira","fleet":"a"},` + oldGitHub
		dir := migrateDir(t, migrateConfig(taken))
		if m, err := config.Migrate(dir, migrateVersion); err != nil || m == nil {
			t.Fatalf("Migrate = %+v, %v", m, err)
		}
		srcs, _ := readSources(t, dir)
		if srcs["github-b"]["plugin"] != "jira" {
			t.Errorf("github-b was overwritten: %v", srcs["github-b"])
		}
		if s := srcs["github-b-2"]; s == nil || s["fleet"] != "b" || s["plugin"] != "github" {
			t.Errorf("sources = %v, want github-b-2 on fleet b", keysOfSources(srcs))
		}
	})
	t.Run("a fleet that already has a GitHub source gets no second one", func(t *testing.T) {
		both := oldGitHub + `,"mine":{"plugin":"github","name":"Mine","fleet":"b","settings":{"views":[]}}`
		dir := migrateDir(t, migrateConfig(both))
		if m, err := config.Migrate(dir, migrateVersion); err != nil || m == nil {
			t.Fatalf("Migrate = %+v, %v", m, err)
		}
		srcs, _ := readSources(t, dir)
		if len(srcs) != 2 {
			t.Errorf("sources = %v, want github and mine only", keysOfSources(srcs))
		}
		wantJSON(t, "mine settings", settingsOf(t, srcs["mine"]), `{"views":[]}`)
	})
}

func TestMigrateLeavesBuiltInAndOtherConfigs(t *testing.T) {
	cases := map[string]string{
		"github sources that already have views": migrateConfig(
			`"github":{"plugin":"github","name":"GitHub","fleet":"a","settings":{"views":[{"title":"Bugs","query":"label:bug"}]}}`),
		"a user who deleted a fleet's GitHub source": migrateConfig(
			`"github":{"plugin":"github","name":"GitHub","fleet":"a","settings":{"views":[]}}`),
		"no GitHub source": migrateConfig(`"tracker":{"plugin":"jira","name":"Jira","fleet":"a"}`),
	}
	for name, cfg := range cases {
		t.Run(name, func(t *testing.T) {
			dir := migrateDir(t, cfg)
			if m, err := config.Migrate(dir, migrateVersion); err != nil || m != nil {
				t.Fatalf("Migrate = %+v, %v, want nil, nil", m, err)
			}
			if got, _ := os.ReadFile(filepath.Join(dir, "config.json")); string(got) != cfg {
				t.Errorf("config.json changed:\n%s", got)
			}
			if b, _ := filepath.Glob(filepath.Join(dir, "config.json.pre-*")); len(b) != 0 {
				t.Errorf("a backup was written: %v", b)
			}
		})
	}
}

// A backup already there is the older, truer copy.
func TestMigrateGitHubKeepsAnOlderBackup(t *testing.T) {
	dir := migrateDir(t, migrateConfig(oldGitHub))
	backup := filepath.Join(dir, "config.json.pre-"+migrateVersion)
	if err := os.WriteFile(backup, []byte("older backup"), 0o600); err != nil {
		t.Fatal(err)
	}
	m, err := config.Migrate(dir, migrateVersion)
	if err != nil || m == nil {
		t.Fatalf("Migrate = %+v, %v, want a migration", m, err)
	}
	if got, _ := os.ReadFile(backup); string(got) != "older backup" {
		t.Errorf("the older backup was replaced: %q", got)
	}
	if srcs, _ := readSources(t, dir); srcs["github-b"] == nil {
		t.Errorf("the step did not run: %v", keysOfSources(srcs))
	}
}
