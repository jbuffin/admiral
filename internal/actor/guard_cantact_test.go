package actor_test

import (
	"net/http"
	"strings"
	"testing"
)

// While a turn that can't act runs in a fleet's chat, the guard refuses AdmiralBot's writes for that fleet only.

func cantActRig(t *testing.T) (*rig, *map[string]bool) {
	t.Helper()
	r := newRig(t)
	running := map[string]bool{}
	r.g.TurnCantAct = func(fleet string) bool { return running[fleet] }
	return r, &running
}

var writes = []struct{ method, path, body string }{
	{"POST", "/api/ships", `{"fleet":"work"}`},
	{"POST", "/api/sources/w-src/orders", `{}`},
	{"POST", "/api/inbox/w-1/q-1/answer", `{}`},
	{"POST", "/api/ships/w-1/stop", ""},
	{"POST", "/api/ships/w-1/dismiss", ""},
	{"POST", "/api/ships/w-1/next", ""},
	{"POST", "/api/ships/w-1/wake", ""},
	{"POST", "/api/chat/questions", `{}`},
	{"POST", "/api/fleets/work/routines/r1/off", ""},
	{"POST", "/api/fleets/work/routines/draft", `{}`},
	{"POST", "/api/fleets/work/campaign-drafts", `{"repo":"acme/widgets","title":"T","body":"B"}`},
	{"POST", "/api/fleets/work/hold-fire/on", ""},
	{"POST", "/api/chat/notes", `{}`},
	{"POST", "/api/chat/commitments", `{}`},
	{"POST", "/api/chat/campaign-commitments", `{}`},
}

func TestEveryWriteIsRefusedForTheFleetWhoseTurnCantAct(t *testing.T) {
	r, running := cantActRig(t)
	(*running)["work"] = true
	for _, w := range writes {
		rec := r.do(w.method, w.path, w.body, "tok-work", local)
		if rec.Code != http.StatusForbidden || r.reached != 0 {
			t.Errorf("%s %s: code %d, reached %d; want 403 and the handler not reached", w.method, w.path, rec.Code, r.reached)
			continue
		}
		body := rec.Body.String()
		if !strings.Contains(body, `"error"`) || !strings.Contains(body, "plain text") {
			t.Errorf("%s %s: body %q; want a JSON error that tells AdmiralBot to offer the action in plain text", w.method, w.path, body)
		}
		if strings.Contains(strings.ToLower(body), "report") {
			t.Errorf("%s %s: body %q names a report; the gate serves every turn that can't act", w.method, w.path, body)
		}
	}
}

func TestReadsStillPassWhileATurnCantAct(t *testing.T) {
	r, running := cantActRig(t)
	(*running)["work"] = true
	for _, path := range []string{"/api/ships", "/api/inbox", "/api/chat/questions/q1/answer", "/api/fleets/work/routines"} {
		if rec := r.do("GET", path, "", "tok-work", local); rec.Code != http.StatusOK || r.reached != 1 {
			t.Errorf("GET %s: code %d, reached %d; want 200 and reached", path, rec.Code, r.reached)
		}
	}
}

func TestTheGateCoversOnlyTheFleetWhoseTurnRunsAndEndsWithIt(t *testing.T) {
	r, running := cantActRig(t)
	(*running)["work"] = true
	if rec := r.do("POST", "/api/ships", `{"fleet":"personal"}`, "tok-personal", local); rec.Code != http.StatusOK {
		t.Errorf("personal's launch while work's turn runs: %d %s; want 200", rec.Code, rec.Body)
	}
	if rec := r.do("POST", "/api/ships", `{"fleet":"work"}`, "tok-work", local); rec.Code != http.StatusForbidden {
		t.Fatalf("work's launch during its turn: %d; want 403", rec.Code)
	}
	(*running)["work"] = false
	if rec := r.do("POST", "/api/ships", `{"fleet":"work"}`, "tok-work", local); rec.Code != http.StatusOK || r.reached != 1 {
		t.Errorf("work's launch after its turn: %d %s; want 200", rec.Code, rec.Body)
	}
}

func TestTheEngineersWritesAreNeverGatedByATurnThatCantAct(t *testing.T) {
	r, running := cantActRig(t)
	(*running)["work"] = true
	if rec := r.do("POST", "/api/ships", `{"fleet":"work"}`, "", local); rec.Code != http.StatusOK || r.reached != 1 {
		t.Errorf("the engineer's launch: %d %s; want 200", rec.Code, rec.Body)
	}
}
