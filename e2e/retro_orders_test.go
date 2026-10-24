package e2e

import (
	"encoding/json"
	"net/http"
	"testing"
	"time"
)

// Retro orders (#117): after `retro_every` finished autopilot runs on a panel,
// Admiral adds `/retro <source>/<panel> --since <ts>` to the resume panel and
// writes a ship-less `retro_queued` record. The real binary, a freeform source
// "orders" on autopilot with retro_every 2, and a freeform source "retros" with
// no autopilot that autopilot.resume_source names.
//
// Wire shape under test:
//
//	config  sources.orders.autopilot.orders.retro_every = 2
//	        autopilot.resume_source = {"source": "retros", "panel": "orders"}
//	order   "/retro orders/orders --since <launched ts of the first counted run>"
//	outcome retro_queued {source, panel, since, text, resume_source, resume_panel, order_id}

func (w *world) retroConfig() {
	w.t.Helper()
	var cfg map[string]any
	if err := json.Unmarshal([]byte(w.config()), &cfg); err != nil {
		w.t.Fatal(err)
	}
	cfg["sources"] = map[string]any{
		"orders": map[string]any{"plugin": "freeform", "name": "Orders",
			"autopilot": map[string]any{"orders": apEntry(true, 1, map[string]any{"retro_every": 2})}},
		"retros": map[string]any{"plugin": "freeform", "name": "Retros"},
	}
	cfg["autopilot"] = map[string]any{"resume_source": resumeTarget("retros", "orders")}
	b, err := json.Marshal(cfg)
	if err != nil {
		w.t.Fatal(err)
	}
	writeConfig(w.t, w.home, string(b))
}

// oneRun adds an order for the checkout, waits for autopilot's ship, lets it
// sail off and waits for its ended record. It returns the ship's launched ts.
// seen holds the sessions of earlier runs, so a ship that is slow to leave the
// list is not taken for this one.
func (w *world) oneRun(name string, seen map[string]bool) string {
	w.t.Helper()
	code, body := send(w.t, http.MethodPost, w.in.LocalURL+"/api/sources/orders/orders",
		map[string]any{"panel": "orders", "text": "Review " + remoteOf(name)})
	w.mustOK(code, body, "add an order")
	fresh := func(s shipJSON) bool { return live(s) && !seen[s.Session] }
	w.waitShipCount(15*time.Second, "the ship of "+name, fresh, 1)
	ship := w.shipsWhere(fresh)[0]
	seen[ship.Session] = true
	w.sail(ship.Session)
	w.waitEvent(ship.Session, "ended", 1)
	for _, m := range w.shipLog(ship.Session) {
		if m["event"] == "launched" {
			return m["ts"].(string)
		}
	}
	w.t.Fatalf("no launched record for %s", ship.Session)
	return ""
}

// AC1 and AC3: one run, a restart, and a second run add exactly one retro order
// that starts at the first run; a third run adds nothing more.
func TestRetroOrderAfterNRunsAcrossARestart(t *testing.T) {
	w := newWorld(t)
	w.writeCue("wait go", "exit 0")
	for _, n := range []string{"r1", "r2", "r3"} {
		w.repo(n, remoteOf(n))
	}
	w.retroConfig()
	w.start()
	w.waitSource("orders", 10*time.Second, "state ok", isOK)
	w.waitSource("retros", 10*time.Second, "state ok", isOK)

	seen := map[string]bool{}
	first := w.oneRun("r1", seen)
	w.holdOrders("a retro was added after one run of two", "retros")

	w.restart()
	w.waitSource("orders", 10*time.Second, "state ok", isOK)
	w.waitSource("retros", 10*time.Second, "state ok", isOK)
	w.holdOrders("a restart added a retro", "retros")

	w.oneRun("r2", seen)
	want := "/retro orders/orders --since " + first
	w.waitOrders("retros", want)
	rec := w.waitOutcome("retro_queued", 1)[0]
	if rec["ship"] != "" || rec["source"] != "orders" || rec["panel"] != "orders" || rec["since"] != first || rec["text"] != want ||
		rec["resume_source"] != "retros" || rec["resume_panel"] != "orders" || rec["order_id"] == nil || rec["order_id"] == "" {
		t.Errorf("retro_queued = %v, want a ship-less record for orders/orders since %s with the order id", rec, first)
	}

	w.oneRun("r3", seen)
	w.holdOrders("a third run added another retro", "retros", want)
	if n := len(w.outcomesOf("retro_queued")); n != 1 {
		t.Errorf("%d retro_queued records after the third run, want 1", n)
	}
}
