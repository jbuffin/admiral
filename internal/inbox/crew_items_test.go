package inbox_test

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"testing"

	"github.com/jbuffin/admiral/internal/inbox"
)

const (
	adHocDefinition = "---\nname: scout\nrole: scout\nbased-on: implementer\npick-me-when: a run needs a map of the repo\nad-hoc: true\nad-hoc-run: \"run-a\"\n---\n\nMap the repo.\n"
	longNote        = "x"
)

func workFleet(key string) (inbox.FleetInfo, bool) {
	return inbox.FleetInfo{Key: "work", Name: "Work", Color: "#38bdf8"}, key == "work"
}

func crewDirOf(home string) string { return filepath.Join(home, "fleets", "work", "crew") }

func putAdHoc(t *testing.T, home, name, text string) string {
	t.Helper()
	p := filepath.Join(crewDirOf(home), "ad-hoc", name+".md")
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(text), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func crewRun(home string) inbox.CrewRun {
	return inbox.CrewRun{Home: home, FleetKey: "work", Fleets: workFleet, Ship: "Saltpaw", Session: "saltpaw",
		Launched: "2026-11-10T09:00:00Z", RunDate: "2026-11-10"}
}

func reportOf(extra map[string]any) []byte {
	r := map[string]any{"version": 2, "result": "done", "why": "ok"}
	for k, v := range extra {
		r[k] = v
	}
	b, _ := json.Marshal(r)
	return b
}

func noteOf(member, role, scope, repo, text string, replaces any, adHoc bool) map[string]any {
	var r any
	if repo != "" {
		r = repo
	}
	return map[string]any{"member": member, "role": role, "scope": scope, "repo": r, "note": text, "replaces": replaces, "ad-hoc": adHoc}
}

// crewItems reads the crew items written into Admiral's own inbox, by kind.
func crewItems(t *testing.T, home string) map[string][]map[string]any {
	t.Helper()
	files, _ := filepath.Glob(filepath.Join(home, "inbox", inbox.AdmiralSession, "crew-*.json"))
	out := map[string][]map[string]any{}
	for _, f := range files {
		if strings.HasSuffix(f, ".answer.json") {
			continue
		}
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		var it map[string]any
		if err := json.Unmarshal(b, &it); err != nil {
			t.Fatal(err)
		}
		out[it["kind"].(string)] = append(out[it["kind"].(string)], it)
	}
	return out
}

func notesIn(t *testing.T, it map[string]any, kind string) []map[string]any {
	t.Helper()
	obj, _ := it[kind].(map[string]any)
	var out []map[string]any
	for _, n := range obj["notes"].([]any) {
		out = append(out, n.(map[string]any))
	}
	return out
}

func texts(ns []map[string]any) []string {
	out := []string{}
	for _, n := range ns {
		out = append(out, n["note"].(string))
	}
	return out
}

func idHash(parts ...string) string {
	sum := sha256.Sum256([]byte(strings.Join(parts, "\x00")))
	return hex.EncodeToString(sum[:])[:16]
}

func TestAReportWithNotesAndAnAdHocMemberWritesOneItemOfEachKindAndNothingToTheCrew(t *testing.T) {
	home := t.TempDir()
	adhoc := putAdHoc(t, home, "scout", adHocDefinition)
	report := reportOf(map[string]any{
		"cast": []any{
			map[string]any{"member": "bosun", "role": "implementer", "ad_hoc": false},
			map[string]any{"member": "scout", "role": "scout", "ad_hoc": true},
		},
		"notes": []any{
			noteOf("bosun", "implementer", "fleet", "", "the build needs make", nil, false),
			noteOf("bosun", "implementer", "repo", "acme/widgets", "tests need tmux", "2026-10-01 (old): tests need nothing", false),
			noteOf("scout", "scout", "repo", "acme/widgets", "the map lives in docs", nil, true),
		},
	})
	if err := inbox.WriteCrewItems(crewRun(home), report); err != nil {
		t.Fatal(err)
	}
	items := crewItems(t, home)
	if len(items["crew_notes"]) != 1 || len(items["crew_member"]) != 1 {
		t.Fatalf("items = %v, want one crew_notes and one crew_member", items)
	}

	cn := items["crew_notes"][0]
	h := idHash("saltpaw", "2026-11-10T09:00:00Z")
	if cn["id"] != "crew-notes-"+h || cn["version"] != float64(1) || cn["title"] == "" || cn["created"] == nil {
		t.Errorf("crew_notes header = %v, want id crew-notes-%s, version 1, a title and created", cn, h)
	}
	obj := cn["crew_notes"].(map[string]any)
	if obj["fleet"] != "work" || obj["ship"] != "Saltpaw" || obj["run_date"] != "2026-11-10" {
		t.Errorf("crew_notes object = %v", obj)
	}
	ns := notesIn(t, cn, "crew_notes")
	if len(ns) != 2 {
		t.Fatalf("crew_notes holds %v, want the two notes of the member that isn't ad hoc", ns)
	}
	if ns[0]["member"] != "bosun" || ns[0]["role"] != "implementer" || ns[0]["scope"] != "fleet" || ns[0]["note"] != "the build needs make" || ns[0]["replaces"] != nil || ns[0]["id"] == "" {
		t.Errorf("first note = %v", ns[0])
	}
	if ns[1]["scope"] != "repo" || ns[1]["repo"] != "acme/widgets" || ns[1]["replaces"] != "2026-10-01 (old): tests need nothing" || ns[1]["id"] == ns[0]["id"] {
		t.Errorf("second note = %v", ns[1])
	}

	cm := items["crew_member"][0]
	hm := idHash("saltpaw", "2026-11-10T09:00:00Z", "scout")
	mo := cm["crew_member"].(map[string]any)
	if cm["id"] != "crew-member-"+hm || cm["title"] == "" || mo["fleet"] != "work" || mo["ship"] != "Saltpaw" || mo["run_date"] != "2026-11-10" ||
		mo["member"] != "scout" || mo["role"] != "scout" || mo["definition"] != adHocDefinition {
		t.Errorf("crew_member = %v, want id crew-member-%s and the definition as read", cm, hm)
	}
	if got := texts(notesIn(t, cm, "crew_member")); !reflect.DeepEqual(got, []string{"the map lives in docs"}) {
		t.Errorf("crew_member notes = %v, want the ad hoc note", got)
	}

	if _, err := os.Stat(filepath.Join(crewDirOf(home), "memory")); !os.IsNotExist(err) {
		t.Errorf("crew/memory exists before any answer: %v", err)
	}
	if b, _ := os.ReadFile(adhoc); string(b) != adHocDefinition {
		t.Error("the ad hoc file changed")
	}
}

func TestWritingTheSameRunAgainWritesNoDuplicates(t *testing.T) {
	home := t.TempDir()
	putAdHoc(t, home, "scout", adHocDefinition)
	report := reportOf(map[string]any{
		"cast":  []any{map[string]any{"member": "scout", "role": "scout", "ad_hoc": true}},
		"notes": []any{noteOf("bosun", "implementer", "fleet", "", "the build needs make", nil, false)},
	})
	for i := 0; i < 2; i++ {
		if err := inbox.WriteCrewItems(crewRun(home), report); err != nil {
			t.Fatal(err)
		}
	}
	files, _ := filepath.Glob(filepath.Join(home, "inbox", inbox.AdmiralSession, "*.json"))
	if len(files) != 2 {
		t.Errorf("files = %v, want the two items once", files)
	}
}

func TestAnAdHocMemberOfTheCastWithNoNotesStillGetsAnItem(t *testing.T) {
	home := t.TempDir()
	putAdHoc(t, home, "scout", adHocDefinition)
	report := reportOf(map[string]any{"cast": []any{map[string]any{"member": "scout", "role": "scout", "ad_hoc": true}}})
	if err := inbox.WriteCrewItems(crewRun(home), report); err != nil {
		t.Fatal(err)
	}
	items := crewItems(t, home)
	if len(items["crew_member"]) != 1 || len(items["crew_notes"]) != 0 {
		t.Fatalf("items = %v, want one crew_member and no crew_notes", items)
	}
	if got := notesIn(t, items["crew_member"][0], "crew_member"); len(got) != 0 {
		t.Errorf("notes = %v, want none", got)
	}
}

func TestBadNotesAreSkippedOneByOneAndTheRestAreKept(t *testing.T) {
	home := t.TempDir()
	good := func(i string) map[string]any {
		return noteOf("bosun", "implementer", "fleet", "", "good fact "+i, nil, false)
	}
	notes := []any{
		good("1"),
		noteOf("Bad Name", "implementer", "fleet", "", "bad member name", nil, false),
		noteOf("bosun", "implementer", "repo", "acme/..", "dotdot repo", nil, false),
		noteOf("bosun", "implementer", "repo", "../widgets", "dotdot owner", nil, false),
		noteOf("bosun", "implementer", "fleet", "", "two\nlines", nil, false),
		noteOf("bosun", "implementer", "fleet", "", "hidden‮character", nil, false),
		noteOf("bosun", "implementer", "fleet", "", "   ", nil, false),
		noteOf("bosun", "implementer", "fleet", "", strings.Repeat("x", 301), nil, false),
		noteOf("bosun", "implementer", "galaxy", "", "bad scope", nil, false),
		noteOf("bosun", "implementer", "fleet", "", "replaces a non-note", "just some text", false),
		noteOf("checker", "fix-checker", "fleet", "", "a fix checker has no memory", nil, false),
		good("2"),
		good("3"),
		good("4 is over the cap of three per member"),
		noteOf("lookout", "reviewer", "fleet", "", "lookout fact", nil, false),
	}
	if err := inbox.WriteCrewItems(crewRun(home), reportOf(map[string]any{"notes": notes})); err != nil {
		t.Fatal(err)
	}
	items := crewItems(t, home)
	if len(items["crew_notes"]) != 1 {
		t.Fatalf("items = %v", items)
	}
	want := []string{"good fact 1", "good fact 2", "good fact 3", "lookout fact"}
	if got := texts(notesIn(t, items["crew_notes"][0], "crew_notes")); !reflect.DeepEqual(got, want) {
		t.Errorf("notes = %v, want %v", got, want)
	}
}

func TestAFleetNoteDropsItsRepoAndAMaximumLengthNoteIsKept(t *testing.T) {
	home := t.TempDir()
	max := strings.Repeat("x", 300)
	report := reportOf(map[string]any{"notes": []any{
		noteOf("bosun", "implementer", "fleet", "acme/widgets", max, nil, false),
	}})
	if err := inbox.WriteCrewItems(crewRun(home), report); err != nil {
		t.Fatal(err)
	}
	ns := notesIn(t, crewItems(t, home)["crew_notes"][0], "crew_notes")
	if len(ns) != 1 || ns[0]["note"] != max {
		t.Fatalf("notes = %v, want the 300-character note", ns)
	}
	if repo, _ := ns[0]["repo"].(string); repo != "" {
		t.Errorf("repo = %v, want it dropped on a fleet note", ns[0]["repo"])
	}
}

func TestNoItemIsWrittenWhenThereIsNothingToProposeOrNoFleetToProposeTo(t *testing.T) {
	note := []any{noteOf("bosun", "implementer", "fleet", "", "the build needs make", nil, false)}
	for name, c := range map[string]struct {
		run    func(inbox.CrewRun) inbox.CrewRun
		report []byte
	}{
		"no fleet":           {func(r inbox.CrewRun) inbox.CrewRun { r.FleetKey = ""; return r }, reportOf(map[string]any{"notes": note})},
		"unconfigured fleet": {func(r inbox.CrewRun) inbox.CrewRun { r.FleetKey = "gone"; return r }, reportOf(map[string]any{"notes": note})},
		"a key that climbs":  {func(r inbox.CrewRun) inbox.CrewRun { r.FleetKey = "../work"; return r }, reportOf(map[string]any{"notes": note})},
		"version 1":          {func(r inbox.CrewRun) inbox.CrewRun { return r }, []byte(`{"version":1,"result":"done","why":"ok","notes":[{"member":"bosun","role":"implementer","scope":"fleet","repo":null,"note":"the build needs make","replaces":null,"ad-hoc":false}]}`)},
		"no notes":           {func(r inbox.CrewRun) inbox.CrewRun { return r }, reportOf(nil)},
		"only fix-checker":   {func(r inbox.CrewRun) inbox.CrewRun { return r }, reportOf(map[string]any{"notes": []any{noteOf("checker", "fix-checker", "fleet", "", "x", nil, false)}})},
		"not JSON":           {func(r inbox.CrewRun) inbox.CrewRun { return r }, []byte(`{`)},
	} {
		t.Run(name, func(t *testing.T) {
			home := t.TempDir()
			_ = inbox.WriteCrewItems(c.run(crewRun(home)), c.report)
			if items := crewItems(t, home); len(items) != 0 {
				t.Errorf("items = %v, want none", items)
			}
		})
	}
}

func TestAnAdHocMemberWithoutAUsableFileGetsNoItemAndItsNotesAreDropped(t *testing.T) {
	report := reportOf(map[string]any{"notes": []any{
		noteOf("scout", "scout", "fleet", "", "the map lives in docs", nil, true),
		noteOf("bosun", "implementer", "fleet", "", "the build needs make", nil, false),
	}})
	for name, setup := range map[string]func(t *testing.T, home string){
		"a symlink": func(t *testing.T, home string) {
			real := filepath.Join(t.TempDir(), "scout.md")
			if err := os.WriteFile(real, []byte(adHocDefinition), 0o600); err != nil {
				t.Fatal(err)
			}
			if err := os.MkdirAll(filepath.Join(crewDirOf(home), "ad-hoc"), 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(real, filepath.Join(crewDirOf(home), "ad-hoc", "scout.md")); err != nil {
				t.Fatal(err)
			}
		},
		"a file without ad-hoc: true": func(t *testing.T, home string) {
			putAdHoc(t, home, "scout", strings.Replace(adHocDefinition, "ad-hoc: true\n", "", 1))
		},
		"no file": func(t *testing.T, home string) {},
	} {
		t.Run(name, func(t *testing.T) {
			home := t.TempDir()
			setup(t, home)
			if err := inbox.WriteCrewItems(crewRun(home), report); err != nil {
				t.Fatal(err)
			}
			items := crewItems(t, home)
			if len(items["crew_member"]) != 0 {
				t.Errorf("crew_member items = %v, want none", items["crew_member"])
			}
			if len(items["crew_notes"]) != 1 || !reflect.DeepEqual(texts(notesIn(t, items["crew_notes"][0], "crew_notes")), []string{"the build needs make"}) {
				t.Errorf("crew_notes = %v, want only the note of the member that isn't ad hoc, never the ad hoc one", items["crew_notes"])
			}
		})
	}
}

func TestTheListTagsCrewItemsWithTheirFleetAndLeavesTheTagsOffForAFleetThatIsGone(t *testing.T) {
	s, home, _ := newInbox(t)
	s.Fleet = workFleet
	putAdHoc(t, home, "scout", adHocDefinition)
	report := reportOf(map[string]any{
		"cast":  []any{map[string]any{"member": "scout", "role": "scout", "ad_hoc": true}},
		"notes": []any{noteOf("bosun", "implementer", "fleet", "", "the build needs make", nil, false)},
	})
	if err := inbox.WriteCrewItems(crewRun(home), report); err != nil {
		t.Fatal(err)
	}
	gone := crewNotesItem("crew-notes-gone", "gone", "Saltpaw", []map[string]any{crewNote("n1", "bosun", "fleet", "", "x", nil)})
	writeItem(t, shipDir(t, home, inbox.AdmiralSession), "crew-notes-gone.json", gone)

	byID := map[string]map[string]any{}
	for _, it := range listed(t, s) {
		byID[it["id"].(string)] = it
	}
	for id, it := range byID {
		if id == "crew-notes-gone" {
			if it["fleet"] != nil || it["fleet_name"] != nil || it["fleet_color"] != nil {
				t.Errorf("%s = %v, want no fleet tags for a fleet that is no longer configured", id, it)
			}
			continue
		}
		if it["fleet"] != "work" || it["fleet_name"] != "Work" || it["fleet_color"] != "#38bdf8" || it["name"] != "Admiral" {
			t.Errorf("%s = %v, want the work fleet's tags on an item named Admiral", id, it)
		}
	}
	if len(byID) != 3 {
		t.Errorf("listed %d items, want the two written and the one seeded", len(byID))
	}
}

var _ = regexp.MustCompile

func TestAnAdHocMemberNamedLikeAStandingMemberGetsNoItem(t *testing.T) {
	for name, member := range map[string]string{"a built-in": "reviewer", "a fleet member": "scout"} {
		t.Run(name, func(t *testing.T) {
			home := t.TempDir()
			def := strings.ReplaceAll(adHocDefinition, "name: scout", "name: "+member)
			putAdHoc(t, home, member, def)
			if member == "scout" {
				if err := os.WriteFile(filepath.Join(crewDirOf(home), "scout.md"), []byte(strings.Replace(adHocDefinition, "ad-hoc: true\nad-hoc-run: \"run-a\"\n", "", 1)), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			run := crewRun(home)
			run.BuiltIns = func(string) []string { return []string{"reviewer"} }
			report := reportOf(map[string]any{
				"cast":  []any{map[string]any{"member": member, "role": member, "ad_hoc": true}},
				"notes": []any{noteOf(member, "implementer", "fleet", "", "a fact", nil, true)},
			})
			if err := inbox.WriteCrewItems(run, report); err != nil {
				t.Fatal(err)
			}
			if items := crewItems(t, home); len(items["crew_member"]) != 0 || len(items["crew_notes"]) != 0 {
				t.Errorf("items = %v, want none", items)
			}
		})
	}
}

func TestANoteOfAnAdHocCastMemberWaitsOnTheMembersItemEvenWithItsFlagOff(t *testing.T) {
	home := t.TempDir()
	putAdHoc(t, home, "scout", adHocDefinition)
	report := reportOf(map[string]any{
		"cast":  []any{map[string]any{"member": "scout", "role": "scout", "ad_hoc": true}},
		"notes": []any{noteOf("scout", "scout", "fleet", "", "the map lives in docs", nil, false)},
	})
	if err := inbox.WriteCrewItems(crewRun(home), report); err != nil {
		t.Fatal(err)
	}
	items := crewItems(t, home)
	if len(items["crew_notes"]) != 0 || len(items["crew_member"]) != 1 {
		t.Fatalf("items = %v, want one crew_member and no crew_notes", items)
	}
	if got := texts(notesIn(t, items["crew_member"][0], "crew_member")); !reflect.DeepEqual(got, []string{"the map lives in docs"}) {
		t.Errorf("member notes = %v", got)
	}
}

func TestAnAdHocFileThatIsNotUTF8GetsNoItem(t *testing.T) {
	home := t.TempDir()
	putAdHoc(t, home, "scout", adHocDefinition+"\xff\xfe\n")
	report := reportOf(map[string]any{"cast": []any{map[string]any{"member": "scout", "role": "scout", "ad_hoc": true}}})
	if err := inbox.WriteCrewItems(crewRun(home), report); err != nil {
		t.Fatal(err)
	}
	if items := crewItems(t, home); len(items["crew_member"]) != 0 {
		t.Errorf("items = %v, want none", items)
	}
}
