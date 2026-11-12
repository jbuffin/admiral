package inbox

import (
	"strings"
	"testing"
)

func TestNoticeOnlyFromAdmiralSession(t *testing.T) {
	b := []byte(`{"version":1,"id":"n-1","kind":"notice","created":"2026-11-10T00:00:00Z","title":"t","notice":{"body":"b"}}`)
	if _, err := parseItem(b, AdmiralSession, "n-1"); err != nil {
		t.Errorf("notice from _admiral: %v", err)
	}
	if _, err := parseItem(b, "some-ship", "n-1"); err == nil {
		t.Error("a notice in a ship's inbox was accepted")
	}
}

func TestAMigrationNoticeCarriesItsEntriesToTheShell(t *testing.T) {
	home := t.TempDir()
	entries := []NoticeEntry{{Key: "gh", Name: "GitHub", Panel: "mine"}, {Key: "notes", Name: "Notes", Panel: "orders"}}
	if _, err := WriteNotice(home, "Migrated", "body", entries...); err != nil {
		t.Fatal(err)
	}
	if _, err := WriteNotice(home, "Plain", "no entries"); err != nil {
		t.Fatal(err)
	}
	var withEntries, plain *Item
	for _, it := range OpenNotices(home) {
		it := it
		if it.NoticeBody() == "body" {
			withEntries = &it
		} else {
			plain = &it
		}
	}
	if withEntries == nil || plain == nil {
		t.Fatalf("notices = %+v, want both", OpenNotices(home))
	}
	got := withEntries.NoticeEntries()
	if len(got) != 2 || got[0] != entries[0] || got[1] != entries[1] {
		t.Errorf("entries = %+v, want %+v", got, entries)
	}
	if plain.NoticeEntries() != nil {
		t.Errorf("a plain note's entries = %+v, want none", plain.NoticeEntries())
	}
	b, err := withEntries.MarshalJSON()
	if err != nil || !strings.Contains(string(b), `"entries":[{"key":"gh","name":"GitHub","panel":"mine"}`) {
		t.Errorf("item JSON = %s (%v), want notice.entries with key, name and panel", b, err)
	}
}
