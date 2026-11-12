package shipdiff

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/jbuffin/admiral/internal/config"
	"github.com/jbuffin/admiral/internal/launcher"
	"github.com/jbuffin/admiral/internal/moor"
	"github.com/jbuffin/admiral/internal/outcomes"
	"github.com/jbuffin/admiral/internal/server"
	"github.com/jbuffin/admiral/internal/sessionhost"
	"github.com/jbuffin/admiral/internal/ships"
	"github.com/jbuffin/admiral/internal/state"
)

// A moored ship has no session, but its checkout is still on disk, so its changes can be reviewed.
func TestAMooredShipsChangesAreServedFromItsRecordedCheckout(t *testing.T) {
	dir := repo(t)
	home := t.TempDir()
	st, err := state.Open(home)
	if err != nil {
		t.Fatal(err)
	}
	cfg := &config.Config{Fleets: []config.Fleet{{Key: "work", Name: "Work"}}}
	w := outcomes.New(filepath.Join(home, "outcomes"), t.Logf)
	t.Cleanup(w.Close)
	ss := &ships.Service{Host: sessionhost.New("no-such-tmux", filepath.Join(home, "tmux.sock")),
		Launcher: &launcher.Launcher{Home: home, Config: func() *config.Config { return cfg }}, State: st, Hub: server.NewHub(), Outcomes: w}
	rec := moor.Record{Name: "Moored", Session: "moored", Fleet: "work", Harness: "claude", Checkout: dir,
		PRs: []string{"https://github.com/acme/widgets/pull/7"}, Resume: "/commodore resume run-7", MooredAt: time.Now().UTC().Truncate(time.Second)}
	if err := moor.Write(home, rec); err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	(&Service{Ships: ss}).Routes(mux)

	get := func(session string) *httptest.ResponseRecorder {
		r := httptest.NewRecorder()
		mux.ServeHTTP(r, httptest.NewRequest("GET", "/api/ships/"+session+"/changes", nil))
		return r
	}
	r := get("moored")
	if r.Code != 200 {
		t.Fatalf("moored changes = %d %s, want 200", r.Code, r.Body)
	}
	var res Result
	if err := json.Unmarshal(r.Body.Bytes(), &res); err != nil || res.State != "ok" || byPath(res)["untracked.txt"].Path == "" {
		t.Errorf("moored changes = %s (%v), want the checkout's work", r.Body, err)
	}
	if r := get("nobody"); r.Code != 404 {
		t.Errorf("unknown ship changes = %d, want 404", r.Code)
	}
}
