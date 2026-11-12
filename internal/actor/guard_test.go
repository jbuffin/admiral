package actor_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/jbuffin/admiral/internal/actor"
)

type rig struct {
	g       *actor.Guard
	h       http.Handler
	reached int
	fleet   string
	acting  bool
}

// Tokens: tok-personal and tok-work. Every ship, item, source and campaign named "w-*" belongs to work, "p-*" to personal; "ghost" has no known fleet.
func newRig(t *testing.T) *rig {
	t.Helper()
	r := &rig{}
	of := func(id string) (string, bool) {
		switch {
		case strings.HasPrefix(id, "w-"):
			return "work", true
		case strings.HasPrefix(id, "p-"):
			return "personal", true
		}
		return "", false
	}
	r.g = &actor.Guard{
		Tokens: func(tok string) (string, bool) {
			switch tok {
			case "tok-personal":
				return "personal", true
			case "tok-work":
				return "work", true
			}
			return "", false
		},
		ShipFleet:     of,
		ItemFleet:     func(session, _ string) (string, bool) { return of(session) },
		SourceFleet:   of,
		CampaignFleet: of,
	}
	r.h = r.g.Middleware(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		r.reached++
		r.fleet, r.acting = actor.FleetOf(req.Context())
		w.WriteHeader(http.StatusOK)
	}))
	return r
}

const (
	local = "127.0.0.1:50000"
	lan   = "192.168.1.20:50000"
)

func (r *rig) do(method, target, body, token, remote string) *httptest.ResponseRecorder {
	r.reached = 0
	req := httptest.NewRequest(method, target, strings.NewReader(body))
	req.RemoteAddr = remote
	req.Host = "127.0.0.1:7420"
	if remote == lan {
		req.Host = "192.168.1.10:7420"
	}
	if token != "" {
		req.Header.Set(actor.Header, token)
	}
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	rec := httptest.NewRecorder()
	r.h.ServeHTTP(rec, req)
	return rec
}

func TestAValidTokenMakesTheRequestAdmiralBotsForItsFleet(t *testing.T) {
	r := newRig(t)
	if c := r.do("GET", "/api/ships", "", "tok-personal", local).Code; c != 200 || r.reached != 1 || !r.acting || r.fleet != "personal" {
		t.Errorf("code %d, reached %d, acting %v for %q; want 200 and the handler told it acts for personal", c, r.reached, r.acting, r.fleet)
	}
}

func TestNoTokenOnLoopbackIsTheEngineersOwnRequest(t *testing.T) {
	r := newRig(t)
	for _, c := range []struct{ method, target string }{{"GET", "/api/config"}, {"POST", "/api/ships/w-1/answer"}, {"PUT", "/api/fleets/work"}} {
		if code := r.do(c.method, c.target, "{}", "", local).Code; code != 200 || r.reached != 1 || r.acting {
			t.Errorf("%s %s with no token: code %d, reached %d, acting %v; want it through as the engineer", c.method, c.target, code, r.reached, r.acting)
		}
	}
}

func TestAnUnknownOrStaleTokenIsRefusedNotTreatedAsTheEngineer(t *testing.T) {
	r := newRig(t)
	for _, tok := range []string{"tok-stale", "tok-personal "} {
		if c := r.do("GET", "/api/ships", "", tok, local).Code; c != 401 || r.reached != 0 {
			t.Errorf("token %q: code %d, reached %d; want 401 and the handler not reached", tok, c, r.reached)
		}
	}
}

func TestTheTokenOffLoopbackIsRefusedEvenWhenValid(t *testing.T) {
	r := newRig(t)
	if c := r.do("GET", "/api/ships", "", "tok-personal", lan).Code; c < 400 || r.reached != 0 {
		t.Errorf("a LAN request with a valid token: code %d, reached %d; want a refusal", c, r.reached)
	}
}

func TestAnActorReachesOnlyTheRoutesAdmiralBotMayCall(t *testing.T) {
	own := `{"fleet":"personal"}`
	for _, prefix := range []string{"/api", "/api/v1"} {
		for _, c := range []struct{ method, path, body string }{
			{"GET", "/ships", ""},
			{"GET", "/ships/p-1/conversation", ""},
			{"GET", "/inbox", ""},
			{"GET", "/sources", ""},
			{"GET", "/campaigns", ""},
			{"GET", "/campaigns/p-1", ""},
			{"POST", "/ships", own},
			{"POST", "/sources/p-1/orders", `{"text":"x"}`},
			{"POST", "/inbox/p-1/i-1/answer", `{}`},
			{"POST", "/ships/p-1/stop", ""},
			{"POST", "/ships/p-1/dismiss", ""},
		} {
			r := newRig(t)
			if code := r.do(c.method, prefix+c.path, c.body, "tok-personal", local).Code; code != 200 || r.reached != 1 || !r.acting {
				t.Errorf("%s %s%s: code %d, reached %d, acting %v; want AdmiralBot let through as the actor", c.method, prefix, c.path, code, r.reached, r.acting)
			}
		}
	}
}

func TestAnActorIsRefusedEverythingOffTheList(t *testing.T) {
	for _, prefix := range []string{"/api", "/api/v1"} {
		for _, c := range []struct{ method, path string }{
			{"POST", "/ships/p-1/answer"},
			{"POST", "/ships/p-1/crew/boat/answer"},
			{"POST", "/ships/p-1/messages"},
			{"POST", "/ships/p-1/interrupt"},
			{"POST", "/ships/p-1/wake"},
			{"GET", "/config"},
			{"PUT", "/fleets/personal"},
			{"POST", "/sources"},
			{"PUT", "/sources/p-1"},
			{"DELETE", "/sources/p-1"},
			{"GET", "/sources/p-1/settings"},
			{"PUT", "/sources/p-1/orders/o-1"},
			{"POST", "/campaigns"},
			{"GET", "/devices"},
			{"POST", "/inbox/p-1/i-1/submit"},
		} {
			r := newRig(t)
			if code := r.do(c.method, prefix+c.path, "{}", "tok-personal", local).Code; code != 403 || r.reached != 0 {
				t.Errorf("%s %s%s: code %d, reached %d; want 403 and the handler not reached", c.method, prefix, c.path, code, r.reached)
			}
		}
	}
}

func TestAnActorCannotSlipPastTheListByHowThePathIsWritten(t *testing.T) {
	for _, target := range []string{
		"/api/ships/p-1/stop/../answer",
		"/api/ships/p-1/answer/",
		"/api//ships/p-1/answer",
		"/api/v1/v1/ships/p-1/answer",
		"/api/ships/p-1%2Fanswer",
		"/ws",
		"/ws/v1",
		"/ws/bridge/p-1",
		"/ws/v1/bridge/p-1",
	} {
		r := newRig(t)
		r.do("POST", target, "{}", "tok-personal", local)
		if r.reached != 0 {
			t.Errorf("POST %s reached the handler as AdmiralBot", target)
		}
	}
}

func TestAnActorTouchingAnotherFleetIsRefusedByNamingIt(t *testing.T) {
	for _, c := range []struct{ method, target, body string }{
		{"POST", "/api/ships/w-1/stop", ""},
		{"POST", "/api/ships/w-1/dismiss", ""},
		{"GET", "/api/ships/w-1/conversation", ""},
		{"POST", "/api/inbox/w-1/i-1/answer", `{}`},
		{"POST", "/api/ships", `{"fleet":"work"}`},
		{"POST", "/api/ships", `{"fleet":"personal","source":"w-1"}`},
		{"POST", "/api/sources/w-1/orders", `{"text":"x"}`},
		{"GET", "/api/campaigns/w-1", ""},
		{"POST", "/api/v1/ships/w-1/stop", ""},
	} {
		r := newRig(t)
		rec := r.do(c.method, c.target, c.body, "tok-personal", local)
		if rec.Code != 403 || r.reached != 0 || !strings.Contains(rec.Body.String(), "work") {
			t.Errorf("%s %s %s: code %d, reached %d, body %q; want 403 naming fleet work and nothing reached", c.method, c.target, c.body, rec.Code, r.reached, rec.Body)
		}
	}
}

func TestAnInboxItemOfNoKnownFleetIsRefusedWhenAnswered(t *testing.T) {
	r := newRig(t)
	rec := r.do("POST", "/api/inbox/ghost/i-1/answer", `{}`, "tok-personal", local)
	if rec.Code != 403 || r.reached != 0 || !strings.Contains(strings.ToLower(rec.Body.String()), "unknown") {
		t.Errorf("code %d, reached %d, body %q; want 403 saying the item's fleet is unknown", rec.Code, r.reached, rec.Body)
	}
}

func TestAnEngineersRequestIsNotFencedToAFleet(t *testing.T) {
	r := newRig(t)
	if c := r.do("POST", "/api/ships/w-1/stop", "", "", local).Code; c != 200 || r.reached != 1 {
		t.Errorf("code %d, reached %d; want the engineer's stop of a ship in any fleet through", c, r.reached)
	}
}
