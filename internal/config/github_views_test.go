package config_test

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/jbuffin/admiral/internal/config"
)

// A custom view that shared its title's slug with a built-in used to replace it. It now sits beside it
// under a new id, so the old panel_order and the routines that named the old id are read through the move.
func TestMigrateReadsAnOldPanelOrderThroughTheIDsThatMoved(t *testing.T) {
	const src = `"github":{"plugin":"github","name":"GitHub","fleet":"a",
  "settings":{"views":[{"title":"My PRs","query":"author:me label:x"},{"title":"Bugs","query":"label:bug"}]},
  "panel_order":["bugs","my-prs"]}`
	dir := migrateDir(t, migrateConfig(src))
	m, err := config.Migrate(dir, migrateVersion)
	if err != nil || m == nil {
		t.Fatalf("Migrate = %v, %v", m, err)
	}
	srcs, _ := readSources(t, dir)
	// my-prs named the custom, now my-prs-2: it trades places with Bugs, and the built-in stays put.
	wantJSON(t, "github settings.views", settingsOf(t, srcs["github"])["views"],
		`[{"id":"for-you","hidden":false},{"id":"assigned-to-me","hidden":false},{"id":"review-requested","hidden":false},{"id":"my-prs","hidden":false},`+
			`{"title":"Bugs","query":"label:bug"},{"title":"My PRs","query":"author:me label:x"}]`)
	want := []config.ViewIDMove{{Fleet: "a", Source: "github", From: "my-prs", To: "my-prs-2"}}
	if !reflect.DeepEqual(m.ViewMoves, want) {
		t.Errorf("ViewMoves = %+v, want %+v", m.ViewMoves, want)
	}

	m.RoutinesMoved = 2
	if _, body := m.Notice(); !strings.Contains(body, "2 routine(s)") {
		t.Errorf("the notice does not count the routines it pointed at the new id:\n%s", body)
	}
}

// The ids a views list is served under and the ids a drag names come from one rule.
func TestADragNamesTheIDsTheListIsServedUnder(t *testing.T) {
	// An entry with an unknown id but a title and a query is a custom view: its id is the title's slug.
	const settings = `{"views":[{"id":"x","title":"Bugs","query":"q1"},{"title":"Bugs","query":"q2"}]}`
	ids := config.ViewEntryIDs([]config.ViewEntry{{ID: "x", Title: "Bugs", Query: "q1"}, {Title: "Bugs", Query: "q2"}})
	if !reflect.DeepEqual(ids, []string{"bugs", "bugs-2"}) {
		t.Fatalf("ViewEntryIDs = %v, want [bugs bugs-2]", ids)
	}
	var out struct{ Views []struct{ Query string } }
	if err := json.Unmarshal(config.ReorderGitHubViews(json.RawMessage(settings), []string{"bugs-2", "bugs"}), &out); err != nil {
		t.Fatal(err)
	}
	if len(out.Views) != 2 || out.Views[0].Query != "q2" || out.Views[1].Query != "q1" {
		t.Errorf("a drag naming bugs-2 first gave %+v, want q2 then q1", out.Views)
	}
}
