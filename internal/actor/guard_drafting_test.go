package actor_test

import (
	"strings"
	"testing"
)

func TestAnActorMayReadItsOwnFleetsDraftingSkillWithOrWithoutADirAndNoOthers(t *testing.T) {
	r := newRig(t)
	for _, target := range []string{"/api/fleets/work/drafting", "/api/fleets/work/drafting?dir=%2Fcode%2Fwidget"} {
		if code := r.do("GET", target, "", "tok-work", local).Code; code != 200 || r.reached != 1 || r.fleet != "work" || !r.acting {
			t.Errorf("GET %s: code %d, reached %d for %q; want 200 acting for work", target, code, r.reached, r.fleet)
		}
		r.reached = 0
	}
	rec := r.do("GET", "/api/fleets/personal/drafting", "", "tok-work", local)
	if rec.Code != 403 || r.reached != 0 || !strings.Contains(rec.Body.String(), "fleet personal is not fleet work") {
		t.Errorf("GET another fleet's drafting skill: code %d, reached %d, body %q; want 403 naming both fleets, handler not reached", rec.Code, r.reached, rec.Body)
	}
}

func TestAnActorNeverWritesTheDraftingSkill(t *testing.T) {
	r := newRig(t)
	for _, c := range []struct{ method, path string }{
		{"PUT", "/api/fleets/work/drafting"},
		{"POST", "/api/fleets/work/drafting"},
		{"DELETE", "/api/fleets/work/drafting"},
	} {
		if code := r.do(c.method, c.path, `{"skill":"wayfinder"}`, "tok-work", local).Code; code != 403 || r.reached != 0 {
			t.Errorf("%s %s: code %d, reached %d; want 403 and the handler not reached", c.method, c.path, code, r.reached)
		}
	}
}
