package launcher

import (
	"net/http"
	"testing"

	"github.com/jbuffin/admiral/internal/moor"
)

func TestLaunchUnderAGivenNameTakesTheNameItsRecordReserves(t *testing.T) {
	l, work, argv := recordingLauncher(t, "claude")
	if err := moor.Write(l.Home, moor.Record{Name: "Saltpaw II", Session: "saltpaw-ii", Fleet: "work", Harness: "claude"}); err != nil {
		t.Fatal(err)
	}
	got, err := l.Launch(Request{Fleet: "work", Harness: "claude", Dir: work, Skill: Skill{Kind: "none"}, Text: "go",
		Name: "Saltpaw II", Session: "saltpaw-ii", Round: 2})
	if err != nil {
		t.Fatal(err)
	}
	if got.Session != "saltpaw-ii" || got.Options["name"] != "Saltpaw II" {
		t.Errorf("launched session %q named %q, want saltpaw-ii named Saltpaw II", got.Session, got.Options["name"])
	}
	if got.Options["round"] != "2" {
		t.Errorf("round option = %q, want 2", got.Options["round"])
	}
	if a := argv(); a[len(a)-1] != "go" {
		t.Errorf("argv = %q, want the text as the prompt", a)
	}
}

func TestLaunchUnderAGivenNameRefusesWhileThatSessionIsLive(t *testing.T) {
	l, work, _ := recordingLauncher(t, "claude")
	req := Request{Fleet: "work", Harness: "claude", Dir: work, Skill: Skill{Kind: "none"}, Name: "Saltpaw II", Session: "saltpaw-ii", Round: 1}
	if _, err := l.Launch(req); err != nil {
		t.Fatal(err)
	}
	_, err := l.Launch(req)
	if le, ok := err.(*Error); !ok || le.Status != http.StatusConflict {
		t.Errorf("a second launch under a live ship's name = %v, want a 409", err)
	}
}
