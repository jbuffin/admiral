package config_test

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/jbuffin/admiral/internal/config"
)

func openView(t *testing.T, cfg string) (*config.Manager, string) {
	t.Helper()
	dir := t.TempDir()
	if cfg != "" {
		writeFile(t, dir, cfg)
	}
	m, err := config.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(m.Close)
	return m, dir
}

func fleetOf(v config.View, key string) config.FleetView {
	for _, f := range v.Fleets {
		if f.Key == key {
			return f
		}
	}
	return config.FleetView{}
}

func dockOf(f config.FleetView, harness string) config.DockView {
	for _, d := range f.Docks {
		if d.Harness == harness {
			return d
		}
	}
	return config.DockView{}
}

func keysOf(v config.View) []string {
	k := []string{}
	for _, f := range v.Fleets {
		k = append(k, f.Key)
	}
	return k
}

func withFleets(fleets string) string { return `{"version":1,"fleets":{` + fleets + `}}` }

func TestViewKeepsFileOrderAndResolvesColorsAndDocks(t *testing.T) {
	m, _ := openView(t, `{
	  "version": 1,
	  "favorite_skills": ["commodore", "review"],
	  "fleets": {
	    "zeta": {"name": "Zeta Fleet", "color": "#112233", "codex": {}, "claude": {}, "copilot": {}},
	    "f1": {"name": "F1"}, "f2": {"name": "F2"}, "f3": {"name": "F3"},
	    "f4": {"name": "F4"}, "f5": {"name": "F5"}, "f6": {"name": "F6"},
	    "yellow": {"name": "Yellow", "color": "#FFFF00"}
	  }
	}`)
	v := m.View()
	if want := []string{"zeta", "f1", "f2", "f3", "f4", "f5", "f6", "yellow"}; !reflect.DeepEqual(keysOf(v), want) {
		t.Fatalf("fleets = %v, want file order %v", keysOf(v), want)
	}
	for _, w := range []struct{ key, color, ink string }{
		{"zeta", "#112233", "#FFFFFF"}, // an explicit color doesn't use up the sequence
		{"f1", "#3366E0", "#FFFFFF"},
		{"f2", "#F28C28", "#14304A"},
		{"f6", "#3366E0", "#FFFFFF"}, // cycles after the fifth
		{"yellow", "#FFFF00", "#14304A"},
	} {
		if f := fleetOf(v, w.key); f.Color != w.color || f.Ink != w.ink {
			t.Errorf("fleet %s: color %q ink %q, want %q ink %q", w.key, f.Color, f.Ink, w.color, w.ink)
		}
	}
	var order []string
	for _, d := range fleetOf(v, "zeta").Docks {
		order = append(order, d.Harness)
	}
	if want := []string{"claude", "copilot", "codex"}; !reflect.DeepEqual(order, want) {
		t.Errorf("docks = %v, want %v whatever the file order", order, want)
	}
	if !reflect.DeepEqual(v.FavoriteSkills, []string{"commodore", "review"}) {
		t.Errorf("favorite_skills = %v", v.FavoriteSkills)
	}
}

func TestInvalidEditKeepsTheLastGoodConfigThenRecovers(t *testing.T) {
	m, dir := openView(t, withFleets(`"work":{"name":"Work","claude":{}}`))
	waitView := func(what string, ok func(config.View) bool) config.View {
		t.Helper()
		deadline := time.Now().Add(5 * time.Second)
		for {
			if v := m.View(); ok(v) {
				return v
			}
			if time.Now().After(deadline) {
				t.Fatalf("the view never showed %s: %+v", what, m.View())
			}
			time.Sleep(20 * time.Millisecond)
		}
	}

	writeFile(t, dir, withFleets(`"work":{"name":"Work","claude":{"evn":{}}},"other":{"name":"Other"}`))
	v := waitView("an error", func(v config.View) bool { return v.Error != nil })
	if !strings.HasSuffix(v.Error.File, "config.json") || !strings.Contains(v.Error.Message, "fleets.work.claude.evn") {
		t.Errorf("error = %+v, want config.json and the path fleets.work.claude.evn", v.Error)
	}
	if !reflect.DeepEqual(keysOf(v), []string{"work"}) {
		t.Errorf("fleets while invalid = %v, want the last good [work]", keysOf(v))
	}

	writeFile(t, dir, withFleets(`"work":{"name":"Work"},"other":{"name":"Other"}`))
	waitView("the error cleared", func(v config.View) bool {
		return v.Error == nil && reflect.DeepEqual(keysOf(v), []string{"work", "other"})
	})
}

func TestStartupWithAnInvalidOrMissingFile(t *testing.T) {
	m, _ := openView(t, withFleets(`"work":{"name":"Work","claud":{}}`))
	v := m.View()
	if len(v.Fleets) != 0 || v.Error == nil || !strings.Contains(v.Error.Message, "fleets.work.claud") {
		t.Errorf("an invalid file at startup: fleets %v error %+v, want none and the path named", keysOf(v), v.Error)
	}

	m, _ = openView(t, "")
	raw, _ := json.Marshal(m.View())
	if !strings.Contains(string(raw), `"fleets":[]`) || !strings.Contains(string(raw), `"error":null`) {
		t.Errorf("no file: %s, want an empty fleets array and no error", raw)
	}
}

func TestParseNamesTheBadPath(t *testing.T) {
	for _, tc := range []struct{ name, content, want string }{
		{"not JSON", `{"version":1,`, ""},
		{"newer version", `{"version":2,"fleets":{}}`, "version"},
		{"version missing", `{"fleets":{}}`, "version"},
		{"version not an integer", `{"version":1.5,"fleets":{}}`, "version"},
		{"version below one", `{"version":0,"fleets":{}}`, "version"},
		{"fleets not an object", `{"version":1,"fleets":[]}`, "fleets"},
		{"duplicate fleet key", withFleets(`"work":{"name":"A"},"work":{"name":"B"}`), "work"},
		{"bad fleet key", withFleets(`"my fleet":{"name":"A"}`), "my fleet"},
		{"unknown key in fleet", withFleets(`"work":{"name":"A","colour":"#112233"}`), "fleets.work.colour"},
		{"unknown harness", withFleets(`"work":{"name":"A","gemini":{}}`), "fleets.work.gemini"},
		{"name missing", withFleets(`"work":{}`), "fleets.work.name"},
		{"name blank", withFleets(`"work":{"name":"  "}`), "fleets.work.name"},
		{"bad color", withFleets(`"work":{"name":"A","color":"red"}`), "fleets.work.color"},
		{"relative search root", withFleets(`"work":{"name":"A","search_roots":["src/x"]}`), "fleets.work.search_roots"},
		{"unknown key in harness", withFleets(`"work":{"name":"A","claude":{"evn":{}}}`), "fleets.work.claude.evn"},
		{"args not strings", withFleets(`"work":{"name":"A","codex":{"args":[1]}}`), "fleets.work.codex.args"},
		{"env value not a string", withFleets(`"work":{"name":"A","copilot":{"env":{"X":1}}}`), "fleets.work.copilot.env"},
		{"six favorites", `{"version":1,"fleets":{},"favorite_skills":["a","b","c","d","e","f"]}`, "favorite_skills"},
	} {
		_, err := config.Parse([]byte(tc.content))
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: Parse error = %v, want one naming %q", tc.name, err, tc.want)
		}
	}
}

func TestOddButValidShapesLoad(t *testing.T) {
	m, _ := openView(t, `{"version":1,"future":{"x":1},"sources":{"s":{"plugin":"notinstalled","fleet":"bare"}},"fleets":{"bare":{"name":"Bare"}}}`)
	v := m.View()
	if v.Error != nil {
		t.Fatalf("unknown top-level keys and a source whose plugin is not installed must load: %+v", v.Error)
	}
	if f := fleetOf(v, "bare"); f.Docks == nil || len(f.Docks) != 0 || f.CanLaunch {
		t.Errorf("a fleet with no harness or search roots: %+v, want docks [] and can_launch false", f)
	}
	if _, err := config.Parse([]byte(`{"version":1}`)); err != nil {
		t.Errorf("a file without fleets is valid, got %v", err)
	}
}

func TestExecutablesAreResolvedNeverRun(t *testing.T) {
	userHome := t.TempDir()
	t.Setenv("HOME", userHome)
	marks := t.TempDir()
	probe := func(path string, mode os.FileMode) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		script := fmt.Sprintf("#!/bin/sh\ntouch %q\n", filepath.Join(marks, filepath.Base(path)))
		if err := os.WriteFile(path, []byte(script), mode); err != nil {
			t.Fatal(err)
		}
	}
	fleetBin, adminBin := t.TempDir(), t.TempDir()
	probe(filepath.Join(fleetBin, "onfleetpath"), 0o755)
	probe(filepath.Join(userHome, "bin", "x"), 0o755)
	probe(filepath.Join(adminBin, "codex"), 0o755)
	plain := filepath.Join(t.TempDir(), "plain")
	probe(plain, 0o644)
	t.Setenv("PATH", adminBin)

	m, _ := openView(t, withFleets(fmt.Sprintf(`
	  "missing":  {"name":"M","claude":{"executable":"/nonexistent/dir/claude"}},
	  "onpath":   {"name":"P","claude":{"executable":"onfleetpath","env":{"PATH":%q}}},
	  "notonpath":{"name":"N","claude":{"executable":"onfleetpath"}},
	  "tilde":    {"name":"T","claude":{"executable":"~/bin/x"}},
	  "plain":    {"name":"X","claude":{"executable":%q}},
	  "defaults": {"name":"D","codex":{},"copilot":{}}`, fleetBin, plain)))
	v := m.View()
	for _, tc := range []struct {
		fleet, harness string
		found          bool
		why            string
	}{
		{"missing", "claude", false, "absolute path that doesn't exist"},
		{"onpath", "claude", true, "bare name on the fleet's PATH"},
		{"notonpath", "claude", false, "bare name only on another fleet's PATH"},
		{"tilde", "claude", true, "~/bin/x under HOME"},
		{"plain", "claude", false, "file without the executable bit"},
		{"defaults", "codex", true, "default name on Admiral's PATH"},
		{"defaults", "copilot", false, "default name on neither"},
	} {
		if got := dockOf(fleetOf(v, tc.fleet), tc.harness).ExecutableFound; got != tc.found {
			t.Errorf("%s/%s (%s): executable_found = %v, want %v", tc.fleet, tc.harness, tc.why, got, tc.found)
		}
	}
	if entries, _ := os.ReadDir(marks); len(entries) != 0 {
		t.Fatalf("resolving executables must never run them, but %d ran", len(entries))
	}
}

func TestSharedIdentityWarnsOnBothDocks(t *testing.T) {
	userHome := t.TempDir()
	for _, c := range []struct {
		name, adminEnv, a, b string
		shared               bool
	}{
		{"same dir written two ways", "", `{"env":{"CLAUDE_CONFIG_DIR":"~/x"}}`,
			fmt.Sprintf(`{"env":{"CLAUDE_CONFIG_DIR":%q}}`, userHome+"/y/../x/"), true},
		{"different dirs", "", `{"env":{"CLAUDE_CONFIG_DIR":"~/x"}}`, `{"env":{"CLAUDE_CONFIG_DIR":"~/z"}}`, false},
		{"one falls back to Admiral's environment", userHome + "/shared", `{}`, `{"env":{"CLAUDE_CONFIG_DIR":"~/shared"}}`, true},
		{"both use the harness default", "", `{}`, `{}`, true},
	} {
		t.Run(c.name, func(t *testing.T) {
			t.Setenv("HOME", userHome)
			t.Setenv("CLAUDE_CONFIG_DIR", c.adminEnv)
			m, _ := openView(t, withFleets(fmt.Sprintf(
				`"work":{"name":"Work Fleet","claude":%s,"codex":{}},"home":{"name":"Home Fleet","claude":%s,"codex":{"env":{"CODEX_HOME":"~/other"}}}`, c.a, c.b)))
			v := m.View()
			if v.Error != nil {
				t.Fatalf("a shared identity loads anyway, got %+v", v.Error)
			}
			for _, pair := range [][2]string{{"work", "Home Fleet"}, {"home", "Work Fleet"}} {
				w := dockOf(fleetOf(v, pair[0]), "claude").Warnings
				if c.shared && (len(w) != 1 || !strings.Contains(w[0], pair[1])) {
					t.Errorf("%s claude warnings = %q, want one naming %s", pair[0], w, pair[1])
				}
				if !c.shared && len(w) != 0 {
					t.Errorf("%s claude warnings = %q, want none", pair[0], w)
				}
			}
			if w := dockOf(fleetOf(v, "work"), "codex").Warnings; len(w) != 0 {
				t.Errorf("codex docks use different homes, got %q", w)
			}
		})
	}
}

func TestOnlyAFleetWithSearchRootsCanLaunch(t *testing.T) {
	m, _ := openView(t, withFleets(`
	  "empty":  {"name":"Empty","search_roots":[],"claude":{},"codex":{}},
	  "rooted": {"name":"Rooted","search_roots":["~/code"],"claude":{}}`))
	v := m.View()
	if f := fleetOf(v, "empty"); f.CanLaunch || len(f.Docks) != 2 {
		t.Errorf("empty search_roots: can_launch %v docks %d, want false and 2", f.CanLaunch, len(f.Docks))
	}
	if !fleetOf(v, "rooted").CanLaunch {
		t.Error("a fleet with search roots can launch")
	}
}

func TestViewNeverCarriesEnvOrArgs(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	m, _ := openView(t, withFleets(`"work":{"name":"Work","search_roots":["/tmp"],
	  "claude":{"args":["--api-key","arg-secret-123"],"env":{"CLAUDE_CONFIG_DIR":"~/identity-secret-789","API_TOKEN":"env-secret-456"}}},
	  "home":{"name":"Home","claude":{"env":{"CLAUDE_CONFIG_DIR":"~/identity-secret-789"}}}`))
	v := m.View()
	if len(dockOf(fleetOf(v, "work"), "claude").Warnings) == 0 {
		t.Fatal("the shared identity must warn, or this test never reaches the warning text")
	}
	raw, _ := json.Marshal(v)
	for _, secret := range []string{"arg-secret-123", "env-secret-456", "identity-secret-789", "--api-key", "API_TOKEN", `"env"`, `"args"`} {
		if strings.Contains(string(raw), secret) {
			t.Errorf("the view leaks %s: %s", secret, raw)
		}
	}
}
