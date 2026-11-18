package main

import (
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/jbuffin/admiral/internal/chat"
	"github.com/jbuffin/admiral/internal/outcomes"
)

// The detector must read the records the ships service really writes: exit_code and signal on an ended line, the reason of
// an unmoored one and the returned flag of a stopped one.
func TestTheShipCardRunnerReadsTheOutcomeLogsRealRecordShapes(t *testing.T) {
	dir := t.TempDir()
	at := time.Date(2026, 11, 17, 10, 0, 0, 0, time.UTC)
	w := outcomes.New(filepath.Join(dir, "outcomes"), func(string, ...any) {})
	w.Now = func() time.Time { return at }
	zero, kill := 0, "KILL"
	for _, r := range []outcomes.Record{
		{Ship: "clean", Event: "ended", Fields: map[string]any{"exit_code": zero}},
		{Ship: "killed", Event: "ended", Fields: map[string]any{"signal": kill}},
		{Ship: "failed", Event: "ended", Fields: map[string]any{"exit_code": 1}},
		{Ship: "merged", Event: "unmoored", Fields: map[string]any{"reason": "merged", "prs": []map[string]string{{"url": "u", "state": "MERGED"}}}},
		{Ship: "back", Event: "stopped", Fields: map[string]any{"returned": true}},
		{Ship: "scouted", Event: "stopped", Fields: map[string]any{"scout": true}},
		{Ship: "halted", Event: "stopped"},
	} {
		w.Enqueue(r, nil)
	}
	w.Close()

	r := newShipCardRunner(dir, func(string, chat.ShipCard) error { return nil }, nil, nil)
	got := r.records(at.Add(time.Minute))
	want := []chat.ShipRecord{
		{At: at, Ship: "clean", Event: "ended", Clean: true},
		{At: at, Ship: "killed", Event: "ended"},
		{At: at, Ship: "failed", Event: "ended"},
		{At: at, Ship: "merged", Event: "unmoored", Reason: "merged"},
		{At: at, Ship: "back", Event: "stopped", Returned: true},
		{At: at, Ship: "scouted", Event: "stopped", Scout: true},
		{At: at, Ship: "halted", Event: "stopped"},
	}
	if !slices.Equal(got, want) {
		t.Errorf("records = %+v, want %+v", got, want)
	}
}
