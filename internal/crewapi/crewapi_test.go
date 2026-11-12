package crewapi_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jbuffin/admiral/internal/config"
	"github.com/jbuffin/admiral/internal/crewapi"
)

type notes struct {
	Fleet []string            `json:"fleet"`
	Repos map[string][]string `json:"repos"`
}

type member struct {
	Name         string   `json:"name"`
	Role         string   `json:"role"`
	BasedOn      *string  `json:"based-on"`
	Skills       []string `json:"skills"`
	Model        *string  `json:"model"`
	Harness      *string  `json:"harness"`
	PickMeWhen   string   `json:"pick-me-when"`
	Instructions string   `json:"instructions"`
	Layer        string   `json:"layer"`
	Customized   bool     `json:"customized"`
	Notes        notes    `json:"notes"`
}

type unclaimed struct {
	Member string `json:"member"`
	Notes  notes  `json:"notes"`
}

type crewResp struct {
	Harness   string            `json:"harness"`
	Members   []member          `json:"members"`
	Unclaimed []unclaimed       `json:"unclaimed"`
	Warnings  []json.RawMessage `json:"warnings"`
	Errors    []json.RawMessage `json:"errors"`
}

type fixture struct {
	t    *testing.T
	home string
	srv  *httptest.Server
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	return newFixtureWith(t, `{"version":1,"fleets":{"work":{"name":"Work","claude":{},"codex":{}}}}`)
}

func newFixtureWith(t *testing.T, cfg string) *fixture {
	t.Helper()
	root, _ := filepath.EvalSymlinks(t.TempDir())
	f := &fixture{t: t, home: filepath.Join(root, "home")}
	if err := os.MkdirAll(f.home, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(f.home, config.FileName), []byte(cfg), 0o600); err != nil {
		t.Fatal(err)
	}
	m, err := config.Open(f.home)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(m.Close)
	mux := http.NewServeMux()
	(&crewapi.Service{Home: f.home, Config: m}).Routes(mux)
	f.srv = httptest.NewServer(mux)
	t.Cleanup(f.srv.Close)
	return f
}

// commodoreAt installs a Commodore in the claude identity directory and returns its crew/ directory.
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

func (f *fixture) do(method, path string, body any) (int, string) {
	f.t.Helper()
	var r *bytes.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		r = bytes.NewReader(b)
	} else {
		r = bytes.NewReader(nil)
	}
	req, _ := http.NewRequest(method, f.srv.URL+path, r)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		f.t.Fatal(err)
	}
	defer resp.Body.Close()
	var buf bytes.Buffer
	_, _ = buf.ReadFrom(resp.Body)
	return resp.StatusCode, buf.String()
}

func (f *fixture) get(query string) (int, crewResp, string) {
	f.t.Helper()
	status, body := f.do("GET", "/api/fleets/work/crew"+query, nil)
	var out crewResp
	_ = json.Unmarshal([]byte(body), &out)
	return status, out, body
}

func (f *fixture) listed() map[string]member {
	f.t.Helper()
	status, got, body := f.get("")
	if status != 200 {
		f.t.Fatalf("GET crew: %d %s", status, body)
	}
	m := map[string]member{}
	for _, c := range got.Members {
		m[c.Name] = c
	}
	return m
}

func (f *fixture) crewDir() string { return filepath.Join(f.home, "fleets", "work", "crew") }
func (f *fixture) memDir() string  { return filepath.Join(f.crewDir(), "memory") }

func memberText(name, role, pick, extra string) string {
	return "---\nname: " + name + "\nrole: " + role + "\npick-me-when: " + pick + "\n" + extra + "---\n\nDo the work.\n"
}

func (f *fixture) fleetMember(name, role, pick, extra string) {
	f.t.Helper()
	writeFile(f.t, filepath.Join(f.crewDir(), name+".md"), memberText(name, role, pick, extra))
}

func builtInMember(t *testing.T, dir, name, role, pick string) string {
	t.Helper()
	text := memberText(name, role, pick, "")
	writeFile(t, filepath.Join(dir, name+".md"), text)
	return text
}

func writeFile(t *testing.T, path, text string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func (f *fixture) fleetNotes(name string, lines ...string) string {
	f.t.Helper()
	path := filepath.Join(f.memDir(), name, "fleet.md")
	writeFile(f.t, path, strings.Join(lines, "\n")+"\n")
	return path
}

func (f *fixture) repoNotes(name, slug string, lines ...string) string {
	f.t.Helper()
	path := filepath.Join(f.memDir(), name, "repos", filepath.FromSlash(slug)+".md")
	writeFile(f.t, path, strings.Join(lines, "\n")+"\n")
	return path
}

func note(i int) string { return fmt.Sprintf("2026-11-10 (run-a): fact %d", i) }

func lines(n int) []string {
	var out []string
	for i := 0; i < n; i++ {
		out = append(out, "- "+note(i))
	}
	return out
}

func (f *fixture) postNotes(body map[string]any) (int, string) {
	f.t.Helper()
	return f.do("POST", "/api/fleets/work/crew/notes", body)
}

func memberBody(role, pick string) map[string]any {
	return map[string]any{"role": role, "based-on": nil, "skills": []string{}, "model": nil, "harness": nil, "pick-me-when": pick, "instructions": "Do the work."}
}

func (f *fixture) put(name string, body map[string]any) (int, string) {
	f.t.Helper()
	return f.do("PUT", "/api/fleets/work/crew/members/"+name, body)
}

func exists(path string) bool { _, err := os.Lstat(path); return err == nil }

func TestGetListsBuiltInAndFleetMembersByLayerWithCustomized(t *testing.T) {
	builtIn := commodoreAt(t)
	f := newFixture(t)
	builtInMember(t, builtIn, "mate", "reviewer", "Built-in mate.")
	builtInMember(t, builtIn, "scout", "scribe", "Built-in scout.")
	f.fleetMember("scout", "scribe", "Customized scout.", "")
	f.fleetMember("bosun", "triager", "Fleet bosun.", "")

	status, got, body := f.get("")
	if status != 200 || len(got.Errors) != 0 || got.Harness != "claude" {
		t.Fatalf("%d %s, want 200, no errors and the first dock", status, body)
	}
	m := f.listed()
	if len(m) != 3 {
		t.Fatalf("members = %s, want mate, scout and bosun", body)
	}
	if c := m["mate"]; c.Layer != "built-in" || c.Customized {
		t.Errorf("mate = %+v, want built-in and not customized", c)
	}
	if c := m["scout"]; c.Layer != "fleet" || !c.Customized || c.PickMeWhen != "Customized scout." {
		t.Errorf("scout = %+v, want the fleet's customized member", c)
	}
	if c := m["bosun"]; c.Layer != "fleet" || c.Customized {
		t.Errorf("bosun = %+v, want a fleet member that is not customized", c)
	}
}

func TestGetShowsFleetNotesAndRepoNotesKeyedByOwnerRepo(t *testing.T) {
	commodoreAt(t)
	f := newFixture(t)
	f.fleetMember("bosun", "triager", "Fleet bosun.", "")
	f.fleetNotes("bosun", "- "+note(1))
	f.repoNotes("bosun", "jbuffin/admiral", "- "+note(2))

	got := f.listed()["bosun"].Notes
	if len(got.Fleet) != 1 || got.Fleet[0] != note(1) {
		t.Errorf("fleet notes = %v, want %q without the dash", got.Fleet, note(1))
	}
	if r := got.Repos["jbuffin/admiral"]; len(r) != 1 || r[0] != note(2) {
		t.Errorf("repo notes = %v, want jbuffin/admiral to hold %q", got.Repos, note(2))
	}
}

func TestGetShowsNoNotesForTheFixChecker(t *testing.T) {
	commodoreAt(t)
	f := newFixture(t)
	writeFile(t, filepath.Join(f.crewDir(), "checker.md"), "---\nname: checker\nrole: fix-checker\npick-me-when: Checks fixes.\n---\n")
	f.fleetNotes("checker", "- "+note(1))

	got := f.listed()["checker"].Notes
	if len(got.Fleet) != 0 || len(got.Repos) != 0 {
		t.Errorf("fix checker notes = %+v, want none", got)
	}
}

func TestGetListsUnclaimedMemoryOfAValidNameAndSkipsTheRest(t *testing.T) {
	commodoreAt(t)
	f := newFixture(t)
	f.fleetMember("bosun", "triager", "Fleet bosun.", "")
	f.fleetNotes("bosun", "- "+note(1))
	f.fleetNotes("old-name", "- "+note(2))
	f.repoNotes("old-name", "acme/api", "- "+note(3))
	writeFile(t, filepath.Join(f.memDir(), "empty-one", "fleet.md"), "")
	f.fleetNotes("Bad_Name", "- "+note(4))

	status, got, body := f.get("")
	if status != 200 {
		t.Fatalf("%d %s", status, body)
	}
	if len(got.Unclaimed) != 1 || got.Unclaimed[0].Member != "old-name" {
		t.Fatalf("unclaimed = %s, want old-name only", body)
	}
	u := got.Unclaimed[0].Notes
	if len(u.Fleet) != 1 || u.Fleet[0] != note(2) || len(u.Repos["acme/api"]) != 1 {
		t.Errorf("old-name notes = %+v, want its fleet note and its acme/api note", u)
	}
}

func TestGetWithAResolverErrorHasNoMembersAndListsTheErrors(t *testing.T) {
	commodoreAt(t)
	f := newFixture(t)
	f.fleetMember("bosun", "triager", "Fleet bosun.", "")
	writeFile(t, filepath.Join(f.crewDir(), "broken.md"), "---\nname: broken\n---\n")

	status, got, body := f.get("")
	if status != 200 || len(got.Members) != 0 || len(got.Errors) == 0 || !strings.Contains(body, `"members":[]`) {
		t.Errorf("%d %s, want 200, members [] and the errors", status, body)
	}
}

func TestGetRefusals(t *testing.T) {
	commodoreAt(t)
	f := newFixture(t)
	if status, _ := f.do("GET", "/api/fleets/ghost/crew", nil); status != 404 {
		t.Errorf("unknown fleet: %d, want 404", status)
	}
	if status, _, body := f.get("?harness=copilot"); status != 400 || !strings.Contains(body, `"error"`) {
		t.Errorf("unknown harness: %d %s, want 400 with an error", status, body)
	}
}

func TestGetCreatesNoDirectory(t *testing.T) {
	commodoreAt(t)
	f := newFixture(t)
	if status, _, body := f.get(""); status != 200 {
		t.Fatalf("%d %s", status, body)
	}
	if exists(filepath.Join(f.home, "fleets")) {
		t.Error("GET created the fleet's directories")
	}
}

func TestPutRewritesTheFleetMemberAndGetShowsIt(t *testing.T) {
	commodoreAt(t)
	f := newFixture(t)
	f.fleetMember("bosun", "triager", "Old pick.", "")

	body := memberBody("triager", "New pick.")
	body["model"] = "opus"
	body["harness"] = "codex"
	body["instructions"] = "Triage fast."
	if status, out := f.put("bosun", body); status != 200 {
		t.Fatalf("PUT: %d %s", status, out)
	}
	got := f.listed()["bosun"]
	if got.PickMeWhen != "New pick." || got.Instructions != "Triage fast." || got.Model == nil || *got.Model != "opus" || got.Harness == nil || *got.Harness != "codex" {
		t.Errorf("bosun = %+v, want the PUT's values", got)
	}
}

func TestPutPickMeWhenWithYAMLSpecialCharactersRoundTrips(t *testing.T) {
	commodoreAt(t)
	f := newFixture(t)
	f.fleetMember("bosun", "triager", "Old pick.", "")

	pick := `Use: when #1 "quoted" and 'single' - or - dash`
	if status, out := f.put("bosun", memberBody("triager", pick)); status != 200 {
		t.Fatalf("PUT: %d %s", status, out)
	}
	if got := f.listed()["bosun"].PickMeWhen; got != pick {
		t.Errorf("pick-me-when = %q, want %q", got, pick)
	}
}

func TestPutWithABadHarnessIs400NamingTheField(t *testing.T) {
	commodoreAt(t)
	f := newFixture(t)
	f.fleetMember("bosun", "triager", "Old pick.", "")
	before := readFile(t, filepath.Join(f.crewDir(), "bosun.md"))

	body := memberBody("triager", "New pick.")
	body["harness"] = "emacs"
	status, out := f.put("bosun", body)
	if status != 400 || !strings.Contains(out, `"field":"harness"`) {
		t.Errorf("%d %s, want 400 naming harness", status, out)
	}
	if after := readFile(t, filepath.Join(f.crewDir(), "bosun.md")); after != before {
		t.Errorf("file changed to %q", after)
	}
}

func TestPutCannotChangeACustomizedBuiltInsRole(t *testing.T) {
	builtIn := commodoreAt(t)
	f := newFixture(t)
	builtInMember(t, builtIn, "mate", "reviewer", "Built-in mate.")
	if status, out := f.do("POST", "/api/fleets/work/crew/members/mate/customize", nil); status != 200 {
		t.Fatalf("customize: %d %s", status, out)
	}
	path := filepath.Join(f.crewDir(), "mate.md")
	before := readFile(t, path)

	status, out := f.put("mate", memberBody("triager", "Now a triager."))
	if status != 400 || !strings.Contains(out, `"field":"role"`) {
		t.Errorf("%d %s, want 400 naming role", status, out)
	}
	if after := readFile(t, path); after != before {
		t.Errorf("file changed to %q", after)
	}
}

func TestPutCustomRoleWithoutBasedOnIs400(t *testing.T) {
	commodoreAt(t)
	f := newFixture(t)
	f.fleetMember("bosun", "triager", "Old pick.", "")

	status, out := f.put("bosun", memberBody("deckhand", "Custom."))
	if status != 400 || !strings.Contains(out, `"field":"based-on"`) {
		t.Errorf("%d %s, want 400 naming based-on", status, out)
	}
}

func TestPutRepairsAFleetFileThatFailsToParse(t *testing.T) {
	commodoreAt(t)
	f := newFixture(t)
	writeFile(t, filepath.Join(f.crewDir(), "broken.md"), "---\nname: broken\n---\n")
	if _, got, _ := f.get(""); len(got.Errors) == 0 {
		t.Fatal("the broken file should be an error to start with")
	}

	if status, out := f.put("broken", memberBody("triager", "Fixed.")); status != 200 {
		t.Fatalf("PUT: %d %s", status, out)
	}
	if got := f.listed()["broken"]; got.PickMeWhen != "Fixed." {
		t.Errorf("broken = %+v, want the repaired member", got)
	}
}

func TestPutRefusesABuiltInWithNoFleetFile(t *testing.T) {
	builtIn := commodoreAt(t)
	f := newFixture(t)
	builtInMember(t, builtIn, "mate", "reviewer", "Built-in mate.")

	status, out := f.put("mate", memberBody("reviewer", "Edited."))
	if status != 409 || !strings.Contains(out, "customize") {
		t.Errorf("%d %s, want 409 telling to customize first", status, out)
	}
	if exists(filepath.Join(f.crewDir(), "mate.md")) {
		t.Error("a fleet file was created")
	}
}

func TestPutRefusesAnUnknownName(t *testing.T) {
	commodoreAt(t)
	f := newFixture(t)
	if status, out := f.put("nobody", memberBody("triager", "Nope.")); status != 404 {
		t.Errorf("%d %s, want 404", status, out)
	}
}

func TestCustomizeCopiesTheBuiltInByteForByteAndKeepsItsNotes(t *testing.T) {
	builtIn := commodoreAt(t)
	f := newFixture(t)
	text := builtInMember(t, builtIn, "mate", "reviewer", "Built-in mate.")
	f.fleetNotes("mate", "- "+note(1))

	if status, out := f.do("POST", "/api/fleets/work/crew/members/mate/customize", nil); status != 200 && status != 201 {
		t.Fatalf("customize: %d %s", status, out)
	}
	if got := readFile(t, filepath.Join(f.crewDir(), "mate.md")); got != text {
		t.Errorf("fleet file = %q, want the built-in's bytes %q", got, text)
	}
	got := f.listed()["mate"]
	if got.Layer != "fleet" || !got.Customized || len(got.Notes.Fleet) != 1 {
		t.Errorf("mate = %+v, want fleet, customized and its note kept", got)
	}
}

func TestCustomizeTwiceIs409(t *testing.T) {
	builtIn := commodoreAt(t)
	f := newFixture(t)
	builtInMember(t, builtIn, "mate", "reviewer", "Built-in mate.")
	f.do("POST", "/api/fleets/work/crew/members/mate/customize", nil)

	if status, out := f.do("POST", "/api/fleets/work/crew/members/mate/customize", nil); status != 409 {
		t.Errorf("%d %s, want 409", status, out)
	}
}

func rename(f *fixture, name string, body map[string]any) (int, string) {
	f.t.Helper()
	return f.do("POST", "/api/fleets/work/crew/members/"+name+"/rename", body)
}

func TestRenameWithoutCarryLeavesTheNotesUnclaimed(t *testing.T) {
	commodoreAt(t)
	f := newFixture(t)
	f.fleetMember("bosun", "triager", "Fleet bosun.", "")
	f.fleetNotes("bosun", "- "+note(1))

	if status, out := rename(f, "bosun", map[string]any{"to": "mate-two", "carry_notes": false}); status != 200 {
		t.Fatalf("rename: %d %s", status, out)
	}
	_, got, body := f.get("")
	m := f.listed()
	if _, ok := m["bosun"]; ok || len(m["mate-two"].Notes.Fleet) != 0 {
		t.Errorf("members = %s, want mate-two with no notes and no bosun", body)
	}
	if len(got.Unclaimed) != 1 || got.Unclaimed[0].Member != "bosun" || len(got.Unclaimed[0].Notes.Fleet) != 1 {
		t.Errorf("unclaimed = %+v, want bosun's note", got.Unclaimed)
	}
}

func TestRenameWithCarryMovesTheNotes(t *testing.T) {
	commodoreAt(t)
	f := newFixture(t)
	f.fleetMember("bosun", "triager", "Fleet bosun.", "")
	f.fleetNotes("bosun", "- "+note(1))
	f.repoNotes("bosun", "acme/api", "- "+note(2))

	if status, out := rename(f, "bosun", map[string]any{"to": "mate-two", "carry_notes": true}); status != 200 {
		t.Fatalf("rename: %d %s", status, out)
	}
	_, got, body := f.get("")
	n := f.listed()["mate-two"].Notes
	if len(n.Fleet) != 1 || len(n.Repos["acme/api"]) != 1 || len(got.Unclaimed) != 0 {
		t.Errorf("%s, want the notes on mate-two and nothing unclaimed", body)
	}
}

func TestRenameToAListedNameIs409(t *testing.T) {
	builtIn := commodoreAt(t)
	f := newFixture(t)
	builtInMember(t, builtIn, "mate", "reviewer", "Built-in mate.")
	f.fleetMember("bosun", "triager", "Fleet bosun.", "")

	if status, out := rename(f, "bosun", map[string]any{"to": "mate"}); status != 409 {
		t.Errorf("%d %s, want 409", status, out)
	}
	if !exists(filepath.Join(f.crewDir(), "bosun.md")) {
		t.Error("bosun.md is gone")
	}
}

func TestRenameCarryIntoANameWithNotesIs409AndChangesNothing(t *testing.T) {
	commodoreAt(t)
	f := newFixture(t)
	f.fleetMember("bosun", "triager", "Fleet bosun.", "")
	mine := f.fleetNotes("bosun", "- "+note(1))
	theirs := f.fleetNotes("mate-two", "- "+note(2))

	if status, out := rename(f, "bosun", map[string]any{"to": "mate-two", "carry_notes": true}); status != 409 {
		t.Fatalf("%d %s, want 409", status, out)
	}
	if !exists(filepath.Join(f.crewDir(), "bosun.md")) || exists(filepath.Join(f.crewDir(), "mate-two.md")) {
		t.Error("the member file moved")
	}
	if readFile(t, mine) != "- "+note(1)+"\n" || readFile(t, theirs) != "- "+note(2)+"\n" {
		t.Error("a notes file changed")
	}
}

func TestDeletingACustomizedBuiltInFallsBackToTheBuiltInAndKeepsNotes(t *testing.T) {
	builtIn := commodoreAt(t)
	f := newFixture(t)
	builtInMember(t, builtIn, "mate", "reviewer", "Built-in mate.")
	f.do("POST", "/api/fleets/work/crew/members/mate/customize", nil)
	f.fleetNotes("mate", "- "+note(1))

	if status, out := f.do("DELETE", "/api/fleets/work/crew/members/mate", nil); status != 200 && status != 204 {
		t.Fatalf("delete: %d %s", status, out)
	}
	got := f.listed()["mate"]
	if got.Layer != "built-in" || got.Customized || len(got.Notes.Fleet) != 1 {
		t.Errorf("mate = %+v, want the built-in with its note kept", got)
	}
}

func TestNotesEditKeepsDateAndRun(t *testing.T) {
	commodoreAt(t)
	f := newFixture(t)
	f.fleetMember("bosun", "triager", "Fleet bosun.", "")
	path := f.fleetNotes("bosun", "- "+note(1))

	status, out := f.postNotes(map[string]any{"op": "edit", "member": "bosun", "scope": "fleet", "note": note(1), "text": "a better fact"})
	if status != 200 {
		t.Fatalf("%d %s", status, out)
	}
	if got := readFile(t, path); got != "- 2026-11-10 (run-a): a better fact\n" {
		t.Errorf("file = %q", got)
	}
	var resp notes
	_ = json.Unmarshal([]byte(out), &resp)
	if len(resp.Fleet) != 1 || resp.Fleet[0] != "2026-11-10 (run-a): a better fact" {
		t.Errorf("response = %s, want the member's notes after the change", out)
	}
}

func TestNotesEditOfAMissingNoteIs409(t *testing.T) {
	commodoreAt(t)
	f := newFixture(t)
	f.fleetMember("bosun", "triager", "Fleet bosun.", "")
	f.fleetNotes("bosun", "- "+note(1))

	status, out := f.postNotes(map[string]any{"op": "edit", "member": "bosun", "scope": "fleet", "note": note(9), "text": "x"})
	if status != 409 {
		t.Errorf("%d %s, want 409", status, out)
	}
}

func TestNotesEditKeepsMalformedLinesByteForByte(t *testing.T) {
	commodoreAt(t)
	f := newFixture(t)
	path := filepath.Join(f.memDir(), "bosun", "fleet.md")
	writeFile(t, path, "# heading\n- not a note\n\n- "+note(1)+"\nfree text  \n- 2026-02-30 (run-a): no such day\n")

	status, out := f.postNotes(map[string]any{"op": "edit", "member": "bosun", "scope": "fleet", "note": note(1), "text": "changed"})
	if status != 200 {
		t.Fatalf("%d %s", status, out)
	}
	want := "# heading\n- not a note\n\n- 2026-11-10 (run-a): changed\nfree text  \n- 2026-02-30 (run-a): no such day\n"
	if got := readFile(t, path); got != want {
		t.Errorf("file = %q, want %q", got, want)
	}
}

func TestNotesEditRefusesTextWithAHiddenCharacterOrANewline(t *testing.T) {
	commodoreAt(t)
	f := newFixture(t)
	path := f.fleetNotes("bosun", "- "+note(1))
	for _, text := range []string{"zero​width", "two\nlines"} {
		status, out := f.postNotes(map[string]any{"op": "edit", "member": "bosun", "scope": "fleet", "note": note(1), "text": text})
		if status != 400 {
			t.Errorf("text %q: %d %s, want 400", text, status, out)
		}
	}
	if readFile(t, path) != "- "+note(1)+"\n" {
		t.Error("the file changed")
	}
}

func TestNotesEditFindsACRLFLineAndKeepsItsCarriageReturn(t *testing.T) {
	commodoreAt(t)
	f := newFixture(t)
	path := filepath.Join(f.memDir(), "bosun", "fleet.md")
	writeFile(t, path, "- "+note(1)+"\r\n- "+note(2)+"\r\n")

	status, out := f.postNotes(map[string]any{"op": "edit", "member": "bosun", "scope": "fleet", "note": note(1), "text": "changed"})
	if status != 200 {
		t.Fatalf("%d %s", status, out)
	}
	if got, want := readFile(t, path), "- 2026-11-10 (run-a): changed\r\n- "+note(2)+"\r\n"; got != want {
		t.Errorf("file = %q, want %q", got, want)
	}
}

func TestNotesMoveFromFleetToRepoCreatesTheRepoFile(t *testing.T) {
	commodoreAt(t)
	f := newFixture(t)
	fleetPath := f.fleetNotes("bosun", "- "+note(1), "- "+note(2))

	status, out := f.postNotes(map[string]any{"op": "move", "member": "bosun", "scope": "fleet", "note": note(1), "to": map[string]any{"scope": "repo", "repo": "acme/api"}})
	if status != 200 {
		t.Fatalf("%d %s", status, out)
	}
	repoPath := filepath.Join(f.memDir(), "bosun", "repos", "acme", "api.md")
	if got := readFile(t, repoPath); got != "- "+note(1)+"\n" {
		t.Errorf("repo file = %q, want the note unchanged", got)
	}
	if got := readFile(t, fleetPath); got != "- "+note(2)+"\n" {
		t.Errorf("fleet file = %q, want the moved note gone", got)
	}
}

func TestNotesMoveIntoAFullScopeIs409AndChangesNothing(t *testing.T) {
	commodoreAt(t)
	f := newFixture(t)
	src := f.fleetNotes("bosun", "- "+note(100))
	dst := f.repoNotes("bosun", "acme/api", lines(30)...)

	status, out := f.postNotes(map[string]any{"op": "move", "member": "bosun", "scope": "fleet", "note": note(100), "to": map[string]any{"scope": "repo", "repo": "acme/api"}})
	if status != 409 {
		t.Errorf("%d %s, want 409", status, out)
	}
	if readFile(t, src) != "- "+note(100)+"\n" || readFile(t, dst) != strings.Join(lines(30), "\n")+"\n" {
		t.Error("a notes file changed")
	}
}

func TestNotesDeleteWorksOnAnUnclaimedName(t *testing.T) {
	commodoreAt(t)
	f := newFixture(t)
	path := f.fleetNotes("old-name", "- "+note(1), "- "+note(2))

	status, out := f.postNotes(map[string]any{"op": "delete", "member": "old-name", "scope": "fleet", "note": note(1)})
	if status != 200 {
		t.Fatalf("%d %s", status, out)
	}
	if got := readFile(t, path); got != "- "+note(2)+"\n" {
		t.Errorf("file = %q, want only the second note", got)
	}
}

func TestNotesRefuseAMemberOrRepoThatIsNotAValidPart(t *testing.T) {
	commodoreAt(t)
	f := newFixture(t)
	f.fleetNotes("bosun", "- "+note(1))
	outside := filepath.Join(f.crewDir(), "x", "fleet.md")
	for _, c := range []map[string]any{
		{"op": "delete", "member": "../x", "scope": "fleet", "note": note(1)},
		{"op": "delete", "member": "bosun", "scope": "repo", "repo": "acme/..", "note": note(1)},
	} {
		if status, out := f.postNotes(c); status != 400 {
			t.Errorf("%v: %d %s, want 400", c, status, out)
		}
	}
	if exists(outside) {
		t.Error("a file was written outside memory")
	}
}

func TestNotesRefuseAMemoryPathThroughALink(t *testing.T) {
	commodoreAt(t)
	f := newFixture(t)
	elsewhere := t.TempDir()
	outsideFile := filepath.Join(elsewhere, "fleet.md")
	writeFile(t, outsideFile, "- "+note(1)+"\n")
	if err := os.MkdirAll(f.memDir(), 0o755); err != nil {
		t.Fatal(err)
	}
	// A linked member directory pointing outside the crew directory.
	if err := os.Symlink(elsewhere, filepath.Join(f.memDir(), "out")); err != nil {
		t.Fatal(err)
	}
	// A linked notes file pointing at another member's file inside the crew directory.
	other := f.fleetNotes("other", "- "+note(2))
	writeFile(t, filepath.Join(f.memDir(), "inner", "keep"), "")
	if err := os.Symlink(other, filepath.Join(f.memDir(), "inner", "fleet.md")); err != nil {
		t.Fatal(err)
	}
	// A linked directory inside the crew directory.
	real := filepath.Join(f.memDir(), "real")
	f.fleetNotes("real", "- "+note(3))
	if err := os.Symlink(real, filepath.Join(f.memDir(), "alias")); err != nil {
		t.Fatal(err)
	}

	for _, c := range []struct{ member, op string }{{"out", "edit"}, {"inner", "edit"}, {"alias", "delete"}, {"out", "delete"}} {
		status, out := f.postNotes(map[string]any{"op": c.op, "member": c.member, "scope": "fleet", "note": note(1), "text": "changed"})
		if status < 400 || status >= 500 {
			t.Errorf("%s %s: %d %s, want a refusal", c.op, c.member, status, out)
		}
	}
	status, out := f.postNotes(map[string]any{"op": "edit", "member": "inner", "scope": "fleet", "note": note(2), "text": "changed"})
	if status < 400 || status >= 500 {
		t.Errorf("edit through a linked file: %d %s, want a refusal", status, out)
	}
	for path, want := range map[string]string{outsideFile: "- " + note(1) + "\n", other: "- " + note(2) + "\n", filepath.Join(real, "fleet.md"): "- " + note(3) + "\n"} {
		if got := readFile(t, path); got != want {
			t.Errorf("%s = %q, want it untouched", path, got)
		}
	}
}

func TestNotesMoveRefusesALinkedRepoDirectoryAndWritesNothing(t *testing.T) {
	commodoreAt(t)
	f := newFixture(t)
	src := f.fleetNotes("bosun", "- "+note(1))
	elsewhere := t.TempDir()
	if err := os.Symlink(elsewhere, filepath.Join(f.memDir(), "bosun", "repos")); err != nil {
		t.Fatal(err)
	}

	status, out := f.postNotes(map[string]any{"op": "move", "member": "bosun", "scope": "fleet", "note": note(1), "to": map[string]any{"scope": "repo", "repo": "acme/api"}})
	if status < 400 || status >= 500 {
		t.Errorf("%d %s, want a refusal", status, out)
	}
	if entries, _ := os.ReadDir(elsewhere); len(entries) != 0 {
		t.Errorf("the link's target got %d entries", len(entries))
	}
	if readFile(t, src) != "- "+note(1)+"\n" {
		t.Error("the source changed")
	}
}

func TestGetDefaultsToTheFleetsFirstDock(t *testing.T) {
	commodoreAt(t)
	f := newFixtureWith(t, `{"version":1,"fleets":{"work":{"name":"Work","codex":{}}}}`)
	if status, got, body := f.get(""); status != 200 || got.Harness != "codex" {
		t.Errorf("%d %s, want the codex dock", status, body)
	}
}

func TestNotesEditAcceptsExactly500BytesOfText(t *testing.T) {
	commodoreAt(t)
	f := newFixture(t)
	f.fleetNotes("bosun", "- "+note(1))
	if status, out := f.postNotes(map[string]any{"op": "edit", "member": "bosun", "scope": "fleet", "note": note(1), "text": strings.Repeat("a", 500)}); status != 200 {
		t.Errorf("%d %s, want 200", status, out)
	}
}

func TestNotesEditOfTextOver500BytesIs400(t *testing.T) {
	commodoreAt(t)
	f := newFixture(t)
	path := f.fleetNotes("bosun", "- "+note(1))
	if status, out := f.postNotes(map[string]any{"op": "edit", "member": "bosun", "scope": "fleet", "note": note(1), "text": strings.Repeat("a", 501)}); status != 400 {
		t.Errorf("%d %s, want 400", status, out)
	}
	if readFile(t, path) != "- "+note(1)+"\n" {
		t.Error("the file changed")
	}
}

func TestNotesMoveToTheSameScopeIs400(t *testing.T) {
	commodoreAt(t)
	f := newFixture(t)
	f.fleetNotes("bosun", "- "+note(1))
	for _, from := range []string{"fleet", "repo"} {
		f.repoNotes("bosun", "acme/api", "- "+note(1))
		body := map[string]any{"op": "move", "member": "bosun", "scope": from, "repo": "acme/api", "note": note(1), "to": map[string]any{"scope": from, "repo": "acme/api"}}
		if status, out := f.postNotes(body); status != 400 {
			t.Errorf("%s to itself: %d %s, want 400", from, status, out)
		}
	}
}

func TestCustomizeOfAnUnknownNameIs404(t *testing.T) {
	commodoreAt(t)
	f := newFixture(t)
	if status, out := f.do("POST", "/api/fleets/work/crew/members/nobody/customize", nil); status != 404 {
		t.Errorf("%d %s, want 404", status, out)
	}
}

func TestAppendNoteMakesTheCrewDirectoryAndFileWithPrivateModes(t *testing.T) {
	f := newFixture(t)
	if err := crewapi.AppendNote(f.home, "work", "bosun", "acme/api", "- "+note(1)); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(f.memDir(), "bosun", "repos", "acme", "api.md")
	if got := readFile(t, path); got != "- "+note(1)+"\n" {
		t.Errorf("file = %q", got)
	}
	for _, p := range []string{f.crewDir(), filepath.Join(f.memDir(), "bosun", "repos", "acme")} {
		if fi, _ := os.Stat(p); fi.Mode().Perm() != 0o700 {
			t.Errorf("%s mode = %v, want 0700", p, fi.Mode().Perm())
		}
	}
	if fi, _ := os.Stat(path); fi.Mode().Perm() != 0o600 {
		t.Errorf("file mode = %v, want 0600", fi.Mode().Perm())
	}
}

func TestAppendNoteToAFullScopeAnswersScopeFullAndChangesNothing(t *testing.T) {
	f := newFixture(t)
	path := f.fleetNotes("bosun", lines(30)...)
	before := readFile(t, path)
	if err := crewapi.AppendNote(f.home, "work", "bosun", "", "- "+note(99)); !errors.Is(err, crewapi.ErrScopeFull) {
		t.Errorf("err = %v, want ErrScopeFull", err)
	}
	if readFile(t, path) != before {
		t.Error("the file changed")
	}
}

func TestAppendNoteRefusesALinkAnywhereInThePathAndWritesNothing(t *testing.T) {
	for name, link := range map[string]func(f *fixture) string{
		"the crew directory": func(f *fixture) string { return f.crewDir() },
		"the memory root":    func(f *fixture) string { return f.memDir() },
		"the member's dir":   func(f *fixture) string { return filepath.Join(f.memDir(), "bosun") },
	} {
		t.Run(name, func(t *testing.T) {
			f := newFixture(t)
			elsewhere := t.TempDir()
			at := link(f)
			if err := os.MkdirAll(filepath.Dir(at), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(elsewhere, at); err != nil {
				t.Fatal(err)
			}
			if err := crewapi.AppendNote(f.home, "work", "bosun", "", "- "+note(1)); err == nil {
				t.Error("no error, want a refusal")
			}
			if entries, _ := os.ReadDir(elsewhere); len(entries) != 0 {
				t.Errorf("wrote through the link: %v", entries)
			}
		})
	}
}

func TestRenameCarryIntoANameWhoseNotesWereAllDeletedSucceeds(t *testing.T) {
	commodoreAt(t)
	f := newFixture(t)
	f.fleetNotes("old", "- "+note(1))
	if status, out := f.postNotes(map[string]any{"op": "delete", "member": "old", "scope": "fleet", "note": note(1)}); status != 200 {
		t.Fatalf("delete: %d %s", status, out)
	}
	f.fleetMember("bosun", "triager", "Fleet bosun.", "")
	f.fleetNotes("bosun", "- "+note(2))

	if status, out := rename(f, "bosun", map[string]any{"to": "old", "carry_notes": true}); status != 200 {
		t.Fatalf("rename: %d %s", status, out)
	}
	if n := f.listed()["old"].Notes; len(n.Fleet) != 1 || n.Fleet[0] != note(2) {
		t.Errorf("notes = %+v, want bosun's note", n)
	}
}

func TestNotesDeleteOfTheLastNoteLeavesNoFileAndNoEmptyScope(t *testing.T) {
	commodoreAt(t)
	f := newFixture(t)
	path := f.fleetNotes("old", "- "+note(1))
	f.postNotes(map[string]any{"op": "delete", "member": "old", "scope": "fleet", "note": note(1)})
	if exists(path) {
		t.Error("the empty file is still there")
	}
	if _, got, body := f.get(""); len(got.Unclaimed) != 0 {
		t.Errorf("%s, want nothing unclaimed", body)
	}
}

func TestRenameCarryFromAMemberWithNoNotesIntoANameWithNotesIs409(t *testing.T) {
	commodoreAt(t)
	f := newFixture(t)
	f.fleetMember("bosun", "triager", "Fleet bosun.", "")
	theirs := f.fleetNotes("old", "- "+note(2))
	if status, out := rename(f, "bosun", map[string]any{"to": "old", "carry_notes": true}); status != 409 {
		t.Fatalf("%d %s, want 409", status, out)
	}
	if !exists(filepath.Join(f.crewDir(), "bosun.md")) || readFile(t, theirs) != "- "+note(2)+"\n" {
		t.Error("something changed")
	}
}

func TestPutTrimsBlankLinesAroundInstructions(t *testing.T) {
	commodoreAt(t)
	f := newFixture(t)
	f.fleetMember("bosun", "triager", "Fleet bosun.", "")
	body := memberBody("triager", "New pick.")
	body["instructions"] = "\n\nTriage fast.\n\n"
	if status, out := f.put("bosun", body); status != 200 {
		t.Fatalf("%d %s", status, out)
	}
	if got := f.listed()["bosun"].Instructions; got != "Triage fast." {
		t.Errorf("instructions = %q", got)
	}
}

func TestPutOfOversizedInstructionsIs400NamingInstructions(t *testing.T) {
	commodoreAt(t)
	f := newFixture(t)
	f.fleetMember("bosun", "triager", "Fleet bosun.", "")
	body := memberBody("triager", "New pick.")
	body["instructions"] = strings.Repeat("a", 70000)
	status, out := f.put("bosun", body)
	if status != 400 || !strings.Contains(out, `"field":"instructions"`) {
		t.Errorf("%d %s, want 400 with field instructions", status, out)
	}
}

func TestMemberRoutesRefuseALinkedMemberFile(t *testing.T) {
	commodoreAt(t)
	f := newFixture(t)
	elsewhere := filepath.Join(t.TempDir(), "bosun.md")
	writeFile(t, elsewhere, memberText("bosun", "triager", "Outside.", ""))
	if err := os.MkdirAll(f.crewDir(), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(elsewhere, filepath.Join(f.crewDir(), "bosun.md")); err != nil {
		t.Fatal(err)
	}
	before := readFile(t, elsewhere)
	if status, out := f.put("bosun", memberBody("triager", "New pick.")); status != 409 {
		t.Errorf("put: %d %s, want 409", status, out)
	}
	if status, out := f.do("DELETE", "/api/fleets/work/crew/members/bosun", nil); status != 409 {
		t.Errorf("delete: %d %s, want 409", status, out)
	}
	if readFile(t, elsewhere) != before {
		t.Error("the link target changed")
	}
	if fi, err := os.Lstat(filepath.Join(f.crewDir(), "bosun.md")); err != nil || fi.Mode()&os.ModeSymlink == 0 {
		t.Error("the link is gone")
	}
}

func TestCustomizeRefusesALinkedCrewDirectory(t *testing.T) {
	builtIn := commodoreAt(t)
	builtInMember(t, builtIn, "mate", "reviewer", "Built-in mate.")
	f := newFixture(t)
	elsewhere := t.TempDir()
	if err := os.MkdirAll(filepath.Dir(f.crewDir()), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(elsewhere, f.crewDir()); err != nil {
		t.Fatal(err)
	}
	if status, out := f.do("POST", "/api/fleets/work/crew/members/mate/customize", nil); status != 409 {
		t.Errorf("%d %s, want 409", status, out)
	}
	if entries, _ := os.ReadDir(elsewhere); len(entries) != 0 {
		t.Errorf("wrote through the link: %v", entries)
	}
}

func TestGetSkipsDotfilesInTheMemoryRootWithoutAWarning(t *testing.T) {
	commodoreAt(t)
	f := newFixture(t)
	writeFile(t, filepath.Join(f.memDir(), ".DS_Store"), "x")
	if _, got, body := f.get(""); len(got.Warnings) != 0 {
		t.Errorf("%s, want no warning", body)
	}
}
