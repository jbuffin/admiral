package main

import (
	"encoding/json"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/jbuffin/admiral/internal/chat"
	"github.com/jbuffin/admiral/internal/moor"
	"github.com/jbuffin/admiral/internal/outcomes"
	"github.com/jbuffin/admiral/internal/ships"
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
		{Ship: "reported", Event: "ended", Fields: map[string]any{"exit_code": zero, "report": json.RawMessage(`{"version":2,"prs":[],"result":"done"}`)}},
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
		{At: at, Ship: "reported", Event: "ended", Clean: true, ReportResult: "done"},
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

// A clean-exited ship's report file is gone by the time it is moored, so the result must come from its ended record.
func TestAMooredShipsReportTurnCarriesTheResultFromItsEndedRecord(t *testing.T) {
	dir := t.TempDir()
	at := time.Date(2026, 11, 17, 10, 0, 0, 0, time.UTC)
	w := outcomes.New(filepath.Join(dir, "outcomes"), func(string, ...any) {})
	w.Now = func() time.Time { return at.Add(time.Minute) }
	zero := 0
	w.Enqueue(outcomes.Record{Ship: "calm-otter", Event: "ended", Fields: map[string]any{"exit_code": zero, "report": json.RawMessage(`{"version":2,"prs":[{"url":"https://github.com/acme/api/pull/7"}],"result":"done"}`)}}, nil)
	w.Close()

	quick := ships.Ship{Session: "calm-otter", Name: "Calm Otter", Fleet: "work", Via: "admiralbot", FirstLaunched: at.Format(time.RFC3339), Status: ships.StatusUnderway}
	snap := []ships.Ship{quick}
	var got []string
	r := newShipCardRunner(dir, func(string, chat.ShipCard) error { return nil }, func() []ships.Ship { return snap }, nil)
	r.cards.QueueReports(shipReportHook(func(fleet string, c chat.ShipCard, result string) error {
		got = append(got, c.Result+":"+result)
		return nil
	}, func(string) string { return "" }))
	r.now = func() time.Time { return at.Add(2 * time.Minute) }
	idle := ships.Ship{Session: "idle-gull", Fleet: "home", FirstLaunched: at.Format(time.RFC3339), Status: ships.StatusUnderway}
	snap = []ships.Ship{idle}
	r.pass()
	quick.Status, quick.PRs = ships.StatusMoored, []moor.PR{{URL: "https://github.com/acme/api/pull/7"}}
	snap = []ships.Ship{idle, quick}
	r.pass()
	if !slices.Equal(got, []string{"moored:done"}) {
		t.Errorf("report turns = %v, want [moored:done]", got)
	}
}
