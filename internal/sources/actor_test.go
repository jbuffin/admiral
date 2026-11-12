package sources

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/jbuffin/admiral/internal/actor"
)

const twoFleetSources = `{"version":1,"fleets":{"work":{"name":"Work","claude":{}},"home":{"name":"Home","claude":{}}},
  "sources":{
    "pw":{"plugin":"notinstalled","fleet":"work","name":"Passwords","settings":{}},
    "chores":{"plugin":"notinstalled","fleet":"home","name":"Chores","settings":{}}}}`

func TestAnActorsSourceListHoldsOnlyItsFleetsSources(t *testing.T) {
	f := newServiceFixture(t, twoFleetSources)
	f.startSources()
	f.waitView("pw", "listed", func(SourceView) bool { return true })
	f.waitView("chores", "listed", func(SourceView) bool { return true })

	for _, c := range []struct {
		fleet string
		want  int
		key   string
	}{{"work", 1, "pw"}, {"home", 1, "chores"}, {"", 2, ""}} {
		req := httptest.NewRequest(http.MethodGet, "/api/sources", nil)
		if c.fleet != "" {
			req = req.WithContext(actor.WithFleet(context.Background(), c.fleet))
		}
		rec := httptest.NewRecorder()
		f.Svc.serveList(rec, req)
		var got []SourceView
		if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
			t.Fatalf("fleet %q: %v: %s", c.fleet, err, rec.Body)
		}
		if len(got) != c.want || (c.key != "" && got[0].Key != c.key) {
			t.Errorf("fleet %q lists %+v, want %d source(s) %s", c.fleet, got, c.want, c.key)
		}
	}
}
