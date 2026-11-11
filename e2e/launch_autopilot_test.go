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
func autopilotWorld(t *testing.T, maxShips int) *world {
	t.Helper()
	w := newWorld(t)
	w.writeCue("wait go", "exit 0") // the ship stays underway
	var cfg map[string]any
	if err := json.Unmarshal([]byte(w.config()), &cfg); err != nil {
		t.Fatal(err)
	}
	cfg["resources"] = map[string]any{"phone": map[string]any{"capacity": 1}}
	if maxShips != 0 {
		cfg["autopilot"] = map[string]any{"max_ships": maxShips}
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

// The cap counts every ship underway or needing you; it refuses an autopilot
// launch and never an interactive one.
func TestAutopilotLaunchOverTheCapIsRefused(t *testing.T) {
	t.Parallel()
	w := autopilotWorld(t, 1)
	// The ship that fills the cap is a hand launch: every ship counts, not only
	// autopilot ones.
	first := w.launch(w.dockLaunch("claude", "cap1", "go"))
	w.waitShipCount(10*time.Second, "the first ship underway", func(s shipJSON) bool { return s.Session == first.Session && live(s) }, 1)

	before := w.sessions()
	code, body := w.post(w.autopilotLaunch("claude", "cap2", nil))
	if code != http.StatusConflict {
		t.Errorf("an autopilot launch over the cap = %d %s, want 409", code, body)
	}
	if after := w.sessions(); !reflect.DeepEqual(before, after) {
		t.Errorf("a refused launch changed the sessions: before %v, after %v", before, after)
	}

	hand := w.launch(w.dockLaunch("claude", "cap-hand", "go"))
	if hand.Session == "" || hand.Session == first.Session {
		t.Errorf("an interactive launch over the cap gave %+v, want a new ship", hand)
	}
}
