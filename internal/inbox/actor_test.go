package inbox_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/jbuffin/admiral/internal/actor"
	"github.com/jbuffin/admiral/internal/inbox"
	"github.com/jbuffin/admiral/internal/outcomes"
	"github.com/jbuffin/admiral/internal/server"
)

// Ships: s1 is in work, s2 in personal; any other session has no known ship.
func actorInbox(t *testing.T) (*inbox.Service, string, *http.ServeMux) {
	t.Helper()
	home := t.TempDir()
	w := outcomes.New(filepath.Join(home, "outcomes"), t.Logf)
	t.Cleanup(w.Close)
	fleets := map[string]string{"s1": "work", "s2": "personal"}
	s := &inbox.Service{Home: home, Hub: server.NewHub(), Outcomes: w, Ship: func(session string) (inbox.ShipInfo, bool) {
		f, ok := fleets[session]
		return inbox.ShipInfo{Name: "Dory", Harness: "claude", Fleet: f, FleetName: f}, ok
	}}
	mux := http.NewServeMux()
	s.Routes(mux)
	return s, home, mux
}

func asActor(mux http.Handler, fleet, method, path, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, strings.NewReader(body)).WithContext(actor.WithFleet(context.Background(), fleet))
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	return rec
}

func TestAnActorsInboxHoldsOnlyItsFleetsItemsAndHidesOnesOfNoKnownFleet(t *testing.T) {
	s, home, mux := actorInbox(t)
	writeItem(t, shipDir(t, home, "s1"), "q-work.json", question("q-work", "2026-10-22T10:00:00Z"))
	writeItem(t, shipDir(t, home, "s2"), "q-mine.json", question("q-mine", "2026-10-22T10:01:00Z"))
	writeItem(t, shipDir(t, home, "ghost"), "q-ghost.json", question("q-ghost", "2026-10-22T10:02:00Z"))
	s.Refresh()

	rec := asActor(mux, "personal", "GET", "/api/inbox", "")
	var got struct{ Items []map[string]any }
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil || rec.Code != 200 {
		t.Fatalf("list = %d %s", rec.Code, rec.Body)
	}
	if ids := idsOf(got.Items); !reflect.DeepEqual(ids, []string{"q-mine"}) {
		t.Errorf("an actor for personal sees %v, want only q-mine", ids)
	}
}

func TestAnsweringAQuestionAsAnActorStampsItsAnsweredRecordViaAdmiralbot(t *testing.T) {
	s, home, mux := actorInbox(t)
	writeItem(t, shipDir(t, home, "s2"), "q-mine.json", question("q-mine", "2026-10-22T10:01:00Z"))
	s.Refresh()
	body := `{"answers":{"1":{"choice":"Alpha"},"2":{"choice":["X"]}}}`

	if rec := asActor(mux, "personal", "POST", "/api/inbox/s2/q-mine/answer", body); rec.Code != 200 {
		t.Fatalf("answer = %d %s", rec.Code, rec.Body)
	}
	var rec map[string]any
	deadline := time.Now().Add(5 * time.Second)
	for rec == nil && time.Now().Before(deadline) {
		files, _ := filepath.Glob(filepath.Join(home, "outcomes", "*.jsonl"))
		for _, p := range files {
			b, _ := os.ReadFile(p)
			for _, line := range strings.Split(string(b), "\n") {
				var r map[string]any
				if json.Unmarshal([]byte(line), &r) == nil && r["event"] == "answered" && r["ship"] == "s2" {
					rec = r
				}
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	if rec == nil || rec["via"] != "admiralbot" {
		t.Errorf("answered record = %v, want via admiralbot", rec)
	}
}
