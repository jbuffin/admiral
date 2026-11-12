package routines_test

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/jbuffin/admiral/internal/actor"
	"github.com/jbuffin/admiral/internal/routines"
)

type api struct {
	t   *testing.T
	rig *rig
	srv *httptest.Server
	bot bool // requests carry AdmiralBot's actor context
}

func newAPI(t *testing.T) *api {
	t.Helper()
	a := &api{t: t, rig: newRig(t)}
	mux := http.NewServeMux()
	(&routines.Service{Store: a.rig.store}).Routes(mux)
	a.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if a.bot {
			r = r.WithContext(actor.WithFleet(r.Context(), "work"))
		}
		mux.ServeHTTP(w, r)
	}))
	t.Cleanup(a.srv.Close)
	return a
}

func (a *api) do(method, path, body string) (int, map[string]any) {
	a.t.Helper()
	req, _ := http.NewRequest(method, a.srv.URL+path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		a.t.Fatal(err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	var out map[string]any
	if len(bytes.TrimSpace(raw)) > 0 {
		if err := json.Unmarshal(raw, &out); err != nil {
			a.t.Fatalf("%s %s answered %d with a body that is not a JSON object: %q", method, path, resp.StatusCode, raw)
		}
	}
	return resp.StatusCode, out
}

const routineBody = `{"name":"Nightly sweep","enabled":true,"when":"30 8 * * 1-5","before_acting":"ask_first","notify":true,
 "do":{"launch":{"harness":"claude","dir":"/tmp/repo","text":"sweep the board","args":["--effort","high"]}}}`

func (a *api) post() map[string]any {
	a.t.Helper()
	code, body := a.do("POST", "/api/fleets/work/routines", routineBody)
	if code != http.StatusCreated {
		a.t.Fatalf("POST routines = %d %v, want 201", code, body)
	}
	return body
}

func wantError(t *testing.T, code int, body map[string]any, want int) {
	t.Helper()
	if code != want {
		t.Fatalf("status = %d %v, want %d", code, body, want)
	}
	if msg, _ := body["error"].(string); msg == "" {
		t.Fatalf("a %d answers {\"error\": …}, got %v", want, body)
	}
}

func TestListIsAnEmptyArrayNotNull(t *testing.T) {
	a := newAPI(t)
	code, body := a.do("GET", "/api/fleets/work/routines", "")
	if code != http.StatusOK {
		t.Fatalf("GET routines = %d %v", code, body)
	}
	if list, ok := body["routines"].([]any); !ok || len(list) != 0 {
		t.Fatalf("routines = %#v, want []", body["routines"])
	}
}

func TestCreateAnswers201WithTheRoutineInSnakeCase(t *testing.T) {
	a := newAPI(t)
	got := a.post()
	if got["id"] == "" || got["id"] == nil || got["name"] != "Nightly sweep" || got["enabled"] != true ||
		got["when"] != "30 8 * * 1-5" || got["before_acting"] != "ask_first" || got["notify"] != true {
		t.Fatalf("routine = %v", got)
	}
	for _, k := range []string{"created_at", "updated_at"} {
		if s, _ := got[k].(string); s == "" {
			t.Errorf("routine lacks %s: %v", k, got)
		}
	}
	launch, _ := got["do"].(map[string]any)["launch"].(map[string]any)
	if launch["harness"] != "claude" || launch["dir"] != "/tmp/repo" || launch["text"] != "sweep the board" {
		t.Fatalf("do.launch = %v", launch)
	}
	_, list := a.do("GET", "/api/fleets/work/routines", "")
	if l, _ := list["routines"].([]any); len(l) != 1 {
		t.Fatalf("list after create = %v", list)
	}
}

func TestUpdateDeleteAndTheSwitchRoutes(t *testing.T) {
	a := newAPI(t)
	id := a.post()["id"].(string)
	base := "/api/fleets/work/routines/" + id

	code, body := a.do("PUT", base, strings.Replace(routineBody, "Nightly sweep", "Renamed", 1))
	if code != http.StatusOK || body["name"] != "Renamed" {
		t.Fatalf("PUT = %d %v", code, body)
	}
	if code, body = a.do("POST", base+"/off", ""); code != http.StatusOK || body["enabled"] != false {
		t.Fatalf("POST off = %d %v", code, body)
	}
	if code, body = a.do("POST", base+"/on", ""); code != http.StatusOK || body["enabled"] != true {
		t.Fatalf("POST on = %d %v", code, body)
	}
	if code, _ = a.do("DELETE", base, ""); code != http.StatusNoContent {
		t.Fatalf("DELETE = %d, want 204", code)
	}
	code, body = a.do("DELETE", base, "")
	wantError(t, code, body, http.StatusNotFound)
}

func TestRoutineRoutesAnswer404ForAnUnknownFleetAndRoutine(t *testing.T) {
	a := newAPI(t)
	a.post()
	for _, c := range []struct{ method, path string }{
		{"GET", "/api/fleets/ghost/routines"},
		{"POST", "/api/fleets/ghost/routines"},
		{"GET", "/api/fleets/work/routines/nope/runs"},
		{"POST", "/api/fleets/work/routines/nope/on"},
		{"PUT", "/api/fleets/work/routines/nope"},
	} {
		code, body := a.do(c.method, c.path, routineBody)
		t.Run(c.method+" "+c.path, func(t *testing.T) { wantError(t, code, body, http.StatusNotFound) })
	}
}

func TestASaveThatFailsValidationIs400WithTheReason(t *testing.T) {
	a := newAPI(t)
	good := a.post()
	bad := strings.Replace(routineBody, "30 8 * * 1-5", "0 30 8 * * 1-5", 1)
	code, body := a.do("POST", "/api/fleets/work/routines", bad)
	wantError(t, code, body, http.StatusBadRequest)
	if !strings.Contains(strings.ToLower(body["error"].(string)), "cron") {
		t.Errorf("the error does not name the cron: %v", body["error"])
	}
	code, body = a.do("PUT", "/api/fleets/work/routines/"+good["id"].(string), bad)
	wantError(t, code, body, http.StatusBadRequest)
	code, body = a.do("POST", "/api/fleets/work/routines", "{not json")
	wantError(t, code, body, http.StatusBadRequest)
	_, list := a.do("GET", "/api/fleets/work/routines", "")
	if l, _ := list["routines"].([]any); len(l) != 1 {
		t.Fatalf("a refused save changed the list: %v", list)
	}
}

func TestRunsAreARoutinesRecordsNewestFirstInSnakeCase(t *testing.T) {
	a := newAPI(t)
	id := a.post()["id"].(string)
	var first routines.Trigger
	for i, o := range []routines.Outcome{routines.Launched, routines.Missed} {
		a.rig.now = t0.Add(time.Duration(i) * time.Hour)
		rec, err := a.rig.store.AppendTrigger("work", routines.Trigger{Routine: id, Outcome: o, Tier: "T0", Ship: "ship-a"})
		if err != nil {
			t.Fatal(err)
		}
		if i == 0 {
			first = rec
		}
	}
	if err := a.rig.store.Undo("work", first.ID); err != nil {
		t.Fatal(err)
	}
	code, body := a.do("GET", "/api/fleets/work/routines/"+id+"/runs", "")
	runs, _ := body["runs"].([]any)
	if code != http.StatusOK || len(runs) != 2 {
		t.Fatalf("GET runs = %d %v", code, body)
	}
	newest, oldest := runs[0].(map[string]any), runs[1].(map[string]any)
	if newest["outcome"] != "missed" || oldest["outcome"] != "launched" || oldest["undone"] != true ||
		oldest["routine"] != id || oldest["tier"] != "T0" || oldest["ship"] != "ship-a" {
		t.Fatalf("runs = %v", runs)
	}
}

func TestRunsOfARoutineWithNoneIsAnEmptyArray(t *testing.T) {
	a := newAPI(t)
	id := a.post()["id"].(string)
	_, body := a.do("GET", "/api/fleets/work/routines/"+id+"/runs", "")
	if runs, ok := body["runs"].([]any); !ok || len(runs) != 0 {
		t.Fatalf("runs = %#v, want []", body["runs"])
	}
}

func TestHoldFireRoutesTurnItOnAndOffForAFleet(t *testing.T) {
	a := newAPI(t)
	code, body := a.do("GET", "/api/fleets/work/hold-fire", "")
	if code != http.StatusOK || body["on"] != false {
		t.Fatalf("GET hold-fire = %d %v", code, body)
	}
	code, body = a.do("POST", "/api/fleets/work/hold-fire/on", "")
	fleet, _ := body["fleet"].(map[string]any)
	if code != http.StatusOK || body["on"] != true || fleet["on"] != true || fleet["by"] != "engineer" {
		t.Fatalf("POST hold-fire/on = %d %v", code, body)
	}
	if at, _ := fleet["at"].(string); at == "" {
		t.Fatalf("the switch records when: %v", fleet)
	}
	if _, other := a.do("GET", "/api/fleets/play/hold-fire", ""); other["on"] != false {
		t.Fatalf("play went on with work: %v", other)
	}
	code, body = a.do("POST", "/api/fleets/work/hold-fire/off", "")
	if code != http.StatusOK || body["on"] != false {
		t.Fatalf("POST hold-fire/off = %d %v", code, body)
	}
}

func TestGlobalHoldFireShowsInEveryFleetsAnswer(t *testing.T) {
	a := newAPI(t)
	code, body := a.do("POST", "/api/hold-fire/on", "")
	if code != http.StatusOK || body["on"] != true || body["by"] != "engineer" {
		t.Fatalf("POST /api/hold-fire/on = %d %v", code, body)
	}
	_, work := a.do("GET", "/api/fleets/work/hold-fire", "")
	global, _ := work["global"].(map[string]any)
	if work["on"] != true || global["on"] != true {
		t.Fatalf("a fleet's answer carries the global switch: %v", work)
	}
	a.do("POST", "/api/fleets/work/hold-fire/off", "")
	if _, again := a.do("GET", "/api/fleets/work/hold-fire", ""); again["on"] != true {
		t.Fatalf("the fleet's off leaves the global switch on: %v", again)
	}
	if _, g := a.do("POST", "/api/hold-fire/off", ""); g["on"] != false {
		t.Fatalf("POST /api/hold-fire/off = %v", g)
	}
	if code, body := a.do("GET", "/api/hold-fire", ""); code != http.StatusOK || body["on"] != false {
		t.Fatalf("GET /api/hold-fire = %d %v", code, body)
	}
}

func TestHoldFireForAnUnknownFleetIs404(t *testing.T) {
	a := newAPI(t)
	if code, body := a.do("POST", "/api/fleets/work/hold-fire/on", ""); code != http.StatusOK {
		t.Fatalf("baseline POST hold-fire/on = %d %v", code, body)
	}
	code, body := a.do("POST", "/api/fleets/ghost/hold-fire/on", "")
	wantError(t, code, body, http.StatusNotFound)
}

func TestAHoldFireTurnedOnByAdmiralBotSaysSoAndItCannotTurnItOff(t *testing.T) {
	a := newAPI(t)
	a.bot = true
	code, body := a.do("POST", "/api/fleets/work/hold-fire/on", "")
	fleet, _ := body["fleet"].(map[string]any)
	if code != http.StatusOK || fleet["by"] != "admiralbot" {
		t.Fatalf("on by AdmiralBot = %d %v", code, body)
	}
	code, body = a.do("POST", "/api/fleets/work/hold-fire/off", "")
	wantError(t, code, body, http.StatusForbidden)
	code, body = a.do("POST", "/api/hold-fire/off", "")
	wantError(t, code, body, http.StatusForbidden)
	a.bot = false
	if _, now := a.do("GET", "/api/fleets/work/hold-fire", ""); now["on"] != true {
		t.Fatalf("the refused release turned Hold fire off: %v", now)
	}
}

func TestListCarriesEachRoutinesLastRunAndNothingForOneThatNeverRan(t *testing.T) {
	a := newAPI(t)
	ran := a.post()
	never := a.post()
	at := time.Date(2026, 11, 12, 9, 38, 0, 0, time.UTC)
	for _, o := range []routines.Outcome{routines.Skipped, routines.Failed} {
		if _, err := a.rig.store.AppendTrigger("work", routines.Trigger{Routine: ran["id"].(string), At: at, Outcome: o, Tier: "T0"}); err != nil {
			t.Fatal(err)
		}
		at = at.Add(time.Minute)
	}
	_, body := a.do("GET", "/api/fleets/work/routines", "")
	for _, e := range body["routines"].([]any) {
		r := e.(map[string]any)
		last, has := r["last_run"].(map[string]any)
		switch r["id"] {
		case ran["id"]:
			if !has || last["outcome"] != "failed" || last["at"] != "2026-11-12T09:39:00Z" {
				t.Errorf("ran routine's last_run = %v, want the newest record", r["last_run"])
			}
		case never["id"]:
			if has {
				t.Errorf("a routine that never ran has last_run = %v", r["last_run"])
			}
		}
	}
}
