package routines_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/jbuffin/admiral/internal/config"
	"github.com/jbuffin/admiral/internal/routines"
)

func TestRetargetViewsPointsOnlyTheMovedViewsRoutinesAtTheNewID(t *testing.T) {
	home := t.TempDir()
	file := filepath.Join(home, "routines", "a", "routines.json")
	if err := os.MkdirAll(filepath.Dir(file), 0o700); err != nil {
		t.Fatal(err)
	}
	const before = `{"routines":[
  {"id":"r1","name":"on mine","when":"","on":{"source":"github","kind":"new_in_view","panel":"my-prs"},"do":{}},
  {"id":"r2","name":"other source","when":"","on":{"source":"gh2","kind":"new_in_view","panel":"my-prs"},"do":{}},
  {"id":"r3","name":"other view","when":"","on":{"source":"github","kind":"new_in_view","panel":"bugs"},"do":{}},
  {"id":"r4","name":"other kind","when":"","on":{"source":"github","kind":"review_requested","panel":"my-prs"},"do":{}},
  {"id":"r5","name":"timed","when":"0 9 * * *","do":{}}]}`
	if err := os.WriteFile(file, []byte(before), 0o600); err != nil {
		t.Fatal(err)
	}
	s := &routines.Store{Home: home, Fleets: func(string) (config.Fleet, bool) { return config.Fleet{}, true }}
	n, err := s.RetargetViews([]config.ViewIDMove{{Fleet: "a", Source: "github", From: "my-prs", To: "my-prs-2"}})
	if err != nil || n != 1 {
		t.Fatalf("RetargetViews = %d, %v, want 1, nil", n, err)
	}
	list, err := s.List("a")
	if err != nil {
		t.Fatal(err)
	}
	panels := map[string]string{}
	for _, r := range list {
		if r.On != nil {
			panels[r.ID] = r.On.Panel
		}
	}
	want := map[string]string{"r1": "my-prs-2", "r2": "my-prs", "r3": "bugs", "r4": "my-prs"}
	for id, w := range want {
		if panels[id] != w {
			t.Errorf("routine %s watches %q, want %q", id, panels[id], w)
		}
	}
}
