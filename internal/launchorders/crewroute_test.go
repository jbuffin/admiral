package launchorders_test

import (
	"encoding/json"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type crewMember struct {
	Name       string  `json:"name"`
	Role       string  `json:"role"`
	PickMeWhen string  `json:"pick-me-when"`
	Layer      string  `json:"layer"`
	Harness    *string `json:"harness"`
}

type crewResp struct {
	Members  []crewMember      `json:"members"`
	Warnings []json.RawMessage `json:"warnings"`
	Errors   []json.RawMessage `json:"errors"`
}

func writeMember(t *testing.T, dir, name, role, pick, extra string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	text := "---\nname: " + name + "\nrole: " + role + "\npick-me-when: " + pick + "\n" + extra + "---\n\nDo the work.\n"
	if err := os.WriteFile(filepath.Join(dir, name+".md"), []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
}

// commodoreAt installs a Commodore in the claude identity directory the route reads and returns its crew/ directory.
func commodoreAt(t *testing.T) string {
	t.Helper()
	identity := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", identity)
	dir := filepath.Join(identity, "skills", "commodore")
	if err := os.MkdirAll(filepath.Join(dir, "crew"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte("---\nname: commodore\n---\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return filepath.Join(dir, "crew")
}

func (f *fixture) crew(fleet, harness, dir string) (int, crewResp, string) {
	f.t.Helper()
	q := url.Values{"fleet": {fleet}, "harness": {harness}}
	if dir != "" {
		q.Set("dir", dir)
	}
	status, body := f.do("GET", "/api/launch-orders/crew?"+q.Encode(), nil)
	var out crewResp
	_ = json.Unmarshal([]byte(body), &out)
	return status, out, body
}

func (f *fixture) fleetCrew(fleet string) string {
	return filepath.Join(f.home, "fleets", fleet, "crew")
}

func byName(ms []crewMember) map[string]crewMember {
	m := map[string]crewMember{}
	for _, c := range ms {
		m[c.Name] = c
	}
	return m
}

func TestCrewMembersAcrossTheLayers(t *testing.T) {
	builtIn := commodoreAt(t)
	f := newFixture(t)
	repo := f.repo("api", "https://github.com/acme/api")
	writeMember(t, builtIn, "scout", "scribe", "Built-in scout.", "")
	writeMember(t, builtIn, "mate", "reviewer", "Built-in mate.", "")
	writeMember(t, f.fleetCrew("work"), "bosun", "triager", "Fleet bosun.", "harness: claude\n")
	// A repo member of a built-in's name and role replaces it.
	writeMember(t, filepath.Join(repo, ".commodore", "crew"), "scout", "scribe", "Repo scout.", "")

	status, got, body := f.crew("work", "claude", repo)
	if status != 200 || len(got.Errors) != 0 {
		t.Fatalf("%d %s, want 200 and no errors", status, body)
	}
	m := byName(got.Members)
	if len(m) != 3 {
		t.Fatalf("members = %s, want scout, mate and bosun once each", body)
	}
	if s := m["scout"]; s.Layer != "repo" || s.PickMeWhen != "Repo scout." || s.Role != "scribe" {
		t.Errorf("scout = %+v, want the repo's member to replace the built-in one", s)
	}
	if s := m["mate"]; s.Layer != "built-in" {
		t.Errorf("mate = %+v, want the built-in layer", s)
	}
	if s := m["bosun"]; s.Layer != "fleet" || s.Harness == nil || *s.Harness != "claude" {
		t.Errorf("bosun = %+v, want the fleet layer, locked to claude", s)
	}
}

func TestCrewMembersWithNoDirAreTheBuiltInAndFleetLayers(t *testing.T) {
	builtIn := commodoreAt(t)
	f := newFixture(t)
	repo := f.repo("api", "https://github.com/acme/api")
	writeMember(t, builtIn, "mate", "reviewer", "Built-in mate.", "")
	writeMember(t, f.fleetCrew("work"), "bosun", "triager", "Fleet bosun.", "")
	writeMember(t, filepath.Join(repo, ".commodore", "crew"), "lookout", "scout", "Repo lookout.", "")

	status, got, body := f.crew("work", "claude", "")
	if status != 200 {
		t.Fatalf("%d %s, want 200", status, body)
	}
	m := byName(got.Members)
	if _, ok := m["lookout"]; ok || len(m) != 2 || m["mate"].Layer != "built-in" || m["bosun"].Layer != "fleet" {
		t.Errorf("members = %s, want the built-in mate and the fleet's bosun only", body)
	}
}

func TestCrewMembersWithoutAnInstalledCommodoreHaveAnEmptyBuiltInLayer(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	f := newFixture(t)
	writeMember(t, f.fleetCrew("work"), "bosun", "triager", "Fleet bosun.", "")

	status, got, body := f.crew("work", "claude", "")
	if status != 200 || len(got.Members) != 1 || got.Members[0].Name != "bosun" || got.Members[0].Layer != "fleet" {
		t.Errorf("%d %s, want 200 and the fleet's bosun alone", status, body)
	}
}

func TestCrewMembersReportAFileTheResolverRejects(t *testing.T) {
	commodoreAt(t)
	f := newFixture(t)
	if err := os.MkdirAll(f.fleetCrew("work"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(f.fleetCrew("work"), "broken.md"), []byte("---\nname: broken\n---\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	status, got, body := f.crew("work", "claude", "")
	if status != 200 || len(got.Errors) == 0 || !strings.Contains(body, "role is required") {
		t.Errorf("%d %s, want 200 with the resolver's error", status, body)
	}
}

func TestCrewMembersRefusals(t *testing.T) {
	commodoreAt(t)
	f := newFixture(t)
	for _, c := range []struct {
		name, fleet, harness, dir string
		status                    int
	}{
		{"an unknown fleet", "ghost", "claude", "", 404},
		{"a fleet with no such dock", "home", "codex", "", 400},
		{"a directory that is not in the fleet", "work", "claude", t.TempDir(), 400},
	} {
		if status, _, body := f.crew(c.fleet, c.harness, c.dir); status != c.status || !strings.Contains(body, `"error"`) {
			t.Errorf("%s: %d %s, want %d with an error", c.name, status, body, c.status)
		}
	}
}
