package autopilot

import (
	"fmt"
	"net/http"
	"reflect"
	"slices"
	"testing"
	"time"

	"github.com/jbuffin/admiral/internal/config"
	"github.com/jbuffin/admiral/internal/launcher"
	"github.com/jbuffin/admiral/internal/outcomes"
	"github.com/jbuffin/admiral/internal/ships"
	"github.com/jbuffin/admiral/internal/sources"
)

// The engine's passes (#114): which orders it launches under the two caps, the
// launched set, the failure backoff and the skipped record. A pass is called
// by hand, with a fake clock and fakes for the listings, the ships, the
// directory and the launch, so none of this needs tmux.

type rig struct {
	t        *testing.T
	home     string
	now      time.Time
	cfg      *config.Config
	ships    []ships.Ship
	src      []Source
	paused   map[[2]string]bool
	dirs     map[string]string // order id → checkout; none means no matching checkout
	failWith *launcher.Error   // what Launch answers while set
	launches []launcher.Request
	records  []outcomes.Record
	engine   *Engine

	// The retro seams (#117); retro_test.go drives them.
	lines   []outcomes.Line   // the outcome log the engine counts from
	logs    []string          // what the engine logged
	adds    []addCall         // every AddOrder call
	addErr  func(n int) error // the error of the nth AddOrder call (1-based), nil for success
	hold    bool              // Record keeps its writes until confirm
	held    []heldWrite
	minutes int // the clock of the log's lines
}

func entry(max int) config.PanelAutopilot {
	return config.PanelAutopilot{Enabled: true, Fleet: "work", Harness: "claude", Skill: config.Skill{Kind: "none"},
		Args: []string{"--panel-arg"}, MaxShips: max, LowStakes: "never"}
}

// newRig has one source "s" with panel "p" on autopilot (cap 5, global cap 10)
// listing the given orders; every order has a checkout.
func newRig(t *testing.T, orders ...string) *rig {
	t.Helper()
	r := &rig{t: t, home: t.TempDir(), now: time.Date(2026, 10, 22, 12, 0, 0, 0, time.UTC),
		paused: map[[2]string]bool{}, dirs: map[string]string{}}
	r.cfg = &config.Config{AutopilotMaxShips: 10,
		Fleets:     []config.Fleet{{Key: "work", Name: "Work", SearchRoots: []string{"/w"}, Harnesses: []config.Harness{{Name: "claude"}}}},
		SourceList: []config.Source{{Key: "s", Plugin: "p", Autopilot: map[string]config.PanelAutopilot{"p": entry(5)}}}}
	r.src = []Source{{Key: "s", Listed: true, Panels: []sources.Panel{panel("p", orders...)}}}
	for _, o := range orders {
		r.dirs[o] = "/w/" + o
	}
	r.engine = r.newEngine()
	return r
}

func panel(id string, orders ...string) sources.Panel {
	p := sources.Panel{ID: id, Title: id, Orders: []sources.Order{}}
	for _, o := range orders {
		p.Orders = append(p.Orders, sources.Order{ID: o, Title: "Title " + o})
	}
	return p
}

// newEngine opens the launched set in r.home, so a second engine on the same
// home is a restart.
func (r *rig) newEngine() *Engine {
	r.t.Helper()
	store, err := OpenStore(r.home)
	if err != nil {
		r.t.Fatal(err)
	}
	return &Engine{
		Config:  func() *config.Config { return r.cfg },
		Ships:   func() []ships.Ship { return slices.Clone(r.ships) },
		Sources: func() []Source { return r.src },
		Paused:  func(source, panel string) bool { return r.paused[[2]string{source, panel}] },
		Dir: func(_ config.Fleet, _ string, o sources.Order) (string, string) {
			if d, ok := r.dirs[o.ID]; ok {
				return d, ""
			}
			return "", "no checkout has the order's repository"
		},
		Launch: func(req launcher.Request) (ships.Ship, *launcher.Error) {
			r.launches = append(r.launches, req)
			if r.failWith != nil {
				return ships.Ship{}, r.failWith
			}
			sh := ships.Ship{Session: "ship-" + req.OrderID, Source: req.Source, OrderID: req.OrderID, Panel: req.Panel,
				Status: ships.StatusUnderway}
			r.ships = append(r.ships, sh)
			return sh, nil
		},
		Record: func(rec outcomes.Record, done func(error)) {
			r.records = append(r.records, rec)
			w := heldWrite{rec, done}
			if r.hold {
				r.held = append(r.held, w)
				return
			}
			r.commit(w, nil)
		},
		Log:      func() []outcomes.Line { return slices.Clone(r.lines) },
		AddOrder: r.addOrder,
		Logf:     func(format string, args ...any) { r.logs = append(r.logs, fmt.Sprintf(format, args...)) },
		Now:      func() time.Time { return r.now },
		Store:    store,
	}
}

func (r *rig) pass() []string {
	r.t.Helper()
	before := len(r.launches)
	r.engine.Pass()
	ids := []string{}
	for _, l := range r.launches[before:] {
		ids = append(ids, l.Source+"/"+l.Panel+"/"+l.OrderID)
	}
	return ids
}

func (r *rig) recordsOf(event string) []outcomes.Record {
	var out []outcomes.Record
	for _, rec := range r.records {
		if rec.Event == event {
			out = append(out, rec)
		}
	}
	return out
}

func (r *rig) setShips(list ...ships.Ship) { r.ships = list }

func want(t *testing.T, what string, got, want []string) {
	t.Helper()
	if len(got) == 0 && len(want) == 0 {
		return
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("%s: launched %v, want %v", what, got, want)
	}
}

func ship(status, source, panel, order string) ships.Ship {
	return ships.Ship{Session: "x-" + order + status, Source: source, Panel: panel, OrderID: order, Status: status}
}

// AC 2, 3 and 9: a ship counts toward the caps while it is underway or needs
// you, whoever launched it; an anchored or sailing-off one does not. The panel
// cap counts only the ships launched from that panel.
func TestEngineCaps(t *testing.T) {
	hand := func(status string) ships.Ship { return ship(status, "", "", "") }
	tests := []struct {
		name   string
		global int
		panel  int
		ships  []ships.Ship
		want   []string
	}{
		{"anchored and sailing off ships leave the room", 1, 5,
			[]ships.Ship{hand(ships.StatusAnchored), hand(ships.StatusSailingOff)}, []string{"s/p/o1"}},
		{"two hand ships fill a global cap of 2", 2, 5,
			[]ships.Ship{hand(ships.StatusUnderway), hand(ships.StatusUnderway)}, nil},
		{"a needs-you hand ship counts toward the global cap", 2, 5,
			[]ships.Ship{hand(ships.StatusNeedsYou), hand(ships.StatusUnderway)}, nil},
		{"one hand ship leaves one slot", 2, 5, []ships.Ship{hand(ships.StatusUnderway)}, []string{"s/p/o1"}},
		{"a needs-you ship from the panel counts toward its cap", 10, 1,
			[]ships.Ship{ship(ships.StatusNeedsYou, "s", "p", "other")}, nil},
		{"an anchored ship from the panel does not", 10, 1,
			[]ships.Ship{ship(ships.StatusAnchored, "s", "p", "other")}, []string{"s/p/o1"}},
		{"a ship from another panel of the source does not count toward this one", 10, 1,
			[]ships.Ship{ship(ships.StatusUnderway, "s", "q", "other")}, []string{"s/p/o1"}},
		{"a ship from the same panel id of another source does not either", 10, 1,
			[]ships.Ship{ship(ships.StatusUnderway, "t", "p", "other")}, []string{"s/p/o1"}},
		{"a hand ship without a panel counts toward the global cap only", 10, 1,
			[]ships.Ship{hand(ships.StatusUnderway)}, []string{"s/p/o1"}},
		{"the panel cap stops the pass once it is full", 10, 2, nil, []string{"s/p/o1", "s/p/o2"}},
		{"the global cap stops the pass once it is full", 2, 5, nil, []string{"s/p/o1", "s/p/o2"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			r := newRig(t, "o1", "o2", "o3")
			r.cfg.AutopilotMaxShips = tc.global
			r.cfg.SourceList[0].Autopilot["p"] = entry(tc.panel)
			r.setShips(tc.ships...)
			want(t, tc.name, r.pass(), tc.want)
		})
	}
}

// The panel gets a new ship only after the first is off the caps (AC 1, as the
// engine sees it).
func TestEngineLaunchesTheNextOrderWhenTheShipLeaves(t *testing.T) {
	r := newRig(t, "o1", "o2", "o3")
	r.cfg.SourceList[0].Autopilot["p"] = entry(1)
	want(t, "first pass", r.pass(), []string{"s/p/o1"})
	want(t, "while o1 runs", r.pass(), nil)
	r.ships[0].Status = ships.StatusAnchored
	want(t, "once o1 anchored", r.pass(), []string{"s/p/o2"})
}

// Eligible panels are the enabled, unpaused ones, taken in sidebar order: the
// sources in config order, then each source's panel_order. The listings come
// in another order on purpose.
func TestEngineSidebarOrderAndEligibility(t *testing.T) {
	r := newRig(t)
	r.cfg.SourceList = []config.Source{
		{Key: "b", Plugin: "p", PanelOrder: []string{"p2", "p1"}, Autopilot: map[string]config.PanelAutopilot{
			"p1": entry(1), "p2": entry(1), "off": func() config.PanelAutopilot { e := entry(1); e.Enabled = false; return e }(),
			"paused": entry(1)}},
		{Key: "a", Plugin: "p", Autopilot: map[string]config.PanelAutopilot{"p1": entry(1), "unconfigured": {}}},
	}
	r.src = []Source{
		{Key: "a", Listed: true, Panels: []sources.Panel{panel("unconfigured", "u1"), panel("p1", "a1")}},
		{Key: "b", Listed: true, Panels: []sources.Panel{panel("p1", "b1"), panel("off", "x1"), panel("paused", "y1"), panel("p2", "b2")}},
	}
	for _, o := range []string{"u1", "a1", "b1", "b2", "x1", "y1"} {
		r.dirs[o] = "/w/" + o
	}
	r.paused[[2]string{"b", "paused"}] = true
	want(t, "sidebar order", r.pass(), []string{"b/p2/b2", "b/p1/b1", "a/p1/a1"})
}

// A skipped order (no checkout) and a backed-off one let the pass move on to the
// panel's next order.
func TestEngineMovesPastAnOrderItCannotLaunch(t *testing.T) {
	r := newRig(t, "o1", "o2", "o3")
	delete(r.dirs, "o1")
	r.cfg.SourceList[0].Autopilot["p"] = entry(1)
	want(t, "o1 has no checkout", r.pass(), []string{"s/p/o2"})
}

// The launch is an ordinary Request built from the panel's entry, carrying the
// panel (a ship records it in @admiral-panel).
func TestEngineRequestComesFromThePanelEntry(t *testing.T) {
	r := newRig(t, "o1")
	r.cfg.SourceList[0].Autopilot["p"] = config.PanelAutopilot{Enabled: true, Fleet: "work", Harness: "claude",
		Skill: config.Skill{Kind: "skill", Name: "review"}, Args: []string{"--permission-mode", "auto"}, MaxShips: 1, LowStakes: "advisor"}
	r.pass()
	if len(r.launches) != 1 {
		t.Fatalf("launches = %d, want 1", len(r.launches))
	}
	got := r.launches[0]
	exp := launcher.Request{Fleet: "work", Harness: "claude", Dir: "/w/o1", Skill: launcher.Skill{Kind: "skill", Name: "review"},
		Source: "s", OrderID: "o1", Panel: "p", OriginSource: "s", OriginPanel: "p", Autopilot: true, LowStakes: "advisor", ExtraArgs: []string{"--permission-mode", "auto"}}
	if !reflect.DeepEqual(got, exp) {
		t.Errorf("request = %+v, want %+v", got, exp)
	}
}

// AC 4: an order that was launched and has gone isn't launched again while it is
// still listed, even though its ship's hiding is over. It is dropped from the
// launched set once the source lists it no more, so it can launch when it returns.
func TestEngineLaunchesAnOrderOnceWhileItStaysListed(t *testing.T) {
	r := newRig(t, "o1")
	want(t, "first pass", r.pass(), []string{"s/p/o1"})
	r.setShips() // the ship is gone; the order is visible again
	want(t, "ship gone, order still listed", r.pass(), nil)

	r.src[0].Panels = []sources.Panel{panel("p")}
	want(t, "order unlisted", r.pass(), nil)
	r.src[0].Panels = []sources.Panel{panel("p", "o1")}
	want(t, "order back", r.pass(), []string{"s/p/o1"})
}

// The set is pruned from the raw listing, and only for a source that has listed:
// at start-up the runners have no panels yet, and pruning then would wipe it.
func TestEngineDoesNotPruneFromASourceThatHasNotListed(t *testing.T) {
	r := newRig(t, "o1")
	r.pass()
	r.setShips()

	r.src[0].Listed, r.src[0].Panels = false, nil
	want(t, "source starting", r.pass(), nil)
	r.src[0].Listed, r.src[0].Panels = true, []sources.Panel{panel("p", "o1")}
	want(t, "source listed again", r.pass(), nil) // still in the set
}

func TestEngineLaunchedSetSurvivesARestart(t *testing.T) {
	r := newRig(t, "o1", "o2")
	r.cfg.SourceList[0].Autopilot["p"] = entry(1)
	want(t, "before", r.pass(), []string{"s/p/o1"})
	r.setShips()
	r.engine = r.newEngine()
	want(t, "after the restart", r.pass(), []string{"s/p/o2"})
}

// A source in error or restarting still offers its last-known orders.
func TestEngineLaunchesFromASourceThatIsDown(t *testing.T) {
	r := newRig(t, "o1")
	r.src[0].Listed = false
	want(t, "source down", r.pass(), []string{"s/p/o1"})
}

// An order that a ship of any status holds is not visible, so it is not launched.
func TestEngineSkipsAnOrderAShipHolds(t *testing.T) {
	r := newRig(t, "o1", "o2")
	r.setShips(ship(ships.StatusAnchored, "s", "", "o1")) // launched by hand, now anchored
	want(t, "o1 held", r.pass(), []string{"s/p/o2"})
}

// A launch by hand of an order from an autopilot panel joins the launched set.
func TestEngineDoesNotRelaunchAnOrderLaunchedByHand(t *testing.T) {
	r := newRig(t, "o1", "o2")
	r.engine.Launched("s", "o1") // the hand launch; its ship is gone by the pass
	want(t, "after a hand launch", r.pass(), []string{"s/p/o2"})
}

// The third failed launch stops the engine trying that order and marks it hot;
// before it, the order waits 10 minutes, then an hour (#114 "Failures").
func TestEngineBacksOffAndGivesUp(t *testing.T) {
	r := newRig(t, "o1", "o2")
	r.cfg.SourceList[0].Autopilot["p"] = entry(1)
	r.failWith = &launcher.Error{Status: http.StatusInternalServerError, Msg: "boom"}
	failed := func(n int) outcomes.Record {
		t.Helper()
		recs := r.recordsOf("launch_failed")
		if len(recs) != n {
			t.Fatalf("launch_failed records = %d, want %d", len(recs), n)
		}
		return recs[n-1]
	}
	at := func(d time.Duration) string { return r.now.Add(d).UTC().Format(time.RFC3339) }

	want(t, "attempt 1", r.pass(), []string{"s/p/o1", "s/p/o2"}) // o1 failed, the pass moved on to o2
	f := failed(2)
	if f.Ship != "" || f.Fields["order_id"] != "o2" {
		t.Fatalf("latest record = %+v, want o2's", f)
	}
	first := r.recordsOf("launch_failed")[0]
	for k, v := range map[string]any{"source": "s", "panel": "p", "order_id": "o1", "title": "Title o1", "reason": "boom",
		"attempt": 1, "next_retry": at(10 * time.Minute)} {
		if first.Fields[k] != v {
			t.Errorf("attempt 1 field %s = %v, want %v", k, first.Fields[k], v)
		}
	}
	r.records = nil

	r.now = r.now.Add(9 * time.Minute)
	want(t, "9 minutes on", r.pass(), nil)
	r.now = r.now.Add(time.Minute)
	want(t, "10 minutes on", r.pass(), []string{"s/p/o1", "s/p/o2"})
	second := r.recordsOf("launch_failed")[0]
	if second.Fields["attempt"] != 2 || second.Fields["next_retry"] != at(time.Hour) {
		t.Errorf("attempt 2 fields = %v, want attempt 2 and next_retry %s", second.Fields, at(time.Hour))
	}
	r.records = nil

	r.now = r.now.Add(59 * time.Minute)
	want(t, "59 minutes after the second", r.pass(), nil)
	if r.engine.Hot("s", "o1") {
		t.Error("o1 is hot before its third failure")
	}
	r.now = r.now.Add(time.Minute)
	want(t, "an hour after the second", r.pass(), []string{"s/p/o1", "s/p/o2"})
	third := r.recordsOf("launch_failed")[0]
	if third.Fields["attempt"] != 3 {
		t.Errorf("attempt 3 field attempt = %v", third.Fields["attempt"])
	}
	if v, ok := third.Fields["next_retry"]; ok && v != nil {
		t.Errorf("the third failure has a next_retry %v, want none: Admiral gives up", v)
	}
	if !r.engine.Hot("s", "o1") {
		t.Error("o1 is not hot after its third failure")
	}

	r.now = r.now.Add(24 * time.Hour)
	want(t, "a day after giving up", r.pass(), nil)
	if len(r.recordsOf("launch_failed")) != 2 { // the two of the third round: o1's and o2's
		t.Errorf("a given-up order wrote another launch_failed record")
	}

	// It stops being hot once the source stops listing it.
	r.src[0].Panels = []sources.Panel{panel("p")}
	r.pass()
	if r.engine.Hot("s", "o1") {
		t.Error("o1 stays hot after it stopped being listed")
	}
}

func TestEngineKeepsFailureStateAcrossARestart(t *testing.T) {
	r := newRig(t, "o1")
	r.failWith = &launcher.Error{Status: http.StatusInternalServerError, Msg: "boom"}
	r.pass()
	r.engine = r.newEngine()
	want(t, "right after the restart", r.pass(), nil) // still backed off
	r.now = r.now.Add(10 * time.Minute)
	want(t, "after the backoff", r.pass(), []string{"s/p/o1"})
	if got := r.recordsOf("launch_failed")[1].Fields["attempt"]; got != 2 {
		t.Errorf("attempt after the restart = %v, want 2", got)
	}
}

// A 409 (the order already has a ship, or a launch of it is running) and a 503
// (ships are not known yet) skip the order for this pass. Neither is a failure:
// no record, no backoff, never hot, and the next pass tries it again.
func TestEngineTreatsAConflictOrUnknownShipsAsNoFailure(t *testing.T) {
	for _, lerr := range []*launcher.Error{
		{Status: http.StatusConflict, Msg: "this order already has a ship"},
		{Status: http.StatusServiceUnavailable, Msg: "ships are not known yet"},
	} {
		t.Run(lerr.Msg, func(t *testing.T) {
			r := newRig(t, "o1")
			r.failWith = lerr
			for i := 0; i < 4; i++ {
				want(t, "pass", r.pass(), []string{"s/p/o1"})
				r.now = r.now.Add(time.Minute)
			}
			if n := len(r.recordsOf("launch_failed")); n != 0 {
				t.Errorf("launch_failed records = %d, want 0", n)
			}
			if r.engine.Hot("s", "o1") {
				t.Error("o1 is hot")
			}
		})
	}
}

// AC 7: no matching checkout is one skipped record per order until the order
// stops being listed, and no launch.
func TestEngineRecordsASkippedOrderOnce(t *testing.T) {
	r := newRig(t, "o1")
	delete(r.dirs, "o1")
	for i := 0; i < 3; i++ {
		want(t, "no checkout", r.pass(), nil)
	}
	recs := r.recordsOf("skipped")
	if len(recs) != 1 {
		t.Fatalf("skipped records = %d, want 1: %+v", len(recs), r.records)
	}
	rec := recs[0]
	if rec.Ship != "" {
		t.Errorf("skipped record ship = %q, want none", rec.Ship)
	}
	for k, v := range map[string]any{"source": "s", "panel": "p", "order_id": "o1", "title": "Title o1",
		"reason": "no checkout has the order's repository"} {
		if rec.Fields[k] != v {
			t.Errorf("skipped field %s = %v, want %v", k, rec.Fields[k], v)
		}
	}

	r.src[0].Panels = []sources.Panel{panel("p")}
	r.pass()
	r.src[0].Panels = []sources.Panel{panel("p", "o1")}
	r.pass()
	if n := len(r.recordsOf("skipped")); n != 2 {
		t.Errorf("skipped records after the order left and came back = %d, want 2", n)
	}

	r.dirs["o1"] = "/w/o1" // a checkout appears
	want(t, "checkout found", r.pass(), []string{"s/p/o1"})
}

// A paused panel launches nothing, and a resumed one does.
func TestEnginePausedPanelLaunchesNothing(t *testing.T) {
	r := newRig(t, "o1")
	r.paused[[2]string{"s", "p"}] = true
	want(t, "paused", r.pass(), nil)
	r.paused[[2]string{"s", "p"}] = false
	want(t, "resumed", r.pass(), []string{"s/p/o1"})
}
