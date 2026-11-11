package e2e

import (
	"encoding/json"
	"net/http"
	"reflect"
	"testing"
	"time"
)

// autopilotWorld starts a world whose config adds the top-level resource
// `phone` (capacity 1, no check) and, when maxShips is not 0, the global cap.
// The cap is the top-level `max_ships` key.
func autopilotWorld(t *testing.T, maxShips int) *world {
	t.Helper()
	w := newWorld(t)
	w.proveGuard()
	w.writeCue("wait go", "exit 0") // the ship stays underway
	var cfg map[string]any
	if err := json.Unmarshal([]byte(w.config()), &cfg); err != nil {
		t.Fatal(err)
	}
	cfg["resources"] = map[string]any{"phone": map[string]any{"capacity": 1}}
	if maxShips != 0 {
		cfg["max_ships"] = maxShips
	}
	b, _ := json.Marshal(cfg)
	writeConfig(t, w.home, string(b))
	w.start()
	return w
}

// autopilotLaunch is a dock launch in autopilot mode.
func (w *world) autopilotLaunch(harness, repo string, extra map[string]any) map[string]any {
	req := w.dockLaunch(harness, repo, "go")
	req["autopilot"] = true
	for k, v := range extra {
		req[k] = v
	}
	return req
}

func TestAutopilotLaunchSetsTheShipEnvironment(t *testing.T) {
	t.Parallel()
	w := autopilotWorld(t, 0)

	ap := w.record(w.launch(w.autopilotLaunch("claude", "ap", nil)).Session)
	if ap.env["ADMIRAL_AUTOPILOT"] != "1" || ap.env["ADMIRAL_LOW_STAKES"] != "advisor" {
		t.Errorf("autopilot ship env: ADMIRAL_AUTOPILOT=%q ADMIRAL_LOW_STAKES=%q, want 1 and advisor",
			ap.env["ADMIRAL_AUTOPILOT"], ap.env["ADMIRAL_LOW_STAKES"])
	}
	if v, ok := ap.env["ADMIRAL_AUTO_MERGE"]; ok {
		t.Errorf("autopilot ship has ADMIRAL_AUTO_MERGE=%q, want it unset: auto-merge is not allowed anywhere", v)
	}

	hand := w.record(w.launch(w.dockLaunch("claude", "hand", "go")).Session)
	for _, v := range []string{"ADMIRAL_AUTOPILOT", "ADMIRAL_LOW_STAKES", "ADMIRAL_AUTO_MERGE"} {
		if val, ok := hand.env[v]; ok {
			t.Errorf("a launch without the flag has %s=%q, want it unset", v, val)
		}
	}
}

func TestAutopilotLaunchNamesResources(t *testing.T) {
	t.Parallel()
	w := autopilotWorld(t, 0)
	// The launch would race the resource's first refresh.
	deadline := time.Now().Add(10 * time.Second)
	for len(w.resourceList()) == 0 {
		if time.Now().After(deadline) {
			t.Fatal("GET /api/resources never listed phone")
		}
		time.Sleep(50 * time.Millisecond)
	}

	ship := w.launch(w.autopilotLaunch("claude", "res", map[string]any{"resources": []string{"phone"}}))
	got := w.resourceList()
	if len(got) != 1 || got[0].Name != "phone" || got[0].InUse != 1 || len(got[0].Holders) != 1 ||
		got[0].Holders[0].Kind != "ship" || got[0].Holders[0].Session != ship.Session {
		t.Errorf("GET /api/resources = %+v, want phone held by ship %s", got, ship.Session)
	}

	// The held unit is not free: a second launch naming it is refused.
	before := w.sessions()
	code, body := w.post(w.autopilotLaunch("claude", "res-busy", map[string]any{"resources": []string{"phone"}}))
	if code != http.StatusConflict {
		t.Errorf("a launch naming a busy resource = %d %s, want 409", code, body)
	}
	if after := w.sessions(); !reflect.DeepEqual(before, after) {
		t.Errorf("a refused launch changed the sessions: before %v, after %v", before, after)
	}
}

// With one slot free, two autopilot launches posted together give exactly one
// ship and one 409: the cap check and the launch are one step.
func TestConcurrentAutopilotLaunchesOverOneFreeSlot(t *testing.T) {
	t.Parallel()
	w := autopilotWorld(t, 1)
	codes := make(chan int, 2)
	start := make(chan struct{})
	for _, name := range []string{"race-a", "race-b"} {
		req := w.autopilotLaunch("claude", name, nil)
		go func() {
			<-start
			code, _ := send(t, http.MethodPost, w.in.LocalURL+"/api/ships", req)
			codes <- code
		}()
	}
	close(start)
	got := map[int]int{}
	for i := 0; i < 2; i++ {
		got[<-codes]++
	}
	if got[http.StatusCreated] != 1 || got[http.StatusConflict] != 1 {
		t.Errorf("two autopilot launches over one free slot gave statuses %v, want one 201 and one 409", got)
	}
	w.waitShipCount(10*time.Second, "exactly one ship", live, 1)
	if n := len(w.in.ships(t)); n != 1 {
		t.Errorf("ships = %d, want 1", n)
	}
}

// Admiral owns ADMIRAL_AUTOPILOT, ADMIRAL_LOW_STAKES, ADMIRAL_AUTO_MERGE and the capability probe's two: a hand
// launch never has them, whether the fleet's env or Admiral's own environment sets them.
func TestHandLaunchHasNoAutopilotVariables(t *testing.T) {
	t.Parallel()
	vars := []string{"ADMIRAL_AUTOPILOT", "ADMIRAL_LOW_STAKES", "ADMIRAL_AUTO_MERGE", "ADMIRAL_PROBE_NONCE", "ADMIRAL_PROBE_DIR"}
	check := func(t *testing.T, w *world) {
		t.Helper()
		ship := w.launch(w.order("work", "claude", "hand", noSkill, "x"))
		r := w.record(ship.Session)
		for _, v := range vars {
			if val, ok := r.env[v]; ok {
				t.Errorf("a ship launched by hand has %s=%q, want it unset", v, val)
			}
		}
	}
	t.Run("set by the fleet's env", func(t *testing.T) {
		w := newWorld(t)
		env := w.harnessEnv()
		env["ADMIRAL_AUTOPILOT"], env["ADMIRAL_LOW_STAKES"], env["ADMIRAL_AUTO_MERGE"] = "1", "never", "squash"
		env["ADMIRAL_PROBE_NONCE"], env["ADMIRAL_PROBE_DIR"] = "0123456789abcdef", "/tmp"
		cfg, _ := json.Marshal(map[string]any{"version": 1, "fleets": map[string]any{"work": map[string]any{
			"name": "Work", "search_roots": []string{w.work}, "claude": map[string]any{"executable": w.fakeExe("claude"), "env": env}}}})
		writeConfig(t, w.home, string(cfg))
		w.start()
		check(t, w)
	})
	t.Run("set in Admiral's environment", func(t *testing.T) {
		w := newWorld(t)
		w.extra = []string{"ADMIRAL_AUTOPILOT=1", "ADMIRAL_LOW_STAKES=advisor", "ADMIRAL_AUTO_MERGE=rebase", "ADMIRAL_PROBE_NONCE=0123456789abcdef", "ADMIRAL_PROBE_DIR=/tmp"}
		w.start()
		check(t, w)
	})
}

// Two launches of one order that overlap: the order is hidden only once its
// ship exists, so the refusal must hold the order while a launch runs.
func TestConcurrentLaunchesOfOneOrder(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	p := w.newTP("a")
	p.mainOrders(ord("o1", "First"))
	w.startSources(tpSource("a", p, nil))
	w.waitSource("a", 10*time.Second, "an order", func(s sourceJSON) bool { return len(s.orderIDs()) == 1 })
	dir := w.checkout("proj")

	const n = 4
	codes := make(chan int, n)
	for i := 0; i < n; i++ {
		go func() {
			code, _ := send(t, http.MethodPost, w.in.LocalURL+"/api/ships", map[string]any{
				"fleet": "stay", "harness": "claude", "dir": dir, "skill": noSkill, "source": "a", "order_id": "o1"})
			codes <- code
		}()
	}
	got := map[int]int{}
	for i := 0; i < n; i++ {
		got[<-codes]++
	}
	if got[http.StatusCreated] != 1 || got[http.StatusConflict] != n-1 {
		t.Errorf("%d overlapping launches of one order gave statuses %v, want one 201 and %d 409", n, got, n-1)
	}
}
