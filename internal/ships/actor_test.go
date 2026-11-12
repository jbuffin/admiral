package ships

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jbuffin/admiral/internal/actor"
)

func (f *mooredFixture) asActor(t *testing.T, fleet, method, path string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, path, nil).WithContext(actor.WithFleet(context.Background(), fleet))
	rec := httptest.NewRecorder()
	f.mux.ServeHTTP(rec, req)
	return rec
}

func TestAnActorsShipListHoldsOnlyItsFleetsShipsMooredOnesIncluded(t *testing.T) {
	f := mooredService(t, nil)
	f.moor(t, "Saltpaw", "work", prSeven)
	f.moor(t, "Dory", "home", prSeven)

	for _, c := range []struct {
		fleet string
		want  []string
	}{{"work", []string{"saltpaw"}}, {"home", []string{"dory"}}, {"", []string{"dory", "saltpaw"}}} {
		var rec *httptest.ResponseRecorder
		if c.fleet == "" { // the engineer's own request
			rec = httptest.NewRecorder()
			f.mux.ServeHTTP(rec, httptest.NewRequest("GET", "/api/ships", nil))
		} else {
			rec = f.asActor(t, c.fleet, "GET", "/api/ships")
		}
		var got struct{ Ships []struct{ Session string } }
		if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil || rec.Code != 200 {
			t.Fatalf("fleet %q: %d %s", c.fleet, rec.Code, rec.Body)
		}
		var names []string
		for _, s := range got.Ships {
			names = append(names, s.Session)
		}
		if len(names) != len(c.want) || (len(names) == 1 && names[0] != c.want[0]) {
			t.Errorf("fleet %q lists %v, want %v", c.fleet, names, c.want)
		}
	}
}

func outcomeLines(t *testing.T, home, ship, event string) []map[string]any {
	t.Helper()
	files, _ := filepath.Glob(filepath.Join(home, "outcomes", "*.jsonl"))
	var out []map[string]any
	for _, p := range files {
		b, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		for _, line := range strings.Split(string(b), "\n") {
			var rec map[string]any
			if json.Unmarshal([]byte(line), &rec) == nil && rec["ship"] == ship && rec["event"] == event {
				out = append(out, rec)
			}
		}
	}
	return out
}

func TestStoppingAShipAsAnActorStampsItsStoppedRecordViaAdmiralbot(t *testing.T) {
	r := newWakeRig(t, 0)
	r.launchRound(t, 0)
	r.started(t, 1)
	mux := http.NewServeMux()
	r.s.Routes(mux)

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest("POST", "/api/ships/saltpaw/stop", nil).WithContext(actor.WithFleet(context.Background(), "work")))
	if rec.Code != 200 {
		t.Fatalf("stop = %d %s", rec.Code, rec.Body)
	}
	var got []map[string]any
	waitUntil(t, "the stopped record", func() bool { got = outcomeLines(t, r.home, "saltpaw", "stopped"); return len(got) == 1 })
	if got[0]["via"] != "admiralbot" {
		t.Errorf("stopped record = %v, want via admiralbot", got[0])
	}
}

func TestStoppingAShipAsTheEngineerLeavesItsStoppedRecordUnstamped(t *testing.T) {
	r := newWakeRig(t, 0)
	r.launchRound(t, 0)
	r.started(t, 1)
	mux := http.NewServeMux()
	r.s.Routes(mux)

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest("POST", "/api/ships/saltpaw/stop", nil))
	if rec.Code != 200 {
		t.Fatalf("stop = %d %s", rec.Code, rec.Body)
	}
	var got []map[string]any
	waitUntil(t, "the stopped record", func() bool { got = outcomeLines(t, r.home, "saltpaw", "stopped"); return len(got) == 1 })
	if _, has := got[0]["via"]; has {
		t.Errorf("stopped record = %v, want no via field", got[0])
	}
}
