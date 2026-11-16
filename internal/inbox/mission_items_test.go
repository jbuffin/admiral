package inbox_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/jbuffin/admiral/internal/inbox"
)

const missionText = "---\nname: sweep\ndescription: sweep the repo ✓\n---\n\nRead every file.\n"

func proposal(name, scope string, repo any, text string) map[string]any {
	return map[string]any{"name": name, "scope": scope, "repo": repo, "text": text}
}

func missionItems(t *testing.T, home string) []map[string]any {
	t.Helper()
	files, _ := filepath.Glob(filepath.Join(home, "inbox", inbox.AdmiralSession, "mission-*.json"))
	var out []map[string]any
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
		out = append(out, it)
	}
	return out
}

func writeMissions(t *testing.T, home string, missions any) {
	t.Helper()
	notes := []any{noteOf("bosun", "implementer", "fleet", "", "the build needs make", nil, false)}
	if err := inbox.WriteCrewItems(crewRun(home), reportOf(map[string]any{"missions": missions, "notes": notes})); err != nil {
		t.Fatal(err)
	}
}

func TestAMissionProposalWritesOneMissionItemInAdmiralsInboxAndNothingToTheFleet(t *testing.T) {
	home := t.TempDir()
	writeMissions(t, home, []any{proposal("sweep", "fleet", "acme/widgets", missionText)})
	items := missionItems(t, home)
	if len(items) != 1 {
		t.Fatalf("mission items = %v, want one", items)
	}
	it := items[0]
	wantID := "mission-" + idHash("saltpaw", "2026-11-10T09:00:00Z", "sweep")
	if it["id"] != wantID || it["kind"] != "mission" || it["version"] != float64(1) || it["created"] == nil || it["title"] == "" {
		t.Errorf("item header = %v, want id %s, kind mission, version 1, created and a title", it, wantID)
	}
	want := map[string]any{"fleet": "work", "ship": "Saltpaw", "run_date": "2026-11-10", "name": "sweep", "scope": "fleet", "repo": nil, "text": missionText}
	if got, _ := it["mission"].(map[string]any); !reflect.DeepEqual(got, want) {
		t.Errorf("mission = %v, want %v (a fleet proposal carries no repo)", got, want)
	}
	if _, err := os.Stat(filepath.Join(home, "fleets")); !os.IsNotExist(err) {
		t.Errorf("fleets/ was written before any answer: %v", err)
	}
	if n := len(crewItems(t, home)["crew_notes"]); n != 1 {
		t.Errorf("crew_notes items = %d, want the report's notes to go too", n)
	}
}

func TestARepoMissionProposalCarriesItsRepo(t *testing.T) {
	home := t.TempDir()
	writeMissions(t, home, []any{proposal("sweep", "repo", "acme/widgets", missionText)})
	items := missionItems(t, home)
	if len(items) != 1 {
		t.Fatalf("mission items = %v, want one", items)
	}
	if o, _ := items[0]["mission"].(map[string]any); o["scope"] != "repo" || o["repo"] != "acme/widgets" {
		t.Errorf("mission = %v, want scope repo and repo acme/widgets", o)
	}
}

func TestWritingTheSameMissionReportAgainWritesNoSecondItem(t *testing.T) {
	home := t.TempDir()
	for i := 0; i < 2; i++ {
		writeMissions(t, home, []any{proposal("sweep", "fleet", nil, missionText)})
	}
	if items := missionItems(t, home); len(items) != 1 {
		t.Errorf("mission items = %v, want one", items)
	}
}

func TestATextOfExactlyTheByteCapIsKept(t *testing.T) {
	home := t.TempDir()
	writeMissions(t, home, []any{proposal("sweep", "fleet", nil, strings.Repeat("x", 65536))})
	if items := missionItems(t, home); len(items) != 1 {
		t.Errorf("mission items = %v, want one for 65536 bytes", items)
	}
}

func TestAMissionProposalThatFailsTheRulesGetsNoItemAndTheCrewItemsStillGo(t *testing.T) {
	for name, p := range map[string]map[string]any{
		"an uppercase name":                              proposal("Sweep", "fleet", nil, missionText),
		"an empty name":                                  proposal("", "fleet", nil, missionText),
		"a name with a slash":                            proposal("a/b", "fleet", nil, missionText),
		"a dotdot name":                                  proposal("../x", "fleet", nil, missionText),
		"a leading dash":                                 proposal("-sweep", "fleet", nil, missionText),
		"a name over 64 characters":                      proposal(strings.Repeat("a", 65), "fleet", nil, missionText),
		"the built-in build":                             proposal("build", "fleet", nil, missionText),
		"the built-in pr-feedback":                       proposal("pr-feedback", "repo", "acme/widgets", missionText),
		"the built-in review":                            proposal("review", "fleet", nil, missionText),
		"an unknown scope":                               proposal("sweep", "galaxy", nil, missionText),
		"a repo scope with no repo":                      proposal("sweep", "repo", nil, missionText),
		"a repo that isn't a slug":                       proposal("sweep", "repo", "widgets", missionText),
		"a dotdot repo":                                  proposal("sweep", "repo", "acme/..", missionText),
		"an empty text":                                  proposal("sweep", "fleet", nil, ""),
		"a text over the byte cap":                       proposal("sweep", "fleet", nil, strings.Repeat("x", 65537)),
		"a text of few characters but over the byte cap": proposal("sweep", "fleet", nil, strings.Repeat("é", 32769)),
	} {
		t.Run(name, func(t *testing.T) {
			home := t.TempDir()
			writeMissions(t, home, []any{p, proposal("fine-one", "fleet", nil, missionText)})
			items := missionItems(t, home)
			if len(items) != 1 || items[0]["id"] != "mission-"+idHash("saltpaw", "2026-11-10T09:00:00Z", "fine-one") {
				t.Errorf("mission items = %v, want only the valid proposal beside it", items)
			}
			if n := len(crewItems(t, home)["crew_notes"]); n != 1 {
				t.Errorf("crew_notes items = %d, want the report's notes to go", n)
			}
		})
	}
}

func TestAMissionFieldThatIsNotTheSchemasShapeIsIgnoredAndTheValidOnesStay(t *testing.T) {
	for name, missions := range map[string]any{
		"missions is a string":    "sweep",
		"missions is an object":   proposal("sweep", "fleet", nil, missionText),
		"an entry is a string":    []any{"sweep"},
		"the name is a number":    []any{map[string]any{"name": 7, "scope": "fleet", "repo": nil, "text": missionText}},
		"the text is a list":      []any{map[string]any{"name": "sweep", "scope": "fleet", "repo": nil, "text": []any{"a"}}},
		"the scope is missing":    []any{map[string]any{"name": "sweep", "repo": nil, "text": missionText}},
		"the repo is a number":    []any{map[string]any{"name": "sweep", "scope": "repo", "repo": 3, "text": missionText}},
		"an entry is a null":      []any{nil},
		"missions is a null":      nil,
		"missions is an empty []": []any{},
	} {
		t.Run(name, func(t *testing.T) {
			home := t.TempDir()
			writeMissions(t, home, missions)
			if items := missionItems(t, home); len(items) != 0 {
				t.Errorf("mission items = %v, want none", items)
			}
			if n := len(crewItems(t, home)["crew_notes"]); n != 1 {
				t.Errorf("crew_notes items = %d, want the report's notes to go", n)
			}
		})
	}
	home := t.TempDir()
	writeMissions(t, home, []any{"junk", proposal("Bad", "fleet", nil, missionText), proposal("sweep", "fleet", nil, missionText)})
	if items := missionItems(t, home); len(items) != 1 || items[0]["id"] != "mission-"+idHash("saltpaw", "2026-11-10T09:00:00Z", "sweep") {
		t.Errorf("mission items = %v, want only the valid proposal", items)
	}
}

func TestNoMissionItemWithoutAVersionTwoReportOrAConfiguredFleet(t *testing.T) {
	home := t.TempDir()
	v1 := reportOf(map[string]any{"version": 1, "missions": []any{proposal("sweep", "fleet", nil, missionText)}})
	if err := inbox.WriteCrewItems(crewRun(home), v1); err != nil {
		t.Fatal(err)
	}
	run := crewRun(home)
	run.Fleets = func(string) (inbox.FleetInfo, bool) { return inbox.FleetInfo{}, false }
	if err := inbox.WriteCrewItems(run, reportOf(map[string]any{"missions": []any{proposal("sweep", "fleet", nil, missionText)}})); err != nil {
		t.Fatal(err)
	}
	if items := missionItems(t, home); len(items) != 0 {
		t.Errorf("mission items = %v, want none", items)
	}
	if err := inbox.WriteCrewItems(crewRun(home), reportOf(map[string]any{"missions": []any{proposal("sweep", "fleet", nil, missionText)}})); err != nil {
		t.Fatal(err)
	}
	if items := missionItems(t, home); len(items) != 1 {
		t.Errorf("mission items = %v, want one from a version 2 report of a configured fleet", items)
	}
}

func seededMission(id, fleet, name, scope string, repo any, text string) map[string]any {
	return map[string]any{"version": 1, "id": id, "kind": "mission", "created": "2026-11-10T10:00:00Z",
		"title":   "Keep mission " + name + "?",
		"mission": map[string]any{"fleet": fleet, "ship": "Saltpaw", "run_date": "2026-11-10", "name": name, "scope": scope, "repo": repo, "text": text}}
}

func TestTheListTagsAMissionItemWithItsFleetAndAFleetScopedActorSeesItOnlyThere(t *testing.T) {
	s, home, mux := actorInbox(t)
	s.Fleet = func(key string) (inbox.FleetInfo, bool) {
		return inbox.FleetInfo{Key: key, Name: strings.ToUpper(key[:1]) + key[1:], Color: "#38bdf8"}, key == "work" || key == "personal"
	}
	dir := shipDir(t, home, inbox.AdmiralSession)
	writeItem(t, dir, "mission-0123456789abcdef.json", seededMission("mission-0123456789abcdef", "work", "sweep", "fleet", nil, missionText))
	writeItem(t, dir, "mission-fedcba9876543210.json", seededMission("mission-fedcba9876543210", "gone", "sweep", "fleet", nil, missionText))
	s.Refresh()

	byID := map[string]map[string]any{}
	for _, it := range listed(t, s) {
		byID[it["id"].(string)] = it
	}
	it := byID["mission-0123456789abcdef"]
	if it == nil || it["kind"] != "mission" || it["name"] != "Admiral" || it["fleet"] != "work" || it["fleet_name"] != "Work" || it["fleet_color"] != "#38bdf8" {
		t.Errorf("mission item = %v, want kind mission named Admiral with the work fleet's tags", it)
	}
	if o, _ := it["mission"].(map[string]any); o["name"] != "sweep" || o["text"] != missionText {
		t.Errorf("mission object = %v", o)
	}
	if gone := byID["mission-fedcba9876543210"]; gone == nil || gone["fleet"] != nil || gone["fleet_name"] != nil {
		t.Errorf("item of a fleet that is gone = %v, want listed with no fleet tags", gone)
	}

	for fleet, want := range map[string][]string{"work": {"mission-0123456789abcdef"}, "personal": {}} {
		rec := asActor(mux, fleet, "GET", "/api/inbox", "")
		var out struct {
			Items []map[string]any `json:"items"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil || rec.Code != 200 {
			t.Fatalf("list = %d %s", rec.Code, rec.Body)
		}
		if got := idsOf(out.Items); !reflect.DeepEqual(got, want) {
			t.Errorf("an actor for %s sees %v, want %v", fleet, got, want)
		}
	}
}

func TestAMissionItemOutsideAdmiralsInboxOrWithABadObjectIsNotListed(t *testing.T) {
	s, home, _ := newInbox(t)
	s.Fleet = workFleet
	ship := seededMission("mission-aaaaaaaaaaaaaaaa", "work", "sweep", "fleet", nil, missionText)
	writeItem(t, shipDir(t, home, "s1"), "mission-aaaaaaaaaaaaaaaa.json", ship)
	dir := shipDir(t, home, inbox.AdmiralSession)
	for id, it := range map[string]map[string]any{
		"mission-bbbbbbbbbbbbbbbb": seededMission("mission-bbbbbbbbbbbbbbbb", "", "sweep", "fleet", nil, missionText),
		"mission-cccccccccccccccc": seededMission("mission-cccccccccccccccc", "work", "Bad Name", "fleet", nil, missionText),
		"mission-dddddddddddddddd": seededMission("mission-dddddddddddddddd", "work", "sweep", "galaxy", nil, missionText),
		"mission-eeeeeeeeeeeeeeee": seededMission("mission-eeeeeeeeeeeeeeee", "work", "sweep", "repo", nil, missionText),
		"mission-ffffffffffffffff": seededMission("mission-ffffffffffffffff", "work", "sweep", "fleet", nil, ""),
		"mission-1111111111111111": seededMission("mission-1111111111111111", "work", "sweep", "fleet", nil, strings.Repeat("x", 65537)),
	} {
		writeItem(t, dir, id+".json", it)
	}
	writeItem(t, dir, "mission-2222222222222222.json", seededMission("mission-2222222222222222", "work", "sweep", "fleet", nil, strings.Repeat("x", 65536)))
	if got := idsOf(listed(t, s)); !reflect.DeepEqual(got, []string{"mission-2222222222222222"}) {
		t.Errorf("listed %v, want only the well-formed one", got)
	}
}
