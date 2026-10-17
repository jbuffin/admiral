package config_test

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/jbuffin/admiral/internal/config"
)

// The write API shape these tests assume (the implementer may add to it, but
// these two functions on an ADMIRAL_HOME directory are the contract):
//
//	config.SetFavoriteSkills(dir string, skills []string) error
//	config.SetSources(dir string, sources json.RawMessage) error
//
// Both read $dir/config.json fresh, refuse when it is invalid, replace one
// top-level key and write the file atomically.

const richConfig = `{
  "version": 1,
  "future_setting": {"a": [1, 2, {"b": null}], "c": "kept"},
  "favorite_skills": ["old"],
  "sources": [{"key": "jira", "plugin": "jira", "settings": {"project": "ADM"}}],
  "fleets": {
    "zeta": {
      "name": "Zeta",
      "color": "#112233",
      "search_roots": ["~/code"],
      "claude": {"executable": "~/bin/claude", "args": ["--x"], "env": {"CLAUDE_CONFIG_DIR": "~/.claude-zeta"}}
    },
    "alpha": {
      "name": "Alpha",
      "search_roots": ["/srv/alpha"],
      "codex": {}
    }
  }
}
`

func writeFile(t *testing.T, dir, content string) string {
	t.Helper()
	p := filepath.Join(dir, "config.json")
	if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func decode(t *testing.T, path string) map[string]json.RawMessage {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]json.RawMessage
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatalf("%s is not JSON after the write: %v\n%s", path, err, b)
	}
	return m
}

func jsonEqual(t *testing.T, a, b json.RawMessage) bool {
	t.Helper()
	var x, y any
	if err := json.Unmarshal(a, &x); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(b, &y); err != nil {
		t.Fatal(err)
	}
	return reflect.DeepEqual(x, y)
}

// fleetKeyOrder returns the keys of the "fleets" object in file order.
func fleetKeyOrder(t *testing.T, path string) []string {
	t.Helper()
	fleets := decode(t, path)["fleets"]
	dec := json.NewDecoder(bytes.NewReader(fleets))
	if _, err := dec.Token(); err != nil { // {
		t.Fatal(err)
	}
	var keys []string
	for dec.More() {
		tok, err := dec.Token()
		if err != nil {
			t.Fatal(err)
		}
		keys = append(keys, tok.(string))
		var skip json.RawMessage
		if err := dec.Decode(&skip); err != nil {
			t.Fatal(err)
		}
	}
	return keys
}

// S9: replacing favorite_skills keeps the unknown top-level key, sources and
// the fleets (content and order), and leaves no temp file behind.
func TestS9_SetFavoriteSkillsKeepsEverythingElse(t *testing.T) {
	dir := t.TempDir()
	path := writeFile(t, dir, richConfig)
	before := decode(t, path)

	if err := config.SetFavoriteSkills(dir, []string{"commodore", "review"}); err != nil {
		t.Fatalf("SetFavoriteSkills: %v", err)
	}

	after := decode(t, path)
	if !jsonEqual(t, after["favorite_skills"], json.RawMessage(`["commodore","review"]`)) {
		t.Fatalf("favorite_skills = %s, want [commodore review]", after["favorite_skills"])
	}
	for _, k := range []string{"version", "future_setting", "sources", "fleets"} {
		if !jsonEqual(t, before[k], after[k]) {
			t.Errorf("%q changed: before %s, after %s", k, before[k], after[k])
		}
	}
	if len(after) != len(before) {
		t.Errorf("top-level keys changed: before %d, after %d", len(before), len(after))
	}
	if got, want := fleetKeyOrder(t, path), []string{"zeta", "alpha"}; !reflect.DeepEqual(got, want) {
		t.Errorf("fleet order = %v, want %v (file order is marina order)", got, want)
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) != 1 {
		t.Errorf("the write must leave only config.json in %s (temp file renamed away), found %d entries", dir, len(entries))
	}
}

// S9 (sources half): SetSources replaces sources only.
func TestS9_SetSourcesKeepsEverythingElse(t *testing.T) {
	dir := t.TempDir()
	path := writeFile(t, dir, richConfig)
	before := decode(t, path)

	newSources := json.RawMessage(`[{"key":"freeform","plugin":"freeform","settings":{}}]`)
	if err := config.SetSources(dir, newSources); err != nil {
		t.Fatalf("SetSources: %v", err)
	}

	after := decode(t, path)
	if !jsonEqual(t, after["sources"], newSources) {
		t.Fatalf("sources = %s, want %s", after["sources"], newSources)
	}
	for _, k := range []string{"version", "future_setting", "favorite_skills", "fleets"} {
		if !jsonEqual(t, before[k], after[k]) {
			t.Errorf("%q changed: before %s, after %s", k, before[k], after[k])
		}
	}
	if got, want := fleetKeyOrder(t, path), []string{"zeta", "alpha"}; !reflect.DeepEqual(got, want) {
		t.Errorf("fleet order = %v, want %v", got, want)
	}
}

// S9: six favorites are refused and the file is untouched.
func TestS9_SixFavoritesRefused(t *testing.T) {
	dir := t.TempDir()
	path := writeFile(t, dir, richConfig)

	err := config.SetFavoriteSkills(dir, []string{"a", "b", "c", "d", "e", "f"})
	if err == nil {
		t.Fatal("six favorites must be refused (at most five)")
	}
	got, _ := os.ReadFile(path)
	if string(got) != richConfig {
		t.Fatalf("a refused write must leave the file untouched, got:\n%s", got)
	}
	if err := config.SetFavoriteSkills(dir, []string{"a", "b", "c", "d", "e"}); err != nil {
		t.Fatalf("five favorites are allowed: %v", err)
	}
}

// S9: no write while the file on disk is invalid, for either key.
func TestS9_WriteRefusedWhileFileInvalid(t *testing.T) {
	invalid := `{"version":1,"fleets":{"work":{"name":"Work","claude":{"evn":{}}}}}`
	dir := t.TempDir()
	path := writeFile(t, dir, invalid)

	if err := config.SetFavoriteSkills(dir, []string{"a"}); err == nil {
		t.Error("SetFavoriteSkills must refuse while the file is invalid")
	}
	if err := config.SetSources(dir, json.RawMessage(`[]`)); err == nil {
		t.Error("SetSources must refuse while the file is invalid")
	}
	got, _ := os.ReadFile(path)
	if string(got) != invalid {
		t.Fatalf("a refused write must leave the file untouched, got:\n%s", got)
	}
}

// S9: with no file on disk the write creates one with version 1 and empty fleets.
func TestS9_WriteCreatesFileWhenMissing(t *testing.T) {
	dir := t.TempDir()
	if err := config.SetSources(dir, json.RawMessage(`[{"key":"freeform"}]`)); err != nil {
		t.Fatalf("SetSources on a fresh install: %v", err)
	}
	m := decode(t, filepath.Join(dir, "config.json"))
	if !jsonEqual(t, m["version"], json.RawMessage(`1`)) {
		t.Errorf("version = %s, want 1", m["version"])
	}
	if !jsonEqual(t, m["fleets"], json.RawMessage(`{}`)) {
		t.Errorf("fleets = %s, want {}", m["fleets"])
	}
	if !jsonEqual(t, m["sources"], json.RawMessage(`[{"key":"freeform"}]`)) {
		t.Errorf("sources = %s", m["sources"])
	}
}
