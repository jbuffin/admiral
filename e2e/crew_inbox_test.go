package e2e

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func sprintAdHoc(name, run string) string {
	return "---\nname: " + name + "\nrole: scout\npick-me-when: a run needs a map\nad-hoc: true\nad-hoc-run: \"" + run + "\"\n---\n\nMap it.\n"
}

func sleep50() { time.Sleep(50 * time.Millisecond) }

// A Commodore run's crew proposals become Inbox items, and the fleet's crew files change only on an answer.
func TestCrewItemsFromAReportAreWrittenToTheFleetOnlyWhenAnswered(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	w.cueFleets(map[string][]string{"f": {"hook working", "wait go", "exit 0"}})
	w.start()
	ship := w.launchIn("f", "claude", "crewrepo")
	rec := w.record(ship.Session)
	crewDir := rec.env["ADMIRAL_CREW_DIR"]
	if crewDir == "" {
		t.Fatal("the ship's env has no ADMIRAL_CREW_DIR")
	}
	// The run id source is an open question in the u3 report: this test assumes a top-level "run" field in the report.
	const runID = "e2e-run-1"
	adHoc := filepath.Join(crewDir, "ad-hoc")
	if err := os.MkdirAll(adHoc, 0o700); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"scout", "mapper"} {
		if err := os.WriteFile(filepath.Join(adHoc, name+".md"), []byte(sprintAdHoc(name, runID)), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	note := func(member, scope, repo, text string, adHoc bool) map[string]any {
		var r any
		if repo != "" {
			r = repo
		}
		return map[string]any{"member": member, "role": "implementer", "scope": scope, "repo": r, "note": text, "replaces": nil, "ad-hoc": adHoc}
	}
	report, _ := json.Marshal(map[string]any{"version": 2, "result": "done", "why": "ok",
		"run":  runID,
		"cast": []any{map[string]any{"member": "scout", "role": "scout", "ad_hoc": true}, map[string]any{"member": "mapper", "role": "scout", "ad_hoc": true}},
		"notes": []any{
			note("bosun", "fleet", "", "the build needs make", false),
			note("scout", "repo", "acme/widgets", "the map lives in docs", true),
		}})
	reportPath, _ := shipPaths(rec)
	if err := os.WriteFile(reportPath, report, 0o600); err != nil {
		t.Fatal(err)
	}
	w.touch(ship.Session, "go")
	w.waitEvent(ship.Session, "ended", 1)

	var notesID, scoutID, mapperID string
	deadline := 0
	for notesID == "" || scoutID == "" || mapperID == "" {
		for _, it := range w.in.inbox(t) {
			switch {
			case it.Kind == "crew_notes":
				notesID = it.ID
			case it.Kind == "crew_member" && strings.Contains(it.Title, "scout"):
				scoutID = it.ID
			case it.Kind == "crew_member" && strings.Contains(it.Title, "mapper"):
				mapperID = it.ID
			}
		}
		if deadline++; deadline > 300 {
			t.Fatalf("within 15s GET /api/inbox never listed the crew items: notes %q scout %q mapper %q", notesID, scoutID, mapperID)
		}
		sleep50()
	}
	holdThat(t, "the fleet's crew memory was written before any answer", func() bool {
		_, err := os.Stat(filepath.Join(crewDir, "memory"))
		return os.IsNotExist(err) && exists(filepath.Join(adHoc, "scout.md")) && !exists(filepath.Join(crewDir, "scout.md"))
	})

	if code, body := send(t, http.MethodPost, w.in.LocalURL+"/api/inbox/_admiral/"+scoutID+"/crew-member", map[string]any{"decision": "keep"}); code != 200 {
		t.Fatalf("keep = %d: %s", code, body)
	}
	if !exists(filepath.Join(crewDir, "scout.md")) || exists(filepath.Join(adHoc, "scout.md")) {
		t.Errorf("keeping scout left scout.md missing or the ad hoc file in place")
	}
	if b, _ := os.ReadFile(filepath.Join(crewDir, "scout.md")); strings.Contains(string(b), "ad-hoc") {
		t.Errorf("scout.md still has ad-hoc lines: %s", b)
	}
	if b, _ := os.ReadFile(filepath.Join(crewDir, "memory", "scout", "repos", "acme", "widgets.md")); !strings.Contains(string(b), "the map lives in docs") {
		t.Errorf("scout's note was not written: %q", b)
	}

	if code, body := send(t, http.MethodPost, w.in.LocalURL+"/api/inbox/_admiral/"+mapperID+"/crew-member", map[string]any{"decision": "drop"}); code != 200 {
		t.Fatalf("drop = %d: %s", code, body)
	}
	if exists(filepath.Join(adHoc, "mapper.md")) || exists(filepath.Join(crewDir, "mapper.md")) {
		t.Errorf("dropping mapper left a file behind")
	}
	if exists(filepath.Join(crewDir, "memory", "bosun")) {
		t.Errorf("bosun's note was written although its item is unanswered")
	}
}
