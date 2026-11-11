package sources

import (
	"encoding/json"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/jbuffin/admiral/internal/config"
)

// apFixture is source "a" with these autopilot entries, in a config file
// written tab-indented with an unknown top-level key, so any rewrite shows as
// different bytes. Fleet work's claude dock sets no args; strict's sets
// --disallowedTools.
func apFixture(t *testing.T, entries map[string]any) *serviceFixture {
	t.Helper()
	cfg := map[string]any{
		"version": 1,
		"x_note":  "kept",
		"fleets": map[string]any{
			"work":   map[string]any{"name": "Work", "search_roots": []string{"/srv"}, "claude": map[string]any{}},
			"home":   map[string]any{"name": "Home", "search_roots": []string{"/srv"}, "claude": map[string]any{}},
			"strict": map[string]any{"name": "Strict", "search_roots": []string{"/srv"}, "claude": map[string]any{"args": []string{"--disallowedTools", "Bash"}}},
		},
		"sources": map[string]any{"a": map[string]any{"plugin": "notinstalled", "fleet": "work", "settings": map[string]any{"flagdir": "/tmp/x"}, "autopilot": entries}},
	}
	if entries == nil {
		delete(cfg["sources"].(map[string]any)["a"].(map[string]any), "autopilot")
	}
	b, _ := json.MarshalIndent(cfg, "", "\t")
	return newServiceFixture(t, string(b)+"\n")
}

func apEntry(extra map[string]any) map[string]any {
	e := map[string]any{"enabled": true, "fleet": "work", "harness": "claude", "skill": map[string]any{"kind": "none"}}
	for k, v := range extra {
		e[k] = v
	}
	return e
}

type apGet struct {
	Entry       json.RawMessage `json:"entry"`
	Version     string          `json:"version"`
	MaxShips    int             `json:"max_ships"`
	ArgsError   string          `json:"args_error"`
	ConfigError string          `json:"config_error"`
}

func apPath(panel string) string { return "/a/panels/" + url.PathEscape(panel) + "/autopilot/settings" }

func (f *serviceFixture) apGet(panel string) apGet {
	f.t.Helper()
	code, body := f.do(http.MethodGet, apPath(panel), nil)
	if code != http.StatusOK {
		f.t.Fatalf("GET settings of %s = %d: %s", panel, code, body)
	}
	var g apGet
	if err := json.Unmarshal([]byte(body), &g); err != nil {
		f.t.Fatal(err)
	}
	return g
}

func (f *serviceFixture) apPut(panel string, entry map[string]any, version string) (int, string) {
	f.t.Helper()
	return f.do(http.MethodPut, apPath(panel), map[string]any{"entry": entry, "version": version})
}

func (f *serviceFixture) apDel(panel, version string) (int, string) {
	f.t.Helper()
	return f.do(http.MethodDelete, apPath(panel)+"?version="+url.QueryEscape(version), nil)
}

func (f *serviceFixture) entry(panel string) map[string]any {
	f.t.Helper()
	var cfg struct {
		Sources map[string]struct {
			Autopilot map[string]map[string]any `json:"autopilot"`
		} `json:"sources"`
	}
	if err := json.Unmarshal([]byte(f.file()), &cfg); err != nil {
		f.t.Fatal(err)
	}
	return cfg.Sources["a"].Autopilot[panel]
}

func (f *serviceFixture) refused(code int, body string, want int, msg, before, what string) {
	f.t.Helper()
	if code != want || !strings.Contains(strings.ReplaceAll(body, `\"`, `"`), msg) {
		f.t.Errorf("%s = %d %s, want %d naming %q", what, code, body, want, msg)
	}
	if f.file() != before {
		f.t.Errorf("%s changed config.json", what)
	}
}

func ok(t *testing.T, code int, body, what string) {
	t.Helper()
	if code != http.StatusOK {
		t.Fatalf("%s = %d %s, want 200", what, code, body)
	}
}

func TestAutopilotSettingsPutAddsAnEntry(t *testing.T) {
	f := apFixture(t, nil)
	g := f.apGet("main")
	if string(g.Entry) != "null" || g.Version == "" || g.MaxShips != config.DefaultAutopilotMaxShips || g.ArgsError != "" {
		t.Errorf("GET of a panel with no entry = %+v, want entry null, a version and the global cap", g)
	}
	code, body := f.apPut("main", apEntry(map[string]any{"args": []string{"--model", "opus"}, "max_ships": 1}), g.Version)
	ok(t, code, body, "PUT the new entry")
	var after apGet
	_ = json.Unmarshal([]byte(body), &after)
	if after.Version == "" || after.Version == g.Version || f.apGet("main").Version != after.Version {
		t.Errorf("PUT answered version %q, want a new one that GET then reports", after.Version)
	}
	if e := f.entry("main"); e["fleet"] != "work" || e["max_ships"] != float64(1) {
		t.Errorf("config.json entry = %v, want the saved entry", e)
	}
	// A well-formed panel id the plugin doesn't send is fine.
	code, body = f.apPut("not sent", apEntry(nil), f.apGet("not sent").Version)
	ok(t, code, body, "PUT for a panel the plugin does not send")
}

func TestAutopilotSettingsRefusalsLeaveTheFileUnchanged(t *testing.T) {
	f := apFixture(t, map[string]any{"main": apEntry(map[string]any{"enabled": false})})
	g := f.apGet("main")
	before := f.file()
	for _, c := range []struct {
		name  string
		entry map[string]any
		msg   string
	}{
		{"args off the allowlist", apEntry(map[string]any{"args": []string{"--dangerously-skip-permissions"}}), "--dangerously-skip-permissions is refused"},
		{"an unknown fleet", apEntry(map[string]any{"fleet": "nope"}), `no fleet "nope"`},
		{"max_ships 0", apEntry(map[string]any{"max_ships": 0}), "max_ships"},
		{"a bad low_stakes", apEntry(map[string]any{"low_stakes": "sometimes"}), "low_stakes"},
		{"a harness other than claude", apEntry(map[string]any{"harness": "codex"}), "Claude Code only"},
		{"a switch to a fleet whose dock sets the flag", apEntry(map[string]any{"fleet": "strict", "args": []string{"--disallowedTools", "Bash"}}), "the dock already sets it"},
	} {
		code, body := f.apPut("main", c.entry, g.Version)
		f.refused(code, body, http.StatusBadRequest, c.msg, before, c.name)
	}
	code, body := f.apPut("main", apEntry(nil), "")
	f.refused(code, body, http.StatusBadRequest, "version is required", before, "a PUT without a version")
	code, body = f.do(http.MethodGet, "/a/panels/bad%01id/autopilot/settings", nil)
	f.refused(code, body, http.StatusBadRequest, "panel", before, "a control character in the panel id")
	code, body = f.do(http.MethodGet, "/nope/panels/main/autopilot/settings", nil)
	f.refused(code, body, http.StatusNotFound, "nope", before, "an unknown source")
}

// Stored args that already fail (a hand edit) survive a save that keeps the
// fleet, but not a switch to another fleet.
func TestAutopilotSettingsKeepStoredFailingArgsOnlyOnTheSameFleet(t *testing.T) {
	failing := []any{"--dangerously-skip-permissions"}
	f := apFixture(t, map[string]any{
		"main":  apEntry(map[string]any{"enabled": false, "args": failing, "max_ships": 1}),
		"other": apEntry(map[string]any{"fleet": "home", "args": []string{"--model", "opus"}}),
	})
	g := f.apGet("main")
	if g.ArgsError == "" {
		t.Fatal("GET args_error is empty for stored args that fail the allowlist")
	}
	var stored map[string]any
	_ = json.Unmarshal(g.Entry, &stored)

	moved := map[string]any{}
	for k, v := range stored {
		moved[k] = v
	}
	moved["fleet"] = "home"
	before := f.file()
	code, body := f.apPut("main", moved, g.Version)
	f.refused(code, body, http.StatusBadRequest, "--dangerously-skip-permissions is refused", before, "a fleet switch with stored failing args")

	stored["max_ships"] = 2
	code, body = f.apPut("main", stored, g.Version)
	ok(t, code, body, "PUT with the stored failing args and a new max_ships")
	if e := f.entry("main"); !reflect.DeepEqual(e["args"], failing) || e["max_ships"] != float64(2) {
		t.Errorf("entry = %v, want the args kept and max_ships 2", e)
	}
	if o := f.entry("other"); o["fleet"] != "home" || !strings.Contains(f.file(), `"x_note": "kept"`) {
		t.Errorf("the save lost another panel's entry (%v) or the unknown top-level key", o)
	}
}

func TestAutopilotSettingsUnchangedPutWritesNothing(t *testing.T) {
	f := apFixture(t, map[string]any{"main": apEntry(map[string]any{"enabled": false, "max_ships": 2})})
	g := f.apGet("main")
	before := f.file()
	info, _ := os.Stat(filepath.Join(f.home, config.FileName))
	time.Sleep(10 * time.Millisecond) // a rewrite would move the mtime

	reordered := json.RawMessage(`{"version":"` + g.Version + `","entry":{"max_ships":2,"skill":{"kind":"none"},"harness":"claude","fleet":"work","enabled":false}}`)
	code, body := f.do(http.MethodPut, apPath("main"), reordered)
	ok(t, code, body, "PUT the entry in another key order")
	after, _ := os.Stat(filepath.Join(f.home, config.FileName))
	if f.file() != before || !after.ModTime().Equal(info.ModTime()) {
		t.Error("an unchanged PUT rewrote config.json")
	}
}

// A save or a Remove after a hand edit to the same entry is refused and the
// hand edit stays; an edit to another panel's entry is no conflict.
func TestAutopilotSettingsStaleVersionIsRefused(t *testing.T) {
	f := apFixture(t, map[string]any{
		"main":  apEntry(map[string]any{"enabled": false, "max_ships": 1}),
		"other": apEntry(map[string]any{"enabled": false, "max_ships": 3}),
	})
	g := f.apGet("main")
	path := filepath.Join(f.home, config.FileName)
	edit := func(from, to string) string {
		t.Helper()
		edited := strings.Replace(f.file(), from, to, 1)
		if err := os.WriteFile(path, []byte(edited), 0o600); err != nil {
			t.Fatal(err)
		}
		return edited
	}

	edited := edit(`"max_ships": 1`, `"max_ships": 4`)
	f.State.SetPaused("a", "main", true)
	code, body := f.apPut("main", apEntry(map[string]any{"max_ships": 2}), g.Version)
	f.refused(code, body, http.StatusConflict, "the config changed; reopen", edited, "a PUT with a stale version")
	code, body = f.apDel("main", g.Version)
	f.refused(code, body, http.StatusConflict, "the config changed; reopen", edited, "a DELETE with a stale version")
	if !f.State.Paused("a", "main") {
		t.Error("a refused DELETE cleared the pause")
	}

	cur := f.apGet("main").Version
	edit(`"max_ships": 3`, `"max_ships": 5`)
	code, body = f.apPut("main", apEntry(map[string]any{"max_ships": 2}), cur)
	ok(t, code, body, "a PUT after a hand edit to another panel")
}

func TestAutopilotSettingsDeleteClearsThePause(t *testing.T) {
	f := apFixture(t, map[string]any{"main": apEntry(map[string]any{"enabled": false}), "other": apEntry(nil)})
	f.State.SetPaused("a", "main", true)
	code, body := f.do(http.MethodDelete, apPath("main"), nil)
	f.refused(code, body, http.StatusBadRequest, "version is required", f.file(), "a DELETE without a version")

	code, body = f.apDel("main", f.apGet("main").Version)
	ok(t, code, body, "DELETE the entry")
	if f.entry("main") != nil || f.entry("other") == nil {
		t.Errorf("after DELETE main = %v other = %v, want main gone and other kept", f.entry("main"), f.entry("other"))
	}
	if f.State.Paused("a", "main") {
		t.Error("DELETE left the pause, so a re-added entry would come back paused")
	}
	code, body = f.apDel("main", f.apGet("main").Version)
	ok(t, code, body, "DELETE an absent entry")
}

func TestAutopilotSettingsRefusedWhileTheConfigIsInvalid(t *testing.T) {
	f := apFixture(t, map[string]any{"main": apEntry(map[string]any{"enabled": false})})
	g := f.apGet("main")
	bad := strings.Replace(f.file(), `"harness": "claude"`, `"harness": "codex"`, 1)
	if err := os.WriteFile(filepath.Join(f.home, config.FileName), []byte(bad), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := f.apGet("main").ConfigError; !strings.Contains(got, "Claude Code only") {
		t.Errorf("GET config_error = %q, want the load error", got)
	}
	code, body := f.apPut("main", apEntry(nil), g.Version)
	f.refused(code, body, http.StatusConflict, "Claude Code only", bad, "a PUT while the config is invalid")
	code, body = f.apDel("main", g.Version)
	f.refused(code, body, http.StatusConflict, "Claude Code only", bad, "a DELETE while the config is invalid")
}

// The settings write reloads the config at once, so a plugin settings save
// right after it starts from the new entry.
func TestAutopilotSettingsThenPluginSettingsKeepTheEntry(t *testing.T) {
	f := apFixture(t, nil)
	code, body := f.apPut("main", apEntry(map[string]any{"max_ships": 2}), f.apGet("main").Version)
	ok(t, code, body, "PUT the entry")
	code, body = f.do(http.MethodPut, "/a", map[string]any{"name": "Renamed", "settings": map[string]any{"flagdir": "/tmp/y"}})
	ok(t, code, body, "plugin settings save")
	if e := f.entry("main"); e["max_ships"] != float64(2) {
		t.Errorf("after the plugin settings save the entry = %v, want it kept", e)
	}
}
