package shipconv

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/jbuffin/admiral/internal/sessionhost"
	"github.com/jbuffin/admiral/internal/ships"
)

func TestCrewConversationEndpoint(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	f.harness["alpha"] = "claude"
	boats := map[string]sessionhost.Crew{
		"rev": {Name: "rev", Harness: "claude", Launched: "B1", Transcript: f.path("zz")},
		"cx":  {Name: "cx", Harness: "codex", Launched: "B2", Transcript: f.path("zz")},
		"new": {Name: "new", Harness: "claude", Launched: "B3"},
		"out": {Name: "out", Harness: "claude", Launched: "B4", Transcript: filepath.Join(filepath.Dir(f.cfg), "outside.jsonl")},
	}
	f.svc.crew = func(sh ships.Ship, name string) (sessionhost.Crew, string, bool) {
		b, ok := boats[name]
		return b, f.cfg, ok
	}
	appendTo(t, f.path("zz"), lines(1, 2))
	if err := os.WriteFile(boats["out"].Transcript, []byte(line(9)), 0o644); err != nil {
		t.Fatal(err)
	}

	mux := http.NewServeMux()
	f.svc.Routes(mux)
	get := func(path string) (int, pageJSON) {
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, httptest.NewRequest("GET", path, nil))
		var p pageJSON
		_ = json.Unmarshal(rec.Body.Bytes(), &p)
		return rec.Code, p
	}
	base := "/api/ships/alpha/crew/"
	if code, p := get(base + "rev/conversation"); code != 200 || p.State != "ok" || len(p.Items) != 2 {
		t.Errorf("claude boat = %d %+v, want 200 ok with 2 items", code, p)
	}
	for _, name := range []string{"cx", "new"} {
		if code, p := get(base + name + "/conversation"); code != 200 || p.State != "none" {
			t.Errorf("%s = %d %+v, want 200 none", name, code, p)
		}
	}
	if code, p := get(base + "out/conversation"); code != 200 || p.State == "ok" || len(p.Items) != 0 {
		t.Errorf("a path outside projects = %d %+v, want it refused", code, p)
	}
	if code, _ := get(base + "nobody/conversation"); code != 404 {
		t.Errorf("unknown boat = %d, want 404", code)
	}
	if code, _ := get("/api/ships/nope/crew/rev/conversation"); code != 404 {
		t.Errorf("unknown ship = %d, want 404", code)
	}
}
