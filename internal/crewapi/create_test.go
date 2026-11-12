package crewapi_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jbuffin/admiral/internal/crew"
)

func (f *fixture) create(body map[string]any) (int, string) {
	f.t.Helper()
	return f.do("POST", "/api/fleets/work/crew/members", body)
}

func newMember(name string) map[string]any {
	b := memberBody("implementer", "Pick for provisioning chores.")
	b["name"] = name
	return b
}

func fieldOf(body string) string {
	var e struct {
		Field string `json:"field"`
	}
	_ = json.Unmarshal([]byte(body), &e)
	return e.Field
}

func TestCreateWritesAFleetMemberFileTheResolverAccepts(t *testing.T) {
	commodoreAt(t)
	f := newFixture(t)
	body := newMember("quartermaster")
	body["instructions"] = "Check the stores.\n\tThen the lamps."
	status, out := f.create(body)
	if status != 201 || !strings.Contains(out, `"name":"quartermaster"`) {
		t.Fatalf("%d %s, want 201 naming the member", status, out)
	}
	raw := readFile(t, filepath.Join(f.crewDir(), "quartermaster.md"))
	if _, ferr := crew.CheckFile([]byte(raw), "quartermaster"); ferr != nil {
		t.Errorf("CheckFile refused the written file: %v\n%s", ferr, raw)
	}
	m := f.listed()["quartermaster"]
	if m.Layer != "fleet" || m.PickMeWhen != "Pick for provisioning chores." || m.Instructions != "Check the stores.\n\tThen the lamps." || m.BasedOn != nil || m.Model != nil || m.Harness != nil {
		t.Errorf("listed = %+v, want the fleet member as sent, with the omitted keys absent", m)
	}
}

func TestCreateRefusesEachFailedCheckWith400NamingTheFieldAndWritesNothing(t *testing.T) {
	commodoreAt(t)
	twentyOne := make([]string, 21)
	for i := range twentyOne {
		twentyOne[i] = "skill-" + string(rune('a'+i))
	}
	cases := []struct {
		name  string
		over  map[string]any
		field string
	}{
		{"a name that is no member name", map[string]any{"name": "Bad Name"}, "name"},
		{"a path as the name", map[string]any{"name": "../escape"}, "name"},
		{"a custom role with no based-on", map[string]any{"role": "cartographer"}, "based-on"},
		{"a harness that is none", map[string]any{"harness": "gemini"}, "harness"},
		{"a skill the resolver would drop", map[string]any{"skills": []string{"not a skill"}}, "skills"},
		{"more than 20 skills", map[string]any{"skills": twentyOne}, "skills"},
		{"a line break in pick-me-when", map[string]any{"pick-me-when": "one\ntwo"}, "pick-me-when"},
		{"a zero-width space in pick-me-when", map[string]any{"pick-me-when": "Pick​ me."}, "pick-me-when"},
		{"a tag character in instructions", map[string]any{"instructions": "Work.\U000E0041"}, "instructions"},
		{"a bell character in instructions", map[string]any{"instructions": "Work.\a"}, "instructions"},
		{"a file over 65536 bytes", map[string]any{"instructions": strings.Repeat("x", 70000)}, "instructions"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newFixture(t)
			body := newMember("quartermaster")
			for k, v := range tc.over {
				body[k] = v
			}
			status, out := f.create(body)
			if status != 400 || fieldOf(out) != tc.field {
				t.Errorf("%d %s, want 400 with field %q", status, out, tc.field)
			}
			if exists(filepath.Join(f.home, "fleets")) {
				t.Error("a refused create wrote under fleets/")
			}
			if _, err := os.Lstat(filepath.Join(f.home, "escape.md")); err == nil {
				t.Error("the name reached a path")
			}
		})
	}
}

func TestCreateOfATakenNameIs409OnTheNameFieldAndKeepsTheFile(t *testing.T) {
	builtIn := commodoreAt(t)
	builtInMember(t, builtIn, "scout", "scribe", "Built-in scout.")
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("CODEX_HOME", filepath.Join(home, ".codex"))
	codexCommodore := filepath.Join(home, ".agents", "skills", "commodore")
	writeFile(t, filepath.Join(codexCommodore, "SKILL.md"), "---\nname: commodore\n---\n")
	builtInMember(t, filepath.Join(codexCommodore, "crew"), "lookout", "reviewer", "Codex built-in lookout.")
	f := newFixtureWith(t, `{"version":1,"fleets":{"work":{"name":"Work","claude":{},"codex":{}}}}`)
	f.fleetMember("bosun", "triager", "Fleet bosun.", "")
	if status, out := f.do("GET", "/api/fleets/work/crew?harness=codex", nil); status != 200 || !strings.Contains(out, `"name":"lookout"`) || !strings.Contains(out, `"layer":"built-in"`) {
		t.Fatalf("test setup: the codex dock's Commodore is not visible: %d %s", status, out)
	}
	bosun := readFile(t, filepath.Join(f.crewDir(), "bosun.md"))

	for _, name := range []string{"bosun", "scout", "lookout"} {
		if status, out := f.create(newMember(name)); status != 409 || fieldOf(out) != "name" {
			t.Errorf("%s: %d %s, want 409 with field name (a fleet member, a built-in, and a built-in of another dock)", name, status, out)
		}
	}
	if got := readFile(t, filepath.Join(f.crewDir(), "bosun.md")); got != bosun {
		t.Error("a refused create changed the existing member")
	}
	for _, name := range []string{"scout.md", "lookout.md"} {
		if exists(filepath.Join(f.crewDir(), name)) {
			t.Errorf("a create shadowed the built-in %s", name)
		}
	}

	if status, out := f.create(newMember("quartermaster")); status != 201 {
		t.Fatalf("first create: %d %s", status, out)
	}
	first := readFile(t, filepath.Join(f.crewDir(), "quartermaster.md"))
	second := newMember("quartermaster")
	second["pick-me-when"] = "Pick for something else."
	if status, out := f.create(second); status != 409 || fieldOf(out) != "name" {
		t.Errorf("second create: %d %s, want 409 with field name", status, out)
	}
	if got := readFile(t, filepath.Join(f.crewDir(), "quartermaster.md")); got != first {
		t.Error("a second create overwrote the first")
	}
}

func TestCreateRefusesALinkedCrewDirectoryOrMemberFileAndWritesNothingThroughIt(t *testing.T) {
	commodoreAt(t)
	f := newFixture(t)
	elsewhere := t.TempDir()
	if err := os.MkdirAll(filepath.Dir(f.crewDir()), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(elsewhere, f.crewDir()); err != nil {
		t.Fatal(err)
	}
	if status, out := f.create(newMember("quartermaster")); status != 409 {
		t.Errorf("linked crew dir: %d %s, want 409", status, out)
	}
	if entries, _ := os.ReadDir(elsewhere); len(entries) != 0 {
		t.Errorf("wrote through the link: %v", entries)
	}

	g := newFixture(t)
	target := filepath.Join(t.TempDir(), "target.md")
	writeFile(t, target, "keep")
	if err := os.MkdirAll(g.crewDir(), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, filepath.Join(g.crewDir(), "quartermaster.md")); err != nil {
		t.Fatal(err)
	}
	if status, out := g.create(newMember("quartermaster")); status != 409 {
		t.Errorf("linked member file: %d %s, want 409", status, out)
	}
	if readFile(t, target) != "keep" {
		t.Error("wrote through the member file's link")
	}
}
