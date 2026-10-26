package e2e

import (
	"encoding/json"
	"net/http"
	"net/url"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"
)

// The autopilot settings routes (#181, u1): GET, PUT and DELETE on
// /api/sources/{key}/panels/{panel}/autopilot/settings. The config file is the
// truth, so most checks read config.json's bytes, not a response.

const apMsgChanged = "the config changed; reopen"

// apSettings is one world with source "a" (the test plugin, panel "main" with
// one order) and a config file written by hand, tab-indented with an unknown
// top-level key, so any rewrite of the file shows as different bytes.
type apSettings struct {
	*world
	p *tplug
}

func newApSettings(t *testing.T, entries map[string]any, mutate func(cfg map[string]any)) *apSettings {
	t.Helper()
	w := newWorld(t)
	w.writeCue("wait go", "exit 0")
	w.repo("r1", remoteOf("r1"))
	p := w.newTP("a")
	p.mainOrders(apOrder("o1", remoteOf("r1")))
	w.installPlugin("testplug", "testplug")
	src := map[string]any{"plugin": "testplug", "name": "Test", "settings": p.settings(nil)}
	if entries != nil {
		src["autopilot"] = entries
	}
	var cfg map[string]any
	if err := json.Unmarshal([]byte(w.config()), &cfg); err != nil {
		t.Fatal(err)
	}
	cfg["sources"] = map[string]any{"a": src}
	cfg["x_note"] = "decoy-secret-5e7a1c"
	if mutate != nil {
		mutate(cfg)
	}
	b, err := json.MarshalIndent(cfg, "", "\t")
	if err != nil {
		t.Fatal(err)
	}
	writeConfig(t, w.home, string(b)+"\n")
	w.start()
	w.waitSource("a", 10*time.Second, "state ok", isOK)
	return &apSettings{world: w, p: p}
}

func (a *apSettings) at(t *testing.T) *apSettings { return &apSettings{world: a.world.at(t), p: a.p} }

func (a *apSettings) path(panel string) string {
	return "/a/panels/" + url.PathEscape(panel) + "/autopilot/settings"
}

func (a *apSettings) bytes() string {
	a.t.Helper()
	b, err := os.ReadFile(configPath(a.home))
	if err != nil {
		a.t.Fatal(err)
	}
	return string(b)
}

type apGet struct {
	Entry        json.RawMessage `json:"entry"`
	Version      string          `json:"version"`
	MaxShips     int             `json:"max_ships"`
	ResumeSource bool            `json:"resume_source"`
	ArgsError    string          `json:"args_error"`
	ConfigError  string          `json:"config_error"`
}

func (a *apSettings) get(panel string) apGet {
	a.t.Helper()
	code, body := a.srcReq(http.MethodGet, a.path(panel), nil)
	if code != http.StatusOK {
		a.t.Fatalf("GET settings of %s = %d, want 200: %s", panel, code, body)
	}
	var g apGet
	if err := json.Unmarshal([]byte(body), &g); err != nil {
		a.t.Fatalf("GET settings body is not JSON: %v: %s", err, body)
	}
	return g
}

func (a *apSettings) put(panel string, entry map[string]any, version string) (int, string) {
	a.t.Helper()
	return a.srcReq(http.MethodPut, a.path(panel), map[string]any{"entry": entry, "version": version})
}

func (a *apSettings) del(panel, version string) (int, string) {
	a.t.Helper()
	q := ""
	if version != "" {
		q = "?version=" + url.QueryEscape(version)
	}
	return a.srcReq(http.MethodDelete, a.path(panel)+q, nil)
}

// entryOf is the panel's autopilot entry in config.json, decoded.
func (a *apSettings) entryOf(panel string) map[string]any {
	a.t.Helper()
	var cfg struct {
		Sources map[string]struct {
			Autopilot map[string]map[string]any `json:"autopilot"`
		} `json:"sources"`
	}
	if err := json.Unmarshal([]byte(a.bytes()), &cfg); err != nil {
		a.t.Fatalf("config.json is not JSON: %v", err)
	}
	return cfg.Sources["a"].Autopilot[panel]
}

func (a *apSettings) wantRefused(code int, body string, want int, msg, before, what string) {
	a.t.Helper()
	if code != want || !strings.Contains(strings.ReplaceAll(body, `\"`, `"`), msg) {
		a.t.Errorf("%s = %d %s, want %d containing %q", what, code, body, want, msg)
	}
	if got := a.bytes(); got != before {
		a.t.Errorf("%s changed config.json:\nbefore: %s\nafter:  %s", what, before, got)
	}
}

func apNew(extra map[string]any) map[string]any {
	e := map[string]any{"enabled": true, "fleet": "work", "harness": "claude", "skill": map[string]any{"kind": "none"}}
	for k, v := range extra {
		e[k] = v
	}
	return e
}

// AC 1, server half: the engineer turns autopilot on for a panel with no entry,
// and the engine launches a ship from it with no hand edit and no restart. GET
// before the save reports no entry, the global cap and no resume_source.
func TestAutopilotSettingsPutEnablesAPanel(t *testing.T) {
	a := newApSettings(t, nil, nil)
	g := a.get("main")
	if string(g.Entry) != "null" || g.Version == "" || g.MaxShips != 3 || g.ResumeSource || g.ArgsError != "" || g.ConfigError != "" {
		t.Errorf("GET of a panel with no entry = %+v (entry %s), want entry null, a version, max_ships 3 and nothing else set", g, g.Entry)
	}

	code, body := a.put("main", apNew(map[string]any{"args": []string{"--model", "opus"}, "max_ships": 1}), g.Version)
	a.mustOK(code, body, "PUT the new entry")
	var after apGet
	if err := json.Unmarshal([]byte(body), &after); err != nil || after.Version == "" || after.Version == g.Version {
		t.Errorf("PUT answered %s, want the GET shape with a new version", body)
	}
	var got map[string]any
	if err := json.Unmarshal(after.Entry, &got); err != nil || got["fleet"] != "work" || got["enabled"] != true {
		t.Errorf("PUT answered entry %s, want the saved entry", after.Entry)
	}
	if e := a.entryOf("main"); e == nil || e["fleet"] != "work" || e["harness"] != "claude" {
		t.Errorf("config.json entry = %v, want the saved entry", e)
	}
	if next := a.get("main"); next.Version != after.Version {
		t.Errorf("GET version %q after the PUT, want the one the PUT answered, %q", next.Version, after.Version)
	}

	a.waitShipCount(15*time.Second, "an autopilot ship from the enabled panel", fromOrder("o1"), 1)
}

// AC 2 and the fleet switch: a save that the whole-file check, the fleet
// lookup or the args allowlist refuses leaves config.json byte for byte.
func TestAutopilotSettingsRefusalsLeaveTheFileUnchanged(t *testing.T) {
	stored := apNew(map[string]any{"enabled": false, "args": []string{"--panel-arg"}, "max_ships": 2})
	a := newApSettings(t, map[string]any{"main": stored}, nil)
	g := a.get("main")
	if g.ArgsError == "" {
		t.Fatalf("GET args_error is empty for stored args %v that fail the allowlist", stored["args"])
	}
	before := a.bytes()

	for _, c := range []struct {
		name  string
		entry map[string]any
		code  int
		msg   string
	}{
		{"args off the allowlist", apNew(map[string]any{"args": []string{"--dangerously-skip-permissions"}}), 400, "--dangerously-skip-permissions is refused"},
		{"an unknown fleet", apNew(map[string]any{"fleet": "nope"}), 400, `no fleet "nope"`},
		{"max_ships 0", apNew(map[string]any{"max_ships": 0}), 400, "max_ships"},
		{"a bad low_stakes", apNew(map[string]any{"low_stakes": "sometimes"}), 400, "low_stakes"},
		{"a harness other than claude", apNew(map[string]any{"harness": "codex"}), 400, "Claude Code only"},
	} {
		t.Run(c.name, func(t *testing.T) {
			s := a.at(t)
			code, body := s.put("main", c.entry, g.Version)
			s.wantRefused(code, body, c.code, c.msg, before, c.name)
		})
	}
}

// ADR 0022, fleet switch: unchanged args that pass the stored fleet's dock but
// fail the new fleet's dock are refused. So are stored args that already fail:
// only a save that keeps the fleet skips the check (see the next test).
func TestAutopilotSettingsFleetSwitchChecksArgsAgainstTheNewDock(t *testing.T) {
	args := []string{"--disallowedTools", "Bash"}
	a := newApSettings(t, map[string]any{
		"p2": apNew(map[string]any{"enabled": false, "args": args}),
		"p3": apNew(map[string]any{"enabled": false, "args": []string{"--dangerously-skip-permissions"}}),
	}, func(cfg map[string]any) {
		fleets := cfg["fleets"].(map[string]any)
		strict := map[string]any{}
		for k, v := range fleets["home"].(map[string]any) {
			strict[k] = v
		}
		claude := map[string]any{}
		for k, v := range strict["claude"].(map[string]any) {
			claude[k] = v
		}
		claude["args"] = args // the dock already sets --disallowedTools
		strict["claude"] = claude
		strict["name"] = "Strict"
		fleets["strict"] = strict
	})
	g := a.get("p2")
	if g.ArgsError != "" {
		t.Fatalf("GET args_error = %q, want none: the stored args pass the work dock", g.ArgsError)
	}
	before := a.bytes()
	code, body := a.put("p2", apNew(map[string]any{"enabled": false, "fleet": "strict", "args": args}), g.Version)
	a.wantRefused(code, body, http.StatusBadRequest, "--arg --disallowedTools is refused: the dock already sets it", before, "a fleet switch to a dock that already sets the flag")

	// Stored args that already fail do not pass a fleet switch.
	g3 := a.get("p3")
	code, body = a.put("p3", apNew(map[string]any{"enabled": false, "fleet": "strict", "args": []string{"--dangerously-skip-permissions"}}), g3.Version)
	a.wantRefused(code, body, http.StatusBadRequest, "--dangerously-skip-permissions is refused", before, "a fleet switch with stored args that fail")
}

// AC 3: stored args that fail the allowlist (a hand edit) survive a save of
// other fields. The file is rewritten, so compare the parsed args. The save
// also keeps the other panel's entry, the rest of the source and the unknown
// top-level key.
func TestAutopilotSettingsKeepStoredFailingArgs(t *testing.T) {
	failing := []any{"--panel-arg", "--dangerously-skip-permissions"}
	other := apNew(map[string]any{"enabled": false, "fleet": "home", "args": []string{"--model", "opus"}})
	a := newApSettings(t, map[string]any{
		"main": apNew(map[string]any{"enabled": false, "args": failing, "max_ships": 1}),
		"gone": other, // a panel the plugin does not send
	}, nil)
	g := a.get("main")
	if g.ArgsError == "" {
		t.Fatal("GET args_error is empty for stored args that fail the allowlist")
	}
	var stored map[string]any
	_ = json.Unmarshal(g.Entry, &stored)
	if stored == nil {
		t.Fatalf("GET entry = %s, want the stored entry", g.Entry)
	}
	stored["max_ships"] = 2
	code, body := a.put("main", stored, g.Version)
	a.mustOK(code, body, "PUT with the stored failing args and a new max_ships")

	e := a.entryOf("main")
	if !reflect.DeepEqual(e["args"], failing) || e["max_ships"] != float64(2) {
		t.Errorf("entry after the save = %v, want args %v kept and max_ships 2", e, failing)
	}
	if o := a.entryOf("gone"); !reflect.DeepEqual(o["args"], []any{"--model", "opus"}) || o["fleet"] != "home" {
		t.Errorf("the entry of a panel the plugin does not send = %v, want it kept as it was", o)
	}
	if !strings.Contains(a.bytes(), `"x_note": "decoy-secret-5e7a1c"`) || !strings.Contains(a.bytes(), `"flagdir"`) {
		t.Errorf("config.json lost the unknown top-level key or the source's settings:\n%s", a.bytes())
	}
}

// AC 4: a save that changes nothing, even one that spells the entry in
// another key order, writes nothing. The bytes (tabs, the unknown key) stay.
func TestAutopilotSettingsUnchangedPutWritesNothing(t *testing.T) {
	a := newApSettings(t, map[string]any{"main": apNew(map[string]any{"enabled": false, "max_ships": 2, "args": []string{"--model", "opus"}})}, nil)
	g := a.get("main")
	before := a.bytes()
	info, err := os.Stat(configPath(a.home))
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(1100 * time.Millisecond) // a rewrite would move the mtime

	// The same entry as a different JSON text: reversed key order.
	reordered := `{"version":` + string(mustJSON(t, g.Version)) + `,"entry":{"args":["--model","opus"],"max_ships":2,"skill":{"kind":"none"},"harness":"claude","fleet":"work","enabled":false}}`
	code, body := a.srcReq(http.MethodPut, a.path("main"), json.RawMessage(reordered))
	a.mustOK(code, body, "PUT the entry unchanged")
	if got := a.bytes(); got != before {
		t.Errorf("an unchanged PUT changed config.json:\nbefore: %s\nafter:  %s", before, got)
	}
	if after, err := os.Stat(configPath(a.home)); err != nil || !after.ModTime().Equal(info.ModTime()) {
		t.Errorf("an unchanged PUT moved config.json's mtime: %v -> %v (%v)", info.ModTime(), after.ModTime(), err)
	}
}

// AC 5: a save after a hand edit to the same entry is refused and the hand edit
// stays; so is a Remove. A hand edit to another panel's entry is not a conflict.
func TestAutopilotSettingsStaleVersionIsRefused(t *testing.T) {
	a := newApSettings(t, map[string]any{
		"main":  apNew(map[string]any{"enabled": false, "max_ships": 1}),
		"other": apNew(map[string]any{"enabled": false, "max_ships": 1}),
	}, nil)
	g := a.get("main")

	// A hand edit to "main" (max_ships 4), written in another layout.
	edited := strings.Replace(a.bytes(), `"max_ships": 1`, `"max_ships": 4`, 1)
	if edited == a.bytes() {
		t.Fatal("the hand edit changed nothing; fix the test's fixture")
	}
	writeConfig(t, a.home, edited)
	if v := a.get("main").Version; v == g.Version {
		t.Fatalf("GET version is still %q after a hand edit to the entry", v)
	}

	a.setPaused("a", "main", true)
	code, body := a.put("main", apNew(map[string]any{"max_ships": 2}), g.Version)
	a.wantRefused(code, body, http.StatusConflict, apMsgChanged, edited, "a PUT with a stale version")
	code, body = a.put("main", apNew(map[string]any{"max_ships": 2}), "")
	a.wantRefused(code, body, http.StatusBadRequest, "version is required", edited, "a PUT without a version")
	code, body = a.del("main", g.Version)
	a.wantRefused(code, body, http.StatusConflict, apMsgChanged, edited, "a DELETE with a stale version")
	if p, ok := a.autopilotView("a", "main"); !ok || p.Autopilot == nil || !p.Autopilot.Paused {
		t.Errorf("after the stale DELETE the panel's autopilot = %+v, want it still paused", p.Autopilot)
	}

	// A hand edit to another panel's entry is no conflict for this one.
	cur := a.get("main").Version
	writeConfig(t, a.home, strings.Replace(edited, `"max_ships": 1`, `"max_ships": 5`, 1))
	code, body = a.put("main", apNew(map[string]any{"max_ships": 2}), cur)
	a.mustOK(code, body, "a PUT after a hand edit to another panel")
}

// AC 6: Remove deletes the entry and clears the pause, so the pause does not
// block a later re-add. It keeps the other panels' entries, needs a version,
// and clears nothing when it refuses.
func TestAutopilotSettingsDeleteClearsThePause(t *testing.T) {
	a := newApSettings(t, map[string]any{
		"main": apNew(map[string]any{"enabled": false}),
		"gone": apNew(nil),
	}, nil)
	a.setPaused("a", "main", true)
	g := a.get("main")

	// Refused deletes leave the entry and the pause.
	code, body := a.del("main", "")
	if code != http.StatusBadRequest || !strings.Contains(body, "version is required") {
		t.Errorf("DELETE without a version = %d %s, want 400 version is required", code, body)
	}
	code, body = a.srcReq(http.MethodDelete, "/nope/panels/main/autopilot/settings?version=x", nil)
	if code != http.StatusNotFound {
		t.Errorf("DELETE on an unknown source = %d %s, want 404", code, body)
	}
	if p, ok := a.autopilotView("a", "main"); !ok || p.Autopilot == nil || !p.Autopilot.Paused {
		t.Fatalf("after the refused deletes the panel's autopilot = %+v, want it still paused", p.Autopilot)
	}

	code, body = a.del("main", g.Version)
	a.mustOK(code, body, "DELETE the entry")
	if e := a.entryOf("main"); e != nil {
		t.Errorf("config.json still holds the entry after DELETE: %v", e)
	}
	if e := a.entryOf("gone"); e == nil {
		t.Error("DELETE of main removed the entry of another panel")
	}
	// Removing what is not there is a success.
	code, body = a.del("main", a.get("main").Version)
	a.mustOK(code, body, "DELETE an absent entry")

	// Re-adding must not come back paused. GET /api/sources reads the in-memory
	// pause, so no wait for state.json's flush.
	code, body = a.put("main", apNew(nil), a.get("main").Version)
	a.mustOK(code, body, "PUT the entry again")
	a.waitSource("a", 5*time.Second, "an unpaused autopilot on the re-added panel", func(s sourceJSON) bool {
		for _, p := range s.Panels {
			if p.ID == "main" {
				return p.Autopilot != nil && !p.Autopilot.Paused
			}
		}
		return false
	})
}

// AC 7: while config.json does not load, GET still answers with the error, and
// PUT and DELETE are refused without touching the file.
func TestAutopilotSettingsRefusedWhileTheConfigIsInvalid(t *testing.T) {
	a := newApSettings(t, map[string]any{"main": apNew(map[string]any{"enabled": false, "max_ships": 1})}, nil)
	g := a.get("main")

	bad := strings.Replace(a.bytes(), `"harness": "claude"`, `"harness": "codex"`, 1)
	writeConfig(t, a.home, bad)
	waitAPI(t, a.in.instance, "an error", hasErr)

	got := a.get("main")
	if got.ConfigError == "" || !strings.Contains(got.ConfigError, "autopilot runs on Claude Code only") {
		t.Errorf("GET config_error = %q, want the load error", got.ConfigError)
	}
	code, body := a.put("main", apNew(map[string]any{"max_ships": 2}), g.Version)
	a.wantRefused(code, body, http.StatusConflict, "Claude Code only", bad, "a PUT while the config is invalid")
	code, body = a.del("main", g.Version)
	a.wantRefused(code, body, http.StatusConflict, "Claude Code only", bad, "a DELETE while the config is invalid")
}

// AC 8: saving a source's plugin settings right after a settings write keeps
// the new autopilot entry (the write reloads the config at once, so the plugin
// settings save does not start from the stale one).
func TestAutopilotSettingsThenPluginSettingsKeepTheEntry(t *testing.T) {
	a := newApSettings(t, nil, nil)
	code, body := a.put("main", apNew(map[string]any{"max_ships": 2}), a.get("main").Version)
	a.mustOK(code, body, "PUT the entry")

	code, body = a.srcReq(http.MethodPut, "/a", map[string]any{"name": "Renamed", "settings": a.p.settings(nil)})
	a.mustOK(code, body, "plugin settings save")
	if e := a.entryOf("main"); e == nil || e["max_ships"] != float64(2) {
		t.Errorf("after the plugin settings save, autopilot.main = %v, want the entry kept", e)
	}
}

// The panel id is checked like the order routes check it, and any well-formed
// id is accepted.
func TestAutopilotSettingsPanelIDs(t *testing.T) {
	a := newApSettings(t, nil, nil)
	before := a.bytes()
	code, body := a.srcReq(http.MethodGet, "/a/panels/bad%01id/autopilot/settings", nil)
	if code != http.StatusBadRequest {
		t.Errorf("GET with a control character in the panel id = %d %s, want 400", code, body)
	}
	if code, body = a.srcReq(http.MethodGet, "/nope/panels/main/autopilot/settings", nil); code != http.StatusNotFound {
		t.Errorf("GET on an unknown source = %d %s, want 404", code, body)
	}
	if a.bytes() != before {
		t.Error("config.json changed")
	}
	code, body = a.put("not a panel the plugin sends", apNew(nil), a.get("not a panel the plugin sends").Version)
	a.mustOK(code, body, "PUT for a well-formed panel id the plugin does not send")
}
