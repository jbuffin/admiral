package e2e

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

// The autopilot engine (#114, u2): the real binary, the fake harness, and
// orders from the freeform source and the test plugin. A ship's cue is
// `wait go`, then `exit 0`: touching "go" in its recording directory sails it off.
//
// Each test that holds ("nothing launches") first sees something launch or
// record, so a missing engine fails it. Holds are short and fixed; the engine
// wakes on every change, so a launch it was going to make shows within a moment.

const hold = 2500 * time.Millisecond

func apEntry(enabled bool, max int, extra map[string]any) map[string]any {
	e := map[string]any{"enabled": enabled, "fleet": "work", "harness": "claude", "skill": map[string]any{"kind": "none"},
		"args": []string{"--panel-arg"}, "max_ships": max, "low_stakes": "never"}
	for k, v := range extra {
		e[k] = v
	}
	return e
}

// writeAutopilotConfig writes the default fleets plus the sources (key →
// source object) and the global cap; 0 leaves the cap at its default.
func (w *world) writeAutopilotConfig(sources map[string]any, global int) {
	w.t.Helper()
	var cfg map[string]any
	if err := json.Unmarshal([]byte(w.config()), &cfg); err != nil {
		w.t.Fatal(err)
	}
	cfg["sources"] = sources
	if global > 0 {
		cfg["autopilot"] = map[string]any{"max_ships": global}
	}
	b, err := json.Marshal(cfg)
	if err != nil {
		w.t.Fatal(err)
	}
	writeConfig(w.t, w.home, string(b))
}

func tpAutopilot(p *tplug, panel string, entry map[string]any) map[string]any {
	return map[string]any{"plugin": "testplug", "name": "Test", "settings": p.settings(nil),
		"autopilot": map[string]any{panel: entry}}
}

// apOrder is a test-plugin order whose repository is the checkout's remote.
func apOrder(id, remote string) map[string]any {
	o := ord(id, "Order "+id)
	o["repo"], o["text"] = remote, "Review "+remote
	return o
}

func remoteOf(name string) string { return "https://github.com/acme/" + name }

func (w *world) shipsWhere(pred func(shipJSON) bool) []shipJSON {
	w.t.Helper()
	out := []shipJSON{}
	for _, s := range w.in.ships(w.t) {
		if pred(s) {
			out = append(out, s)
		}
	}
	return out
}

func (w *world) waitShipCount(d time.Duration, what string, pred func(shipJSON) bool, n int) []shipJSON {
	w.t.Helper()
	return w.in.waitShipsList(w.t, d, what, func(list []shipJSON) bool {
		c := 0
		for _, s := range list {
			if pred(s) {
				c++
			}
		}
		return c >= n
	})
}

// holdThat polls check for the length of hold and fails the test with msg the
// first time it does not hold.
func holdThat(t *testing.T, msg string, check func() bool) {
	t.Helper()
	end := time.Now().Add(hold)
	for time.Now().Before(end) {
		if !check() {
			t.Fatalf("%s", msg)
		}
		time.Sleep(100 * time.Millisecond)
	}
}

// sail lets the ship's harness exit: its recording directory exists once the
// harness has started.
func (w *world) sail(session string) {
	w.t.Helper()
	w.waitFile(session, "recorded")
	w.touch(session, "go")
}

func live(s shipJSON) bool { return s.Status == "underway" || s.Status == "needs_you" }

func fromOrder(id string) func(shipJSON) bool {
	return func(s shipJSON) bool { return s.OrderID == id }
}

func (w *world) outcomesOf(event string) []outRec {
	w.t.Helper()
	out := []outRec{}
	for _, m := range w.outcomeLog() {
		if m["event"] == event {
			out = append(out, m)
		}
	}
	return out
}

func (w *world) waitOutcome(event string, n int) []outRec {
	w.t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for {
		if got := w.outcomesOf(event); len(got) >= n {
			return got
		}
		if time.Now().After(deadline) {
			w.t.Fatalf("within 15s the outcome log never held %d %q record(s); it holds %v", n, event, w.outcomeLog())
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func (w *world) autopilotView(source, panel string) (panelJSON, bool) {
	w.t.Helper()
	s, ok := find(w.sources(), source)
	if !ok {
		return panelJSON{}, false
	}
	for _, p := range s.Panels {
		if p.ID == panel {
			return p, true
		}
	}
	return panelJSON{}, false
}

func (w *world) setPaused(source, panel string, paused bool) {
	w.t.Helper()
	code, body := send(w.t, http.MethodPut, w.in.LocalURL+"/api/sources/"+source+"/panels/"+panel+"/autopilot", map[string]any{"paused": paused})
	w.mustOK(code, body, "PUT the autopilot pause")
}

// AC 1, 8 (autopilot half) and the panel option: cap 1 and three freeform
// orders give one ship, and the next launches only once it has sailed off. The
// restart keeps the cap: the running ship fills the panel's cap by its
// @admiral-panel option.
func TestEngineCapOneLaunchesOneShipAtATime(t *testing.T) {
	w := newWorld(t)
	w.writeCue("wait go", "exit 0")
	for _, n := range []string{"r1", "r2", "r3"} {
		w.repo(n, remoteOf(n))
	}
	freeform := func(enabled bool) map[string]any {
		return map[string]any{"orders": map[string]any{"plugin": "freeform", "name": "Orders",
			"autopilot": map[string]any{"orders": apEntry(enabled, 1, nil)}}}
	}
	w.writeAutopilotConfig(freeform(false), 0)
	w.start()
	w.waitSource("orders", 10*time.Second, "state ok", isOK)
	for _, n := range []string{"r1", "r2", "r3"} {
		code, body := send(t, http.MethodPost, w.in.LocalURL+"/api/sources/orders/orders",
			map[string]any{"panel": "orders", "text": "Review " + remoteOf(n)})
		w.mustOK(code, body, "add an order")
	}
	w.waitSource("orders", 5*time.Second, "three orders", func(s sourceJSON) bool { return len(s.orderIDs()) == 3 })

	w.writeAutopilotConfig(freeform(true), 0) // the engine wakes on the config change
	first := w.waitShipCount(15*time.Second, "the first autopilot ship", live, 1)[0]
	holdThat(t, "a second ship launched while the first ran, with a panel cap of 1", func() bool {
		return len(w.shipsWhere(func(shipJSON) bool { return true })) == 1
	})

	r := w.record(first.Session)
	if r.env["ADMIRAL_AUTOPILOT"] != "1" || r.env["ADMIRAL_LOW_STAKES"] != "never" {
		t.Errorf("ship env ADMIRAL_AUTOPILOT=%q ADMIRAL_LOW_STAKES=%q, want 1 and never", r.env["ADMIRAL_AUTOPILOT"], r.env["ADMIRAL_LOW_STAKES"])
	}
	fleetArg, panelArg := slices.Index(r.argv, "--fa-claude"), slices.Index(r.argv, "--panel-arg")
	if fleetArg < 0 || panelArg < fleetArg {
		t.Errorf("argv = %q, want the panel's --panel-arg after the fleet's --fa-claude", r.argv)
	}
	if got := showOption(t, w, first.Session, "-v", "@admiral-panel"); got != "orders" {
		t.Errorf("@admiral-panel = %q, want orders", got)
	}
	launched := w.waitOutcome("launched", 1)[0]
	if launched["autopilot"] != true || launched["ship"] != first.Session {
		t.Errorf("launched record = %v, want autopilot true for %s", launched, first.Session)
	}

	w.restart()
	w.waitShipCount(10*time.Second, "the running ship after the restart", live, 1)
	holdThat(t, "after a restart a second ship launched into a panel whose cap the first ship fills", func() bool {
		return len(w.shipsWhere(func(shipJSON) bool { return true })) == 1
	})

	w.sail(first.Session)
	w.waitShipCount(15*time.Second, "the next order's ship", func(s shipJSON) bool { return s.Session != first.Session && live(s) }, 1)
}

// AC 2: two ships launched by hand fill a global cap of 2, so nothing launches;
// when one leaves, the panel launches its order.
func TestEngineGlobalCapCountsHandLaunchedShips(t *testing.T) {
	w := newWorld(t)
	w.writeCue("wait go", "exit 0")
	w.repo("r1", remoteOf("r1"))
	w.repo("r2", remoteOf("r2"))
	p := w.newTP("a")
	p.mainOrders()
	w.installPlugin("testplug", "testplug")
	w.writeAutopilotConfig(map[string]any{"a": tpAutopilot(p, "main", apEntry(true, 5, nil))}, 2)
	w.start()
	w.waitSource("a", 10*time.Second, "state ok", isOK)

	// The global cap reaches the UI on the config view.
	code, body := send(t, http.MethodGet, w.in.LocalURL+"/api/config", nil)
	var view struct {
		Autopilot *struct {
			MaxShips int `json:"max_ships"`
		} `json:"autopilot"`
	}
	if err := json.Unmarshal([]byte(body), &view); code != http.StatusOK || err != nil || view.Autopilot == nil || view.Autopilot.MaxShips != 2 {
		t.Errorf("GET /api/config = %d %s, want autopilot.max_ships 2", code, body)
	}

	h1 := w.launch(w.order("work", "claude", "hand1", noSkill, "x"))
	h2 := w.launch(w.order("work", "claude", "hand2", noSkill, "x"))
	p.mainOrders(apOrder("o1", remoteOf("r1")), apOrder("o2", remoteOf("r2")))
	p.do("orders-changed", "")
	w.waitSource("a", 5*time.Second, "both orders", func(s sourceJSON) bool { return len(s.orderIDs()) == 2 })
	holdThat(t, "autopilot launched with two hand-launched ships filling the global cap of 2", func() bool {
		return len(w.shipsWhere(fromOrder("o1"))) == 0 && len(w.shipsWhere(fromOrder("o2"))) == 0
	})

	w.sail(h1.Session)
	_ = h2
	w.waitShipCount(15*time.Second, "the order launched once a slot opened", fromOrder("o1"), 1)
}

// A launch by hand of an order from an autopilot panel joins the launched set,
// and an order that was launched and has gone is not launched again while it is
// still listed (AC 4); once it stops being listed and returns, it can be.
func TestEngineLaunchesEachOrderOncePerListing(t *testing.T) {
	w := newWorld(t)
	w.writeCue("wait go", "exit 0")
	w.repo("r1", remoteOf("r1"))
	w.repo("r2", remoteOf("r2"))
	p := w.newTP("a")
	p.mainOrders(apOrder("o1", remoteOf("r1")), apOrder("o2", remoteOf("r2")))
	w.installPlugin("testplug", "testplug")
	w.writeAutopilotConfig(map[string]any{"a": tpAutopilot(p, "main", apEntry(false, 1, nil))}, 0)
	w.start()
	w.waitSource("a", 10*time.Second, "both orders", func(s sourceJSON) bool { return len(s.orderIDs()) == 2 })

	hand := w.mustLaunchFrom("work", "a", "o1")
	w.sail(hand.Session)
	w.in.waitShipsList(t, 15*time.Second, "the hand ship gone", func(l []shipJSON) bool { return len(l) == 0 })

	w.writeAutopilotConfig(map[string]any{"a": tpAutopilot(p, "main", apEntry(true, 1, nil))}, 0)
	second := w.waitShipCount(15*time.Second, "o2's ship", fromOrder("o2"), 1)[0]
	w.sail(second.Session)
	w.in.waitShipsList(t, 15*time.Second, "o2's ship gone", func(l []shipJSON) bool { return len(l) == 0 })
	holdThat(t, "autopilot launched an order that was already launched and is still listed", func() bool {
		return len(w.in.ships(t)) == 0
	})

	p.mainOrders(apOrder("o1", remoteOf("r1")))
	p.do("orders-changed", "")
	w.waitSource("a", 5*time.Second, "o2 unlisted", func(s sourceJSON) bool { return !s.hasOrder("o2") })
	p.mainOrders(apOrder("o1", remoteOf("r1")), apOrder("o2", remoteOf("r2")))
	p.do("orders-changed", "")
	w.waitShipCount(15*time.Second, "o2 launched again after it returned", fromOrder("o2"), 1)

	count := func(id string) int {
		n := 0
		for _, m := range w.outcomesOf("launched") {
			if m["order_id"] == id {
				n++
			}
		}
		return n
	}
	if count("o1") != 1 || count("o2") != 2 {
		t.Errorf("launched records: o1 %d (want 1, by hand), o2 %d (want 2)", count("o1"), count("o2"))
	}
}

// AC 5, server half: pausing stops new launches and leaves running ships alone,
// the panel's view says so, and the pause outlives a restart.
func TestEnginePauseSurvivesARestart(t *testing.T) {
	w := newWorld(t)
	w.writeCue("wait go", "exit 0")
	w.repo("r1", remoteOf("r1"))
	p := w.newTP("a")
	p.mainOrders()
	w.installPlugin("testplug", "testplug")
	w.writeAutopilotConfig(map[string]any{"a": tpAutopilot(p, "main", apEntry(true, 2, nil))}, 0)
	w.start()
	w.waitSource("a", 10*time.Second, "state ok", isOK)
	view, _ := w.autopilotView("a", "main")
	if view.Autopilot == nil || !view.Autopilot.Enabled || view.Autopilot.Paused || view.Autopilot.MaxShips != 2 {
		t.Fatalf("panel autopilot view = %+v, want enabled, not paused, max_ships 2", view.Autopilot)
	}

	running := w.launch(w.order("work", "claude", "hand", noSkill, "x"))
	w.setPaused("a", "main", true)
	view, _ = w.autopilotView("a", "main")
	if view.Autopilot == nil || !view.Autopilot.Paused {
		t.Fatalf("panel autopilot view after pausing = %+v, want paused", view.Autopilot)
	}
	p.mainOrders(apOrder("o1", remoteOf("r1")))
	p.do("orders-changed", "")
	w.waitSource("a", 5*time.Second, "o1", func(s sourceJSON) bool { return s.hasOrder("o1") })
	holdThat(t, "a paused panel launched an order, or the running ship was touched", func() bool {
		l := w.in.ships(t)
		return len(l) == 1 && l[0].Session == running.Session && live(l[0])
	})

	w.restart()
	w.waitSource("a", 10*time.Second, "o1 after the restart", func(s sourceJSON) bool { return s.hasOrder("o1") })
	view, _ = w.autopilotView("a", "main")
	if view.Autopilot == nil || !view.Autopilot.Paused {
		t.Fatalf("panel autopilot view after the restart = %+v, want still paused", view.Autopilot)
	}
	holdThat(t, "a panel launched after the restart while it was still paused", func() bool {
		return len(w.shipsWhere(fromOrder("o1"))) == 0
	})

	w.setPaused("a", "main", false)
	w.waitShipCount(15*time.Second, "o1 once resumed", fromOrder("o1"), 1)
}

// The pause route refuses a panel that is unknown or has no autopilot entry.
func TestEnginePauseRouteRefusesUnknownPanels(t *testing.T) {
	w := newWorld(t)
	p := w.newTP("a")
	p.mainOrders()
	w.installPlugin("testplug", "testplug")
	w.writeAutopilotConfig(map[string]any{"a": tpAutopilot(p, "main", apEntry(true, 1, nil)), "b": tpAutopilot(w.newTP("b"), "other", apEntry(true, 1, nil))}, 0)
	w.start()
	w.waitSource("a", 10*time.Second, "state ok", isOK)
	w.setPaused("a", "main", true) // a panel with an entry takes it
	// "b" has an entry for a panel its plugin does not list; "a/side" has none.
	for _, path := range []string{"/api/sources/a/panels/side/autopilot", "/api/sources/b/panels/main/autopilot", "/api/sources/nosuch/panels/main/autopilot"} {
		code, body := send(t, http.MethodPut, w.in.LocalURL+path, map[string]any{"paused": true})
		if code != http.StatusNotFound {
			t.Errorf("PUT %s = %d, want 404: %s", path, code, body)
		}
	}
}

// AC 7: an order with no matching checkout gets a skipped record and no ship,
// and stays visible. The dock has a last directory from a hand launch: autopilot
// never falls back to it. The next order, with a checkout, still launches, and
// `admiral outcomes` prints the skipped line (which has no ship).
func TestEngineSkipsAnOrderWithNoMatchingCheckout(t *testing.T) {
	w := newWorld(t)
	w.writeCue("wait go", "exit 0")
	w.repo("r2", remoteOf("r2"))
	p := w.newTP("a")
	p.mainOrders()
	w.installPlugin("testplug", "testplug")
	w.writeAutopilotConfig(map[string]any{"a": tpAutopilot(p, "main", apEntry(true, 1, nil))}, 0)
	w.start()
	w.waitSource("a", 10*time.Second, "state ok", isOK)
	w.launch(w.order("work", "claude", "hand", noSkill, "x")) // sets the dock's last directory

	p.mainOrders(apOrder("o1", remoteOf("nowhere")), apOrder("o2", remoteOf("r2")))
	p.do("orders-changed", "")
	// Cap 1 and the hand ship is not from the panel: o2 launches, o1 is skipped.
	w.waitShipCount(15*time.Second, "o2's ship", fromOrder("o2"), 1)
	rec := w.waitOutcome("skipped", 1)[0]
	if rec["ship"] != "" || rec["order_id"] != "o1" || rec["source"] != "a" || rec["panel"] != "main" {
		t.Errorf("skipped record = %v, want ship \"\" and source a, panel main, order o1", rec)
	}
	if reason, _ := rec["reason"].(string); reason == "" {
		t.Errorf("skipped record has no reason: %v", rec)
	}
	if n := len(w.shipsWhere(fromOrder("o1"))); n != 0 {
		t.Errorf("o1 has %d ship(s), want none", n)
	}
	if s, _ := find(w.sources(), "a"); !s.hasOrder("o1") {
		t.Errorf("o1 is not visible after it was skipped: %s", dump(s))
	}
	time.Sleep(hold)
	if n := len(w.outcomesOf("skipped")); n != 1 {
		t.Errorf("skipped records = %d, want one per order", n)
	}

	out := w.admiral("outcomes").stdout
	if !strings.Contains(out, `"event":"skipped"`) {
		t.Errorf("admiral outcomes prints no skipped line:\n%s", out)
	}
	if byShip := w.admiral("outcomes", "--ship", "someone").stdout; strings.Contains(byShip, `"skipped"`) {
		t.Errorf("admiral outcomes --ship printed a record that has no ship:\n%s", byShip)
	}
}

// A launch that fails writes a launch_failed record, leaves the order visible
// and backs it off: a second try within the 10 minutes would write a second record.
func TestEngineLaunchFailureWritesARecordAndBacksOff(t *testing.T) {
	w := newWorld(t)
	w.repo("r1", remoteOf("r1"))
	if err := os.RemoveAll(filepath.Join(w.userHom, ".claude", "skills", "commodore")); err != nil {
		t.Fatal(err)
	}
	p := w.newTP("a")
	p.mainOrders(apOrder("o1", remoteOf("r1")))
	w.installPlugin("testplug", "testplug")
	w.writeAutopilotConfig(map[string]any{"a": tpAutopilot(p, "main",
		apEntry(true, 1, map[string]any{"skill": map[string]any{"kind": "commodore"}}))}, 0)
	w.start()

	rec := w.waitOutcome("launch_failed", 1)[0]
	if rec["ship"] != "" || rec["order_id"] != "o1" || rec["attempt"] != float64(1) {
		t.Errorf("launch_failed record = %v, want ship \"\", order o1, attempt 1", rec)
	}
	next, err := time.Parse(time.RFC3339, rec["next_retry"].(string))
	if err != nil || time.Until(next) < 9*time.Minute || time.Until(next) > 11*time.Minute {
		t.Errorf("next_retry = %v (%v), want about 10 minutes from now", rec["next_retry"], err)
	}
	time.Sleep(hold)
	if n := len(w.outcomesOf("launch_failed")); n != 1 {
		t.Errorf("launch_failed records = %d, want 1 while backed off", n)
	}
	if n := len(w.in.ships(t)); n != 0 {
		t.Errorf("ships = %d, want none", n)
	}
	if s, _ := find(w.sources(), "a"); !s.hasOrder("o1") {
		t.Errorf("o1 is not visible after its launch failed")
	}
}

// With several checkouts of the order's repository, autopilot picks the one the
// launch dialog pre-selects (ADR 0009), not the first.
func TestEnginePicksTheDirectoryTheDialogPicks(t *testing.T) {
	w := newWorld(t)
	w.writeCue("wait go", "exit 0")
	remote := remoteOf("proj")
	w.repo("proj-a", remote)
	recent := w.repo("proj-b", remote)
	p := w.newTP("a")
	p.mainOrders()
	w.installPlugin("testplug", "testplug")
	w.writeAutopilotConfig(map[string]any{"a": tpAutopilot(p, "main", apEntry(true, 1, nil))}, 0)
	w.start()
	w.waitSource("a", 10*time.Second, "state ok", isOK)
	req := w.order("work", "claude", "proj-b", noSkill, "x")
	req["dir"] = recent
	w.launch(req) // proj-b is now the most recently used

	d := w.dirs("work", "claude", remote, "")
	if d.selected() != recent {
		t.Fatalf("the dialog selects %q, want %q: the test needs recency to decide", d.selected(), recent)
	}
	p.mainOrders(apOrder("o1", remote))
	p.do("orders-changed", "")
	got := w.waitShipCount(15*time.Second, "o1's ship", fromOrder("o1"), 1)
	for _, s := range got {
		if s.OrderID == "o1" && s.Dir != recent {
			t.Errorf("autopilot launched in %q, want %q (the dialog's pick)", s.Dir, recent)
		}
	}
}

// A panel whose first orders.list after a restart fails lists nothing, which is
// not the same as its orders being gone: the launched set keeps the order, so
// it is not launched again when the panel lists it.
func TestEngineKeepsTheLaunchedSetWhenAPanelFailsToList(t *testing.T) {
	w := newWorld(t)
	w.writeCue("wait go", "exit 0")
	w.repo("r1", remoteOf("r1"))
	p := w.newTP("a")
	p.mainOrders(apOrder("o1", remoteOf("r1")))
	w.installPlugin("testplug", "testplug")
	w.writeAutopilotConfig(map[string]any{"a": tpAutopilot(p, "main", apEntry(true, 1, nil))}, 0)
	w.start()
	first := w.waitShipCount(15*time.Second, "o1's ship", fromOrder("o1"), 1)[0]
	w.sail(first.Session)
	w.in.waitShipsList(t, 15*time.Second, "o1's ship gone", func(l []shipJSON) bool { return len(l) == 0 })

	p.put("fail-orders.list-main", "listing failed")
	w.restart()
	w.waitSource("a", 10*time.Second, "ok with a panel that failed to list", func(s sourceJSON) bool {
		return s.State == "ok" && len(s.Panels) == 1 && s.Panels[0].Error != nil
	})
	p.clear("fail-orders.list-main")
	p.do("orders-changed", "")
	w.waitSource("a", 5*time.Second, "o1 listed again", func(s sourceJSON) bool { return s.hasOrder("o1") })
	holdThat(t, "an order autopilot had launched was launched again after its panel failed to list", func() bool {
		return len(w.in.ships(t)) == 0
	})
}

// An order autopilot gave up on shows as hot in the sources view and is not launched.
func TestEngineShowsAGivenUpOrderAsHot(t *testing.T) {
	w := newWorld(t)
	w.repo("r1", remoteOf("r1"))
	p := w.newTP("a")
	p.mainOrders(apOrder("o1", remoteOf("r1")))
	w.installPlugin("testplug", "testplug")
	w.writeAutopilotConfig(map[string]any{"a": tpAutopilot(p, "main", apEntry(true, 1, nil))}, 0)
	seed := `{"version":1,"launched":[],"failures":[{"source":"a","order_id":"o1","attempts":3,"next_retry":"0001-01-01T00:00:00Z","gave_up":true}]}`
	if err := os.WriteFile(filepath.Join(w.home, "autopilot.json"), []byte(seed), 0o600); err != nil {
		t.Fatal(err)
	}
	w.start()
	w.waitSource("a", 10*time.Second, "o1 shown as hot", func(s sourceJSON) bool {
		return len(s.Panels) == 1 && len(s.Panels[0].Orders) == 1 && s.Panels[0].Orders[0].Hot
	})
	holdThat(t, "autopilot launched an order it gave up on", func() bool { return len(w.in.ships(t)) == 0 })
}

// #120, the launch half of auto_merge: a panel entry's method reaches the ship
// as ADMIRAL_AUTO_MERGE, and an autopilot launch whose entry has none has the
// variable unset, whatever the fleet's env or Admiral's environment says. Admiral
// owns the variable, as it does ADMIRAL_AUTOPILOT.
func TestEngineSetsAutoMergeFromThePanel(t *testing.T) {
	cases := []struct {
		name  string
		entry map[string]any
		want  string // "" is unset
	}{
		{"a method", map[string]any{"auto_merge": "rebase"}, "rebase"},
		{"off", nil, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			w := newWorld(t)
			w.writeCue("wait go", "exit 0")
			w.repo("r1", remoteOf("r1"))
			p := w.newTP("a")
			p.mainOrders(apOrder("o1", remoteOf("r1")))
			w.installPlugin("testplug", "testplug")
			w.extra = append(w.extra, "ADMIRAL_AUTO_MERGE=from-admiral-env")
			w.writeAutopilotConfig(map[string]any{"a": tpAutopilot(p, "main", apEntry(true, 1, c.entry))}, 0)
			// The fleet's env sets it too.
			var cfg map[string]any
			if err := json.Unmarshal([]byte(w.readFile(filepath.Join(w.home, "config.json"))), &cfg); err != nil {
				t.Fatal(err)
			}
			env := cfg["fleets"].(map[string]any)["work"].(map[string]any)["claude"].(map[string]any)["env"].(map[string]any)
			env["ADMIRAL_AUTO_MERGE"] = "from-fleet-env"
			b, _ := json.Marshal(cfg)
			writeConfig(t, w.home, string(b))
			w.start()

			ship := w.waitShipCount(15*time.Second, "o1's ship", fromOrder("o1"), 1)[0]
			r := w.record(ship.Session)
			if r.env["ADMIRAL_AUTOPILOT"] != "1" {
				t.Fatalf("ship env ADMIRAL_AUTOPILOT = %q, want 1", r.env["ADMIRAL_AUTOPILOT"])
			}
			got, set := r.env["ADMIRAL_AUTO_MERGE"]
			if c.want == "" && set || c.want != "" && got != c.want {
				t.Errorf("ship env ADMIRAL_AUTO_MERGE = %q (set %v), want %q (unset when empty)", got, set, c.want)
			}
		})
	}
}
