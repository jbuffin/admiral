package e2e

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/coder/websocket"
)

// Crew boats and subagents in the ships API, the inbox, closing a crew boat,
// ending with the ship and restart (#152 AC5, AC6, AC7, spec #72 stories 15-17
// and 20). The seam is the built binary on a private tmux socket with the fake
// harness; the checks read GET /api/ships and the `ships` WebSocket message as
// raw JSON, since shipJSON has no crew fields.
//
// Every ship and crew boat of a fleet runs the fleet's one cue. The two fleets
// here park each process on `wait go` after `hook working`, so a test releases
// the ship alone (create <record dir>/<session>/go) or one crew boat alone
// (<record dir>/<session>/crew/<name>/go), and the exit code is the fleet's:
// "zero" sails off when released, "one" anchors.

type apiCrew struct {
	Name         string  `json:"name"`
	Harness      string  `json:"harness"`
	Dir          string  `json:"dir"`
	Status       string  `json:"status"`
	Reason       *string `json:"reason"`
	Launched     string  `json:"launched"`
	LastActivity *string `json:"last_activity"`
}

type apiSub struct {
	ID    string `json:"id"`
	Type  string `json:"type"`
	Start string `json:"start"`
}

type apiShip struct {
	shipJSON
	Reason    string    `json:"reason"`
	StopSince string    `json:"stop_since"`
	Crew      []apiCrew `json:"crew"`
	Subagents []apiSub  `json:"subagents"`
	CrewLive  int       `json:"crew_live"`
	raw       map[string]json.RawMessage
	rawAll    string
}

func parseShip(t *testing.T, raw json.RawMessage) apiShip {
	t.Helper()
	var s apiShip
	if err := json.Unmarshal(raw, &s); err != nil {
		t.Fatalf("ship is not JSON: %v\n%s", err, raw)
	}
	if err := json.Unmarshal(raw, &s.raw); err != nil {
		t.Fatalf("ship is not a JSON object: %v\n%s", err, raw)
	}
	s.rawAll = string(raw)
	return s
}

func parseShips(t *testing.T, body string) []apiShip {
	t.Helper()
	var items []json.RawMessage
	if json.Unmarshal([]byte(body), &items) != nil {
		var wrap struct {
			Ships []json.RawMessage `json:"ships"`
		}
		if err := json.Unmarshal([]byte(body), &wrap); err != nil {
			t.Fatalf("ship list is not JSON: %v\n%s", err, body)
		}
		items = wrap.Ships
	}
	out := make([]apiShip, 0, len(items))
	for _, it := range items {
		out = append(out, parseShip(t, it))
	}
	return out
}

func fetchShips(t *testing.T, in *fleetInst) []apiShip {
	t.Helper()
	code, body := send(t, http.MethodGet, in.LocalURL+"/api/ships", nil)
	if code != 200 {
		t.Fatalf("GET /api/ships = %d, want 200: %s", code, body)
	}
	return parseShips(t, body)
}

// waitShip polls GET /api/ships until the ship satisfies pred.
func (w *world) waitShip(session, what string, pred func(apiShip) bool) apiShip {
	w.t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	last := "(ship not listed)"
	for {
		for _, s := range fetchShips(w.t, w.in) {
			if s.Session == session {
				if pred(s) {
					return s
				}
				last = s.rawAll
			}
		}
		if time.Now().After(deadline) {
			w.t.Fatalf("within 10s GET /api/ships never showed %s for %s; last: %s", what, session, last)
		}
		time.Sleep(100 * time.Millisecond)
	}
}

func (w *world) waitGone(session, what string) {
	w.t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		gone := true
		for _, s := range fetchShips(w.t, w.in) {
			if s.Session == session {
				gone = false
			}
		}
		if gone {
			return
		}
		if time.Now().After(deadline) {
			w.t.Fatalf("within 10s %s: %s was still listed", what, session)
		}
		time.Sleep(100 * time.Millisecond)
	}
}

// wsShip reads `ships` messages until the ship satisfies pred. A timed-out read
// closes the connection, so the caller doesn't reuse it afterwards.
func wsShip(t *testing.T, c *websocket.Conn, d time.Duration, session, what string, pred func(apiShip) bool) apiShip {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), d)
	defer cancel()
	last := ""
	for {
		_, data, err := c.Read(ctx)
		if err != nil {
			t.Fatalf("within %s the websocket never delivered a `ships` message with %s for %s (last ship: %s): %v", d, what, session, last, err)
		}
		var m struct {
			Type  string            `json:"type"`
			Ships []json.RawMessage `json:"ships"`
		}
		if json.Unmarshal(data, &m) != nil || m.Type != "ships" {
			continue
		}
		for _, it := range m.Ships {
			s := parseShip(t, it)
			if s.Session == session {
				last = s.rawAll
				if pred(s) {
					return s
				}
			}
		}
	}
}

func rawIs(s apiShip, key, want string) bool {
	return strings.TrimSpace(string(s.raw[key])) == want
}

// ---- fixtures ----

func newCrewAPIWorld(t *testing.T) *world {
	t.Helper()
	w := newWorld(t)
	w.cueFleets(map[string][]string{
		"zero": {"hook working", "wait go", "exit 0"},
		"one":  {"hook working", "wait go", "exit 1"},
	})
	w.start()
	return w
}

func (w *world) opt(pane, name string) string {
	out, _ := w.tmux("show-options", "-pqv", "-t", pane, name)
	return out
}

func (w *world) waitPaneWorking(pane string) {
	w.t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for w.opt(pane, "@admiral-state") != "working" {
		if time.Now().After(deadline) {
			w.t.Fatalf("pane %s never reached @admiral-state working (the cue's first step)", pane)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// crewShip launches a ship and waits for its cue to make it working. It returns
// the ship and its pane.
func (w *world) crewShip(fleet, repo string) (shipJSON, string) {
	w.t.Helper()
	s := w.launchIn(fleet, "claude", repo)
	w.waitFile(s.Session, "recorded")
	pane := strings.TrimSpace(w.mustTmux("display-message", "-p", "-t", "="+s.Session+":0", "#{pane_id}"))
	w.waitPaneWorking(pane)
	return s, pane
}

// crewBoat launches a teammate and waits for its cue to make it working.
func (w *world) crewBoat(session, name, harness string) crewPane {
	w.t.Helper()
	dir := filepath.Join(w.root, "anywhere")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		w.t.Fatal(err)
	}
	w.expectExit(crewCall(w, session, "", "launch", "--name", name, "--harness", harness, "--dir", dir, "--", "go"), 0, "crew launch "+name)
	w.waitFile(session+"/crew/"+name, "recorded")
	p := w.paneOf(session, name)
	w.waitPaneWorking(p.pane)
	return p
}

func (w *world) hookAt(pane, event, stdin string) {
	w.t.Helper()
	r := w.admiralWith(cliOpts{env: []string{"TMUX=" + w.sock() + ",0,0", "TMUX_PANE=" + pane}, stdin: stdin}, "hook", event)
	w.expectExit(r, 0, "admiral hook "+event)
}

const (
	hookPrompt = `{"hook_event_name":"UserPromptSubmit","prompt":"go"}`
	hookStop   = `{"hook_event_name":"Stop","background_tasks":[],"session_crons":[]}`
	hookSubOne = `{"hook_event_name":"SubagentStart","agent_id":"a0f790cb4a518e0f9","agent_type":"Explore"}`
	hookSubEnd = `{"hook_event_name":"SubagentStop","agent_id":"a0f790cb4a518e0f9","agent_type":"Explore","background_tasks":[]}`
)

// releaseGo lets the ship (name "") or one crew boat past its `wait go`.
func (w *world) releaseGo(session, crew string) {
	w.t.Helper()
	d := filepath.Join(w.rec, session)
	if crew != "" {
		d = filepath.Join(d, "crew", crew)
	}
	if err := os.WriteFile(filepath.Join(d, "go"), nil, 0o600); err != nil {
		w.t.Fatal(err)
	}
}

func alive(pid string) bool {
	n, err := strconv.Atoi(pid)
	return err == nil && syscall.Kill(n, 0) == nil
}

func waitDead(t *testing.T, what, pid string) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for alive(pid) {
		if time.Now().After(deadline) {
			t.Fatalf("%s (pid %s) was still running 10s later", what, pid)
		}
		time.Sleep(100 * time.Millisecond)
	}
}

func crewNames(s apiShip) []string {
	names := []string{}
	for _, c := range s.Crew {
		names = append(names, c.Name)
	}
	return names
}

func crewOf(s apiShip, name string) (apiCrew, bool) {
	for _, c := range s.Crew {
		if c.Name == name {
			return c, true
		}
	}
	return apiCrew{}, false
}

func crewIs(name, status string) func(apiShip) bool {
	return func(s apiShip) bool {
		c, ok := crewOf(s, name)
		return ok && c.Status == status
	}
}

// ---- AC6: the shape on the wire ----

func TestCrewAPI_Shape(t *testing.T) {
	w := newCrewAPIWorld(t)
	a, aPane := w.crewShip("zero", "alpha")
	b, _ := w.crewShip("zero", "beta") // a ship with no crew
	// scout is launched before mate, so launch order and name order differ.
	scout := w.crewBoat(a.Session, "scout", "claude")
	w.crewBoat(a.Session, "mate", "codex")

	// Before any hook, a crew boat has no last_activity.
	w.waitShip(a.Session, "two crew boats", func(s apiShip) bool { return len(s.Crew) == 2 })
	w.hookAt(scout.pane, "PostToolUse", `{"hook_event_name":"PostToolUse","tool_name":"Bash"}`)
	w.hookAt(aPane, "SubagentStart", hookSubOne)

	ready := func(s apiShip) bool {
		sc, ok := crewOf(s, "scout")
		return len(s.Crew) == 2 && len(s.Subagents) == 1 && ok && sc.LastActivity != nil
	}
	check := func(t *testing.T, where string, s apiShip) {
		t.Helper()
		if got := crewNames(s); strings.Join(got, ",") != "mate,scout" {
			t.Fatalf("%s: crew = %v, want [mate scout] sorted by name", where, got)
		}
		dir := filepath.Join(w.root, "anywhere")
		for i, want := range []struct{ name, harness string }{{"mate", "codex"}, {"scout", "claude"}} {
			c := s.Crew[i]
			if c.Name != want.name || c.Harness != want.harness || c.Dir != dir || c.Status != "underway" || c.Reason != nil {
				t.Errorf("%s: crew[%d] = %+v, want %s %s in %s, underway, no reason", where, i, c, want.name, want.harness, dir)
			}
			if _, err := time.Parse(time.RFC3339, c.Launched); err != nil {
				t.Errorf("%s: %s launched %q is not RFC 3339", where, c.Name, c.Launched)
			}
		}
		if s.Crew[0].LastActivity != nil {
			t.Errorf("%s: mate last_activity = %q before any hook, want it omitted", where, *s.Crew[0].LastActivity)
		}
		if la := s.Crew[1].LastActivity; la == nil {
			t.Errorf("%s: scout last_activity is omitted after a hook", where)
		} else if _, err := time.Parse(time.RFC3339, *la); err != nil {
			t.Errorf("%s: scout last_activity %q is not RFC 3339", where, *la)
		}
		if len(s.Subagents) != 1 || s.Subagents[0].ID != "a0f790cb4a518e0f9" || s.Subagents[0].Type != "Explore" {
			t.Errorf("%s: subagents = %+v, want the one Explore entry", where, s.Subagents)
		} else if _, err := time.Parse(time.RFC3339, s.Subagents[0].Start); err != nil {
			t.Errorf("%s: subagent start %q is not RFC 3339", where, s.Subagents[0].Start)
		}
		if s.CrewLive != 2 {
			t.Errorf("%s: crew_live = %d, want 2", where, s.CrewLive)
		}
	}
	emptyList := func(t *testing.T, where string, s apiShip) {
		t.Helper()
		for k, want := range map[string]string{"crew": "[]", "subagents": "[]", "crew_live": "0"} {
			if !rawIs(s, k, want) {
				t.Errorf("%s: %s = %s, want %s on the wire (present, not null)", where, k, s.raw[k], want)
			}
		}
	}

	t.Run("GET /api/ships", func(t *testing.T) {
		w := w.sub(t)
		got := w.waitShip(a.Session, "crew, a subagent and a last_activity", ready)
		check(t, "GET", got)
		emptyList(t, "GET, ship without crew", w.waitShip(b.Session, "the ship without crew", func(apiShip) bool { return true }))
	})

	t.Run("the ships WebSocket message", func(t *testing.T) {
		c := dialWS(t, w.in.instance)
		got := wsShip(t, c, 10*time.Second, a.Session, "crew, a subagent and a last_activity", ready)
		check(t, "WS", got)
		// A connection gets one `ships` message at connect, with every ship, so the
		// second ship is read from a fresh connection.
		emptyList(t, "WS, ship without crew", wsShip(t, dialWS(t, w.in.instance), 10*time.Second, b.Session, "the ship without crew", func(apiShip) bool { return true }))

		// SubagentStop empties the list on the wire within a tick, on the same connection.
		w.sub(t).hookAt(aPane, "SubagentStop", hookSubEnd)
		got = wsShip(t, c, 5*time.Second, a.Session, "an empty subagents list", func(s apiShip) bool { return rawIs(s, "subagents", "[]") })
		if len(got.Crew) != 2 {
			t.Errorf("after SubagentStop crew = %v, want it unchanged", crewNames(got))
		}
		w.sub(t).waitShip(a.Session, "an empty subagents list over GET", func(s apiShip) bool { return rawIs(s, "subagents", "[]") })
	})
}

// ---- the reader rules for @admiral-subagents ----

func TestCrewAPI_SubagentReaderRules(t *testing.T) {
	w := newCrewAPIWorld(t)
	s, pane := w.crewShip("zero", "alpha")
	const t1, t2 = "2026-10-24T10:00:00Z", "2026-10-24T10:05:00Z"
	// Printable JSON with no separator and no newline: an unsafe id (a space, a
	// slash), an unsafe type, a start that isn't RFC 3339 and a duplicate id.
	value := fmt.Sprintf(`[{"id":"good1","type":"Explore","start":%q},`+
		`{"id":"has space","type":"Plan","start":%q},`+
		`{"id":"a/b","type":"Plan","start":%q},`+
		`{"id":"good2","type":"a b","start":%q},`+
		`{"id":"good3","type":"Plan","start":"yesterday"},`+
		`{"id":"good1","type":"Plan","start":%q}]`, t1, t1, t1, t2, t2)
	w.mustTmux("set", "-p", "-t", pane, "@admiral-subagents", value)

	got := w.waitShip(s.Session, "the subagents list read", func(s apiShip) bool { return len(s.Subagents) > 0 })
	want := []apiSub{{"good1", "Explore", t1}, {"good2", "", t2}}
	if fmt.Sprint(got.Subagents) != fmt.Sprint(want) {
		t.Errorf("subagents = %+v, want %+v: unsafe ids, a bad start and a duplicate dropped, an unsafe type blanked, first duplicate wins", got.Subagents, want)
	}

	// A dead ship pane serves no subagents, though the option still holds entries.
	p := w.crewPanes(s.Session)[0]
	pid, err := strconv.Atoi(p.pid)
	if err != nil {
		t.Fatal(err)
	}
	if err := syscall.Kill(pid, syscall.SIGKILL); err != nil {
		t.Fatal(err)
	}
	dead := w.waitShip(s.Session, "the ship anchored with an empty subagents list", func(s apiShip) bool { return s.Status == "anchored" && rawIs(s, "subagents", "[]") })
	if w.opt(pane, "@admiral-subagents") == "" {
		t.Errorf("the dead pane's @admiral-subagents was cleared; the test needs it set (%s)", dead.rawAll)
	}
}

// ---- AC-inbox: a crew boat needing you ----

func TestCrewAPI_InboxCountsCrew(t *testing.T) {
	w := newCrewAPIWorld(t)
	s, shipPane := w.crewShip("one", "alpha")
	scout := w.crewBoat(s.Session, "scout", "claude")
	mate := w.crewBoat(s.Session, "mate", "claude")
	w.hookAt(shipPane, "UserPromptSubmit", hookPrompt) // the ship has its own state

	w.waitShip(s.Session, "an underway ship with two crew boats", func(x apiShip) bool { return x.Status == "underway" && len(x.Crew) == 2 })

	// scout's Stop: the ship reads needs_you for "crew" while its own pane stays working.
	w.hookAt(scout.pane, "Stop", hookStop)
	got := w.waitShip(s.Session, "needs_you for a crew boat", func(x apiShip) bool { return x.Status == "needs_you" })
	if got.Reason != "crew" {
		t.Errorf("ship reason = %q, want crew", got.Reason)
	}
	sc, _ := crewOf(got, "scout")
	if sc.Status != "needs_you" || sc.Reason == nil || *sc.Reason != "stop" {
		t.Errorf("scout on the wire = %+v, want needs_you with reason stop", sc)
	}
	if mt, _ := crewOf(got, "mate"); mt.Status != "underway" {
		t.Errorf("mate = %+v, want underway", mt)
	}
	wantSince := sc.Launched
	if sc.LastActivity != nil {
		wantSince = *sc.LastActivity
	}
	if got.StopSince == "" || got.StopSince != wantSince {
		t.Errorf("stop_since = %q, want the needing boat's last_activity (or launched) %q", got.StopSince, wantSince)
	}
	if st := w.opt(shipPane, "@admiral-state"); st != "working" {
		t.Errorf("ship pane @admiral-state = %q, want working: the wire status never writes the pane", st)
	}
	time.Sleep(1100 * time.Millisecond)
	again := w.waitShip(s.Session, "the ship still needing you", func(x apiShip) bool { return true })
	if again.Status != "needs_you" || again.Reason != "crew" || again.StopSince != got.StopSince {
		t.Errorf("a second later: %s %s since %q, want needs_you crew since %q (never now)", again.Status, again.Reason, again.StopSince, got.StopSince)
	}

	// input-seen on the ship leaves both panes alone.
	code, body := send(t, http.MethodPost, w.in.LocalURL+"/api/ships/"+s.Session+"/input-seen", nil)
	if code/100 != 2 {
		t.Fatalf("input-seen = %d: %s", code, body)
	}
	var resp struct {
		Ship apiShip `json:"ship"`
	}
	if err := json.Unmarshal([]byte(body), &resp); err != nil || resp.Ship.Status != "needs_you" || resp.Ship.Reason != "crew" {
		t.Errorf("input-seen response ship = %s %s (%v), want needs_you crew: %s", resp.Ship.Status, resp.Ship.Reason, err, body)
	}
	if st := w.opt(shipPane, "@admiral-state"); st != "working" {
		t.Errorf("after input-seen the ship pane @admiral-state = %q, want working", st)
	}
	if st := w.opt(scout.pane, "@admiral-state"); st != "needs_you" {
		t.Errorf("after input-seen scout's @admiral-state = %q, want needs_you (a browser's input is not the crew boat's)", st)
	}
	w.waitShip(s.Session, "needs_you crew after input-seen", func(x apiShip) bool { return x.Status == "needs_you" && x.Reason == "crew" })

	// The crew boat's own next prompt brings the ship back to underway.
	w.hookAt(scout.pane, "UserPromptSubmit", hookPrompt)
	back := w.waitShip(s.Session, "underway again", func(x apiShip) bool { return x.Status == "underway" })
	if back.Reason != "" || back.StopSince != "" {
		t.Errorf("underway ship carries reason %q stop_since %q, want neither", back.Reason, back.StopSince)
	}

	// A crew boat that dies while needing you also frees the ship.
	w.hookAt(mate.pane, "Stop", hookStop)
	w.waitShip(s.Session, "needs_you for mate", func(x apiShip) bool { return x.Status == "needs_you" && x.Reason == "crew" })
	w.releaseGo(s.Session, "mate") // exit 1: the pane stays, dead
	w.waitShip(s.Session, "mate anchored and the ship underway", func(x apiShip) bool { return crewIs("mate", "anchored")(x) && x.Status == "underway" })

	// A ship that needs you for its own reason keeps it. scout is not working
	// (it needs you) and mate is anchored, so the lead's Stop is not held.
	w.hookAt(scout.pane, "Stop", hookStop)
	w.hookAt(shipPane, "Stop", hookStop)
	own := w.waitShip(s.Session, "the ship's own stop", func(x apiShip) bool { return x.Status == "needs_you" && crewIs("scout", "needs_you")(x) })
	if own.Reason != "stop" {
		t.Errorf("ship reason = %q while its own state is needs_you and scout also needs you, want stop", own.Reason)
	}
	if st, r := w.opt(shipPane, "@admiral-state"), w.opt(shipPane, "@admiral-reason"); st != "needs_you" || r != "stop" {
		t.Errorf("ship pane = %q %q, want needs_you stop", st, r)
	}
}

// ---- closing one crew boat ----

func TestCrewAPI_CloseRoute(t *testing.T) {
	w := newCrewAPIWorld(t)
	s, _ := w.crewShip("zero", "alpha")
	live := w.crewBoat(s.Session, "live", "claude")
	w.crewBoat(s.Session, "keep", "claude")
	gone := w.crewBoat(s.Session, "gone", "claude")
	pid, err := strconv.Atoi(gone.pid)
	if err != nil {
		t.Fatal(err)
	}
	if err := syscall.Kill(pid, syscall.SIGKILL); err != nil {
		t.Fatal(err)
	}
	w.waitShip(s.Session, "gone anchored and two live", func(x apiShip) bool { return crewIs("gone", "anchored")(x) && x.CrewLive == 2 })

	closeURL := func(session, name string) string {
		return w.in.LocalURL + "/api/ships/" + session + "/crew/" + name + "/close"
	}
	hasWindow := func(name string) bool {
		for _, p := range w.crewPanes(s.Session) {
			if p.crew == name {
				return true
			}
		}
		return false
	}

	if code, body := send(t, http.MethodPost, closeURL(s.Session, "live"), nil); code/100 != 2 {
		t.Fatalf("close a live crew boat = %d: %s", code, body)
	}
	got := w.waitShip(s.Session, "live closed", func(x apiShip) bool { return len(x.Crew) == 2 })
	if strings.Join(crewNames(got), ",") != "gone,keep" || got.CrewLive != 1 {
		t.Errorf("after closing live: crew %v, crew_live %d, want [gone keep] and 1", crewNames(got), got.CrewLive)
	}
	if hasWindow("live") {
		t.Errorf("the closed crew boat still has a window")
	}
	waitDead(t, "the closed crew boat", live.pid)

	if code, body := send(t, http.MethodPost, closeURL(s.Session, "gone"), nil); code/100 != 2 {
		t.Fatalf("close an anchored crew boat = %d: %s", code, body)
	}
	got = w.waitShip(s.Session, "gone closed", func(x apiShip) bool { return len(x.Crew) == 1 })
	if strings.Join(crewNames(got), ",") != "keep" || got.CrewLive != 1 || hasWindow("gone") {
		t.Errorf("after closing the anchored boat: crew %v, crew_live %d, want [keep] and 1", crewNames(got), got.CrewLive)
	}

	for _, c := range []struct{ session, name, want string }{
		{"nosuchship", "keep", "ship nosuchship not found"},
		{s.Session, "ghost", "crew boat ghost not found"},
		{s.Session, "live", "crew boat live not found"}, // already closed
	} {
		code, body := send(t, http.MethodPost, closeURL(c.session, c.name), nil)
		if code != http.StatusNotFound || !strings.Contains(body, c.want) {
			t.Errorf("close %s/%s = %d %s, want 404 %q", c.session, c.name, code, body, c.want)
		}
	}
	if !hasWindow("keep") {
		t.Errorf("a refused close removed keep's window")
	}
}

// ---- AC5: ending with the ship ----

func TestCrewAPI_Ending(t *testing.T) {
	w := newCrewAPIWorld(t)

	t.Run("a crew boat that exits alone anchors and never ends the ship", func(t *testing.T) {
		w := w.sub(t)
		s, _ := w.crewShip("zero", "fold")
		a := w.crewBoat(s.Session, "a", "claude")
		b := w.crewBoat(s.Session, "b", "claude")
		w.waitShip(s.Session, "two live crew boats", func(x apiShip) bool { return x.CrewLive == 2 })

		w.releaseGo(s.Session, "a") // exit 0, alone
		w.waitShip(s.Session, "a anchored on the wire", crewIs("a", "anchored"))
		time.Sleep(4 * time.Second) // longer than SailOffDelay (3 s)
		now := w.waitShip(s.Session, "the ship", func(apiShip) bool { return true })
		if now.Status != "underway" || !w.hasSession(s.Session) {
			t.Errorf("after a crew boat exited 0 the ship is %q (session alive: %v), want underway and alive", now.Status, w.hasSession(s.Session))
		}
		if c, _ := crewOf(now, "b"); c.Status != "underway" || !alive(b.pid) || now.CrewLive != 1 {
			t.Errorf("b = %+v alive=%v crew_live=%d, want underway, alive and 1", c, alive(b.pid), now.CrewLive)
		}

		// Sail-off: the ship's own exit 0 takes the live crew boat with it.
		w.releaseGo(s.Session, "")
		w.waitGone(s.Session, "the ship sailed off")
		if w.hasSession(s.Session) {
			t.Errorf("session %s still exists after sail-off", s.Session)
		}
		waitDead(t, "crew boat b after sail-off", b.pid)
	})

	t.Run("Dismiss of an anchored ship ends its crew", func(t *testing.T) {
		w := w.sub(t)
		s, _ := w.crewShip("one", "dismiss")
		a := w.crewBoat(s.Session, "a", "claude")
		b := w.crewBoat(s.Session, "b", "codex")
		w.releaseGo(s.Session, "") // exit 1: the ship anchors
		got := w.waitShip(s.Session, "the ship anchored with two live crew boats", func(x apiShip) bool { return x.Status == "anchored" && x.CrewLive == 2 })
		if strings.Join(crewNames(got), ",") != "a,b" || !alive(a.pid) || !alive(b.pid) {
			t.Fatalf("before Dismiss: crew %v, alive %v/%v; an anchored ship's crew keeps running", crewNames(got), alive(a.pid), alive(b.pid))
		}

		if code := w.dismiss(s.Session); code/100 != 2 {
			t.Fatalf("Dismiss = %d, want 2xx", code)
		}
		if w.hasSession(s.Session) {
			t.Errorf("session %s still exists after Dismiss", s.Session)
		}
		waitDead(t, "crew boat a after Dismiss", a.pid)
		waitDead(t, "crew boat b after Dismiss", b.pid)
	})
}

// ---- AC7: restart ----

func TestCrewAPI_RestartKeepsCrew(t *testing.T) {
	w := newCrewAPIWorld(t)
	s, shipPane := w.crewShip("zero", "alpha")
	scout := w.crewBoat(s.Session, "scout", "claude")
	mate := w.crewBoat(s.Session, "mate", "codex")
	w.hookAt(scout.pane, "PostToolUse", `{"hook_event_name":"PostToolUse","tool_name":"Bash"}`)
	w.hookAt(mate.pane, "Stop", hookStop)
	w.hookAt(shipPane, "SubagentStart", hookSubOne)

	before := w.waitShip(s.Session, "two crew boats (one needing you), a subagent, a last_activity", func(x apiShip) bool {
		sc, _ := crewOf(x, "scout")
		return len(x.Crew) == 2 && len(x.Subagents) == 1 && x.CrewLive == 2 && sc.LastActivity != nil && crewIs("mate", "needs_you")(x)
	})

	w.restart()
	after := w.waitShip(s.Session, "the crew after the restart", func(x apiShip) bool { return len(x.Crew) == 2 && len(x.Subagents) == 1 })
	for _, k := range []string{"crew", "subagents", "crew_live"} {
		if !bytes.Equal(before.raw[k], after.raw[k]) {
			t.Errorf("%s after restart = %s, want what it was: %s", k, after.raw[k], before.raw[k])
		}
	}
	if after.Status != "needs_you" || after.Reason != "crew" {
		t.Errorf("ship after restart = %s %s, want needs_you crew as before", after.Status, after.Reason)
	}
}
