package ships

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jbuffin/admiral/internal/actor"
)

// outcomesFixture holds a month of records: ships a-1 (work) and b-1 (home) with their launches, ghost with no launch
// anywhere, and saltpaw, moored in work with its launch gone from the files.
func outcomesFixture(t *testing.T) *mooredFixture {
	t.Helper()
	f := mooredService(t, nil)
	f.moor(t, "Saltpaw", "work", prSeven)
	lines := []string{
		`{"v":1,"ts":"2026-11-01T10:00:00Z","ship":"a-1","event":"launched","fleet":"work"}`,
		`{"v":1,"ts":"2026-11-01T10:01:00Z","ship":"b-1","event":"launched","fleet":"home"}`,
		`{"v":1,"ts":"2026-11-01T10:02:00Z","ship":"a-1","event":"stopped"}`,
		`{"v":1,"ts":"2026-11-01T10:03:00Z","ship":"b-1","event":"stopped"}`,
		`{"v":1,"ts":"2026-11-01T10:04:00Z","ship":"ghost","event":"stopped"}`,
		`{"v":1,"ts":"2026-11-01T10:05:00Z","ship":"saltpaw","event":"unmoored","reason":"merged"}`,
	}
	dir := filepath.Join(f.home, "outcomes")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "2026-11.jsonl"), []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return f
}

// read answers the "ship event" pairs of GET /api/outcomes as the engineer (fleet "") or as AdmiralBot of a fleet.
func (f *mooredFixture) readOutcomes(t *testing.T, fleet, query string) []string {
	t.Helper()
	req := httptest.NewRequest("GET", "/api/outcomes"+query, nil)
	if fleet != "" {
		req = req.WithContext(actor.WithFleet(context.Background(), fleet))
	}
	rec := httptest.NewRecorder()
	f.mux.ServeHTTP(rec, req)
	var body struct {
		Outcomes []struct{ Ship, Event string }
	}
	if rec.Code != 200 || json.Unmarshal(rec.Body.Bytes(), &body) != nil {
		t.Fatalf("GET /api/outcomes%s as %q = %d %s, want 200 {\"outcomes\": [...]}", query, fleet, rec.Code, rec.Body)
	}
	var got []string
	for _, o := range body.Outcomes {
		got = append(got, o.Ship+" "+o.Event)
	}
	return got
}

func wantOutcomes(t *testing.T, what string, got []string, want ...string) {
	t.Helper()
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("%s: %v, want %v", what, got, want)
	}
}

func TestAnActorReadsOnlyItsFleetsOutcomes(t *testing.T) {
	f := outcomesFixture(t)
	wantOutcomes(t, "work", f.readOutcomes(t, "work", ""), "a-1 launched", "a-1 stopped", "saltpaw unmoored")
	wantOutcomes(t, "home", f.readOutcomes(t, "home", ""), "b-1 launched", "b-1 stopped")
}

func TestTheEngineerReadsEveryOutcome(t *testing.T) {
	f := outcomesFixture(t)
	wantOutcomes(t, "no actor", f.readOutcomes(t, "", ""),
		"a-1 launched", "b-1 launched", "a-1 stopped", "b-1 stopped", "ghost stopped", "saltpaw unmoored")
}

func TestOutcomesCanBeNarrowedToAShipAndCappedToTheLatestFirst(t *testing.T) {
	f := outcomesFixture(t)
	wantOutcomes(t, "ship=a-1", f.readOutcomes(t, "", "?ship=a-1"), "a-1 launched", "a-1 stopped")
	wantOutcomes(t, "limit=2 for the engineer", f.readOutcomes(t, "", "?limit=2"), "ghost stopped", "saltpaw unmoored")
	// The fence applies before the limit: another fleet's newest records must not use up AdmiralBot's.
	wantOutcomes(t, "limit=2 for work", f.readOutcomes(t, "work", "?limit=2"), "a-1 stopped", "saltpaw unmoored")
	wantOutcomes(t, "another fleet's ship", f.readOutcomes(t, "work", "?ship=b-1"))
}
