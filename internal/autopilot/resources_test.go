package autopilot

import (
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/jbuffin/admiral/internal/config"
	"github.com/jbuffin/admiral/internal/launcher"
	"github.com/jbuffin/admiral/internal/resources"
	"github.com/jbuffin/admiral/internal/ships"
	"github.com/jbuffin/admiral/internal/sources"
)

// The gate (#118, locked resources). The interface these tests assume:
//
//	Engine.Resources *resources.Set         // nil gates nothing
//	Engine.OnWait func()                    // a panel started or stopped waiting; nil does nothing
//	(*Engine).WaitingFor(source, panel string) []string  // the resources its waiting order waits for; empty when it isn't waiting
//	config.PanelAutopilot.Resources []string; launcher.Request.Resources []string
//	  (set from the entry on an autopilot launch, nil when the entry names none)
//
// Passes are driven by hand as in engine_test.go; checks run through
// Set.CheckNow, so nothing here waits on a clock.

func needs(max int, names ...string) config.PanelAutopilot {
	e := entry(max)
	e.Resources = names
	return e
}

// gated gives the rig two panels, p and q, each with one order and each
// needing the resources; the engine gets a Set over the rig's ships. A launch
// records the request's resources on the ship, the way the launcher does.
func gated(t *testing.T, defs map[string]config.Resource, names ...string) *rig {
	t.Helper()
	r := newRig(t)
	r.cfg.Resources = defs
	r.cfg.SourceList[0].Autopilot = map[string]config.PanelAutopilot{"p": needs(5, names...), "q": needs(5, names...)}
	r.src[0].Panels = []sources.Panel{panel("p", "o1"), panel("q", "o2")}
	r.dirs = map[string]string{"o1": "/w/o1", "o2": "/w/o2"}
	r.engine.Resources = resources.New(resources.Options{
		Config: func() *config.Config { return r.cfg },
		Ships:  func() []ships.Ship { return append([]ships.Ship(nil), r.ships...) },
	})
	launch := r.engine.Launch
	r.engine.Launch = func(req launcher.Request) (ships.Ship, *launcher.Error) {
		sh, err := launch(req)
		if err == nil {
			r.ships[len(r.ships)-1].Resources = req.Resources
		}
		return sh, err
	}
	return r
}

var phone = map[string]config.Resource{"phone": {Capacity: 1}}

// AC 1: two panels that need the same capacity-1 resource never have two ships
// underway or needing you.
func TestTwoPanelsNeedingOneResourceNeverHaveTwoShipsAtOnce(t *testing.T) {
	r := gated(t, phone, "phone")
	want(t, "first pass", r.pass(), []string{"s/p/o1"})
	want(t, "while o1 is underway", r.pass(), nil)
	r.ships[0].Status = ships.StatusNeedsYou
	want(t, "while o1 needs you", r.pass(), nil)
	r.ships[0].Status = ships.StatusAnchored
	want(t, "once o1 is anchored", r.pass(), []string{"s/q/o2"})
}

func TestCapacityTwoLetsTwoShipsRun(t *testing.T) {
	r := gated(t, map[string]config.Resource{"phone": {Capacity: 2}}, "phone")
	want(t, "capacity 2", r.pass(), []string{"s/p/o1", "s/q/o2"})
}

// The unit is reserved from the gate until Launch returns: a lock taken while
// the launch is in flight finds none, and the ship holds it afterwards. A launch
// that fails, with any error, gives the unit back.
func TestALaunchInFlightHoldsItsUnit(t *testing.T) {
	r := gated(t, phone, "phone")
	launch := r.engine.Launch
	var lockDuringLaunch bool
	r.engine.Launch = func(req launcher.Request) (ships.Ship, *launcher.Error) {
		if rel, err := r.engine.Resources.Lock("phone", "install"); err == nil {
			lockDuringLaunch = true
			rel()
		}
		return launch(req)
	}
	want(t, "pass", r.pass(), []string{"s/p/o1"})
	if lockDuringLaunch {
		t.Error("a lock got the unit while a launch was in flight")
	}
	if _, err := r.engine.Resources.Lock("phone", "install"); !errors.Is(err, resources.ErrBusy) {
		t.Errorf("Lock after the launch = %v, want ErrBusy: the ship holds the unit", err)
	}

	for _, lerr := range []*launcher.Error{
		{Status: http.StatusInternalServerError, Msg: "boom"},
		{Status: http.StatusConflict, Msg: "this order already has a ship"},
		{Status: http.StatusNotFound, Msg: "order gone"},
		{Status: http.StatusServiceUnavailable, Msg: ships.NotKnownMsg},
	} {
		t.Run(lerr.Msg, func(t *testing.T) {
			r := gated(t, phone, "phone")
			r.failWith = lerr
			r.pass()
			rel, err := r.engine.Resources.Lock("phone", "install")
			if err != nil {
				t.Fatalf("the failed launch kept its unit: %v", err)
			}
			rel()
		})
	}
}

// A lock holds the unit like a ship: an order waits until it is released.
func TestALaunchWaitsForALockUntilReleased(t *testing.T) {
	r := gated(t, phone, "phone")
	rel, err := r.engine.Resources.Lock("phone", "install")
	if err != nil {
		t.Fatal(err)
	}
	want(t, "locked", r.pass(), nil)
	rel()
	want(t, "released", r.pass(), []string{"s/p/o1"})
}

// AC 2: a check that hasn't finished or fails stops launches; once it passes
// they resume; a failing one stops them again.
func TestAFailingCheckStopsLaunchesAndAPassingOneResumesThem(t *testing.T) {
	flag := filepath.Join(t.TempDir(), "present")
	r := gated(t, map[string]config.Resource{"phone": {Capacity: 2, Check: []string{"test", "-f", flag}}}, "phone")
	set := r.engine.Resources

	want(t, "before the first check", r.pass(), nil)
	if n := len(r.recordsOf("resource_wait")); n != 0 {
		t.Errorf("a pending check wrote %d resource_wait records, want none", n)
	}
	set.CheckNow()
	want(t, "check failing", r.pass(), nil)
	if err := os.WriteFile(flag, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	set.CheckNow()
	want(t, "check passing", r.pass(), []string{"s/p/o1", "s/q/o2"})
	r.ships[0].Status, r.ships[1].Status = ships.StatusAnchored, ships.StatusAnchored // room again; only the check can stop o3

	if err := os.Remove(flag); err != nil {
		t.Fatal(err)
	}
	set.CheckNow()
	r.src[0].Panels = []sources.Panel{panel("p", "o1", "o3"), panel("q", "o2")}
	r.dirs["o3"] = "/w/o3"
	want(t, "check failing again", r.pass(), nil)
}

// The launch carries the entry's resources so the ship can record them.
func TestRequestCarriesThePanelsResources(t *testing.T) {
	r := gated(t, phone, "phone")
	r.pass()
	if got := r.launches[0].Resources; !reflect.DeepEqual(got, []string{"phone"}) {
		t.Errorf("request resources = %v, want [phone]", got)
	}
	r = newRig(t, "o1")
	r.pass()
	if got := r.launches[0].Resources; got != nil {
		t.Errorf("request resources of a panel naming none = %v, want nil", got)
	}
}

// A wait is one resource_wait record per order, however many passes it lasts
// and however its busy or missing resources change; the engine moves on to the
// next panel when an order waits, and says which resources each panel waits for.
func TestOneResourceWaitRecordPerWait(t *testing.T) {
	flag := filepath.Join(t.TempDir(), "present")
	defs := map[string]config.Resource{"phone": {Capacity: 1}, "ipad": {Capacity: 1, Check: []string{"test", "-f", flag}}}
	r := gated(t, defs, "phone", "ipad")
	r.src[0].Panels = []sources.Panel{panel("p", "o1", "o3"), panel("q", "o2")}
	r.dirs["o3"] = "/w/o3"
	set := r.engine.Resources
	waitChanges := 0
	r.engine.OnWait = func() { waitChanges++ }
	set.CheckNow()                         // ipad missing
	rel, _ := set.Lock("phone", "install") // the unit is free: no error

	want(t, "waiting", r.pass(), nil)
	recs := r.recordsOf("resource_wait")
	if len(recs) != 2 {
		t.Fatalf("resource_wait records = %d, want 2 (o1 of p and o2 of q; o3 waits behind o1): %+v", len(recs), recs)
	}
	got, _ := json.Marshal(recs[0].Fields["resources"])
	if string(got) != `[{"name":"ipad","why":"missing"},{"name":"phone","why":"busy"}]` {
		t.Errorf("resources of the first record = %s", got)
	}
	for k, v := range map[string]any{"source": "s", "panel": "p", "order_id": "o1", "title": "Title o1"} {
		if recs[0].Fields[k] != v {
			t.Errorf("field %s = %v, want %v", k, recs[0].Fields[k], v)
		}
	}
	if recs[1].Fields["order_id"] != "o2" || recs[1].Fields["panel"] != "q" {
		t.Errorf("second record = %v, want o2 of q", recs[1].Fields)
	}
	if got := r.engine.WaitingFor("s", "p"); !reflect.DeepEqual(got, []string{"ipad", "phone"}) {
		t.Errorf("WaitingFor(p) = %v, want [ipad phone]", got)
	}
	if got := r.engine.WaitingFor("s", "nope"); len(got) != 0 {
		t.Errorf("WaitingFor of a panel that isn't waiting = %v", got)
	}
	if waitChanges != 2 {
		t.Errorf("OnWait ran %d times after two panels started waiting, want 2", waitChanges)
	}

	// The busy set changes (the lock goes, ipad stays missing): still the same wait.
	rel()
	want(t, "still waiting", r.pass(), nil)
	want(t, "and again", r.pass(), nil)
	if n := len(r.recordsOf("resource_wait")); n != 2 {
		t.Errorf("resource_wait records after the set changed = %d, want 2", n)
	}
	if got := r.engine.WaitingFor("s", "p"); !reflect.DeepEqual(got, []string{"ipad"}) {
		t.Errorf("WaitingFor(p) = %v, want [ipad]", got)
	}

	// The order leaves the listing: its wait is over, and a new one is recorded.
	r.src[0].Panels = []sources.Panel{panel("p", "o3"), panel("q", "o2")}
	r.pass()
	r.src[0].Panels = []sources.Panel{panel("p", "o1", "o3"), panel("q", "o2")}
	r.pass()
	if n := len(r.recordsOf("resource_wait")); n != 4 {
		t.Errorf("resource_wait records once o1 left and came back = %d, want 4 (o3's wait, then o1's again)", n)
	}

	// The check passes: o1's gate passes and it launches; o3, behind it in the
	// panel, now waits for the unit o1 holds, with the record it already has.
	if err := os.WriteFile(flag, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	set.CheckNow()
	want(t, "free", r.pass(), []string{"s/p/o1"})
	if got := r.engine.WaitingFor("s", "p"); !reflect.DeepEqual(got, []string{"ipad", "phone"}) {
		t.Errorf("WaitingFor(p) with o3 behind the launched o1 = %v, want [ipad phone]: o1's ship holds the only unit of each", got)
	}
	if n := len(r.recordsOf("resource_wait")); n != 4 {
		t.Errorf("resource_wait records after the launch = %d, want 4: o3 was already waiting", n)
	}
}

// A pass that never reaches the waiting order (a cap returns first) neither
// ends the wait nor starts a new one.
func TestACapBetweenPassesDoesNotRestartAWait(t *testing.T) {
	r := gated(t, phone, "phone")
	rel, _ := r.engine.Resources.Lock("phone", "install")
	defer rel()
	want(t, "waiting", r.pass(), nil)

	r.cfg.AutopilotMaxShips = 1
	r.setShips(ship(ships.StatusUnderway, "", "", "")) // a hand ship fills the global cap
	want(t, "capped", r.pass(), nil)
	if got := r.engine.WaitingFor("s", "p"); !reflect.DeepEqual(got, []string{"phone"}) {
		t.Errorf("WaitingFor(p) after a capped pass = %v, want [phone]", got)
	}

	r.cfg.AutopilotMaxShips = 10
	r.setShips()
	want(t, "reached again", r.pass(), nil)
	if n := len(r.recordsOf("resource_wait")); n != 2 { // o1 of p and o2 of q, once each
		t.Errorf("resource_wait records = %d, want 2", n)
	}
}
