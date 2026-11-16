package inbox_test

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jbuffin/admiral/internal/inbox"
)

const missionID = "mission-0123456789abcdef"

func (r *crewRig) missionsDir() string { return filepath.Join(r.home, "fleets", "work", "missions") }

func (r *crewRig) answerMission(id, decision string) (int, map[string]any) {
	r.t.Helper()
	return r.postJSON("/api/inbox/_admiral/"+id+"/mission", map[string]any{"decision": decision})
}

func (r *crewRig) seedMission(name, scope string, repo any, text string) {
	r.t.Helper()
	r.seed(seededMission(missionID, "work", name, scope, repo, text))
}

func (r *crewRig) assertNoFleetDir() {
	r.t.Helper()
	if _, err := os.Stat(filepath.Join(r.home, "fleets")); !os.IsNotExist(err) {
		r.t.Errorf("fleets/ was written: %v", err)
	}
}

func (r *crewRig) outcomeNames() map[string]map[string]any {
	r.t.Helper()
	out := map[string]map[string]any{}
	for _, x := range r.recs() {
		out[fmt.Sprint(x["event"])] = x
	}
	return out
}

func TestKeepingAMissionWritesItsTextByteForByteToTheFleetsMissionsDirectory(t *testing.T) {
	for name, text := range map[string]string{"with a trailing newline": missionText, "without one": "no newline ✓", "with CRLF": "a\r\nb\r\n"} {
		t.Run(name, func(t *testing.T) {
			r := newCrewRig(t)
			r.seedMission("sweep", "fleet", nil, text)
			code, body := r.answerMission(missionID, "keep")
			if code != 200 || body["answered"] != missionID || body["file_written"] != true {
				t.Fatalf("keep = %d %v, want 200 answered with file_written true", code, body)
			}
			// the path Commodore's resolver reads: .md directly in $ADMIRAL_HOME/fleets/<key>/missions
			if b, err := os.ReadFile(filepath.Join(r.home, "fleets", "work", "missions", "sweep.md")); err != nil || string(b) != text {
				t.Errorf("sweep.md = %q (%v), want %q", b, err, text)
			}
			if fi, err := os.Stat(r.missionsDir()); err != nil || fi.Mode().Perm() != 0o700 {
				t.Errorf("missions dir = %v (%v), want mode 0700", fi, err)
			}
			if entries, _ := os.ReadDir(r.missionsDir()); len(entries) != 1 {
				t.Errorf("missions dir holds %v, want only sweep.md and no temp file", entries)
			}
			if _, err := os.Stat(crewDirOf(r.home)); !os.IsNotExist(err) {
				t.Errorf("the crew directory was written: %v", err)
			}
			a := answerOf(t, r.home, missionID)
			if a["version"] != float64(1) || a["id"] != missionID || a["kind"] != "mission" || a["decision"] != "keep" || a["file_written"] != true || a["answered"] == nil {
				t.Errorf("answer file = %v", a)
			}
			rec := r.outcomeNames()["mission_kept"]
			if rec == nil || rec["item"] != missionID || rec["kind"] != "mission" || rec["name"] != "sweep" || rec["ship"] != "Saltpaw" {
				t.Errorf("mission_kept record = %v, want item, kind mission, name sweep and ship Saltpaw", rec)
			}
		})
	}
}

func TestKeepingARepoMissionWritesTheSameFlatFleetFile(t *testing.T) {
	r := newCrewRig(t)
	r.seedMission("sweep", "repo", "acme/widgets", missionText)
	if code, body := r.answerMission(missionID, "keep"); code != 200 || body["file_written"] != true {
		t.Fatalf("keep = %d %v", code, body)
	}
	if b, err := os.ReadFile(filepath.Join(r.missionsDir(), "sweep.md")); err != nil || string(b) != missionText {
		t.Errorf("sweep.md = %q (%v)", b, err)
	}
	if entries, _ := os.ReadDir(r.missionsDir()); len(entries) != 1 {
		t.Errorf("missions dir holds %v, want only sweep.md", entries)
	}
}

func TestKeepingAMissionWhoseIdenticalFileIsThereCountsAsKeptWithoutWritingIt(t *testing.T) {
	r := newCrewRig(t)
	if err := os.MkdirAll(r.missionsDir(), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(r.missionsDir(), "sweep.md"), []byte(missionText), 0o600); err != nil {
		t.Fatal(err)
	}
	r.seedMission("sweep", "fleet", nil, missionText)
	code, body := r.answerMission(missionID, "keep")
	if code != 200 || body["answered"] != missionID || body["file_written"] != false {
		t.Fatalf("keep = %d %v, want 200 with file_written false", code, body)
	}
	if a := answerOf(t, r.home, missionID); a["decision"] != "keep" || a["file_written"] != false {
		t.Errorf("answer file = %v", a)
	}
	if r.outcomeNames()["mission_kept"] == nil {
		t.Error("no mission_kept record")
	}
}

func TestKeepingAMissionWhoseFileDiffersIsRefusedWith409AndChangesNothing(t *testing.T) {
	r := newCrewRig(t)
	if err := os.MkdirAll(r.missionsDir(), 0o700); err != nil {
		t.Fatal(err)
	}
	existing := filepath.Join(r.missionsDir(), "sweep.md")
	if err := os.WriteFile(existing, []byte("an older sweep\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	r.seedMission("sweep", "fleet", nil, missionText)
	if code, body := r.answerMission(missionID, "keep"); code != 409 {
		t.Fatalf("keep = %d %v, want 409", code, body)
	}
	if b, _ := os.ReadFile(existing); string(b) != "an older sweep\n" {
		t.Errorf("sweep.md = %q, want it unchanged", b)
	}
	if entries, _ := os.ReadDir(r.missionsDir()); len(entries) != 1 {
		t.Errorf("missions dir holds %v, want only the old file", entries)
	}
	r.assertUnanswered(missionID)
}

func TestAMissionItemIsAnsweredOnce(t *testing.T) {
	r := newCrewRig(t)
	r.seedMission("sweep", "fleet", nil, missionText)
	if code, body := r.answerMission(missionID, "keep"); code != 200 {
		t.Fatalf("first keep = %d %v", code, body)
	}
	if code, _ := r.answerMission(missionID, "drop"); code != 409 {
		t.Errorf("second answer = %d, want 409", code)
	}
}

func TestKeepingAMissionOfAFleetThatIsNoLongerConfiguredIsRefusedAndWritesNothing(t *testing.T) {
	r := newCrewRig(t)
	r.seedMission("sweep", "fleet", nil, missionText)
	r.s.Fleet = func(string) (inbox.FleetInfo, bool) { return inbox.FleetInfo{}, false }
	if code, body := r.answerMission(missionID, "keep"); code != 409 {
		t.Errorf("keep = %d %v, want 409", code, body)
	}
	r.assertNoFleetDir()
	r.assertUnanswered(missionID)
}

func TestAHandSeededMissionNamedLikeABuiltInIsListedAndKeepIsRefusedWith409(t *testing.T) {
	for _, name := range []string{"build", "pr-feedback", "review"} {
		t.Run(name, func(t *testing.T) {
			r := newCrewRig(t)
			r.seedMission(name, "fleet", nil, missionText)
			var listedIDs []string
			for _, it := range listed(t, r.s) {
				listedIDs = append(listedIDs, it["id"].(string))
			}
			if len(listedIDs) != 1 || listedIDs[0] != missionID {
				t.Fatalf("listed %v, want the built-in-named item (the accessor checks shape only)", listedIDs)
			}
			if code, body := r.answerMission(missionID, "keep"); code != 409 {
				t.Errorf("keep = %d %v, want 409", code, body)
			}
			r.assertNoFleetDir()
			r.assertUnanswered(missionID)
		})
	}
}

func TestAMissionItemThatFailsTheShapeCheckIsNotFoundWhenAnswered(t *testing.T) {
	r := newCrewRig(t)
	r.seedMission("sweep", "fleet", nil, missionText)
	if code, body := r.answerMission(missionID, "keep"); code != 200 {
		t.Fatalf("a good item: keep = %d %v", code, body)
	}
	for i, it := range []map[string]any{
		seededMission("mission-aaaaaaaaaaaaaaaa", "", "sweep", "fleet", nil, missionText),
		seededMission("mission-bbbbbbbbbbbbbbbb", "work", "Bad Name", "fleet", nil, missionText),
		seededMission("mission-cccccccccccccccc", "work", "sweep", "galaxy", nil, missionText),
		seededMission("mission-dddddddddddddddd", "work", "sweep", "repo", nil, missionText),
		seededMission("mission-eeeeeeeeeeeeeeee", "work", "sweep", "fleet", nil, ""),
		seededMission("mission-ffffffffffffffff", "work", "sweep", "fleet", nil, strings.Repeat("é", 32769)),
	} {
		r.seed(it)
		if code, body := r.answerMission(it["id"].(string), "keep"); code != 404 {
			t.Errorf("bad item %d: keep = %d %v, want 404", i, code, body)
		}
	}
}

func TestADecisionOtherThanKeepOrDropIsRefusedAndTheGenericRouteNamesTheMissionRoute(t *testing.T) {
	r := newCrewRig(t)
	r.seedMission("sweep", "fleet", nil, missionText)
	if code, body := r.answerMission(missionID, "edit"); code != 400 {
		t.Errorf("edit = %d %v, want 400", code, body)
	}
	code, body := r.postJSON("/api/inbox/_admiral/"+missionID+"/answer", map[string]any{"answers": map[string]any{}})
	if code != 400 || !strings.Contains(fmt.Sprint(body["error"]), "mission") {
		t.Errorf("generic answer = %d %v, want 400 naming mission", code, body)
	}
	r.assertNoFleetDir()
	r.assertUnanswered(missionID)
}

func TestDroppingOrRemovingAMissionWritesNothingUnderMissions(t *testing.T) {
	r := newCrewRig(t)
	r.seedMission("sweep", "fleet", nil, missionText)
	code, body := r.answerMission(missionID, "drop")
	if code != 200 || body["answered"] != missionID || body["file_written"] != false {
		t.Fatalf("drop = %d %v, want 200 with file_written false", code, body)
	}
	if a := answerOf(t, r.home, missionID); a["kind"] != "mission" || a["decision"] != "drop" || a["file_written"] != false {
		t.Errorf("answer file = %v", a)
	}
	rec := r.outcomeNames()["mission_dropped"]
	if rec == nil || rec["item"] != missionID || rec["kind"] != "mission" || rec["name"] != "sweep" {
		t.Errorf("mission_dropped record = %v", rec)
	}
	r.assertNoFleetDir()

	r.seed(seededMission("mission-fedcba9876543210", "work", "other", "fleet", nil, missionText))
	if code, body := r.postJSON("/api/inbox/_admiral/mission-fedcba9876543210/remove", map[string]any{}); code != 200 || body["forgotten"] != true {
		t.Errorf("remove = %d %v, want 200 forgotten", code, body)
	}
	r.assertNoFleetDir()
}

func TestKeepingAMissionThroughALinkOnThePathAnswers409AndLeavesTheItemUnanswered(t *testing.T) {
	r := newCrewRig(t)
	outside := t.TempDir()
	if err := os.MkdirAll(filepath.Dir(r.missionsDir()), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, r.missionsDir()); err != nil {
		t.Fatal(err)
	}
	r.seedMission("sweep", "fleet", nil, missionText)
	if code, body := r.answerMission(missionID, "keep"); code != 409 {
		t.Errorf("keep = %d %v, want 409", code, body)
	}
	if entries, _ := os.ReadDir(outside); len(entries) != 0 {
		t.Errorf("the link's target holds %v, want nothing written through it", entries)
	}
	r.assertUnanswered(missionID)
}
