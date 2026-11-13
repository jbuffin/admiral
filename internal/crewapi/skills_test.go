package crewapi_test

import (
	"encoding/json"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/jbuffin/admiral/internal/skillindex"
)

type skillRow struct {
	Name        string   `json:"name"`
	Description string   `json:"description"`
	Harnesses   []string `json:"harnesses"`
}

// skillHome is a fleet whose docks read only the skills a test puts under root.
type skillHome struct {
	root, home, claude, codex string
}

func newSkillHome(t *testing.T) skillHome {
	t.Helper()
	for _, k := range []string{"CLAUDE_CONFIG_DIR", "CODEX_HOME", "COPILOT_HOME", "CLAUDE_CODE_PLUGIN_CACHE_DIR"} {
		t.Setenv(k, "")
	}
	root, _ := filepath.EvalSymlinks(t.TempDir())
	return skillHome{root: root, home: filepath.Join(root, "userhome"), claude: filepath.Join(root, "claude"), codex: filepath.Join(root, "codex")}
}

func (s skillHome) config(docks ...string) string {
	env := map[string]map[string]string{
		"claude": {"HOME": s.home, "CLAUDE_CONFIG_DIR": s.claude},
		"codex":  {"HOME": s.home, "CODEX_HOME": s.codex},
	}
	fleet := map[string]any{"name": "Work"}
	for _, d := range docks {
		fleet[d] = map[string]any{"env": env[d]}
	}
	b, _ := json.Marshal(map[string]any{"version": 1, "fleets": map[string]any{"work": fleet}})
	return string(b)
}

func skillMD(t *testing.T, base, name, description string) {
	t.Helper()
	text := "---\nname: x\n"
	if description != "" {
		text += "description: " + description + "\n"
	}
	writeFile(t, filepath.Join(base, name, "SKILL.md"), text+"---\n")
}

// claudePlugin installs and enables a plugin whose skills sit under its skills/ folder.
func (s skillHome) claudePlugin(t *testing.T, plugin string, skills map[string]string) {
	t.Helper()
	path := filepath.Join(s.root, "plugins", plugin)
	writeFile(t, filepath.Join(path, ".claude-plugin", "plugin.json"), `{"name":"`+plugin+`"}`)
	for name, desc := range skills {
		skillMD(t, filepath.Join(path, "skills"), name, desc)
	}
	key := plugin + "@market"
	installed, _ := json.Marshal(map[string]any{"version": 2, "plugins": map[string]any{key: []map[string]any{{"scope": "user", "installPath": path}}}})
	enabled, _ := json.Marshal(map[string]any{"enabledPlugins": map[string]bool{key: true}})
	writeFile(t, filepath.Join(s.claude, "plugins", "installed_plugins.json"), string(installed))
	writeFile(t, filepath.Join(s.claude, "settings.json"), string(enabled))
}

func (f *fixture) skills(query string) (int, []skillRow, string) {
	f.t.Helper()
	status, body := f.do("GET", "/api/fleets/work/crew/skills"+query, nil)
	var out struct {
		Skills []skillRow `json:"skills"`
	}
	_ = json.Unmarshal([]byte(body), &out)
	return status, out.Skills, body
}

func TestSkillsListsEveryDockMergedWithDescriptionsAndHarnesses(t *testing.T) {
	s := newSkillHome(t)
	s.claudePlugin(t, "mattpocock-skills", map[string]string{"tdd": "Test first."})
	skillMD(t, filepath.Join(s.claude, "skills"), "claude-only", "Only here.")
	skillMD(t, filepath.Join(s.home, ".agents", "skills"), "tdd", "")
	skillMD(t, filepath.Join(s.codex, "skills"), "codex-only", "Codex has this.")
	if got := skillindex.List("claude", []string{"HOME=" + s.home, "CLAUDE_CONFIG_DIR=" + s.claude}, ""); len(got) != 2 {
		t.Fatalf("the fixture's plugin is not readable: %+v", got)
	}
	f := newFixtureWith(t, s.config("claude", "codex"))

	status, got, body := f.skills("")
	if status != 200 {
		t.Fatalf("status %d: %s", status, body)
	}
	want := []skillRow{
		{"claude-only", "Only here.", []string{"claude"}},
		{"codex-only", "Codex has this.", []string{"codex"}},
		{"tdd", "Test first.", []string{"claude", "codex"}},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %+v\nwant %+v", got, want)
	}
}

func TestSkillsWithAHarnessListsThatDocksSkillsOnly(t *testing.T) {
	s := newSkillHome(t)
	skillMD(t, filepath.Join(s.claude, "skills"), "claude-only", "")
	skillMD(t, filepath.Join(s.codex, "skills"), "codex-only", "Codex has this.")
	f := newFixtureWith(t, s.config("claude", "codex"))

	status, got, body := f.skills("?harness=codex")
	if status != 200 {
		t.Fatalf("status %d: %s", status, body)
	}
	if want := []skillRow{{"codex-only", "Codex has this.", []string{"codex"}}}; !reflect.DeepEqual(got, want) {
		t.Errorf("got %+v, want %+v", got, want)
	}
}

func TestSkillsNeverReadsACheckoutsSkills(t *testing.T) {
	s := newSkillHome(t)
	skillMD(t, filepath.Join(s.claude, "skills"), "user-skill", "")
	cwd := filepath.Join(s.root, "checkout")
	skillMD(t, filepath.Join(cwd, ".claude", "skills"), "repo-only", "")
	skillMD(t, filepath.Join(cwd, ".agents", "skills"), "repo-only", "")
	t.Chdir(cwd)
	f := newFixtureWith(t, s.config("claude", "codex"))

	for _, q := range []string{"", "?harness=claude", "?harness=codex"} {
		_, got, body := f.skills(q)
		for _, row := range got {
			if row.Name == "repo-only" {
				t.Errorf("GET skills%s lists a repo skill: %s", q, body)
			}
		}
	}
	if _, got, _ := f.skills("?harness=claude"); len(got) != 1 || got[0].Name != "user-skill" {
		t.Errorf("harness=claude lists %+v, want user-skill", got)
	}
}

func TestSkillsLeavesOutNamesASavedMemberWouldRefuse(t *testing.T) {
	s := newSkillHome(t)
	skillMD(t, filepath.Join(s.claude, "skills"), "good-one", "")
	skillMD(t, filepath.Join(s.claude, "skills"), "_private", "")
	skillMD(t, filepath.Join(s.claude, "skills"), "-dash", "")
	f := newFixtureWith(t, s.config("claude"))

	_, got, body := f.skills("")
	if len(got) != 1 || got[0].Name != "good-one" {
		t.Errorf("got %s, want only good-one", body)
	}
}

func TestSkillsRefusesWhatItCannotAnswer(t *testing.T) {
	s := newSkillHome(t)
	f := newFixtureWith(t, s.config("claude", "codex"))

	if status, body := f.do("GET", "/api/fleets/nope/crew/skills", nil); status != 404 || !jsonHas(body, "error", "fleet nope not found") {
		t.Errorf("unknown fleet: %d %s", status, body)
	}
	if status, body := f.do("GET", "/api/fleets/work/crew/skills?harness=copilot", nil); status != 400 || !jsonHas(body, "error", "fleet Work has no copilot dock") {
		t.Errorf("a harness the fleet has no dock for: %d %s", status, body)
	}
}

func TestSkillsOfAFleetWithNoDocksIsAnEmptyList(t *testing.T) {
	s := newSkillHome(t)
	f := newFixtureWith(t, s.config())

	status, body := f.do("GET", "/api/fleets/work/crew/skills", nil)
	if status != 200 || !reflect.DeepEqual(rawField(body, "skills"), []any{}) {
		t.Errorf("got %d %s, want 200 and \"skills\":[]", status, body)
	}
}

func jsonHas(body, key, want string) bool {
	var m map[string]string
	return json.Unmarshal([]byte(body), &m) == nil && m[key] == want
}

func rawField(body, key string) any {
	var m map[string]any
	_ = json.Unmarshal([]byte(body), &m)
	return m[key]
}
