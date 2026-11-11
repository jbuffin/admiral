package moor

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

func TestWriteKeepsTheRecordUnderTheShipsName(t *testing.T) {
	home := t.TempDir()
	at := time.Date(2026, 11, 11, 10, 0, 0, 0, time.UTC)
	rec := Record{Name: "Saltpaw II", Session: "saltpaw-ii", Fleet: "work", Harness: "claude", Model: "opus",
		Skill: Skill{Kind: "commodore", Name: "commodore"}, Checkout: "/w/widgets",
		PRs: []string{"https://github.com/acme/widgets/pull/7"}, Resume: "/commodore resume run-7", MooredAt: at}
	if err := Write(home, rec); err != nil {
		t.Fatal(err)
	}

	b, err := os.ReadFile(filepath.Join(home, "moored", "Saltpaw II.json"))
	if err != nil {
		t.Fatalf("the record is not at moored/<name>.json: %v", err)
	}
	var raw map[string]any
	if err := json.Unmarshal(b, &raw); err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{"name", "session", "fleet", "harness", "model", "skill", "checkout", "prs", "resume", "round", "moored_at"} {
		if _, ok := raw[k]; !ok {
			t.Errorf("the record has no %q: %s", k, b)
		}
	}
	if raw["round"] != float64(0) {
		t.Errorf("round = %v, want 0", raw["round"])
	}

	got, err := List(home)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || !reflect.DeepEqual(got[0].PRs, rec.PRs) || got[0].Name != rec.Name || !got[0].MooredAt.Equal(at) ||
		got[0].Skill != rec.Skill || got[0].Resume != rec.Resume || got[0].Round != 0 {
		t.Errorf("List = %+v, want the record written", got)
	}
}

func TestListIsEmptyBeforeAnyShipMoors(t *testing.T) {
	got, err := List(t.TempDir())
	if err != nil || len(got) != 0 {
		t.Errorf("List = %v, %v, want none and no error", got, err)
	}
}
