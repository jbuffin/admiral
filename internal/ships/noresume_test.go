package ships

import (
	"encoding/json"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/jbuffin/admiral/internal/moor"
)

func (f *mooredFixture) listed(t *testing.T) map[string]map[string]any {
	t.Helper()
	rec := httptest.NewRecorder()
	f.mux.ServeHTTP(rec, httptest.NewRequest("GET", "/api/ships", nil))
	var body struct {
		Ships []map[string]any `json:"ships"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("GET /api/ships = %d %s: %v", rec.Code, rec.Body, err)
	}
	out := map[string]map[string]any{}
	for _, s := range body.Ships {
		out[s["name"].(string)] = s
	}
	return out
}

// no_resume is the one flag a client needs to know a moored ship cannot be woken, so it is true only then and absent otherwise.
func TestAMooredShipWithNoValidResumeTextCarriesNoResumeAndOneWithItDoesNot(t *testing.T) {
	f := mooredService(t, nil)
	for name, resume := range map[string]string{"Bare": "", "Loose": "resume it", "Valid": "/commodore resume run-7"} {
		rec := moor.Record{Name: name, Session: name, Fleet: "work", Harness: "claude", Checkout: "/src/widgets",
			PRs: []string{"https://github.com/acme/widgets/pull/7"}, Resume: resume, MooredAt: time.Now().UTC().Truncate(time.Second)}
		if err := moor.Write(f.home, rec); err != nil {
			t.Fatal(err)
		}
	}
	got := f.listed(t)
	for _, name := range []string{"Bare", "Loose"} {
		if got[name]["no_resume"] != true {
			t.Errorf("%s: no_resume = %v, want true: %v", name, got[name]["no_resume"], got[name])
		}
	}
	if v, has := got["Valid"]["no_resume"]; has {
		t.Errorf("Valid: no_resume = %v, want the key absent", v)
	}
}
