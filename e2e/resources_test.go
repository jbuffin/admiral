package e2e

import (
	"encoding/json"
	"net/http"
	"reflect"
	"testing"
	"time"
)

// Locked resources (#118, u1): the real binary serves GET /api/resources and
// each panel's resources and waiting_for. Two autopilot panels need a
// capacity-1 resource: one ship launches, the other panel waits behind it, and
// launches once the first ship has sailed off.

type resourceJSON struct {
	Name     string `json:"name"`
	Capacity int    `json:"capacity"`
	InUse    int    `json:"in_use"`
	Holders  []struct {
		Kind    string `json:"kind"`
		Session string `json:"session"`
	} `json:"holders"`
	Check string `json:"check"`
}

func (w *world) resourceList() []resourceJSON {
	w.t.Helper()
	code, body := send(w.t, http.MethodGet, w.in.LocalURL+"/api/resources", nil)
	var out struct {
		Resources []resourceJSON `json:"resources"`
	}
	if err := json.Unmarshal([]byte(body), &out); code != http.StatusOK || err != nil {
		w.t.Fatalf("GET /api/resources = %d %s", code, body)
	}
	return out.Resources
}

func (w *world) waitingFor(source, panel string) []string {
	w.t.Helper()
	p, _ := w.autopilotView(source, panel)
	if p.Autopilot == nil {
		return nil
	}
	return p.Autopilot.WaitingFor
}

func TestTwoPanelsNeedingOneResourceLaunchOneShipAndTheOtherWaits(t *testing.T) {
	w := newWorld(t)
	w.writeCue("wait go", "exit 0")
	w.repo("r1", remoteOf("r1"))
	w.repo("r2", remoteOf("r2"))
	p := w.newTP("a")
	p.put("panels.json", `[{"id":"main","title":"Main"},{"id":"other","title":"Other"}]`)
	p.setOrders(map[string][]map[string]any{
		"main":  {apOrder("o1", remoteOf("r1"))},
		"other": {apOrder("o2", remoteOf("r2"))}})
	w.installPlugin("testplug", "testplug")
	needs := map[string]any{"resources": []string{"phone"}}
	src := tpAutopilot(p, "main", apEntry(true, 1, needs))
	src["autopilot"].(map[string]any)["other"] = apEntry(true, 1, needs)

	var cfg map[string]any
	if err := json.Unmarshal([]byte(w.config()), &cfg); err != nil {
		t.Fatal(err)
	}
	cfg["sources"] = map[string]any{"a": src}
	cfg["resources"] = map[string]any{"phone": map[string]any{"capacity": 1}}
	b, _ := json.Marshal(cfg)
	writeConfig(t, w.home, string(b))
	w.start()

	first := w.waitShipCount(15*time.Second, "the first ship", live, 1)[0]
	waiting := "other"
	if first.OrderID == "o2" {
		waiting = "main"
	}
	w.waitSource("a", 10*time.Second, "the other panel waiting for the phone", func(sourceJSON) bool {
		return reflect.DeepEqual(w.waitingFor("a", waiting), []string{"phone"})
	})
	holdThat(t, "a second ship launched while the first held the only unit", func() bool {
		return len(w.shipsWhere(live)) == 1
	})

	got := w.resourceList()
	if len(got) != 1 || got[0].Name != "phone" || got[0].Capacity != 1 || got[0].InUse != 1 || got[0].Check != "none" ||
		len(got[0].Holders) != 1 || got[0].Holders[0].Kind != "ship" || got[0].Holders[0].Session != first.Session {
		t.Errorf("GET /api/resources = %+v, want phone in use by %s", got, first.Session)
	}
	view, _ := w.autopilotView("a", "main")
	if view.Autopilot == nil || !reflect.DeepEqual(view.Autopilot.Resources, []string{"phone"}) {
		t.Errorf("panel resources = %+v, want [phone]", view.Autopilot)
	}
	if recs := w.waitOutcome("resource_wait", 1); recs[0]["panel"] != waiting || recs[0]["ship"] != "" {
		t.Errorf("resource_wait record = %v, want panel %s and no ship", recs[0], waiting)
	}

	w.sail(first.Session)
	w.waitShipCount(15*time.Second, "the waiting panel's ship", func(s shipJSON) bool { return s.Session != first.Session && live(s) }, 1)
}
