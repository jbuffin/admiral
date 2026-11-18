package actorwire_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/jbuffin/admiral/internal/actor"
	"github.com/jbuffin/admiral/internal/actorwire"
)

// The server's guard takes the chat service's "a turn that can't act is running" lookup from its Deps.

func TestTheServersGuardRefusesWritesDuringATurnThatCantActForThatFleetOnly(t *testing.T) {
	running := map[string]bool{"work": true}
	guard := actorwire.Guard(actorwire.Deps{
		FleetKeys:   func() []string { return []string{"work", "home"} },
		ActorToken:  func(fleet string) string { return "tok-" + fleet },
		TurnCantAct: func(fleet string) bool { return running[fleet] },
	})
	reached := 0
	h := guard.Middleware(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { reached++ }))
	as := func(fleet, method, path string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, path, strings.NewReader(""))
		req.RemoteAddr = "127.0.0.1:50000"
		req.Host = "127.0.0.1:7420"
		req.Header.Set(actor.Header, "tok-"+fleet)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec
	}
	if rec := as("work", "POST", "/api/chat/notes"); rec.Code != http.StatusForbidden || !strings.Contains(rec.Body.String(), "plain text") {
		t.Errorf("work's note during its turn: %d %s; want 403 with the plain-text hint", rec.Code, rec.Body)
	}
	if rec := as("home", "POST", "/api/chat/notes"); rec.Code != http.StatusOK {
		t.Errorf("home's note: %d %s; want 200", rec.Code, rec.Body)
	}
	if rec := as("work", "GET", "/api/ships"); rec.Code != http.StatusOK {
		t.Errorf("work's read during its turn: %d; want 200", rec.Code)
	}
	running["work"] = false
	if rec := as("work", "POST", "/api/chat/notes"); rec.Code != http.StatusOK {
		t.Errorf("work's note after its turn: %d; want 200", rec.Code)
	}
}
