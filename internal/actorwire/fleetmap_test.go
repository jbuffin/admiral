package actorwire_test

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jbuffin/admiral/internal/actor"
	"github.com/jbuffin/admiral/internal/actorwire"
	"github.com/jbuffin/admiral/internal/campaigns/follow"
	"github.com/jbuffin/admiral/internal/config"
	"github.com/jbuffin/admiral/internal/crewapi"
	"github.com/jbuffin/admiral/internal/fleetmap"
	"github.com/jbuffin/admiral/internal/inbox"
)

// mapFleets runs the real guard over the real fleet-map and crew handlers of two fleets that differ in every read.
type mapFleets struct {
	h     http.Handler
	token map[string]string
}

func write(t *testing.T, path, text string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
}

func newMapFleets(t *testing.T) *mapFleets {
	t.Helper()
	base, _ := filepath.EvalSymlinks(t.TempDir())
	home := filepath.Join(base, "admiral")
	cfg := fmt.Sprintf(`{"version":1,"fleets":{
	  "work":{"name":"Work","search_roots":[%q],"owners":["acme"],"claude":{"env":{"CLAUDE_CONFIG_DIR":%q}}},
	  "home":{"name":"Home","search_roots":[%q],"owners":["hobby"],"claude":{"env":{"CLAUDE_CONFIG_DIR":%q}}}}}`,
		filepath.Join(base, "work-src"), filepath.Join(base, "work-claude"), filepath.Join(base, "home-src"), filepath.Join(base, "home-claude"))
	write(t, filepath.Join(home, config.FileName), cfg)
	for fleet, member := range map[string]string{"work": "bosun-of-work", "home": "cook-of-home"} {
		write(t, filepath.Join(home, "fleets", fleet, "crew", member+".md"),
			"---\nname: "+member+"\nrole: implementer\npick-me-when: Pick for "+fleet+" chores.\n---\n\nDo the work.\n")
		write(t, filepath.Join(base, fleet+"-claude", "skills", "skill-of-"+fleet, "SKILL.md"), "---\nname: skill-of-"+fleet+"\ndescription: only "+fleet+"\n---\n")
	}
	m, err := config.Open(home)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(m.Close)
	minter, err := actor.NewMinter(home)
	if err != nil {
		t.Fatal(err)
	}
	f := &mapFleets{token: map[string]string{}}
	for _, fleet := range []string{"work", "home"} {
		f.token[fleet] = minter.Token(fleet, "nonce-"+fleet)
	}
	guard := actorwire.Guard(actorwire.Deps{
		FleetKeys:  func() []string { return []string{"work", "home"} },
		ActorToken: func(fleet string) string { return minter.Token(fleet, "nonce-"+fleet) },
		ShipFleet:  func(string) (string, bool) { return "", false },
		Inbox:      func() []inbox.Item { return nil },
		Source:     func(string) (string, bool) { return "", false },
		Campaign:   func(string) (follow.Followed, bool) { return follow.Followed{}, false },
	})
	mux := http.NewServeMux()
	(&fleetmap.Service{Home: home, Config: m.Current}).Routes(mux)
	(&crewapi.Service{Home: home, Config: m}).Routes(mux)
	f.h = guard.Middleware(mux)
	return f
}

func (f *mapFleets) as(fleet, path string) *httptest.ResponseRecorder {
	req := httptest.NewRequest("GET", path, nil)
	req.RemoteAddr = "127.0.0.1:50000"
	req.Host = "127.0.0.1:7420"
	req.Header.Set(actor.Header, f.token[fleet])
	rec := httptest.NewRecorder()
	f.h.ServeHTTP(rec, req)
	return rec
}

func TestAFleetsActorReadsItsOwnMapAndNothingOfAnotherFleetsThroughTheServer(t *testing.T) {
	f := newMapFleets(t)
	mine := map[string][]string{
		"work": {"acme", "bosun-of-work", "skill-of-work"},
		"home": {"hobby", "cook-of-home", "skill-of-home"},
	}
	for _, c := range []struct{ fleet, other string }{{"work", "home"}, {"home", "work"}} {
		for _, read := range []string{"repos", "cloneable", "docks", "crew", "crew/skills"} {
			rec := f.as(c.fleet, "/api/fleets/"+c.fleet+"/"+read)
			if rec.Code != http.StatusOK {
				t.Errorf("%s's actor reading its own %s: %d %s; want 200", c.fleet, read, rec.Code, rec.Body)
				continue
			}
			for _, m := range mine[c.other] {
				if strings.Contains(rec.Body.String(), m) {
					t.Errorf("%s's %s read holds %q of fleet %s:\n%s", c.fleet, read, m, c.other, rec.Body)
				}
			}
			// the same read of the other fleet's route is refused outright
			if rec := f.as(c.fleet, "/api/fleets/"+c.other+"/"+read); rec.Code != http.StatusForbidden || !strings.Contains(rec.Body.String(), c.other) {
				t.Errorf("%s's actor reading fleet %s's %s: %d %s; want 403 naming %s", c.fleet, c.other, read, rec.Code, rec.Body, c.other)
			}
		}
	}
	if body := f.as("work", "/api/fleets/work/crew").Body.String(); !strings.Contains(body, mine["work"][1]) {
		t.Errorf("work's crew read = %s, want its own member", body)
	}
	if body := f.as("work", "/api/fleets/work/crew/skills").Body.String(); !strings.Contains(body, mine["work"][2]) {
		t.Errorf("work's skills read = %s, want its own skill", body)
	}
}
