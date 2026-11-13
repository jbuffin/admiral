package actor_test

import (
	"strings"
	"testing"
)

func TestAnActorMayReadRoutinesTurnOneOffAndTurnHoldFireOnButNothingElseAboutThem(t *testing.T) {
	r := newRig(t)
	for _, c := range []struct{ method, path string }{
		{"GET", "/api/fleets/work/routines"},
		{"GET", "/api/fleets/work/hold-fire"},
		{"GET", "/api/hold-fire"},
		{"POST", "/api/fleets/work/routines/r1/off"},
		{"POST", "/api/fleets/work/hold-fire/on"},
		{"POST", "/api/hold-fire/on"},
	} {
		if code := r.do(c.method, c.path, "", "tok-work", local).Code; code != 200 || r.reached != 1 || r.fleet != "work" || !r.acting {
			t.Errorf("%s %s as work's AdmiralBot: code %d, reached %d for %q; want 200 acting for work", c.method, c.path, code, r.reached, r.fleet)
		}
	}
	for _, c := range []struct{ method, path, why string }{
		{"GET", "/api/fleets/personal/routines", "another fleet's routines"},
		{"GET", "/api/fleets/personal/hold-fire", "another fleet's Hold fire"},
		{"POST", "/api/fleets/personal/routines/r1/off", "another fleet's routine"},
		{"POST", "/api/fleets/personal/hold-fire/on", "another fleet's Hold fire"},
		{"POST", "/api/fleets/work/routines/r1/on", "turning a routine on"},
		{"POST", "/api/fleets/work/hold-fire/off", "turning Hold fire off"},
		{"POST", "/api/hold-fire/off", "turning every fleet's Hold fire off"},
		{"POST", "/api/fleets/work/routines", "saving a routine"},
		{"PUT", "/api/fleets/work/routines/r1", "editing a routine"},
		{"DELETE", "/api/fleets/work/routines/r1", "deleting a routine"},
	} {
		if code := r.do(c.method, c.path, `{"name":"x"}`, "tok-work", local).Code; code != 403 || r.reached != 0 {
			t.Errorf("%s %s (%s): code %d, reached %d; want 403 and the handler not reached", c.method, c.path, c.why, code, r.reached)
		}
	}
}

func TestAnActorMayReadItsOwnFleetsStandingOrdersAndNothingElseAboutThem(t *testing.T) {
	r := newRig(t)
	if code := r.do("GET", "/api/fleets/work/standing-orders", "", "tok-work", local).Code; code != 200 || r.reached != 1 || r.fleet != "work" || !r.acting {
		t.Errorf("GET its own standing orders: code %d, reached %d for %q; want 200 acting for work", code, r.reached, r.fleet)
	}
	for _, c := range []struct{ method, path, why string }{
		{"GET", "/api/fleets/personal/standing-orders", "another fleet's standing orders"},
		{"PUT", "/api/fleets/work/standing-orders", "filing standing orders"},
		{"POST", "/api/fleets/work/standing-orders/copy", "copying standing orders"},
		{"POST", "/api/fleets/work/standing-orders/draft", "the standing orders drafter"},
		{"POST", "/api/fleets/work/routines/draft", "the routine drafter"},
		{"PUT", "/api/fleets/work", "saving the fleet and its Limits"},
		{"DELETE", "/api/fleets/work/standing-orders", "deleting standing orders"},
	} {
		if code := r.do(c.method, c.path, `{"guidance":"x"}`, "tok-work", local).Code; code != 403 || r.reached != 0 {
			t.Errorf("%s %s (%s): code %d, reached %d; want 403 and the handler not reached", c.method, c.path, c.why, code, r.reached)
		}
	}
}

func TestTheRefusalOfAnotherFleetsStandingOrdersDoesNotCallThemRoutines(t *testing.T) {
	r := newRig(t)
	rec := r.do("GET", "/api/fleets/personal/standing-orders", "", "tok-work", local)
	if body := rec.Body.String(); rec.Code != 403 || !strings.Contains(body, "fleet personal is not fleet work") || strings.Contains(body, "its routines") {
		t.Errorf("code %d body %q, want 403 with wording that fits standing orders as well as routines", rec.Code, body)
	}
}

func TestAdmiralBotCannotMuteLaunchDismissOrReadTheDetailOfAnEventProposal(t *testing.T) {
	r := newRig(t)
	for _, c := range []struct{ method, target string }{
		{"POST", "/api/fleets/work/routines/r1/mute"},
		{"GET", "/api/inbox/_admiral/x1/event"},
		{"POST", "/api/inbox/_admiral/x1/launch"},
		{"POST", "/api/inbox/_admiral/x1/dismiss"},
	} {
		if code := r.do(c.method, c.target, `{"by":"sender","value":"a"}`, "tok-work", local).Code; code != 403 || r.reached != 0 {
			t.Errorf("%s %s as AdmiralBot: code %d, reached %d; want 403 and the handler not reached", c.method, c.target, code, r.reached)
		}
	}
}
