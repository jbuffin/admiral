package e2e

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
)

// Config scenarios (u2): the real binary with a temporary ADMIRAL_HOME and a
// hand-written config.json, observed over GET /api/config and the WebSocket
// `config` message.

type cfgDock struct {
	Harness         string   `json:"harness"`
	Executable      string   `json:"executable"`
	ExecutableFound bool     `json:"executable_found"`
	Warnings        []string `json:"warnings"`
}

type cfgFleet struct {
	Key         string    `json:"key"`
	Name        string    `json:"name"`
	Color       string    `json:"color"`
	Ink         string    `json:"ink"`
	SearchRoots []string  `json:"search_roots"`
	CanLaunch   bool      `json:"can_launch"`
	Docks       []cfgDock `json:"docks"`
}

type cfgBody struct {
	Type           string     `json:"type"`
	Fleets         []cfgFleet `json:"fleets"`
	FavoriteSkills []string   `json:"favorite_skills"`
	Error          *struct {
		File    string `json:"file"`
		Message string `json:"message"`
	} `json:"error"`
}

func (b cfgBody) keys() []string {
	k := []string{}
	for _, f := range b.Fleets {
		k = append(k, f.Key)
	}
	return k
}

func (b cfgBody) fleet(key string) cfgFleet {
	for _, f := range b.Fleets {
		if f.Key == key {
			return f
		}
	}
	return cfgFleet{}
}

func (f cfgFleet) dock(harness string) (cfgDock, bool) {
	for _, d := range f.Docks {
		if d.Harness == harness {
			return d, true
		}
	}
	return cfgDock{}, false
}

func configPath(home string) string { return filepath.Join(home, "config.json") }

func writeConfig(t *testing.T, home, content string) {
	t.Helper()
	if err := os.MkdirAll(home, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(configPath(home), []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

// replaceConfig is how most editors save: write a temp file, rename it over.
func replaceConfig(t *testing.T, home, content string) {
	t.Helper()
	tmp := filepath.Join(home, ".config.json.swp")
	if err := os.WriteFile(tmp, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(tmp, configPath(home)); err != nil {
		t.Fatal(err)
	}
}

func withVersion(fleets string) string {
	return fmt.Sprintf(`{"version":1,"fleets":{%s}}`, fleets)
}

func fetchConfig(t *testing.T, in *instance) (cfgBody, string) {
	t.Helper()
	resp, body := get(t, in.LocalURL+"/api/config", nil)
	if resp.StatusCode != 200 {
		t.Fatalf("GET /api/config = %d, want 200: %s", resp.StatusCode, body)
	}
	var b cfgBody
	if err := json.Unmarshal([]byte(body), &b); err != nil {
		t.Fatalf("GET /api/config is not JSON: %v\n%s", err, body)
	}
	return b, body
}

// waitAPI polls GET /api/config until pred holds (the spec says a hand edit
// shows within about a second).
func waitAPI(t *testing.T, in *instance, what string, pred func(cfgBody) bool) cfgBody {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for {
		b, raw := fetchConfig(t, in)
		if pred(b) {
			return b
		}
		if time.Now().After(deadline) {
			t.Fatalf("within 3s GET /api/config never showed %s; last body:\n%s", what, raw)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func dialWS(t *testing.T, in *instance) *websocket.Conn {
	t.Helper()
	c, _, err := wsDial(t, in.LocalURL, "", nil, "")
	if err != nil {
		t.Fatalf("websocket dial: %v", err)
	}
	return c
}

// waitWS reads messages until a `config` message satisfies pred, within d.
func waitWS(t *testing.T, c *websocket.Conn, d time.Duration, what string, pred func(cfgBody) bool) (cfgBody, string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), d)
	defer cancel()
	var last string
	for {
		_, data, err := c.Read(ctx)
		if err != nil {
			t.Fatalf("within %s the websocket never delivered a `config` message with %s (last config message: %s): %v", d, what, last, err)
		}
		var b cfgBody
		if json.Unmarshal(data, &b) != nil || b.Type != "config" {
			continue
		}
		last = string(data)
		if pred(b) {
			return b, string(data)
		}
	}
}

func anyConfig(cfgBody) bool { return true }

func hasErr(b cfgBody) bool { return b.Error != nil }
func noErr(b cfgBody) bool  { return b.Error == nil }
func fleetKeys(keys ...string) func(cfgBody) bool {
	return func(b cfgBody) bool { return reflect.DeepEqual(b.keys(), keys) }
}

// S1: fleets come back in file order with resolved colors and inks, on the API
// and on the WebSocket, with docks in the prototype's order. The color
// sequence skips nothing, counts only fleets without a color, and cycles.
func TestConfigS1_FleetsInFileOrderWithResolvedColorsAndInk(t *testing.T) {
	home := newHome(t)
	writeConfig(t, home, `{
	  "version": 1,
	  "favorite_skills": ["commodore", "review"],
	  "fleets": {
	    "zeta": {"name": "Zeta Fleet", "color": "#112233", "search_roots": ["/tmp"], "codex": {}, "claude": {}, "copilot": {}},
	    "f1": {"name": "F1"}, "f2": {"name": "F2"}, "f3": {"name": "F3"},
	    "f4": {"name": "F4"}, "f5": {"name": "F5"}, "f6": {"name": "F6"},
	    "yellow": {"name": "Yellow", "color": "#FFFF00"},
	    "black":  {"name": "Black", "color": "#000000"},
	    "orange": {"name": "Orange", "color": "#F28C28"},
	    "violet": {"name": "Violet", "color": "#7A5AF0"}
	  }
	}`)
	in := startAdmiral(t, home, "127.0.0.1:0")

	check := func(where string, b cfgBody) {
		t.Helper()
		wantKeys := []string{"zeta", "f1", "f2", "f3", "f4", "f5", "f6", "yellow", "black", "orange", "violet"}
		if got := b.keys(); !reflect.DeepEqual(got, wantKeys) {
			t.Fatalf("%s: fleets = %v, want file order %v", where, got, wantKeys)
		}
		if got := b.fleet("zeta"); got.Name != "Zeta Fleet" {
			t.Errorf("%s: zeta = %+v", where, got)
		}
		want := []struct{ key, color, ink string }{
			{"zeta", "#112233", "#FFFFFF"}, // explicit colors don't use up the sequence
			{"f1", "#3366E0", "#FFFFFF"},
			{"f2", "#F28C28", "#14304A"},
			{"f3", "#7A5AF0", "#FFFFFF"},
			{"f4", "#1F9E8C", "#FFFFFF"},
			{"f5", "#E2447A", "#FFFFFF"},
			{"f6", "#3366E0", "#FFFFFF"}, // cycles after the fifth
			{"yellow", "#FFFF00", "#14304A"},
			{"black", "#000000", "#FFFFFF"},
			{"orange", "#F28C28", "#14304A"},
			{"violet", "#7A5AF0", "#FFFFFF"},
		}
		for _, w := range want {
			if got := b.fleet(w.key); got.Color != w.color || got.Ink != w.ink {
				t.Errorf("%s: fleet %s: color %q ink %q, want %q ink %q", where, w.key, got.Color, got.Ink, w.color, w.ink)
			}
		}
		var order []string
		for _, d := range b.fleet("zeta").Docks {
			order = append(order, d.Harness)
		}
		if want := []string{"claude", "copilot", "codex"}; !reflect.DeepEqual(order, want) {
			t.Errorf("%s: zeta docks = %v, want %v (prototype order, not file order)", where, order, want)
		}
		if !reflect.DeepEqual(b.FavoriteSkills, []string{"commodore", "review"}) {
			t.Errorf("%s: favorite_skills = %v", where, b.FavoriteSkills)
		}
		if b.Error != nil {
			t.Errorf("%s: error = %+v, want null", where, b.Error)
		}
	}

	api, _ := fetchConfig(t, in)
	check("GET /api/config", api)
	ws, _ := waitWS(t, dialWS(t, in), 3*time.Second, "any config", anyConfig)
	check("websocket", ws)
}

// S2: a hand edit, an editor-style replace and a delete + recreate each reach
// a connected client within ~2 s, with no restart; deleting the file keeps the
// last good fleets and sets the error.
func TestConfigS2_LiveReload(t *testing.T) {
	home := newHome(t)
	writeConfig(t, home, withVersion(`"one":{"name":"One"}`))
	in := startAdmiral(t, home, "127.0.0.1:0")
	c := dialWS(t, in)
	waitWS(t, c, 3*time.Second, "the initial config", fleetKeys("one"))

	writeConfig(t, home, withVersion(`"one":{"name":"One"},"two":{"name":"Two"}`))
	waitWS(t, c, 2*time.Second, "fleets one, two after an in-place edit", fleetKeys("one", "two"))

	replaceConfig(t, home, withVersion(`"one":{"name":"One"},"two":{"name":"Two"},"three":{"name":"Three"}`))
	waitWS(t, c, 2*time.Second, "fleets one, two, three after a rename-over save", fleetKeys("one", "two", "three"))

	// Deleting keeps the last good fleets and sets the error; recreating clears it.
	if err := os.Remove(configPath(home)); err != nil {
		t.Fatal(err)
	}
	b, _ := waitWS(t, c, 2*time.Second, "an error after the file was deleted", hasErr)
	if !strings.Contains(b.Error.File, "config.json") || !strings.Contains(strings.ToLower(b.Error.Message), "not found") {
		t.Errorf("error = %+v, want the file named and `not found` in the message", b.Error)
	}
	if !reflect.DeepEqual(b.keys(), []string{"one", "two", "three"}) {
		t.Errorf("fleets after delete = %v, want the last good [one two three]", b.keys())
	}
	writeConfig(t, home, withVersion(`"four":{"name":"Four"}`))
	waitWS(t, c, 2*time.Second, "fleet four and no error after recreate", func(b cfgBody) bool {
		return reflect.DeepEqual(b.keys(), []string{"four"}) && b.Error == nil
	})
	if b, _ := fetchConfig(t, in); !reflect.DeepEqual(b.keys(), []string{"four"}) || b.Error != nil {
		t.Fatalf("GET /api/config fleets = %v error %+v, want [four] and null", b.keys(), b.Error)
	}
}

// S2 (symlink): a config.json that is a symlink follows edits to its target,
// including a rename-over in the target's directory and the target being
// deleted and recreated.
func TestConfigS2_SymlinkedConfigFollowsTarget(t *testing.T) {
	home := newHome(t)
	if err := os.MkdirAll(home, 0o700); err != nil {
		t.Fatal(err)
	}
	dotfiles := t.TempDir()
	target := filepath.Join(dotfiles, "admiral.json")
	if err := os.WriteFile(target, []byte(withVersion(`"one":{"name":"One"}`)), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, configPath(home)); err != nil {
		t.Fatal(err)
	}
	in := startAdmiral(t, home, "127.0.0.1:0")
	waitAPI(t, in, "fleet one", func(b cfgBody) bool { return reflect.DeepEqual(b.keys(), []string{"one"}) })

	tmp := filepath.Join(dotfiles, "admiral.json.tmp")
	if err := os.WriteFile(tmp, []byte(withVersion(`"one":{"name":"One"},"two":{"name":"Two"}`)), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(tmp, target); err != nil {
		t.Fatal(err)
	}
	waitAPI(t, in, "fleets one, two after the symlink target was replaced", func(b cfgBody) bool {
		return reflect.DeepEqual(b.keys(), []string{"one", "two"})
	})

	if err := os.Remove(target); err != nil {
		t.Fatal(err)
	}
	b := waitAPI(t, in, "an error after the symlink target was deleted", hasErr)
	if !reflect.DeepEqual(b.keys(), []string{"one", "two"}) {
		t.Errorf("fleets after the target was deleted = %v, want the last good [one two]", b.keys())
	}
	if err := os.WriteFile(target, []byte(withVersion(`"three":{"name":"Three"}`)), 0o600); err != nil {
		t.Fatal(err)
	}
	waitAPI(t, in, "fleet three after the symlink target was recreated", func(b cfgBody) bool {
		return reflect.DeepEqual(b.keys(), []string{"three"}) && b.Error == nil
	})
}

// S3: an invalid edit keeps the last good fleets and names the file and the
// path of the bad value; fixing the file clears the error.
func TestConfigS3_InvalidEditKeepsLastGoodThenRecovers(t *testing.T) {
	home := newHome(t)
	good := withVersion(`"work":{"name":"Work","claude":{}}`)
	writeConfig(t, home, good)
	in := startAdmiral(t, home, "127.0.0.1:0")
	c := dialWS(t, in)
	waitWS(t, c, 3*time.Second, "the initial config", fleetKeys("work"))

	writeConfig(t, home, withVersion(`"work":{"name":"Work","claude":{"evn":{"A":"b"}}},"other":{"name":"Other"}`))
	b, _ := waitWS(t, c, 2*time.Second, "an error", hasErr)
	if !strings.Contains(b.Error.File, "config.json") {
		t.Errorf("error.file = %q, want it to name config.json", b.Error.File)
	}
	if !strings.Contains(b.Error.Message, "fleets.work.claude.evn") {
		t.Errorf("error.message = %q, want it to name fleets.work.claude.evn", b.Error.Message)
	}
	if got := b.keys(); !reflect.DeepEqual(got, []string{"work"}) {
		t.Errorf("fleets while invalid = %v, want the last good [work]", got)
	}
	api, _ := fetchConfig(t, in)
	if api.Error == nil || !reflect.DeepEqual(api.keys(), []string{"work"}) {
		t.Errorf("GET /api/config while invalid: fleets %v error %+v", api.keys(), api.Error)
	}

	writeConfig(t, home, withVersion(`"work":{"name":"Work","claude":{}},"other":{"name":"Other"}`))
	waitWS(t, c, 2*time.Second, "the error cleared and both fleets", func(b cfgBody) bool {
		return b.Error == nil && reflect.DeepEqual(b.keys(), []string{"work", "other"})
	})
}

// S4: at startup an invalid file means no fleets plus the error; no file means
// no fleets and no error. The server runs either way.
func TestConfigS4_StartupWithInvalidOrMissingFile(t *testing.T) {
	t.Run("invalid file", func(t *testing.T) {
		home := newHome(t)
		writeConfig(t, home, `{"version":1,"fleets":{"work":{"name":"Work","claud":{}}}}`)
		in := startAdmiral(t, home, "127.0.0.1:0")
		b, _ := fetchConfig(t, in)
		if len(b.Fleets) != 0 {
			t.Errorf("fleets = %v, want none", b.keys())
		}
		if b.Error == nil || !strings.Contains(b.Error.File, "config.json") || !strings.Contains(b.Error.Message, "fleets.work.claud") {
			t.Errorf("error = %+v, want file config.json and path fleets.work.claud", b.Error)
		}
	})
	t.Run("no file", func(t *testing.T) {
		in := startAdmiral(t, newHome(t), "127.0.0.1:0")
		b, raw := fetchConfig(t, in)
		if b.Fleets == nil || len(b.Fleets) != 0 {
			t.Errorf("fleets must be an empty array, got %s", raw)
		}
		if b.Error != nil {
			t.Errorf("error = %+v, want null (u12 adds the setup offer)", b.Error)
		}
	})
}

// S5 and the validation rules: each bad file is refused with an error
// that names the file and the offending path, while the last good config stays.
func TestConfigS5_InvalidFilesAreRefusedWithThePathNamed(t *testing.T) {
	home := newHome(t)
	good := withVersion(`"keep":{"name":"Keep"}`)
	writeConfig(t, home, good)
	in := startAdmiral(t, home, "127.0.0.1:0")

	cases := []struct{ name, content, want string }{
		{"newer version", `{"version":2,"fleets":{}}`, "2"},
		{"version missing", `{"fleets":{}}`, "version"},
		{"version not an integer", `{"version":1.5,"fleets":{}}`, "version"},
		{"version a string", `{"version":"1","fleets":{}}`, "version"},
		{"version below one", `{"version":0,"fleets":{}}`, "version"},
		{"not JSON", `{"version":1,`, ""},
		{"fleets not an object", `{"version":1,"fleets":[]}`, "fleets"},
		{"duplicate fleet key", `{"version":1,"fleets":{"work":{"name":"A"},"work":{"name":"B"}}}`, "work"},
		{"bad fleet key", `{"version":1,"fleets":{"my fleet":{"name":"A"}}}`, "my fleet"},
		{"unknown key in fleet", `{"version":1,"fleets":{"work":{"name":"A","colour":"#112233"}}}`, "fleets.work.colour"},
		{"unknown harness", `{"version":1,"fleets":{"work":{"name":"A","gemini":{}}}}`, "fleets.work.gemini"},
		{"name missing", `{"version":1,"fleets":{"work":{}}}`, "fleets.work.name"},
		{"name empty", `{"version":1,"fleets":{"work":{"name":""}}}`, "fleets.work.name"},
		{"name only spaces", `{"version":1,"fleets":{"work":{"name":"  "}}}`, "fleets.work.name"},
		{"bare ~ search root", `{"version":1,"fleets":{"work":{"name":"A","search_roots":["~"]}}}`, "fleets.work.search_roots"},
		{"bad color", `{"version":1,"fleets":{"work":{"name":"A","color":"red"}}}`, "fleets.work.color"},
		{"relative search root", `{"version":1,"fleets":{"work":{"name":"A","search_roots":["src/x"]}}}`, "fleets.work.search_roots"},
		{"unknown key in harness", `{"version":1,"fleets":{"work":{"name":"A","claude":{"evn":{}}}}}`, "fleets.work.claude.evn"},
		{"args not strings", `{"version":1,"fleets":{"work":{"name":"A","codex":{"args":[1]}}}}`, "fleets.work.codex.args"},
		{"env value not a string", `{"version":1,"fleets":{"work":{"name":"A","copilot":{"env":{"X":1}}}}}`, "fleets.work.copilot.env"},
		{"six favorites", `{"version":1,"fleets":{},"favorite_skills":["a","b","c","d","e","f"]}`, "favorite_skills"},
	}
	for _, tc := range cases {
		writeConfig(t, home, tc.content)
		b := waitAPI(t, in, "an error for "+tc.name, hasErr)
		if !strings.Contains(b.Error.File, "config.json") {
			t.Errorf("%s: error.file = %q, want it to name config.json", tc.name, b.Error.File)
		}
		if b.Error.Message == "" || !strings.Contains(b.Error.Message, tc.want) {
			t.Errorf("%s: error.message = %q, want it to contain %q", tc.name, b.Error.Message, tc.want)
		}
		if !reflect.DeepEqual(b.keys(), []string{"keep"}) {
			t.Errorf("%s: fleets = %v, want the last good [keep]", tc.name, b.keys())
		}
		writeConfig(t, home, good)
		waitAPI(t, in, "the error cleared", noErr)
	}
}

// Valid shapes that look odd: unknown top-level keys, no fleets, a fleet with
// no harness entries, a missing search_roots.
func TestConfigValidOddShapesLoad(t *testing.T) {
	home := newHome(t)
	writeConfig(t, home, `{"version":1,"future":{"x":1},"sources":{"s":{"plugin":"notinstalled"}},"fleets":{"bare":{"name":"Bare"}}}`)
	in := startAdmiral(t, home, "127.0.0.1:0")
	b, raw := fetchConfig(t, in)
	if b.Error != nil {
		t.Fatalf("unknown top-level keys and a source whose plugin is not installed must load: %+v", b.Error)
	}
	f := b.fleet("bare")
	if f.Key != "bare" || f.Docks == nil || len(f.Docks) != 0 {
		t.Errorf("a fleet with no harness entries has docks [], got %s", raw)
	}

	writeConfig(t, home, `{"version":1}`)
	b = waitAPI(t, in, "no fleets", func(b cfgBody) bool { return len(b.Fleets) == 0 })
	if b.Error != nil {
		t.Errorf("a file without fleets is valid, got error %+v", b.Error)
	}
}

// S6: executables are resolved, never run.
func TestConfigS6_ExecutableResolutionNeverRuns(t *testing.T) {
	userHome := t.TempDir()
	t.Setenv("HOME", userHome)
	marks := t.TempDir()
	probe := func(path, name string, mode os.FileMode) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		script := fmt.Sprintf("#!/bin/sh\ntouch %q\n", filepath.Join(marks, name))
		if err := os.WriteFile(path, []byte(script), mode); err != nil {
			t.Fatal(err)
		}
	}
	fleetBin := filepath.Join(t.TempDir(), "fleetbin")
	adminBin := filepath.Join(t.TempDir(), "adminbin")
	probe(filepath.Join(fleetBin, "onfleetpath"), "onfleetpath", 0o755)
	probe(filepath.Join(userHome, "bin", "x"), "tilde", 0o755)
	probe(filepath.Join(adminBin, "codex"), "default", 0o755)
	notExec := filepath.Join(t.TempDir(), "plain")
	probe(notExec, "plain", 0o644)
	t.Setenv("PATH", adminBin)

	home := newHome(t)
	writeConfig(t, home, withVersion(fmt.Sprintf(`
	  "missing":  {"name":"M","claude":{"executable":"/nonexistent/dir/claude"}},
	  "onpath":   {"name":"P","claude":{"executable":"onfleetpath","env":{"PATH":%q}}},
	  "notonpath":{"name":"N","claude":{"executable":"onfleetpath"}},
	  "tilde":    {"name":"T","claude":{"executable":"~/bin/x"}},
	  "plain":    {"name":"X","claude":{"executable":%q}},
	  "defaults": {"name":"D","codex":{},"copilot":{}}`, fleetBin, notExec)))
	in := startAdmiral(t, home, "127.0.0.1:0")
	b, _ := fetchConfig(t, in)
	if b.Error != nil {
		t.Fatalf("unexpected error: %+v", b.Error)
	}

	cases := []struct {
		fleet, harness string
		found          bool
		why            string
	}{
		{"missing", "claude", false, "absolute path that doesn't exist"},
		{"onpath", "claude", true, "bare name found on the fleet's env PATH"},
		{"notonpath", "claude", false, "bare name only on another fleet's PATH, not Admiral's"},
		{"tilde", "claude", true, "~/bin/x under HOME"},
		{"plain", "claude", false, "file without the executable bit"},
		{"defaults", "codex", true, "default command name `codex` on Admiral's PATH"},
		{"defaults", "copilot", false, "default command name `copilot` on neither"},
	}
	for _, tc := range cases {
		d, ok := b.fleet(tc.fleet).dock(tc.harness)
		if !ok {
			t.Errorf("%s/%s: no dock", tc.fleet, tc.harness)
			continue
		}
		if d.ExecutableFound != tc.found {
			t.Errorf("%s/%s (%s): executable_found = %v, want %v", tc.fleet, tc.harness, tc.why, d.ExecutableFound, tc.found)
		}
	}

	if entries, _ := os.ReadDir(marks); len(entries) != 0 {
		var names []string
		for _, e := range entries {
			names = append(names, e.Name())
		}
		t.Fatalf("resolving executables must never run them, but these ran: %v", names)
	}
}

// S7: a shared identity directory is a warning on both docks, not an error.
func TestConfigS7_SharedIdentityWarnsOnBothDocks(t *testing.T) {
	type tc struct {
		name       string
		adminEnv   string // Admiral's own CLAUDE_CONFIG_DIR; empty means unset
		a, b       string // JSON for each fleet's claude entry
		wantShared bool
	}
	userHome := t.TempDir()
	cases := []tc{
		{"same dir written two ways", "",
			`{"env":{"CLAUDE_CONFIG_DIR":"~/x"}}`,
			fmt.Sprintf(`{"env":{"CLAUDE_CONFIG_DIR":%q}}`, userHome+"/y/../x/"), true},
		{"different dirs", "",
			`{"env":{"CLAUDE_CONFIG_DIR":"~/x"}}`,
			`{"env":{"CLAUDE_CONFIG_DIR":"~/z"}}`, false},
		{"both fall back to Admiral's environment", userHome + "/shared",
			`{}`, `{"env":{"CLAUDE_CONFIG_DIR":"~/shared"}}`, true},
		{"both fall back to the harness default", "",
			`{}`, `{}`, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Setenv("HOME", userHome)
			if c.adminEnv != "" {
				t.Setenv("CLAUDE_CONFIG_DIR", c.adminEnv)
			} else {
				t.Setenv("CLAUDE_CONFIG_DIR", "")
				os.Unsetenv("CLAUDE_CONFIG_DIR")
			}
			home := newHome(t)
			writeConfig(t, home, withVersion(fmt.Sprintf(
				`"work":{"name":"Work Fleet","claude":%s,"codex":{}},"home":{"name":"Home Fleet","claude":%s,"codex":{"env":{"CODEX_HOME":"~/other"}}}`, c.a, c.b)))
			in := startAdmiral(t, home, "127.0.0.1:0")
			b, raw := fetchConfig(t, in)
			if b.Error != nil {
				t.Fatalf("a shared identity loads anyway, got error %+v", b.Error)
			}
			for _, pair := range [][2]string{{"work", "home"}, {"home", "work"}} {
				d, ok := b.fleet(pair[0]).dock("claude")
				if !ok {
					t.Fatalf("no claude dock on %s: %s", pair[0], raw)
				}
				other := b.fleet(pair[1])
				named := false
				for _, w := range d.Warnings {
					if strings.Contains(w, other.Name) || strings.Contains(w, other.Key) {
						named = true
					}
				}
				if c.wantShared && !named {
					t.Errorf("%s claude dock warnings = %q, want one naming the other fleet (%s / %s)", pair[0], d.Warnings, other.Key, other.Name)
				}
				if !c.wantShared && len(d.Warnings) != 0 {
					t.Errorf("%s claude dock warnings = %q, want none", pair[0], d.Warnings)
				}
			}
			// Different harnesses never conflict: work's codex has no warning.
			if d, _ := b.fleet("work").dock("codex"); len(d.Warnings) != 0 {
				t.Errorf("codex docks use different homes, got warnings %q", d.Warnings)
			}
		})
	}
}

// S8: no search roots means the docks show but the fleet can't launch.
func TestConfigS8_NoSearchRootsShowsDocksButCannotLaunch(t *testing.T) {
	home := newHome(t)
	writeConfig(t, home, withVersion(`
	  "empty":   {"name":"Empty","search_roots":[],"claude":{},"codex":{}},
	  "missing": {"name":"Missing","claude":{}},
	  "rooted":  {"name":"Rooted","search_roots":["~/code","/srv/repos"],"claude":{}}`))
	in := startAdmiral(t, home, "127.0.0.1:0")
	b, _ := fetchConfig(t, in)
	if b.Error != nil {
		t.Fatalf("unexpected error: %+v", b.Error)
	}
	if f := b.fleet("empty"); f.CanLaunch || len(f.Docks) != 2 {
		t.Errorf("empty search_roots: can_launch %v docks %d, want false and 2", f.CanLaunch, len(f.Docks))
	}
	if f := b.fleet("missing"); f.CanLaunch || len(f.Docks) != 1 {
		t.Errorf("missing search_roots: can_launch %v docks %d, want false and 1", f.CanLaunch, len(f.Docks))
	}
	if f := b.fleet("rooted"); !f.CanLaunch {
		t.Errorf("a fleet with search roots can launch")
	}
}

// S10: env and args may hold secrets and never leave the server, and the
// shared-identity warning (a second fleet on the same identity directory)
// doesn't carry the directory either.
func TestConfigS10_EnvAndArgsNeverSent(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	home := newHome(t)
	writeConfig(t, home, withVersion(`"work":{"name":"Work","search_roots":["/tmp"],
	  "claude":{"args":["--api-key","arg-secret-123"],"env":{"CLAUDE_CONFIG_DIR":"~/identity-secret-789","API_TOKEN":"env-secret-456"}}},
	  "home":{"name":"Home","claude":{"env":{"CLAUDE_CONFIG_DIR":"~/identity-secret-789"}}}`))
	in := startAdmiral(t, home, "127.0.0.1:0")

	api, apiRaw := fetchConfig(t, in)
	if d, _ := api.fleet("work").dock("claude"); len(d.Warnings) == 0 {
		t.Fatalf("the shared identity must warn, or this test never reaches the warning path: %s", apiRaw)
	}
	_, ws := waitWS(t, dialWS(t, in), 3*time.Second, "any config", anyConfig)
	for where, body := range map[string]string{"GET /api/config": apiRaw, "websocket": ws} {
		for _, secret := range []string{"arg-secret-123", "env-secret-456", "identity-secret-789", "--api-key", "API_TOKEN"} {
			if strings.Contains(body, secret) {
				t.Errorf("%s leaks %q: %s", where, secret, body)
			}
		}
		if strings.Contains(body, `"env"`) || strings.Contains(body, `"args"`) {
			t.Errorf("%s must not carry env or args keys: %s", where, body)
		}
	}
}
