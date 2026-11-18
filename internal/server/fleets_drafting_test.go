package server_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/jbuffin/admiral/internal/config"
	"github.com/jbuffin/admiral/internal/server"
)

type draftingRig struct {
	t                         *testing.T
	root, claude, codex, home string
	checkout                  string
	h                         http.Handler
	file                      string
}

func writeText(t *testing.T, path, text string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
}

// newDraftingRig is a fleet "work" with a claude and a codex dock, each reading only the skills the test seeds, and one checkout under its search root.
func newDraftingRig(t *testing.T, saved string) *draftingRig {
	t.Helper()
	for _, k := range []string{"CLAUDE_CONFIG_DIR", "CODEX_HOME", "COPILOT_HOME", "CLAUDE_CODE_PLUGIN_CACHE_DIR"} {
		t.Setenv(k, "")
	}
	root, _ := filepath.EvalSymlinks(t.TempDir())
	r := &draftingRig{t: t, root: root, claude: filepath.Join(root, "claude"), codex: filepath.Join(root, "codex"), home: filepath.Join(root, "userhome")}
	r.checkout = filepath.Join(root, "work", "repo")
	if err := os.MkdirAll(filepath.Join(r.checkout, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	fleet := map[string]any{
		"name":         "Work",
		"search_roots": []string{filepath.Join(root, "work")},
		"claude":       map[string]any{"env": map[string]string{"HOME": r.home, "CLAUDE_CONFIG_DIR": r.claude}},
		"codex":        map[string]any{"env": map[string]string{"HOME": r.home, "CODEX_HOME": r.codex}},
	}
	if saved != "" {
		fleet["drafting"] = map[string]string{"skill": saved}
	}
	raw, _ := json.Marshal(map[string]any{"version": 1, "fleets": map[string]any{"work": fleet}})
	cfgHome := filepath.Join(root, "admiral")
	if err := os.MkdirAll(cfgHome, 0o700); err != nil {
		t.Fatal(err)
	}
	r.file = filepath.Join(cfgHome, config.FileName)
	if err := os.WriteFile(r.file, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	m, err := config.Open(cfgHome)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(m.Close)
	r.h = (&server.Server{Config: m}).Handler()
	return r
}

func (r *draftingRig) skill(base, name string) {
	writeText(r.t, filepath.Join(base, name, "SKILL.md"), "---\nname: x\n---\n")
}

// claudePlugins installs and enables plugins, each holding the skills named, in the claude dock's identity.
func (r *draftingRig) claudePlugins(plugins map[string][]string) {
	installed, enabled := map[string]any{}, map[string]bool{}
	for p, skills := range plugins {
		path := filepath.Join(r.root, "plugins", p)
		writeText(r.t, filepath.Join(path, ".claude-plugin", "plugin.json"), `{"name":"`+p+`"}`)
		for _, s := range skills {
			r.skill(filepath.Join(path, "skills"), s)
		}
		installed[p+"@mk"] = []map[string]any{{"scope": "user", "installPath": path}}
		enabled[p+"@mk"] = true
	}
	i, _ := json.Marshal(map[string]any{"version": 2, "plugins": installed})
	e, _ := json.Marshal(map[string]any{"enabledPlugins": enabled})
	writeText(r.t, filepath.Join(r.claude, "plugins", "installed_plugins.json"), string(i))
	writeText(r.t, filepath.Join(r.claude, "settings.json"), string(e))
}

func (r *draftingRig) do(method, target string, body any) (int, string) {
	r.t.Helper()
	var rd *strings.Reader
	if body != nil {
		raw, _ := json.Marshal(body)
		rd = strings.NewReader(string(raw))
	} else {
		rd = strings.NewReader("")
	}
	rec := httptest.NewRecorder()
	r.h.ServeHTTP(rec, httptest.NewRequest(method, target, rd))
	return rec.Code, rec.Body.String()
}

type draftingAnswer struct {
	Skill     string `json:"skill"`
	Harnesses []struct {
		Harness string   `json:"harness"`
		Names   []string `json:"names"`
	} `json:"harnesses"`
}

func (r *draftingRig) get(query string) (int, draftingAnswer, string) {
	r.t.Helper()
	code, body := r.do(http.MethodGet, "/api/fleets/work/drafting"+query, nil)
	var a draftingAnswer
	_ = json.Unmarshal([]byte(body), &a)
	return code, a, body
}

func (a draftingAnswer) byDock() map[string][]string {
	m := map[string][]string{}
	for _, h := range a.Harnesses {
		m[h.Harness] = h.Names
	}
	return m
}

func (r *draftingRig) fileText() string {
	r.t.Helper()
	b, err := os.ReadFile(r.file)
	if err != nil {
		r.t.Fatal(err)
	}
	return string(b)
}

func TestDraftingGet_NamesTheDockThatCarriesTheSavedSkill(t *testing.T) {
	r := newDraftingRig(t, "wayfinder")
	r.skill(filepath.Join(r.claude, "skills"), "wayfinder")
	code, a, body := r.get("?dir=" + r.checkout)
	if code != http.StatusOK {
		t.Fatalf("GET = %d %s, want 200", code, body)
	}
	if a.Skill != "wayfinder" {
		t.Errorf("skill = %q, want wayfinder", a.Skill)
	}
	if want := map[string][]string{"claude": {"wayfinder"}}; !reflect.DeepEqual(a.byDock(), want) {
		t.Errorf("harnesses = %s, want only claude with wayfinder", body)
	}
}

func TestDraftingGet_ListsEveryDockThatCarriesItInDockOrder(t *testing.T) {
	r := newDraftingRig(t, "wayfinder")
	r.skill(filepath.Join(r.claude, "skills"), "wayfinder")
	r.skill(filepath.Join(r.codex, "skills"), "wayfinder")
	_, a, body := r.get("?dir=" + r.checkout)
	if len(a.Harnesses) != 2 || a.Harnesses[0].Harness != "claude" || a.Harnesses[1].Harness != "codex" {
		t.Errorf("harnesses = %s, want claude then codex", body)
	}
}

func TestDraftingGet_ListsBothNamesOfAnAmbiguousSkillOnOneDock(t *testing.T) {
	r := newDraftingRig(t, "wayfinder")
	r.claudePlugins(map[string][]string{"a": {"wayfinder"}, "b": {"wayfinder"}})
	_, a, body := r.get("?dir=" + r.checkout)
	got := a.byDock()["claude"]
	if len(got) != 2 || !((got[0] == "a:wayfinder" && got[1] == "b:wayfinder") || (got[0] == "b:wayfinder" && got[1] == "a:wayfinder")) {
		t.Errorf("claude names = %v (%s), want a:wayfinder and b:wayfinder", got, body)
	}
}

func TestDraftingGet_ReadsTheCheckoutsOwnSkills(t *testing.T) {
	r := newDraftingRig(t, "wayfinder")
	r.skill(filepath.Join(r.checkout, ".claude", "skills"), "wayfinder")
	_, with, _ := r.get("?dir=" + r.checkout)
	_, without, _ := r.get("")
	if got := with.byDock()["claude"]; len(got) != 1 || got[0] != "wayfinder" {
		t.Errorf("with dir, claude names = %v, want [wayfinder]", got)
	}
	if len(without.Harnesses) != 0 {
		t.Errorf("without dir, harnesses = %v, want user-level skills only (none)", without.Harnesses)
	}
}

func TestDraftingGet_ASkillInstalledNowhereKeepsTheSkillAndNoDocks(t *testing.T) {
	r := newDraftingRig(t, "wayfinder")
	code, a, body := r.get("?dir=" + r.checkout)
	if code != http.StatusOK || a.Skill != "wayfinder" || !strings.Contains(body, `"harnesses":[]`) {
		t.Errorf("GET = %d %s, want 200 with skill wayfinder and harnesses []", code, body)
	}
}

func TestDraftingGet_NothingSavedAnswersNoSkillAndNoDocks(t *testing.T) {
	r := newDraftingRig(t, "")
	r.skill(filepath.Join(r.claude, "skills"), "wayfinder")
	code, a, body := r.get("?dir=" + r.checkout)
	if code != http.StatusOK || a.Skill != "" || !strings.Contains(body, `"harnesses":[]`) {
		t.Errorf("GET = %d %s, want 200 with skill \"\" and harnesses []", code, body)
	}
}

func TestDraftingGet_ADirOutsideTheFleetIsRefused(t *testing.T) {
	r := newDraftingRig(t, "wayfinder")
	outside := filepath.Join(r.root, "elsewhere")
	if err := os.MkdirAll(outside, 0o755); err != nil {
		t.Fatal(err)
	}
	if code, body := r.do(http.MethodGet, "/api/fleets/work/drafting?dir="+outside, nil); code != http.StatusBadRequest {
		t.Errorf("GET = %d %s, want 400", code, body)
	}
}

func TestDraftingRoutes_UnknownFleetIs404(t *testing.T) {
	r := newDraftingRig(t, "")
	if code, body := r.do(http.MethodGet, "/api/fleets/nope/drafting", nil); code != http.StatusNotFound {
		t.Errorf("GET = %d %s, want 404", code, body)
	}
	if code, body := r.do(http.MethodPut, "/api/fleets/nope/drafting", map[string]string{"skill": "wayfinder"}); code != http.StatusNotFound {
		t.Errorf("PUT = %d %s, want 404", code, body)
	}
}

func TestDraftingPut_SavesTheSkillForALaterGetAndTheFile(t *testing.T) {
	r := newDraftingRig(t, "")
	r.skill(filepath.Join(r.claude, "skills"), "wayfinder")
	if code, body := r.do(http.MethodPut, "/api/fleets/work/drafting", map[string]string{"skill": "wayfinder"}); code != http.StatusOK {
		t.Fatalf("PUT = %d %s, want 200", code, body)
	}
	if _, a, body := r.get("?dir=" + r.checkout); a.Skill != "wayfinder" || len(a.Harnesses) != 1 {
		t.Errorf("GET after PUT = %s, want skill wayfinder on claude", body)
	}
	if !strings.Contains(r.fileText(), "wayfinder") {
		t.Errorf("config.json does not hold the skill:\n%s", r.fileText())
	}
}

func TestDraftingPut_AnEmptyNameClearsTheSkill(t *testing.T) {
	r := newDraftingRig(t, "wayfinder")
	if code, body := r.do(http.MethodPut, "/api/fleets/work/drafting", map[string]string{"skill": ""}); code != http.StatusOK {
		t.Fatalf("PUT = %d %s, want 200", code, body)
	}
	if _, a, _ := r.get(""); a.Skill != "" {
		t.Errorf("skill after clearing = %q, want none", a.Skill)
	}
	if strings.Contains(r.fileText(), "wayfinder") {
		t.Errorf("config.json still holds the skill:\n%s", r.fileText())
	}
}

func TestDraftingPut_ANamespacedNameIsKept(t *testing.T) {
	r := newDraftingRig(t, "")
	if code, body := r.do(http.MethodPut, "/api/fleets/work/drafting", map[string]string{"skill": "a:wayfinder"}); code != http.StatusOK {
		t.Fatalf("PUT = %d %s, want 200", code, body)
	}
	if _, a, _ := r.get(""); a.Skill != "a:wayfinder" {
		t.Errorf("skill = %q, want a:wayfinder", a.Skill)
	}
}

func TestDraftingPut_ANameThatIsNotASafeInvocationIsRefusedAndChangesNothing(t *testing.T) {
	r := newDraftingRig(t, "wayfinder")
	before := r.fileText()
	for _, bad := range []string{"has space", "tab\there", "a:b:c", ":x", "x:"} {
		if code, body := r.do(http.MethodPut, "/api/fleets/work/drafting", map[string]string{"skill": bad}); code != http.StatusBadRequest {
			t.Errorf("PUT %q = %d %s, want 400", bad, code, body)
		}
	}
	if r.fileText() != before {
		t.Error("a refused save changed config.json")
	}
}
