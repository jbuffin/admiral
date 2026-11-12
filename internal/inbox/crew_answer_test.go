package inbox_test

import (
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jbuffin/admiral/internal/inbox"
)

func crewNote(id, member, scope, repo, text string, replaces any) map[string]any {
	var r any
	if repo != "" {
		r = repo
	}
	return map[string]any{"id": id, "member": member, "role": "implementer", "scope": scope, "repo": r, "note": text, "replaces": replaces}
}

func crewNotesItem(id, fleet, ship string, notes []map[string]any) map[string]any {
	return map[string]any{"version": 1, "id": id, "kind": "crew_notes", "created": "2026-11-10T10:00:00Z",
		"title":      "Crew notes from " + ship,
		"crew_notes": map[string]any{"fleet": fleet, "ship": ship, "run_date": "2026-11-10", "notes": notes}}
}

func crewMemberItem(id, member, definition string, notes []map[string]any) map[string]any {
	if notes == nil {
		notes = []map[string]any{}
	}
	return map[string]any{"version": 1, "id": id, "kind": "crew_member", "created": "2026-11-10T10:00:00Z",
		"title": "Keep ad hoc member " + member + "?",
		"crew_member": map[string]any{"fleet": "work", "ship": "Saltpaw", "run_date": "2026-11-10", "member": member, "role": member,
			"definition": definition, "notes": notes}}
}

type crewRig struct {
	t    *testing.T
	s    *inbox.Service
	home string
	srv  *httptest.Server
	recs func() []map[string]any
}

func newCrewRig(t *testing.T) *crewRig {
	t.Helper()
	s, home, srv := newInbox(t)
	s.Fleet = workFleet
	return &crewRig{t, s, home, srv, logOutcomes(t, s)}
}

func (r *crewRig) seed(item map[string]any) {
	r.t.Helper()
	writeItem(r.t, shipDir(r.t, r.home, inbox.AdmiralSession), item["id"].(string)+".json", item)
	r.s.Refresh()
}

func (r *crewRig) answerNotes(id string, body any) (int, map[string]any) {
	r.t.Helper()
	return r.postJSON("/api/inbox/_admiral/"+id+"/crew-notes", body)
}

func (r *crewRig) answerMember(id, decision string) (int, map[string]any) {
	r.t.Helper()
	return r.postJSON("/api/inbox/_admiral/"+id+"/crew-member", map[string]any{"decision": decision})
}

func (r *crewRig) postJSON(path string, body any) (int, map[string]any) {
	r.t.Helper()
	b, _ := json.Marshal(body)
	code, text := post(r.t, r.srv.URL+path, string(b))
	var out map[string]any
	_ = json.Unmarshal([]byte(text), &out)
	return code, out
}

func (r *crewRig) notesFile(member, repo string) string {
	if repo == "" {
		return filepath.Join(crewDirOf(r.home), "memory", member, "fleet.md")
	}
	return filepath.Join(crewDirOf(r.home), "memory", member, "repos", filepath.FromSlash(repo)+".md")
}

func (r *crewRig) fileLines(path string) []string {
	r.t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		r.t.Fatalf("%v", err)
	}
	return strings.Split(strings.TrimSuffix(string(b), "\n"), "\n")
}

func (r *crewRig) assertNoCrewWritten() {
	r.t.Helper()
	if _, err := os.Stat(filepath.Join(crewDirOf(r.home), "memory")); !os.IsNotExist(err) {
		r.t.Errorf("crew/memory was written: %v", err)
	}
}

func (r *crewRig) assertUnanswered(id string) {
	r.t.Helper()
	if _, err := os.Stat(filepath.Join(r.home, "inbox", inbox.AdmiralSession, id+".answer.json")); !os.IsNotExist(err) {
		r.t.Errorf("%s has an answer: %v", id, err)
	}
}

func fullScope(n int) []string {
	var out []string
	for i := 0; i < n; i++ {
		out = append(out, fmt.Sprintf("- 2026-10-%02d (run-%d): old fact %d", i%28+1, i, i))
	}
	return out
}

func (r *crewRig) fillScope(member, repo string, lines []string) string {
	r.t.Helper()
	p := r.notesFile(member, repo)
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		r.t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
		r.t.Fatal(err)
	}
	return p
}

func threeNotes() []map[string]any {
	return []map[string]any{
		crewNote("n1", "bosun", "fleet", "", "the build needs make", nil),
		crewNote("n2", "bosun", "repo", "acme/widgets", "tests need tmux", nil),
		crewNote("n3", "lookout", "fleet", "", "the lookout fact", nil),
	}
}

func TestTheNotesAnswerWritesKeptAndEditedNotesAndNothingForADrop(t *testing.T) {
	r := newCrewRig(t)
	r.seed(crewNotesItem("crew-notes-0123456789abcdef", "work", "Saltpaw", threeNotes()))
	code, body := r.answerNotes("crew-notes-0123456789abcdef", map[string]any{"notes": map[string]any{
		"n1": map[string]any{"decision": "keep"},
		"n2": map[string]any{"decision": "edit", "note": "tests need tmux and a Mac"},
		"n3": map[string]any{"decision": "drop"},
	}})
	if code != 200 || body["answered"] != "crew-notes-0123456789abcdef" {
		t.Fatalf("answer = %d %v", code, body)
	}
	if got := r.fileLines(r.notesFile("bosun", "")); len(got) != 1 || got[0] != "- 2026-11-10 (Saltpaw): the build needs make" {
		t.Errorf("bosun fleet notes = %q", got)
	}
	if got := r.fileLines(r.notesFile("bosun", "acme/widgets")); len(got) != 1 || got[0] != "- 2026-11-10 (Saltpaw): tests need tmux and a Mac" {
		t.Errorf("bosun repo notes = %q", got)
	}
	if _, err := os.Stat(r.notesFile("lookout", "")); !os.IsNotExist(err) {
		t.Errorf("a dropped note was written: %v", err)
	}

	a := answerOf(t, r.home, "crew-notes-0123456789abcdef")
	ns, _ := a["notes"].(map[string]any)
	if a["kind"] != "crew_notes" || a["version"] != float64(1) || a["answered"] == nil || len(ns) != 3 ||
		ns["n1"].(map[string]any)["decision"] != "keep" || ns["n3"].(map[string]any)["decision"] != "drop" {
		t.Errorf("answer file = %v", a)
	}
	if e := ns["n2"].(map[string]any); e["decision"] != "edit" || e["note"] != "tests need tmux and a Mac" {
		t.Errorf("answer file n2 = %v", e)
	}

	recs := r.recs()
	got := map[string]map[string]any{}
	for _, rec := range recs {
		if f, ok := rec["fields"].(map[string]any); ok && f["item"] == "crew-notes-0123456789abcdef" {
			got[f["note"].(string)] = rec
		}
	}
	for note, want := range map[string]string{"n1": "crew_note_kept", "n2": "crew_note_edited", "n3": "crew_note_dropped"} {
		rec := got[note]
		if rec == nil || rec["event"] != want {
			t.Errorf("record for %s = %v, want %s", note, rec, want)
			continue
		}
		f := rec["fields"].(map[string]any)
		if f["kind"] != "crew_notes" || f["member"] == nil || f["scope"] == nil {
			t.Errorf("record for %s = %v, want kind, member and scope", note, rec)
		}
	}
}

func TestAnEditMovesTheScopeAndClearsReplacesWithNull(t *testing.T) {
	r := newCrewRig(t)
	old := "2026-10-01 (old): the build needs nothing"
	r.fillScope("bosun", "", []string{"- " + old})
	r.seed(crewNotesItem("crew-notes-aaaaaaaaaaaaaaaa", "work", "Saltpaw", []map[string]any{
		crewNote("n1", "bosun", "fleet", "", "the build needs make", old),
		crewNote("n2", "bosun", "fleet", "", "the repo uses go", nil),
	}))
	code, body := r.answerNotes("crew-notes-aaaaaaaaaaaaaaaa", map[string]any{"notes": map[string]any{
		"n1": map[string]any{"decision": "edit", "replaces": nil},
		"n2": map[string]any{"decision": "edit", "scope": "repo", "repo": "acme/gadgets"},
	}})
	if code != 200 {
		t.Fatalf("answer = %d %v", code, body)
	}
	want := []string{"- " + old, "- 2026-11-10 (Saltpaw): the build needs make"}
	if got := r.fileLines(r.notesFile("bosun", "")); strings.Join(got, "|") != strings.Join(want, "|") {
		t.Errorf("fleet notes = %q, want the old line kept and the note appended, as replaces is null", got)
	}
	if got := r.fileLines(r.notesFile("bosun", "acme/gadgets")); len(got) != 1 || got[0] != "- 2026-11-10 (Saltpaw): the repo uses go" {
		t.Errorf("gadgets notes = %q, want the note moved to the repo scope", got)
	}
}

func TestTheNotesAnswerNeedsADecisionForEveryNoteAndNoOther(t *testing.T) {
	for name, notes := range map[string]map[string]any{
		"a missing decision":                     {"n1": map[string]any{"decision": "keep"}, "n2": map[string]any{"decision": "keep"}},
		"an unknown id":                          {"n1": map[string]any{"decision": "keep"}, "n2": map[string]any{"decision": "keep"}, "n3": map[string]any{"decision": "keep"}, "n9": map[string]any{"decision": "keep"}},
		"a bad decision":                         {"n1": map[string]any{"decision": "maybe"}, "n2": map[string]any{"decision": "keep"}, "n3": map[string]any{"decision": "keep"}},
		"an edit with a note that has a newline": {"n1": map[string]any{"decision": "edit", "note": "a\nb"}, "n2": map[string]any{"decision": "keep"}, "n3": map[string]any{"decision": "keep"}},
	} {
		t.Run(name, func(t *testing.T) {
			r := newCrewRig(t)
			r.seed(crewNotesItem("crew-notes-0123456789abcdef", "work", "Saltpaw", threeNotes()))
			if code, body := r.answerNotes("crew-notes-0123456789abcdef", map[string]any{"notes": notes}); code != 400 {
				t.Errorf("answer = %d %v, want 400", code, body)
			}
			r.assertNoCrewWritten()
			r.assertUnanswered("crew-notes-0123456789abcdef")
		})
	}
}

func TestANoteThatDoesNotFitRefusesTheWholeAnswerWith409NamingIt(t *testing.T) {
	r := newCrewRig(t)
	r.fillScope("bosun", "acme/widgets", fullScope(30))
	r.seed(crewNotesItem("crew-notes-0123456789abcdef", "work", "Saltpaw", threeNotes()))
	code, body := r.answerNotes("crew-notes-0123456789abcdef", map[string]any{"notes": map[string]any{
		"n1": map[string]any{"decision": "keep"},
		"n2": map[string]any{"decision": "keep"}, // the full scope, with no replaces
		"n3": map[string]any{"decision": "keep"},
	}})
	if code != 409 || body["note"] != "n2" || body["error"] == nil {
		t.Fatalf("answer = %d %v, want 409 with note n2", code, body)
	}
	if _, err := os.Stat(r.notesFile("bosun", "")); !os.IsNotExist(err) {
		t.Errorf("n1 was written although n2 did not fit: %v", err)
	}
	if _, err := os.Stat(r.notesFile("lookout", "")); !os.IsNotExist(err) {
		t.Errorf("n3 was written although n2 did not fit: %v", err)
	}
	if got := r.fileLines(r.notesFile("bosun", "acme/widgets")); len(got) != 30 {
		t.Errorf("the full scope holds %d lines, want 30", len(got))
	}
	r.assertUnanswered("crew-notes-0123456789abcdef")
}

func TestAReplacesThatMatchesALineReplacesItInPlaceEvenInAFullScope(t *testing.T) {
	r := newCrewRig(t)
	lines := fullScope(30)
	r.fillScope("bosun", "acme/widgets", lines)
	replaced := strings.TrimPrefix(lines[7], "- ")
	r.seed(crewNotesItem("crew-notes-0123456789abcdef", "work", "Saltpaw", []map[string]any{
		crewNote("n1", "bosun", "repo", "acme/widgets", "tests need tmux", replaced),
	}))
	if code, body := r.answerNotes("crew-notes-0123456789abcdef", map[string]any{"notes": map[string]any{"n1": map[string]any{"decision": "keep"}}}); code != 200 {
		t.Fatalf("answer = %d %v", code, body)
	}
	want := append([]string{}, lines...)
	want[7] = "- 2026-11-10 (Saltpaw): tests need tmux"
	if got := r.fileLines(r.notesFile("bosun", "acme/widgets")); strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("notes = %q, want line 8 replaced where it stood", got)
	}
}

func TestAReplacesThatMatchesNothingIs409AndWritesNothing(t *testing.T) {
	r := newCrewRig(t)
	path := r.fillScope("bosun", "", fullScope(2))
	before, _ := os.ReadFile(path)
	r.seed(crewNotesItem("crew-notes-0123456789abcdef", "work", "Saltpaw", []map[string]any{
		crewNote("n1", "bosun", "fleet", "", "the build needs make", "2026-10-01 (old): a line that is not there"),
	}))
	code, body := r.answerNotes("crew-notes-0123456789abcdef", map[string]any{"notes": map[string]any{"n1": map[string]any{"decision": "keep"}}})
	if code != 409 || body["note"] != "n1" {
		t.Fatalf("answer = %d %v, want 409 with note n1", code, body)
	}
	if after, _ := os.ReadFile(path); string(after) != string(before) {
		t.Error("the scope changed")
	}
	r.assertUnanswered("crew-notes-0123456789abcdef")
}

func TestTheSameFactAlreadyInTheScopeIsNotWrittenAgain(t *testing.T) {
	r := newCrewRig(t)
	r.fillScope("bosun", "", []string{"- 2026-09-01 (Earlier): the build needs make"})
	r.seed(crewNotesItem("crew-notes-0123456789abcdef", "work", "Saltpaw", []map[string]any{
		crewNote("n1", "bosun", "fleet", "", "the build needs make", nil),
	}))
	if code, body := r.answerNotes("crew-notes-0123456789abcdef", map[string]any{"notes": map[string]any{"n1": map[string]any{"decision": "keep"}}}); code != 200 {
		t.Fatalf("answer = %d %v", code, body)
	}
	if got := r.fileLines(r.notesFile("bosun", "")); len(got) != 1 {
		t.Errorf("notes = %q, want the one line it had", got)
	}
}

func TestACrewNotesItemIsAnsweredOnce(t *testing.T) {
	r := newCrewRig(t)
	r.seed(crewNotesItem("crew-notes-0123456789abcdef", "work", "Saltpaw", threeNotes()[:1]))
	body := map[string]any{"notes": map[string]any{"n1": map[string]any{"decision": "keep"}}}
	if code, b := r.answerNotes("crew-notes-0123456789abcdef", body); code != 200 {
		t.Fatalf("first answer = %d %v", code, b)
	}
	if code, _ := r.answerNotes("crew-notes-0123456789abcdef", body); code != 409 {
		t.Errorf("second answer = %d, want 409", code)
	}
}

func TestTheGenericAnswerRouteRefusesTheCrewKinds(t *testing.T) {
	r := newCrewRig(t)
	r.seed(crewNotesItem("crew-notes-0123456789abcdef", "work", "Saltpaw", threeNotes()))
	r.seed(crewMemberItem("crew-member-0123456789abcdef", "scout", adHocDefinition, nil))
	for id, route := range map[string]string{"crew-notes-0123456789abcdef": "crew-notes", "crew-member-0123456789abcdef": "crew-member"} {
		code, body := r.postJSON("/api/inbox/_admiral/"+id+"/answer", map[string]any{"answers": map[string]any{}})
		if code != 400 || !strings.Contains(fmt.Sprint(body["error"]), route) {
			t.Errorf("%s: answer = %d %v, want 400 naming %s", id, code, body, route)
		}
		r.assertUnanswered(id)
	}
}

func TestAnItemOfAFleetThatIsNoLongerConfiguredIsRefusedAndWritesNothing(t *testing.T) {
	r := newCrewRig(t)
	r.seed(crewNotesItem("crew-notes-0123456789abcdef", "gone", "Saltpaw", threeNotes()[:1]))
	code, body := r.answerNotes("crew-notes-0123456789abcdef", map[string]any{"notes": map[string]any{"n1": map[string]any{"decision": "keep"}}})
	if code < 400 {
		t.Errorf("answer = %d %v, want a refusal", code, body)
	}
	if _, err := os.Stat(filepath.Join(r.home, "fleets")); !os.IsNotExist(err) {
		t.Errorf("fleets/ was written: %v", err)
	}
	r.assertUnanswered("crew-notes-0123456789abcdef")
}

func (r *crewRig) adHocPath() string { return filepath.Join(crewDirOf(r.home), "ad-hoc", "scout.md") }

func (r *crewRig) seedMember(notes []map[string]any) {
	r.t.Helper()
	putAdHoc(r.t, r.home, "scout", adHocDefinition)
	r.seed(crewMemberItem("crew-member-0123456789abcdef", "scout", adHocDefinition, notes))
}

func TestKeepingAnAdHocMemberWritesItToTheFleetWithoutItsAdHocLinesAndMovesItsNotes(t *testing.T) {
	r := newCrewRig(t)
	r.seedMember([]map[string]any{crewNote("n1", "scout", "repo", "acme/widgets", "the map lives in docs", nil)})
	code, body := r.answerMember("crew-member-0123456789abcdef", "keep")
	if code != 200 || body["answered"] != "crew-member-0123456789abcdef" {
		t.Fatalf("answer = %d %v", code, body)
	}
	raw, err := os.ReadFile(filepath.Join(crewDirOf(r.home), "scout.md"))
	if err != nil {
		t.Fatal(err)
	}
	want := "---\nname: scout\nrole: scout\npick-me-when: a run needs a map of the repo\n---\n\nMap the repo.\n"
	if string(raw) != want {
		t.Errorf("scout.md = %q, want %q", raw, want)
	}
	if got := r.fileLines(r.notesFile("scout", "acme/widgets")); len(got) != 1 || got[0] != "- 2026-11-10 (Saltpaw): the map lives in docs" {
		t.Errorf("scout notes = %q", got)
	}
	if _, err := os.Lstat(r.adHocPath()); !os.IsNotExist(err) {
		t.Errorf("the ad hoc file is still there: %v", err)
	}
	a := answerOf(t, r.home, "crew-member-0123456789abcdef")
	if a["kind"] != "crew_member" || a["decision"] != "keep" || a["file_removed"] != true || a["version"] != float64(1) || a["answered"] == nil {
		t.Errorf("answer file = %v", a)
	}
	var rec map[string]any
	for _, x := range r.recs() {
		if x["event"] == "crew_member_kept" {
			rec = x
		}
	}
	if f, _ := rec["fields"].(map[string]any); f["item"] != "crew-member-0123456789abcdef" || f["kind"] != "crew_member" || f["member"] != "scout" {
		t.Errorf("crew_member_kept = %v", rec)
	}
}

func TestKeepingAnAdHocMemberIsRefusedWith409AndWritesNothingWhenItCannotBeKeptAsSeen(t *testing.T) {
	for name, c := range map[string]struct {
		setup func(r *crewRig)
		notes []map[string]any
	}{
		"a fleet member of that name exists": {func(r *crewRig) {
			if err := os.WriteFile(filepath.Join(crewDirOf(r.home), "scout.md"), []byte("---\nname: scout\nrole: scout\npick-me-when: x\n---\n\nHand made.\n"), 0o600); err != nil {
				r.t.Fatal(err)
			}
		}, nil},
		"the ad hoc file changed": {func(r *crewRig) {
			if err := os.WriteFile(r.adHocPath(), []byte(adHocDefinition+"One more line.\n"), 0o600); err != nil {
				r.t.Fatal(err)
			}
		}, nil},
		"the ad hoc file is gone": {func(r *crewRig) {
			if err := os.Remove(r.adHocPath()); err != nil {
				r.t.Fatal(err)
			}
		}, nil},
		"a note does not fit its scope": {func(r *crewRig) {
			r.fillScope("scout", "", fullScope(30))
		}, []map[string]any{crewNote("n1", "scout", "fleet", "", "the map lives in docs", nil)}},
	} {
		t.Run(name, func(t *testing.T) {
			r := newCrewRig(t)
			r.seedMember(c.notes)
			c.setup(r)
			code, body := r.answerMember("crew-member-0123456789abcdef", "keep")
			if code != 409 {
				t.Fatalf("answer = %d %v, want 409", code, body)
			}
			r.assertUnanswered("crew-member-0123456789abcdef")
			if name == "a fleet member of that name exists" {
				if b, _ := os.ReadFile(filepath.Join(crewDirOf(r.home), "scout.md")); !strings.Contains(string(b), "Hand made.") {
					t.Error("the fleet member was overwritten")
				}
			} else if _, err := os.Stat(filepath.Join(crewDirOf(r.home), "scout.md")); !os.IsNotExist(err) {
				t.Errorf("scout.md was written: %v", err)
			}
			if name == "the ad hoc file is gone" {
				return
			}
			if _, err := os.Lstat(r.adHocPath()); err != nil {
				t.Errorf("the ad hoc file is gone: %v", err)
			}
		})
	}
}

func TestDroppingAnAdHocMemberRemovesTheUnchangedFileAndWritesNoNotes(t *testing.T) {
	r := newCrewRig(t)
	r.seedMember([]map[string]any{crewNote("n1", "scout", "fleet", "", "the map lives in docs", nil)})
	code, body := r.answerMember("crew-member-0123456789abcdef", "drop")
	if code != 200 || body["file_removed"] != true {
		t.Fatalf("answer = %d %v, want 200 and file_removed", code, body)
	}
	if _, err := os.Lstat(r.adHocPath()); !os.IsNotExist(err) {
		t.Errorf("the ad hoc file is still there: %v", err)
	}
	r.assertNoCrewWritten()
	if _, err := os.Stat(filepath.Join(crewDirOf(r.home), "scout.md")); !os.IsNotExist(err) {
		t.Errorf("scout.md was written: %v", err)
	}
	if a := answerOf(t, r.home, "crew-member-0123456789abcdef"); a["decision"] != "drop" || a["file_removed"] != true {
		t.Errorf("answer file = %v", a)
	}
	var found bool
	for _, x := range r.recs() {
		found = found || x["event"] == "crew_member_dropped"
	}
	if !found {
		t.Error("no crew_member_dropped record")
	}
}

func TestDroppingAnAdHocMemberLeavesAChangedFile(t *testing.T) {
	r := newCrewRig(t)
	r.seedMember(nil)
	changed := adHocDefinition + "The engineer edited this.\n"
	if err := os.WriteFile(r.adHocPath(), []byte(changed), 0o600); err != nil {
		t.Fatal(err)
	}
	code, body := r.answerMember("crew-member-0123456789abcdef", "drop")
	if code != 200 || body["file_removed"] != false {
		t.Fatalf("answer = %d %v, want 200 and file_removed false", code, body)
	}
	if b, _ := os.ReadFile(r.adHocPath()); string(b) != changed {
		t.Error("the changed file was touched")
	}
}

func TestRemovingACrewItemFromTheInboxCountsAsDroppingIt(t *testing.T) {
	r := newCrewRig(t)
	r.seedMember([]map[string]any{crewNote("n1", "scout", "fleet", "", "the map lives in docs", nil)})
	r.seed(crewNotesItem("crew-notes-0123456789abcdef", "work", "Saltpaw", threeNotes()))
	for _, id := range []string{"crew-member-0123456789abcdef", "crew-notes-0123456789abcdef"} {
		if code, body := r.postJSON("/api/inbox/_admiral/"+id+"/remove", map[string]any{}); code != 200 {
			t.Fatalf("remove %s = %d %v", id, code, body)
		}
	}
	if _, err := os.Lstat(r.adHocPath()); !os.IsNotExist(err) {
		t.Errorf("removing the member item left the ad hoc file: %v", err)
	}
	r.assertNoCrewWritten()
	seen := map[string]int{}
	for _, x := range r.recs() {
		seen[fmt.Sprint(x["event"])]++
	}
	if seen["crew_note_dropped"] != 3 || seen["crew_member_dropped"] != 1 || seen["item_removed"] != 0 {
		t.Errorf("records = %v, want 3 crew_note_dropped, 1 crew_member_dropped and no item_removed", seen)
	}
}
