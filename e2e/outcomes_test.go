package e2e

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// The outcome log (rung 1 of autopilot): the real binary, the fake harness, and
// the lines in $ADMIRAL_HOME/outcomes/<YYYY-MM>.jsonl.

type outRec = map[string]any

// outcomeLog is every record in the log, oldest month first.
func (w *world) outcomeLog() []outRec {
	w.t.Helper()
	files, _ := filepath.Glob(filepath.Join(w.home, "outcomes", "*.jsonl"))
	sort.Strings(files)
	out := []outRec{}
	for _, f := range files {
		for _, l := range strings.Split(w.readFile(f), "\n") {
			if l == "" {
				continue
			}
			var m outRec
			if err := json.Unmarshal([]byte(l), &m); err != nil {
				w.t.Fatalf("%s holds a line that is not a JSON object: %q: %v", f, l, err)
			}
			out = append(out, m)
		}
	}
	return out
}

func (w *world) shipLog(session string) []outRec {
	w.t.Helper()
	out := []outRec{}
	for _, m := range w.outcomeLog() {
		if m["ship"] == session {
			out = append(out, m)
		}
	}
	return out
}

func eventsOf(log []outRec) []string {
	out := []string{}
	for _, m := range log {
		out = append(out, m["event"].(string))
	}
	return out
}

// waitShipLog waits for the ship's records to satisfy pred.
func (w *world) waitShipLog(session string, d time.Duration, what string, pred func([]outRec) bool) []outRec {
	w.t.Helper()
	deadline := time.Now().Add(d)
	for {
		log := w.shipLog(session)
		if pred(log) {
			return log
		}
		if time.Now().After(deadline) {
			w.t.Fatalf("within %s the outcome log never showed %s for %s; its records: %v", d, what, session, log)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func (w *world) waitEvent(session, event string, n int) []outRec {
	w.t.Helper()
	return w.waitShipLog(session, 15*time.Second, strconv.Itoa(n)+" "+event+" record(s)", func(l []outRec) bool {
		c := 0
		for _, m := range l {
			if m["event"] == event {
				c++
			}
		}
		return c >= n
	})
}

func lastOf(log []outRec, event string) outRec {
	for i := len(log) - 1; i >= 0; i-- {
		if log[i]["event"] == event {
			return log[i]
		}
	}
	return nil
}

// outFleets writes a config with one claude fleet per key, each with its cue
// lines, and the given sources.
func (w *world) outFleets(cues map[string][]string, defs ...srcDef) {
	w.t.Helper()
	fleets := map[string]any{}
	for key, lines := range cues {
		path := filepath.Join(w.root, "cue-"+key)
		if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
			w.t.Fatal(err)
		}
		env := w.harnessEnv()
		env["FAKE_CUE"] = path
		fleets[key] = map[string]any{"name": key, "color": "#336699", "search_roots": []string{w.work},
			"claude": map[string]any{"executable": w.fakeExe("claude"), "env": env}}
	}
	cfg := map[string]any{"version": 1, "fleets": fleets}
	if len(defs) > 0 {
		cfg["sources"] = json.RawMessage(rawSources(defs...))
	}
	b, err := json.Marshal(cfg)
	if err != nil {
		w.t.Fatal(err)
	}
	writeConfig(w.t, w.home, string(b))
}

// touch creates a file in the ship's recording directory, for the cue `wait`.
func (w *world) touch(session, name string) {
	w.t.Helper()
	if err := os.WriteFile(filepath.Join(w.rec, session, name), nil, 0o600); err != nil {
		w.t.Fatal(err)
	}
}

// shipPaths are the report file and the inbox directory the ship was given.
func shipPaths(r record) (report, inbox string) {
	return r.env["ADMIRAL_REPORT"], strings.TrimSuffix(r.env["ADMIRAL_INBOX"], "/")
}

func (w *world) seedShipFiles(r record, reportJSON string) (report, inbox string) {
	w.t.Helper()
	report, inbox = shipPaths(r)
	if err := os.WriteFile(report, []byte(reportJSON), 0o600); err != nil {
		w.t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(inbox, "note.txt"), []byte("hello"), 0o600); err != nil {
		w.t.Fatal(err)
	}
	return report, inbox
}

func waitGone(t *testing.T, d time.Duration, paths ...string) {
	t.Helper()
	deadline := time.Now().Add(d)
	for {
		var left []string
		for _, p := range paths {
			if _, err := os.Lstat(p); err == nil {
				left = append(left, p)
			}
		}
		if len(left) == 0 {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("within %s %v were still there", d, left)
		}
		time.Sleep(100 * time.Millisecond)
	}
}

func exists(p string) bool { _, err := os.Lstat(p); return err == nil }

// killPane kills the harness process; the pane stays dead under remain-on-exit.
func (w *world) killPane(session string) {
	w.t.Helper()
	pid, err := strconv.Atoi(strings.TrimSpace(w.mustTmux("list-panes", "-t", session, "-F", "#{pane_pid}")))
	if err != nil {
		w.t.Fatal(err)
	}
	if err := syscall.Kill(pid, syscall.SIGKILL); err != nil {
		w.t.Fatalf("kill pane process %d: %v", pid, err)
	}
}

func num(t *testing.T, m outRec, key string) float64 {
	t.Helper()
	v, ok := m[key].(float64)
	if !ok {
		t.Fatalf("record %v has no number %q", m, key)
	}
	return v
}

func isNull(m outRec, key string) bool {
	v, ok := m[key]
	return ok && v == nil
}

func (w *world) stderrLines(substr string) int {
	n := 0
	for _, l := range strings.Split(w.in.stderrBuf.String(), "\n") {
		if strings.Contains(strings.ToLower(l), substr) {
			n++
		}
	}
	return n
}

// AC1 and the valid half of AC2: an order launched from a source, a permission
// stop answered, a clean exit. Four lines in order, the report in `ended`, and
// the report file and inbox gone once the ship has sailed off.
func TestOutcomeLogOfAnOrderShip(t *testing.T) {
	w := newWorld(t)
	p := w.newTP("a")
	p.mainOrders(map[string]any{"id": "o1", "title": "Order one", "text": "do it", "url": "https://example.com/issues/1"})
	w.installPlugin("testplug", "testplug")
	w.outFleets(map[string][]string{"f": {"hook working", "sleep 1", `event PermissionRequest {"tool_name":"Bash"}`, "wait go", "sleep 1", "exit 0"}},
		tpSource("a", p, nil))
	w.start()
	w.waitSource("a", 10*time.Second, "order o1", func(s sourceJSON) bool { return isOK(s) && s.hasOrder("o1") })

	ship := w.mustLaunchFrom("f", "a", "o1")
	rec := w.record(ship.Session)
	w.waitEvent(ship.Session, "needs_you", 1)
	time.Sleep(1500 * time.Millisecond) // the stop lasts a measurable while
	if code := w.inputSeen(ship.Session); code/100 != 2 {
		t.Fatalf("input-seen = %d, want 2xx", code)
	}
	w.waitEvent(ship.Session, "underway", 1)
	const report = `{"status":"done","pr":"https://example.com/pull/7"}`
	reportPath, inboxPath := w.seedShipFiles(rec, report)
	w.touch(ship.Session, "go")
	log := w.waitEvent(ship.Session, "ended", 1)

	if got, want := eventsOf(log), []string{"launched", "needs_you", "underway", "ended"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("records = %v, want %v", got, want)
	}
	if n := len(w.outcomeLog()); n != 4 {
		t.Errorf("the log has %d lines, want 4", n)
	}
	var prev time.Time
	for _, m := range log {
		ts, err := time.Parse(time.RFC3339, m["ts"].(string))
		if err != nil || !strings.HasSuffix(m["ts"].(string), "Z") || ts.Before(prev) {
			t.Errorf("record %v: ts must be RFC 3339 UTC and not before the one above (%s)", m, prev)
		}
		prev = ts
		if m["v"] != float64(1) {
			t.Errorf("record %v: v = %v, want 1", m, m["v"])
		}
	}
	if file := filepath.Join(w.home, "outcomes", log[0]["ts"].(string)[:7]+".jsonl"); !exists(file) {
		t.Errorf("%s doesn't exist: the file is named by the month of ts", file)
	}

	l := log[0]
	skill, _ := l["skill"].(map[string]any)
	if l["fleet"] != "f" || l["harness"] != "claude" || l["source"] != "a" || l["panel"] != "main" ||
		l["order_id"] != "o1" || l["order_url"] != "https://example.com/issues/1" || l["title"] != "Order one" ||
		l["dir"] != ship.Dir || l["autopilot"] != false || skill["kind"] != "none" || skill["name"] != "" {
		t.Errorf("launched = %v, want fleet f, harness claude, source a, panel main, order o1 with its title and url, dir %s, skill none/\"\", autopilot false", l, ship.Dir)
	}
	if log[1]["reason"] != "permission:Bash" {
		t.Errorf("needs_you = %v, want reason permission:Bash", log[1])
	}
	if got := num(t, log[2], "waited_s"); got < 1 {
		t.Errorf("underway waited_s = %v, want at least 1: the stop lasted 1.5 s", got)
	}
	e := log[3]
	if num(t, e, "exit_code") != 0 || !isNull(e, "signal") || num(t, e, "duration_s") <= 0 ||
		num(t, e, "needs_you_count") != 1 || num(t, e, "needs_you_s") < 1 {
		t.Errorf("ended = %v, want exit_code 0, signal null, positive duration_s, needs_you_count 1, needs_you_s >= 1", e)
	}
	var want any
	_ = json.Unmarshal([]byte(report), &want)
	if !reflect.DeepEqual(e["report"], want) {
		t.Errorf("ended report = %v, want the ship's report %v", e["report"], want)
	}

	// Sailing off removes the files, and only once `ended` is in the log (it is
	// already: waitEvent returned).
	waitGone(t, 10*time.Second, reportPath, inboxPath)
	if w.hasSession(ship.Session) {
		t.Errorf("the session of the sailed-off ship still exists")
	}
}

// AC2's other half, the report bound, Dismiss and its cleanup: an invalid, an
// oversized and a missing report each give `report: null`, and a log line only
// for the first two. The files stay while the ship is anchored and go with
// Dismiss, after `ended`, and `dismissed` follows `ended`.
func TestOutcomeReportInvalidMissingAndDismiss(t *testing.T) {
	w := newWorld(t)
	w.outFleets(map[string][]string{"f": {"hook working", "wait go", "exit 3"}})
	w.start()

	cases := []struct {
		repo, content string
		line          bool
	}{
		{"bad", `{"status":`, true},
		{"big", `{"summary":"` + strings.Repeat("x", 1<<20) + `"}`, true},
		{"none", "", false},
	}
	lines := 0
	for _, c := range cases {
		ship := w.launchIn("f", "claude", c.repo)
		rec := w.record(ship.Session)
		reportPath, inboxPath := shipPaths(rec)
		if c.content != "" {
			w.seedShipFiles(rec, c.content)
		} else if err := os.WriteFile(filepath.Join(inboxPath, "note.txt"), []byte("hello"), 0o600); err != nil {
			t.Fatal(err)
		}
		w.touch(ship.Session, "go")
		log := w.waitEvent(ship.Session, "ended", 1)
		e := lastOf(log, "ended")
		if !isNull(e, "report") || num(t, e, "exit_code") != 3 || !isNull(e, "signal") {
			t.Errorf("%s: ended = %v, want report null, exit_code 3, signal null", c.repo, e)
		}
		if c.line {
			lines++
		}
		deadline := time.Now().Add(3 * time.Second)
		for w.stderrLines("report") < lines && time.Now().Before(deadline) {
			time.Sleep(50 * time.Millisecond)
		}
		time.Sleep(300 * time.Millisecond)
		if got := w.stderrLines("report"); got != lines {
			t.Errorf("%s: Admiral logged %d lines about the report so far, want %d (one per bad file, none for a missing one); stderr: %s",
				c.repo, got, lines, w.in.stderrBuf.String())
		}

		// An anchored ship keeps its files; Dismiss removes them after `ended`.
		w.in.waitStatusList(t, 5*time.Second, ship.Session, "anchored")
		if !exists(inboxPath) || (c.content != "" && !exists(reportPath)) {
			t.Errorf("%s: the report or inbox is gone while the ship is only anchored", c.repo)
		}
		if code := w.dismiss(ship.Session); code/100 != 2 {
			t.Fatalf("Dismiss = %d, want 2xx", code)
		}
		log = w.waitEvent(ship.Session, "dismissed", 1)
		if got, want := eventsOf(log), []string{"launched", "ended", "dismissed"}; !reflect.DeepEqual(got, want) {
			t.Errorf("%s: records = %v, want %v", c.repo, got, want)
		}
		waitGone(t, 5*time.Second, reportPath, inboxPath)
	}
}

// AC4: restarting Admiral adds no second `launched`; a ship killed while Admiral
// is stopped gets `ended` on the next start, once, charged only for the time it
// ran, and stamped with the time it was written.
func TestOutcomeRestartAddsNoLaunchedAndEndsADeadShip(t *testing.T) {
	w := newWorld(t)
	w.outFleets(map[string][]string{"f": {"hook working"}})
	w.start()
	ship := w.launchIn("f", "claude", "restart")
	w.waitFile(ship.Session, "cues-done")
	log := w.waitEvent(ship.Session, "launched", 1)
	if l := log[0]; l["source"] != "" || l["panel"] != "" || l["fleet"] != "f" {
		t.Errorf("launched = %v, want fleet f and an empty source and panel for a launch from the dock", l)
	}

	w.restart()
	w.in.waitStatusList(t, 10*time.Second, ship.Session, "underway")
	time.Sleep(2 * time.Second)
	if got := eventsOf(w.shipLog(ship.Session)); !reflect.DeepEqual(got, []string{"launched"}) {
		t.Fatalf("after a restart the ship's records are %v, want only the one launched", got)
	}

	w.in.Stop()
	w.killPane(ship.Session)
	time.Sleep(4 * time.Second) // Admiral's downtime must not count as the ship's run
	restartedAt := time.Now()
	w.restart()
	log = w.waitEvent(ship.Session, "ended", 1)
	e := lastOf(log, "ended")
	if !isNull(e, "exit_code") || e["signal"] != "KILL" || !isNull(e, "report") {
		t.Errorf("ended = %v, want exit_code null, signal KILL, report null", e)
	}
	if d := num(t, e, "duration_s"); d >= 4 {
		t.Errorf("duration_s = %v: the ship ran for about a second before it was killed, 4 s before Admiral came back", d)
	}
	if ts, err := time.Parse(time.RFC3339, e["ts"].(string)); err != nil || ts.Before(restartedAt.Add(-time.Second)) {
		t.Errorf("ended ts = %v, want the time it was written, after %s", e["ts"], restartedAt)
	}
	if got := eventsOf(log); !reflect.DeepEqual(got, []string{"launched", "ended"}) {
		t.Errorf("records = %v, want launched, ended", got)
	}

	w.restart()
	w.in.waitStatusList(t, 10*time.Second, ship.Session, "anchored")
	time.Sleep(2 * time.Second)
	if got := eventsOf(w.shipLog(ship.Session)); !reflect.DeepEqual(got, []string{"launched", "ended"}) {
		t.Errorf("after another restart the records are %v, want ended to stay single", got)
	}
}

// A ship that exited 0 while Admiral was stopped gets `ended` before the start-up
// kill, and then its files go.
func TestOutcomeRestartEndsAShipThatSailedOff(t *testing.T) {
	w := newWorld(t)
	w.outFleets(map[string][]string{"f": {"hook working", "sleep 1", "exit 0"}})
	w.start()
	ship := w.launchIn("f", "claude", "zero")
	rec := w.record(ship.Session)
	w.waitEvent(ship.Session, "launched", 1)
	w.in.Stop()
	w.waitFile(ship.Session, "cues-done")
	reportPath, inboxPath := w.seedShipFiles(rec, `{"status":"done"}`)

	w.restart()
	waitGone(t, 15*time.Second, reportPath, inboxPath)
	if w.hasSession(ship.Session) {
		t.Errorf("the session of an exit-0 ship still exists after the restart")
	}
	log := w.shipLog(ship.Session)
	if got := eventsOf(log); !reflect.DeepEqual(got, []string{"launched", "ended"}) {
		t.Fatalf("records = %v, want launched, ended", got)
	}
	e := log[1]
	if num(t, e, "exit_code") != 0 || !reflect.DeepEqual(e["report"], map[string]any{"status": "done"}) {
		t.Errorf("ended = %v, want exit_code 0 and the ship's report", e)
	}
}

// AC5: an outcomes directory that can't be written doesn't stop launching, logs
// one error however many records fail, and the next record after the directory
// becomes writable is written.
func TestOutcomeUnwritableDirectoryDoesNotStopLaunching(t *testing.T) {
	w := newWorld(t)
	w.outFleets(map[string][]string{"f": {"hook working", "sleep 1", "hook needs_you"}})
	blocker := filepath.Join(w.home, "outcomes")
	if err := os.WriteFile(blocker, []byte("a file where the directory belongs"), 0o600); err != nil {
		t.Fatal(err)
	}
	w.start()

	a := w.launchIn("f", "claude", "unw1")
	b := w.launchIn("f", "claude", "unw2")
	w.in.waitStatusList(t, 10*time.Second, a.Session, "needs_you")
	time.Sleep(1 * time.Second)
	if code, _ := send(t, http.MethodGet, w.in.LocalURL+"/api/ships", nil); code != 200 || !w.hasSession(b.Session) {
		t.Errorf("with an unwritable log GET /api/ships = %d, ship b present: %v", code, w.hasSession(b.Session))
	}
	if got := w.stderrLines("outcome"); got != 1 {
		t.Errorf("Admiral logged %d lines about the outcome log across four failed records, want 1; stderr: %s", got, w.in.stderrBuf.String())
	}

	if err := os.Remove(blocker); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(blocker, 0o700); err != nil {
		t.Fatal(err)
	}
	c := w.launchIn("f", "claude", "unw3")
	w.waitEvent(c.Session, "launched", 1)
	if got := w.stderrLines("outcome"); got != 1 {
		t.Errorf("a record written after the repair added log lines: %d in all, want 1", got)
	}
}
