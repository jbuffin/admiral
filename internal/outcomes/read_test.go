package outcomes

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

// The reader autopilot counts retros with (ADR 0017). The interface these tests
// assume:
//
//	type Line struct {
//	    Event, Ship string
//	    TS          time.Time      // the line's ts, whatever offset it was written with
//	    Fields      map[string]any // every other key of the line
//	}
//	func Read(dir string) []Line   // dir is the outcomes directory
//
// Read returns the lines of every *.jsonl file in file-name order, each file in
// line order. A line that is not a JSON object with a v, an RFC 3339 ts and an
// event is skipped; so is a missing directory. Event names are not checked:
// the counter ignores the ones it does not know.

func TestReadWalksFilesInNameOrderAndSkipsWhatItCannotUse(t *testing.T) {
	dir := t.TempDir()
	write := func(name string, lines ...string) {
		t.Helper()
		var b []byte
		for _, l := range lines {
			b = append(b, l+"\n"...)
		}
		if err := os.WriteFile(filepath.Join(dir, name), b, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	// 2026-09 sorts first although its file is written second, and its lines
	// carry later timestamps than the October file: position decides, not ts.
	write("2026-10.jsonl",
		`{"v":1,"ts":"2026-10-01T00:00:00Z","ship":"b","event":"ended","exit":0}`,
		`this is not json`,
		`{"v":1,"ts":"not a time","ship":"x","event":"ended"}`,
		`{"v":1,"ts":"2026-10-01T00:00:05Z","ship":"c","event":"from_the_future","k":"v"}`,
		`[1,2,3]`,
		`{"v":1,"ts":"2026-10-01T02:00:00+02:00","ship":"","event":"retro_queued","panel":"p"}`,
	)
	write("2026-09.jsonl",
		`{"v":1,"ts":"2026-09-30T23:59:59Z","ship":"a","event":"launched","autopilot":true}`,
	)
	write("notes.txt", `{"v":1,"ts":"2026-09-30T23:59:59Z","ship":"z","event":"launched"}`)

	got := Read(dir)
	type brief struct {
		Event, Ship string
		TS          time.Time
	}
	var seen []brief
	for _, l := range got {
		seen = append(seen, brief{l.Event, l.Ship, l.TS})
	}
	utc := func(s string) time.Time {
		ts, err := time.Parse(time.RFC3339, s)
		if err != nil {
			t.Fatal(err)
		}
		return ts.UTC()
	}
	want := []brief{
		{"launched", "a", utc("2026-09-30T23:59:59Z")},
		{"ended", "b", utc("2026-10-01T00:00:00Z")},
		{"from_the_future", "c", utc("2026-10-01T00:00:05Z")},
		{"retro_queued", "", utc("2026-10-01T00:00:00Z")}, // +02:00 comes back as UTC
	}
	if !reflect.DeepEqual(seen, want) {
		t.Fatalf("Read = %v, want %v", seen, want)
	}
	if got[0].Fields["autopilot"] != true || got[3].Fields["panel"] != "p" {
		t.Errorf("fields = %v and %v, want the line's own keys", got[0].Fields, got[3].Fields)
	}
	if n := len(Read(filepath.Join(dir, "nowhere"))); n != 0 {
		t.Errorf("Read of a missing directory = %d lines, want none", n)
	}
}
