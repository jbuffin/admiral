package actorwire_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jbuffin/admiral/internal/actor"
	"github.com/jbuffin/admiral/internal/actorwire"
	"github.com/jbuffin/admiral/internal/campaigns/follow"
	"github.com/jbuffin/admiral/internal/config"
	"github.com/jbuffin/admiral/internal/inbox"
	"github.com/jbuffin/admiral/internal/launcher"
	"github.com/jbuffin/admiral/internal/moor"
	"github.com/jbuffin/admiral/internal/outcomes"
	"github.com/jbuffin/admiral/internal/server"
	"github.com/jbuffin/admiral/internal/sessionhost"
	"github.com/jbuffin/admiral/internal/ships"
	"github.com/jbuffin/admiral/internal/state"
)

// twoFleets runs the real guard over the real ships, inbox and campaign store of fleets work and home.
// Saltpaw is moored in work and Dory in home; each fleet has one question.
type twoFleets struct {
	h        http.Handler
	campaign map[string]string // fleet -> followed campaign id
	token    map[string]string // fleet -> actor token
}

func newTwoFleets(t *testing.T) *twoFleets {
	t.Helper()
	home := t.TempDir()
	st, err := state.Open(home)
	if err != nil {
		t.Fatal(err)
	}
	cfg := &config.Config{Fleets: []config.Fleet{{Key: "work", Name: "Work"}, {Key: "home", Name: "Home"}}}
	w := outcomes.New(filepath.Join(home, "outcomes"), t.Logf)
	t.Cleanup(w.Close)
	shipService := &ships.Service{
		Host:       sessionhost.New("no-such-tmux", filepath.Join(home, "tmux.sock")),
		Launcher:   &launcher.Launcher{Home: home, Config: func() *config.Config { return cfg }},
		State:      st,
		Hub:        server.NewHub(),
		Outcomes:   w,
		MergeGuard: func(h http.Handler) http.Handler { return h },
		GitHub:     func(string) moor.GitHub { return nil },
	}
	for name, fleet := range map[string]string{"Saltpaw": "work", "Dory": "home"} {
		rec := moor.Record{Name: name, Session: strings.ToLower(name), Fleet: fleet, Harness: "claude", Checkout: "/src/widgets",
			PRs: []string{"https://github.com/acme/widgets/pull/7"}, MooredAt: time.Now().UTC().Truncate(time.Second)}
		if err := moor.Write(home, rec); err != nil {
			t.Fatal(err)
		}
	}

	fleetOfShip := map[string]string{"s-work": "work", "s-home": "home"}
	inboxService := &inbox.Service{Home: home, Hub: server.NewHub(), Outcomes: w, Ship: func(session string) (inbox.ShipInfo, bool) {
		f, ok := fleetOfShip[session]
		return inbox.ShipInfo{Name: "Dory", Harness: "claude", Fleet: f, FleetName: f}, ok
	}}
	for session := range fleetOfShip {
		dir := filepath.Join(home, "inbox", session)
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "q-1.json"), []byte(question), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	inboxService.Refresh()

	store, err := follow.OpenStore(home)
	if err != nil {
		t.Fatal(err)
	}
	f := &twoFleets{campaign: map[string]string{}, token: map[string]string{}}
	for fleet, url := range map[string]string{"work": "https://github.com/acme/widgets/issues/1", "home": "https://github.com/acme/widgets/issues/2"} {
		got, _, err := store.Follow(follow.Followed{URL: url, Fleet: fleet, FollowedAt: time.Unix(1000, 0).UTC()})
		if err != nil {
			t.Fatal(err)
		}
		f.campaign[fleet] = got.ID
	}

	minter, err := actor.NewMinter(home)
	if err != nil {
		t.Fatal(err)
	}
	for _, fleet := range []string{"work", "home"} {
		f.token[fleet] = minter.Token(fleet, "nonce-"+fleet)
	}
	guard := actorwire.Guard(actorwire.Deps{
		FleetKeys:  func() []string { return []string{"work", "home"} },
		ActorToken: func(fleet string) string { return minter.Token(fleet, "nonce-"+fleet) },
		ShipFleet:  shipService.FleetOf,
		Inbox:      inboxService.List,
		Source:     func(string) (string, bool) { return "", false },
		Campaign:   store.Get,
	})

	mux := http.NewServeMux()
	shipService.Routes(mux)
	inboxService.Routes(mux)
	mux.HandleFunc("GET /api/campaigns/{id}", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) })
	f.h = guard.Middleware(mux)
	return f
}

const question = `{"version":1,"id":"q-1","kind":"question","created":"2026-10-22T10:00:00Z","title":"Which way?",
 "question":{"questions":[{"id":"1","text":"Pick one","multi":false,"options":[{"label":"Alpha"},{"label":"Beta"}]}]}}`

func (f *twoFleets) as(fleet, method, path, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.RemoteAddr = "127.0.0.1:50000"
	req.Host = "127.0.0.1:7420"
	req.Header.Set(actor.Header, f.token[fleet])
	rec := httptest.NewRecorder()
	f.h.ServeHTTP(rec, req)
	return rec
}

func refusedNaming(t *testing.T, rec *httptest.ResponseRecorder, what, fleet string) {
	t.Helper()
	if rec.Code != http.StatusForbidden || !strings.Contains(rec.Body.String(), fleet) {
		t.Errorf("%s: %d %s; want 403 naming fleet %s", what, rec.Code, rec.Body, fleet)
	}
}

func TestTheServersGuardRefusesAnotherFleetsShipAndLetsItsOwnThrough(t *testing.T) {
	f := newTwoFleets(t)
	refusedNaming(t, f.as("work", "POST", "/api/ships/dory/stop", ""), "work stopping home's ship", "home")
	refusedNaming(t, f.as("work", "POST", "/api/ships/dory/dismiss", ""), "work dismissing home's ship", "home")
	if rec := f.as("work", "POST", "/api/ships/saltpaw/dismiss", ""); rec.Code != http.StatusOK {
		t.Errorf("work dismissing its own moored ship: %d %s; want 200", rec.Code, rec.Body)
	}
}

func TestTheServersGuardRefusesAnotherFleetsInboxItemAndAnswersItsOwn(t *testing.T) {
	f := newTwoFleets(t)
	body := `{"answers":{"1":{"choice":"Alpha"}}}`
	refusedNaming(t, f.as("work", "POST", "/api/inbox/s-home/q-1/answer", body), "work answering home's question", "home")
	if rec := f.as("work", "POST", "/api/inbox/s-work/q-1/answer", body); rec.Code != http.StatusOK {
		t.Errorf("work answering its own question: %d %s; want 200", rec.Code, rec.Body)
	}
}

func TestTheServersGuardRefusesAnotherFleetsCampaignAndAnItemOfNoFleet(t *testing.T) {
	f := newTwoFleets(t)
	refusedNaming(t, f.as("work", "GET", "/api/campaigns/"+f.campaign["home"], ""), "work reading home's campaign", "home")
	if rec := f.as("work", "GET", "/api/campaigns/"+f.campaign["work"], ""); rec.Code != http.StatusOK {
		t.Errorf("work reading its own campaign: %d; want 200", rec.Code)
	}
	if rec := f.as("work", "GET", "/api/campaigns/nobody", ""); rec.Code != http.StatusForbidden {
		t.Errorf("a campaign nobody follows: %d; want 403", rec.Code)
	}
	if rec := f.as("work", "POST", "/api/inbox/s-ghost/q-1/answer", "{}"); rec.Code != http.StatusForbidden {
		t.Errorf("an item of an unknown ship: %d; want 403", rec.Code)
	}
}

func TestTheServersGuardAcceptsEachFleetsOwnTokenAndNoOthers(t *testing.T) {
	f := newTwoFleets(t)
	var got struct{ Ships []struct{ Session string } }
	rec := f.as("home", "GET", "/api/ships", "")
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil || rec.Code != 200 || len(got.Ships) != 1 || got.Ships[0].Session != "dory" {
		t.Errorf("home's ship list = %d %s, want only dory", rec.Code, rec.Body)
	}
	f.token["work"] = "stale-token"
	if rec := f.as("work", "GET", "/api/ships", ""); rec.Code != http.StatusUnauthorized {
		t.Errorf("a stale token: %d; want 401", rec.Code)
	}
}
