package actor_test

import (
	"strings"
	"testing"
)

var fleetMapReads = []string{"repos", "cloneable", "docks", "crew", "crew/skills"}

func TestAnActorMayReadItsOwnFleetsMapAndNothingLeaksToAnother(t *testing.T) {
	r := newRig(t)
	for _, read := range fleetMapReads {
		if code := r.do("GET", "/api/fleets/work/"+read, "", "tok-work", local).Code; code != 200 || r.reached != 1 || r.fleet != "work" || !r.acting {
			t.Errorf("GET its own %s: code %d, reached %d for %q; want 200 acting for work", read, code, r.reached, r.fleet)
		}
		rec := r.do("GET", "/api/fleets/personal/"+read, "", "tok-work", local)
		body := rec.Body.String()
		if rec.Code != 403 || r.reached != 0 || !strings.Contains(body, "fleet personal is not fleet work") {
			t.Errorf("GET another fleet's %s: code %d, reached %d, body %q; want 403 naming both fleets, handler not reached", read, rec.Code, r.reached, body)
		}
		if strings.Contains(body, "routines") || strings.Contains(body, "standing orders") {
			t.Errorf("the refusal of another fleet's %s says %q, want wording that fits a map read", read, body)
		}
	}
}

func TestTheFleetMapReadsAreReadsOnlyAndNoCrossFleetRouteIsOpenToAnActor(t *testing.T) {
	r := newRig(t)
	for _, c := range []struct{ method, path, why string }{
		{"POST", "/api/fleets/work/repos", "writing the repo list"},
		{"POST", "/api/fleets/work/docks", "writing docks"},
		{"PUT", "/api/fleets/work/cloneable", "writing the clone rule"},
		{"POST", "/api/fleets/work/crew/members", "adding a crew member"},
		{"PUT", "/api/fleets/work/crew/members/bosun", "editing a crew member"},
		{"POST", "/api/fleets/work/crew/notes", "writing crew notes"},
		{"POST", "/api/fleets/work/crew/draft", "the crew drafter"},
		{"GET", "/api/harness-capabilities", "every fleet's docks"},
		{"POST", "/api/harness-capabilities/check", "running a probe"},
		{"GET", "/api/config", "every fleet's config"},
		{"GET", "/api/launch-orders/skills?fleet=personal", "a skills read whose fleet the guard can't see"},
		{"GET", "/api/launch-orders/crew?fleet=personal", "a crew read whose fleet the guard can't see"},
		{"POST", "/api/launch-orders/dirs", "the per-launch directory read"},
	} {
		if code := r.do(c.method, c.path, `{"name":"x"}`, "tok-work", local).Code; code != 403 || r.reached != 0 {
			t.Errorf("%s %s (%s): code %d, reached %d; want 403 and the handler not reached", c.method, c.path, c.why, code, r.reached)
		}
	}
}
