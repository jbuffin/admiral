package e2e

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// `admiral crew` (#152, spec #72 stories 2-10, 16 and 20): teammates in their
// own windows of the ship's tmux session. The seam is the built binary on a
// private tmux socket with the fake harness; every call runs with
// ADMIRAL_SHIP set to the ship's session, as a skill inside the ship would, and
// the ship's own ADMIRAL_REPORT and ADMIRAL_INBOX are dropped by baseEnv.
//
// The ship and every crew boat on one harness share FAKE_CUE, so the cue is
// `hook working` for all of them and a crew boat is anchored by killing its
// process (remain-on-exit keeps the dead pane).

type crewRow struct {
	Name, Harness, Dir, State, Launched string
	LastActivity                        *string `json:"last_activity"`
	raw                                 map[string]any
}

func crewCall(w *world, ship string, stdin string, args ...string) cliResult {
	w.t.Helper()
	o := cliOpts{stdin: stdin}
	if ship != "" {
		o.env = []string{"ADMIRAL_SHIP=" + ship}
	}
	return w.admiralWith(o, append([]string{"crew"}, args...)...)
}

func crewList(w *world, ship string) map[string]crewRow {
	w.t.Helper()
	r := crewCall(w, ship, "", "list", "--json")
	w.expectExit(r, 0, "crew list --json")
	var raws []map[string]any
	if err := json.Unmarshal([]byte(r.stdout), &raws); err != nil {
		w.t.Fatalf("crew list --json is not a JSON array: %v\n%s", err, r.stdout)
	}
	rows := map[string]crewRow{}
	for _, m := range raws {
		b, _ := json.Marshal(m)
		var row crewRow
		_ = json.Unmarshal(b, &row)
		row.raw = m
		for _, k := range []string{"name", "harness", "dir", "state", "launched", "last_activity"} {
			if _, ok := m[k]; !ok {
				w.t.Fatalf("crew list --json row %v lacks %q", m, k)
			}
		}
		rows[row.Name] = row
	}
	return rows
}

// waitCrewState polls crew list until the boat has the state.
func waitCrewState(w *world, ship, name, state string) {
	w.t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		r := crewCall(w, ship, "", "list", "--json")
		var rows []crewRow
		if r.code == 0 && json.Unmarshal([]byte(r.stdout), &rows) == nil {
			for _, row := range rows {
				if row.Name == name && row.State == state {
					return
				}
			}
		}
		if time.Now().After(deadline) {
			w.t.Fatalf("crew boat %s never reached state %s within 10s; last list: %s%s", name, state, r.stdout, r.stderr)
		}
		time.Sleep(100 * time.Millisecond)
	}
}

type crewPane struct{ window, crew, pane, pid, dead string }

// panes lists the panes of the session, one per window.
func (w *world) crewPanes(session string) []crewPane {
	w.t.Helper()
	out := w.mustTmux("list-panes", "-s", "-t", "="+session, "-F",
		"#{window_index}\t#{@admiral-crew-name}\t#{pane_id}\t#{pane_pid}\t#{pane_dead}")
	var ps []crewPane
	for _, l := range strings.Split(out, "\n") {
		f := strings.Split(l, "\t")
		if len(f) == 5 {
			ps = append(ps, crewPane{f[0], f[1], f[2], f[3], f[4]})
		}
	}
	return ps
}

func (w *world) paneOf(session, crew string) crewPane {
	w.t.Helper()
	for _, p := range w.crewPanes(session) {
		if p.crew == crew {
			return p
		}
	}
	w.t.Fatalf("session %s has no window for crew boat %q: %v", session, crew, w.crewPanes(session))
	return crewPane{}
}

func TestCrew_LaunchSendListCloseAndRestart(t *testing.T) {
	w := newWorld(t)
	w.writeCue("hook working")
	w.start()
	ship := w.launch(w.order("work", "claude", "alpha", noSkill, "go"))
	w.waitFile(ship.Session, "cues-done")
	other := filepath.Join(w.root, "anywhere") // not in a search root: --dir takes any existing directory
	if err := os.MkdirAll(other, 0o755); err != nil {
		t.Fatal(err)
	}
	sess := ship.Session

	t.Run("without a ship it exits 2", func(t *testing.T) {
		w := w.sub(t)
		for _, args := range [][]string{{"list", "--json"}, {"launch", "--name", "x", "--dir", other, "--", "hi"}, {"close", "x"}} {
			r := crewCall(w, "", "", args...)
			w.expectExit(r, 2, "crew "+args[0]+" outside a ship")
			if !strings.Contains(r.stderr, "not inside an Admiral ship") {
				t.Errorf("crew %s stderr = %q, want it to say `not inside an Admiral ship`", args[0], r.stderr)
			}
		}
	})

	t.Run("launch", func(t *testing.T) {
		w := w.sub(t)
		// --ship by display name, with no ADMIRAL_SHIP at all.
		r := crewCall(w, "", "", "launch", "--ship", ship.Name, "--name", "scout", "--dir", other, "--", "check the thing")
		w.expectExit(r, 0, "crew launch scout")
		r = crewCall(w, sess, "", "launch", "--name", "mate", "--harness", "codex", "--dir", other, "--", "-- second job")
		w.expectExit(r, 0, "crew launch mate --harness codex")

		scout := w.record(sess + "/crew/scout")
		w.waitFile(sess+"/crew/scout", "cues-done")
		wantArgv := []string{"--fa-claude", "--settings", filepath.Join(w.home, "harness", "claude-settings.json"), "--", "check the thing"}
		if !reflect.DeepEqual(scout.argv, wantArgv) {
			t.Errorf("scout argv = %q, want %q", scout.argv, wantArgv)
		}
		if scout.harness != "claude" || scout.cwd != other {
			t.Errorf("scout ran as %q in %q, want claude in %q", scout.harness, scout.cwd, other)
		}
		if !strings.HasPrefix(scout.env["PATH"], filepath.Join(w.home, "bin")+":") {
			t.Errorf("scout PATH = %q, want it to start with %s/bin", scout.env["PATH"], w.home)
		}
		for _, k := range []string{"ADMIRAL_SHIP", "ADMIRAL_REPORT", "ADMIRAL_INBOX"} {
			if v, ok := scout.env[k]; ok {
				t.Errorf("scout has %s=%q; a teammate gets none of the ship's contract", k, v)
			}
		}
		if scout.env["FLEET_VAR"] != "fleet-value" {
			t.Errorf("scout FLEET_VAR = %q, want the fleet's dock env", scout.env["FLEET_VAR"])
		}
		wopts := w.waitFile(sess+"/crew/scout", "window-options")
		for _, want := range []string{"@admiral-crew-name scout", "@admiral-harness claude", "@admiral-dir " + other, "@admiral-launched "} {
			if !strings.Contains(wopts, want) {
				t.Errorf("window options at harness start lack %q (set them before the harness runs):\n%s", want, wopts)
			}
		}

		mate := w.record(sess + "/crew/mate")
		w.waitFile(sess+"/crew/mate", "cues-done")
		if mate.harness != "codex" || len(mate.argv) < 3 || mate.argv[0] != "--fa-codex" ||
			mate.argv[len(mate.argv)-2] != "--" || mate.argv[len(mate.argv)-1] != "-- second job" ||
			!has(mate.argv, `tui.notification_method="bel"`) {
			t.Errorf("mate = %s %q, want codex with its dock args, Admiral's -c hook flags, then -- and the prompt", mate.harness, mate.argv)
		}

		// The ship is still window 0 and the current window; the crew windows are new ones.
		cur := w.mustTmux("display-message", "-p", "-t", "="+sess+":", "#{window_index}")
		first := w.crewPanes(sess)[0]
		if cur != "0" || first.window != "0" || first.crew != "" {
			t.Errorf("current window = %s, first pane = %+v; the ship must stay window 0 and current", cur, first)
		}
		for _, name := range []string{"scout", "mate"} {
			p := w.paneOf(sess, name)
			win := "=" + sess + ":" + p.window
			if p.window == "0" {
				t.Errorf("crew boat %s is in window 0", name)
			}
			for opt, want := range map[string]string{"remain-on-exit": "on", "monitor-bell": "on", "@admiral-crew-name": name} {
				if got := w.mustTmux("show-options", "-wv", "-t", win, opt); got != want {
					t.Errorf("window of %s: %s = %q, want %q", name, opt, got, want)
				}
			}
			if _, err := time.Parse(time.RFC3339, w.mustTmux("show-options", "-wv", "-t", win, "@admiral-launched")); err != nil {
				t.Errorf("window of %s: @admiral-launched is not RFC 3339: %v", name, err)
			}
		}
	})

	t.Run("a ship with crew still reads as it did", func(t *testing.T) {
		w := w.sub(t)
		w.in.waitShipsList(t, 10*time.Second, "the ship underway with crew windows", func(l []shipJSON) bool {
			return len(l) == 1 && l[0].Session == sess && l[0].Status == "underway" && l[0].Harness == "claude" && l[0].Dir == w.checkout("alpha")
		})
		r := w.admiral("ships", "--port", w.in.Port)
		w.expectExit(r, 0, "admiral ships")
		if row := shipTable(t, r.stdout)[sess]; row.status != "underway" || row.harness != "claude" || row.repo != "alpha" {
			t.Errorf("ships row = %+v, want underway claude alpha", row)
		}
	})

	t.Run("list, hook time and needs_you", func(t *testing.T) {
		w := w.sub(t)
		rows := crewList(w, sess)
		if len(rows) != 2 {
			t.Fatalf("crew list = %v, want scout and mate", rows)
		}
		s, m := rows["scout"], rows["mate"]
		if s.Harness != "claude" || s.Dir != other || s.State != "underway" || m.Harness != "codex" || m.State != "underway" {
			t.Errorf("rows = %+v / %+v, want scout claude underway and mate codex underway, dir %s", s, m, other)
		}
		if _, err := time.Parse(time.RFC3339, s.Launched); err != nil {
			t.Errorf("launched %q is not RFC 3339", s.Launched)
		}
		if s.raw["last_activity"] != nil || m.raw["last_activity"] != nil {
			t.Errorf("last_activity before any hook = %v / %v, want null", s.raw["last_activity"], m.raw["last_activity"])
		}

		// A hook event from scout's pane: `admiral hook` on the private socket.
		hook := w.admiralWith(cliOpts{
			env:   []string{"TMUX=" + w.sock() + ",0,0", "TMUX_PANE=" + w.paneOf(sess, "scout").pane},
			stdin: `{"hook_event_name":"PostToolUse","tool_name":"Bash"}`,
		}, "hook", "PostToolUse")
		w.expectExit(hook, 0, "admiral hook")
		rows = crewList(w, sess)
		if la := rows["scout"].LastActivity; la == nil {
			t.Errorf("scout last_activity is null after a hook")
		} else if _, err := time.Parse(time.RFC3339, *la); err != nil {
			t.Errorf("scout last_activity %q is not RFC 3339", *la)
		}
		if rows["mate"].raw["last_activity"] != nil {
			t.Errorf("mate last_activity = %v, want null: only scout's pane had a hook", rows["mate"].raw["last_activity"])
		}

		// The cue's `hook working` named the crew pane, not the session's active one.
		for _, name := range []string{"scout", "mate"} {
			if got := w.mustTmux("show-options", "-pv", "-t", w.paneOf(sess, name).pane, "@admiral-state"); got != "working" {
				t.Errorf("%s pane @admiral-state = %q, want working", name, got)
			}
		}
		w.mustTmux("set", "-p", "-t", w.paneOf(sess, "mate").pane, "@admiral-state", "needs_you")
		rows = crewList(w, sess)
		if rows["mate"].State != "needs_you" || rows["scout"].State != "underway" {
			t.Errorf("states = mate %s, scout %s; want needs_you and underway (never `working`)", rows["mate"].State, rows["scout"].State)
		}
		w.mustTmux("set", "-p", "-t", w.paneOf(sess, "mate").pane, "@admiral-state", "working")
	})

	t.Run("send types the text and submits it", func(t *testing.T) {
		w := w.sub(t)
		w.expectExit(crewCall(w, sess, "", "send", "scout", "hello team"), 0, "crew send with text")
		w.expectExit(crewCall(w, sess, "via stdin", "send", "scout"), 0, "crew send from stdin")
		// Without the altscreen cue the fake has no bracketed paste on: the text, then Enter.
		want := "hello team\rvia stdin\r"
		deadline := time.Now().Add(10 * time.Second)
		for {
			got, _ := os.ReadFile(filepath.Join(w.rec, sess, "crew", "scout", "stdin"))
			if string(got) == want {
				break
			}
			if time.Now().After(deadline) {
				t.Fatalf("scout stdin = %q after 10s, want %q", got, want)
			}
			time.Sleep(50 * time.Millisecond)
		}
		if got, _ := os.ReadFile(filepath.Join(w.rec, sess, "crew", "mate", "stdin")); len(got) != 0 {
			t.Errorf("mate received %q; send went to the wrong window", got)
		}
	})

	t.Run("refusals", func(t *testing.T) {
		w := w.sub(t)
		r := crewCall(w, sess, "", "launch", "--name", "scout", "--dir", other, "--", "again")
		w.expectExit(r, 5, "a taken name")
		for _, bad := range []string{"Scout", "has_underscore", "has space", ""} {
			r := crewCall(w, sess, "", "launch", "--name", bad, "--dir", other, "--", "hi")
			if r.code == 0 || r.code == 5 {
				t.Errorf("launch --name %q exit %d, want a refusal", bad, r.code)
			}
		}
		if r := crewCall(w, sess, "", "launch", "--name", "ghost", "--dir", filepath.Join(other, "missing"), "--", "hi"); r.code == 0 {
			t.Errorf("launch with a missing --dir exited 0")
		}
		for _, args := range [][]string{{"send", "nobody", "hi"}, {"close", "nobody"}} {
			r := crewCall(w, sess, "", args...)
			w.expectExit(r, 1, "crew "+args[0]+" to an unknown name")
			if !strings.Contains(r.stderr, "nobody") {
				t.Errorf("crew %s stderr = %q, want it to name `nobody`", args[0], r.stderr)
			}
		}
		// A harness the ship's fleet has no dock for.
		w.writeCue("hook working")
		lone := w.launch(w.order("claudeonly", "claude", "beta", noSkill, "go"))
		w.waitFile(lone.Session, "cues-done")
		w.expectExit(crewCall(w, lone.Session, "", "launch", "--name", "x", "--harness", "codex", "--dir", other, "--", "hi"), 1, "a harness with no dock")
		for _, p := range w.crewPanes(lone.Session) {
			if p.crew != "" {
				t.Errorf("a refused launch left a crew window: %+v", p)
			}
		}
		for _, p := range w.crewPanes(sess) {
			if p.crew != "" && p.crew != "scout" && p.crew != "mate" {
				t.Errorf("a refused launch left a crew window: %+v", p)
			}
		}
	})

	t.Run("a dead process anchors the crew boat", func(t *testing.T) {
		w := w.sub(t)
		pid, err := strconv.Atoi(w.paneOf(sess, "mate").pid)
		if err != nil {
			t.Fatal(err)
		}
		if err := syscall.Kill(pid, syscall.SIGKILL); err != nil {
			t.Fatal(err)
		}
		waitCrewState(w, sess, "mate", "anchored")
		if rows := crewList(w, sess); rows["scout"].State != "underway" {
			t.Errorf("scout = %s after mate died, want underway", rows["scout"].State)
		}
		w.expectExit(crewCall(w, sess, "", "send", "mate", "anyone there?"), 4, "send to an anchored crew boat")
	})

	t.Run("the crew comes back after Admiral restarts", func(t *testing.T) {
		w := w.sub(t)
		before := crewList(w, sess)
		w.restart()
		w.in.waitShipsList(t, 10*time.Second, "the ship after the restart", func(l []shipJSON) bool {
			return len(l) == 2 && l[0].Status == "underway" && l[1].Status == "underway"
		})
		after := crewList(w, sess)
		if len(after) != 2 || !reflect.DeepEqual(rowsView(before), rowsView(after)) {
			t.Errorf("crew after restart = %v, want the same as before: %v", rowsView(after), rowsView(before))
		}
	})

	t.Run("close removes the window, live or anchored", func(t *testing.T) {
		w := w.sub(t)
		w.expectExit(crewCall(w, sess, "", "close", "mate"), 0, "close an anchored crew boat")
		w.expectExit(crewCall(w, sess, "", "close", "scout"), 0, "close a live crew boat")
		if rows := crewList(w, sess); len(rows) != 0 {
			t.Errorf("crew list after closing both = %v, want none", rows)
		}
		ps := w.crewPanes(sess)
		if len(ps) != 1 || ps[0].window != "0" {
			t.Errorf("panes after closing the crew = %+v, want only the ship's window 0", ps)
		}
	})
}

func rowsView(rows map[string]crewRow) map[string][6]string {
	out := map[string][6]string{}
	for n, r := range rows {
		la := "null"
		if r.LastActivity != nil {
			la = *r.LastActivity
		}
		out[n] = [6]string{r.Harness, r.Dir, r.State, r.Launched, la, n}
	}
	return out
}
