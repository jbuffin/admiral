package ships

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
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

func (r *wakeRig) launchAs(t *testing.T, fleet string) string {
	t.Helper()
	mux := http.NewServeMux()
	r.s.Routes(mux)
	body := `{"fleet":"work","harness":"claude","dir":` + strconv.Quote(r.checkout) + `,"model":"opus","skill":{"kind":"none"},"text":"go"}`
	req := httptest.NewRequest("POST", "/api/ships", strings.NewReader(body))
	if fleet != "" {
		req = req.WithContext(actor.WithFleet(context.Background(), fleet))
	}
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("launch = %d %s", rec.Code, rec.Body)
	}
	var got struct{ Ship struct{ Session string } }
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil || got.Ship.Session == "" {
		t.Fatalf("launch answer %s: %v", rec.Body, err)
	}
	return got.Ship.Session
}

func (r *wakeRig) shipsVia(t *testing.T, session string) (via string, listed bool) {
	t.Helper()
	mux := http.NewServeMux()
	r.s.Routes(mux)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest("GET", "/api/ships", nil))
	var got struct {
		Ships []struct{ Session, Via string }
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	for _, s := range got.Ships {
		if s.Session == session {
			return s.Via, true
		}
	}
	return "", false
}

func TestAShipAnActorLaunchesIsStampedViaAdmiralbotInItsLaunchedRecordAndTheShipList(t *testing.T) {
	r := newWakeRig(t, 0)
	session := r.launchAs(t, "work")
	var got []map[string]any
	waitUntil(t, "the launched record", func() bool { got = outcomeLines(t, r.home, session, "launched"); return len(got) == 1 })
	if got[0]["via"] != "admiralbot" {
		t.Errorf("launched record = %v, want via admiralbot", got[0])
	}
	waitUntil(t, "the ship in the list", func() bool { _, ok := r.shipsVia(t, session); return ok })
	if via, _ := r.shipsVia(t, session); via != "admiralbot" {
		t.Errorf("the ship's via = %q, want admiralbot", via)
	}
}

func TestAShipTheEngineerLaunchesCarriesNoVia(t *testing.T) {
	r := newWakeRig(t, 0)
	session := r.launchAs(t, "")
	var got []map[string]any
	waitUntil(t, "the launched record", func() bool { got = outcomeLines(t, r.home, session, "launched"); return len(got) == 1 })
	if _, has := got[0]["via"]; has {
		t.Errorf("launched record = %v, want no via field", got[0])
	}
	waitUntil(t, "the ship in the list", func() bool { _, ok := r.shipsVia(t, session); return ok })
	if via, _ := r.shipsVia(t, session); via != "" {
		t.Errorf("the ship's via = %q, want none", via)
	}
}

func TestAnActorDismissingAMooredShipStampsItsUnmooredRecordViaAdmiralbot(t *testing.T) {
	f := mooredService(t, nil)
	f.moor(t, "Saltpaw", "work", prSeven)
	if rec := f.asActor(t, "work", "POST", "/api/ships/saltpaw/dismiss"); rec.Code != 200 {
		t.Fatalf("dismiss = %d %s", rec.Code, rec.Body)
	}
	got := f.events(t, "saltpaw", "unmoored")
	if len(got) != 1 || got[0]["via"] != "admiralbot" {
		t.Errorf("unmoored records = %v, want one stamped via admiralbot", got)
	}
}
