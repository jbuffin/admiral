package ships

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"testing"

	"github.com/jbuffin/admiral/internal/actor"
	"github.com/jbuffin/admiral/internal/launcher"
	"github.com/jbuffin/admiral/internal/sessionhost"
)

// The findings route and the scout's result, against a seeded store and no tmux.

const findingsFirst = "2026-11-18T09:30:00Z"

func findingsService(t *testing.T) (*Service, string) {
	t.Helper()
	home := t.TempDir()
	return &Service{Launcher: &launcher.Launcher{Home: home}}, home
}

func (s *Service) seedFindings(t *testing.T, home, session, first, body string) {
	t.Helper()
	p := filepath.Join(home, "src.md")
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	if kept, err := s.findingsKept().Keep(session, first, "work", p); !kept || err != nil {
		t.Fatalf("seeding findings: kept %v, err %v", kept, err)
	}
}

func getFindings(s *Service, session, query string) *httptest.ResponseRecorder {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/ships/{session}/findings", s.serveFindings)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest("GET", "/api/ships/"+session+"/findings"+query, nil))
	return rec
}

func TestTheFindingsRouteAnswersTheKeptVoyageAndRefusesWhatItCannotServe(t *testing.T) {
	s, home := findingsService(t)
	const newer = "2026-11-19T09:30:00Z"
	s.seedFindings(t, home, "dory", findingsFirst, "old\n")
	s.seedFindings(t, home, "dory", newer, "new\n")
	for _, c := range []struct{ query, first, body string }{
		{"?first_launched=" + url.QueryEscape(findingsFirst), findingsFirst, "old\n"},
		{"", newer, "new\n"},
	} {
		rec := getFindings(s, "dory", c.query)
		var got keptFindings
		want := keptFindings{Ship: "dory", FirstLaunched: c.first, Fleet: "work", Findings: c.body}
		if rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &got) != nil || got != want {
			t.Errorf("GET findings%s = %d %s, want 200 with %+v", c.query, rec.Code, rec.Body, want)
		}
	}
	wantError(t, getFindings(s, "dory", "?first_launched=yesterday"), http.StatusBadRequest, "a malformed first_launched")
	wantError(t, getFindings(s, "kestrel", ""), http.StatusNotFound, "a ship with no findings")
	wantError(t, getFindings(s, "dory", "?first_launched="+url.QueryEscape("2026-01-01T00:00:00Z")), http.StatusNotFound, "a voyage with no findings")
}

func TestAScoutIsFailedOnlyWhenItsVoyageHoldsNoKeptFindings(t *testing.T) {
	s, home := findingsService(t)
	scout := func(name string) sessionhost.Session {
		return sessionhost.Session{Name: name, Options: map[string]string{"scout": "1", "fleet": "work", "first-launched": findingsFirst}}
	}
	if kept, failed := s.keepFindings(scout("dory")); kept || !failed {
		t.Errorf("a scout with no findings: kept %v, failed %v, want failed", kept, failed)
	}
	s.seedFindings(t, home, "kestrel", findingsFirst, "round one\n")
	if kept, failed := s.keepFindings(scout("kestrel")); kept || failed {
		t.Errorf("a later round with no file after kept findings: kept %v, failed %v, want neither", kept, failed)
	}
	quick := sessionhost.Session{Name: "plover", Options: map[string]string{"fleet": "work", "first-launched": findingsFirst}}
	if kept, failed := s.keepFindings(quick); kept || failed {
		t.Errorf("a ship that is not a scout: kept %v, failed %v, want neither", kept, failed)
	}
}

func TestAStoreErrorDoesNotFailAScoutThatWroteFindings(t *testing.T) {
	s, home := findingsService(t)
	if err := os.WriteFile(filepath.Join(home, "findings"), []byte("in the way"), 0o600); err != nil {
		t.Fatal(err)
	}
	reports := filepath.Join(home, "reports")
	if err := os.MkdirAll(reports, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(reports, "dory.md"), []byte("# Found\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	sess := sessionhost.Session{Name: "dory", Options: map[string]string{"scout": "1", "fleet": "work", "first-launched": findingsFirst}}
	if kept, failed := s.keepFindings(sess); kept || failed {
		t.Errorf("a store that cannot write: kept %v, failed %v, want neither", kept, failed)
	}
}

func (s *Service) seedFindingsOf(t *testing.T, home, session, first, fleet, body string) {
	t.Helper()
	p := filepath.Join(home, "src-"+fleet+".md")
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	if kept, err := s.findingsKept().Keep(session, first, fleet, p); !kept || err != nil {
		t.Fatalf("seeding findings: kept %v, err %v", kept, err)
	}
}

func getFindingsAs(s *Service, fleet, session, query string) *httptest.ResponseRecorder {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/ships/{session}/findings", s.serveFindings)
	req := httptest.NewRequest("GET", "/api/ships/"+session+"/findings"+query, nil).WithContext(actor.WithFleet(context.Background(), fleet))
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	return rec
}

func TestAnActorReadsFindingsOfItsOwnFleetsShip(t *testing.T) {
	s, home := findingsService(t)
	s.seedFindingsOf(t, home, "dory", findingsFirst, "work", "work findings\n")
	rec := getFindingsAs(s, "work", "dory", "")
	var got keptFindings
	want := keptFindings{Ship: "dory", FirstLaunched: findingsFirst, Fleet: "work", Findings: "work findings\n"}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil || rec.Code != 200 || got != want {
		t.Errorf("work reads dory: %d %s, want 200 with %+v", rec.Code, rec.Body, want)
	}
}

func TestAnActorIsRefusedAnotherFleetsShipAnUnknownShipAndAVoyageWithNoFleetWithOneAndTheSame404(t *testing.T) {
	s, home := findingsService(t)
	s.seedFindingsOf(t, home, "dory", findingsFirst, "home", "home findings\n")
	s.seedFindingsOf(t, home, "plover", findingsFirst, "", "stray findings\n")
	stamp := "?first_launched=" + url.QueryEscape(findingsFirst)
	refusals := map[string]*httptest.ResponseRecorder{
		"another fleet's ship":                getFindingsAs(s, "work", "dory", ""),
		"another fleet's voyage by its stamp": getFindingsAs(s, "work", "dory", stamp),
		"a ship with no kept findings":        getFindingsAs(s, "work", "kestrel", ""),
		"an unknown ship by a stamp":          getFindingsAs(s, "work", "nobody", stamp),
		"a voyage with no fleet":              getFindingsAs(s, "work", "plover", ""),
		"a voyage with no fleet by its stamp": getFindingsAs(s, "work", "plover", stamp),
	}
	want := getFindingsAs(s, "work", "kestrel", "").Body.String()
	wantError(t, getFindingsAs(s, "work", "kestrel", ""), http.StatusNotFound, "a ship with no kept findings")
	for why, rec := range refusals {
		if rec.Code != http.StatusNotFound || rec.Body.String() != want {
			t.Errorf("%s: %d %q, want the same 404 %q as a ship with no findings", why, rec.Code, rec.Body, want)
		}
	}
}

func TestAReusedShipNameShowsAnActorOnlyItsOwnFleetsLatestVoyageAndTheWebTheNewest(t *testing.T) {
	s, home := findingsService(t)
	older := "2026-11-18T09:30:00Z"
	newer := "2026-11-19T09:30:00Z"
	s.seedFindingsOf(t, home, "dory", older, "work", "work voyage\n")
	s.seedFindingsOf(t, home, "dory", newer, "home", "home voyage, newer\n")
	read := func(rec *httptest.ResponseRecorder) keptFindings {
		var got keptFindings
		if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil || rec.Code != 200 {
			t.Fatalf("%d %s, want 200 findings", rec.Code, rec.Body)
		}
		return got
	}
	if got := read(getFindingsAs(s, "work", "dory", "")); got.Findings != "work voyage\n" || got.Fleet != "work" {
		t.Errorf("work without a stamp reads %+v, want its own older voyage", got)
	}
	if got := read(getFindingsAs(s, "home", "dory", "")); got.Findings != "home voyage, newer\n" {
		t.Errorf("home without a stamp reads %+v, want its own voyage", got)
	}
	other := getFindingsAs(s, "work", "dory", "?first_launched="+url.QueryEscape(newer))
	if other.Code != http.StatusNotFound || other.Body.String() != getFindingsAs(s, "work", "kestrel", "").Body.String() {
		t.Errorf("work with home's stamp: %d %q, want the same 404", other.Code, other.Body)
	}
	if got := read(getFindings(s, "dory", "")); got.Fleet != "home" || got.Findings != "home voyage, newer\n" {
		t.Errorf("the web reads %+v, want the newest voyage of any fleet", got)
	}
	if got := read(getFindings(s, "dory", "?first_launched="+url.QueryEscape(older))); got.Fleet != "work" {
		t.Errorf("the web reads %+v by stamp, want the work voyage", got)
	}
}
