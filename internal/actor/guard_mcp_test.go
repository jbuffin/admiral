package actor_test

import (
	"strings"
	"testing"
)

func TestAnActorMayGiveAShipItsNextPromptAndWakeAMooredOneInItsOwnFleet(t *testing.T) {
	for _, prefix := range []string{"/api", "/api/v1"} {
		for _, path := range []string{"/ships/p-1/next", "/ships/p-1/wake"} {
			r := newRig(t)
			if code := r.do("POST", prefix+path, `{"text":"carry on"}`, "tok-personal", local).Code; code != 200 || r.reached != 1 || !r.acting || r.fleet != "personal" {
				t.Errorf("POST %s%s: code %d, reached %d, acting %v for %q; want AdmiralBot let through for personal", prefix, path, code, r.reached, r.acting, r.fleet)
			}
		}
	}
}

func TestAnActorCannotGiveAnotherFleetsShipANextPromptOrWakeIt(t *testing.T) {
	for _, path := range []string{"/api/ships/w-1/next", "/api/ships/w-1/wake", "/api/v1/ships/w-1/next"} {
		r := newRig(t)
		rec := r.do("POST", path, `{"text":"carry on"}`, "tok-personal", local)
		if rec.Code != 403 || r.reached != 0 || !strings.Contains(rec.Body.String(), "work") {
			t.Errorf("POST %s: code %d, reached %d, body %q; want 403 naming fleet work and nothing reached", path, rec.Code, r.reached, rec.Body)
		}
	}
}

func TestAnActorMayReadOutcomesAndUseTheChatQuestionRoutes(t *testing.T) {
	for _, c := range []struct{ method, path, body string }{
		{"GET", "/api/outcomes?ship=p-1&limit=20", ""},
		{"GET", "/api/v1/outcomes", ""},
		{"POST", "/api/chat/questions", `{"title":"t","questions":[]}`},
		{"GET", "/api/chat/questions/20260101T000000-ab12cd34/answer?wait=5", ""},
	} {
		r := newRig(t)
		if code := r.do(c.method, c.path, c.body, "tok-personal", local).Code; code != 200 || r.reached != 1 || !r.acting || r.fleet != "personal" {
			t.Errorf("%s %s: code %d, reached %d, acting %v for %q; want AdmiralBot let through for personal", c.method, c.path, code, r.reached, r.acting, r.fleet)
		}
	}
}
