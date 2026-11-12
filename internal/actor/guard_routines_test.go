package actor_test

import "testing"

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
