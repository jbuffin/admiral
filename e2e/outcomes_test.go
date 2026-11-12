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

type outRec = map[string]any

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

// eventsOf lists the lifecycle events; a `usage` record lands after `ended` at no fixed point among later ones, so the tests that look at it read it by name.
func usageCount(log []outRec) int {
	n := 0
	for _, m := range log {
		if m["event"] == "usage" {
			n++
		}
	}
	return n
}

func eventsOf(log []outRec) []string {
	out := []string{}
	for _, m := range log {
		if m["event"] != "usage" {
			out = append(out, m["event"].(string))
		}
	}
	return out
}

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
			"claude":  map[string]any{"executable": w.fakeExe("claude"), "env": env},
			"codex":   map[string]any{"executable": w.fakeExe("codex"), "env": env},
			"copilot": map[string]any{"executable": w.fakeExe("copilot"), "env": env}}
	}
	cfg := map[string]any{"version": 1, "fleets": fleets}
	if len(defs) > 0 {
		cfg["sources"] = json.RawMessage(rawSourcesIn(firstFleet(w.t, cfg), defs...))
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

func (w *world) waitPaneDead(session string) {
	w.t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for strings.TrimSpace(w.mustTmux("list-panes", "-t", session, "-F", "#{pane_dead}")) != "1" {
		if time.Now().After(deadline) {
			w.t.Fatalf("the pane of %s never died", session)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

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

func TestOutcomeLogOfAnOrderShip(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	p := w.newTP("a")
	p.mainOrders(map[string]any{"id": "o1", "title": "Order one", "text": "do it", "url": "https://example.com/issues/1"})
	w.installPlugin("testplug", "testplug")
	w.outFleets(map[string][]string{
		"f": {"hook working", "sleep 1", `event PermissionRequest {"tool_name":"Bash"}`, "wait go", "sleep 1", "usage", "exit 0"},
		"u": {"hook working", "usage", "wait go", "exit 0"}},
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
	w.waitEvent(ship.Session, "ended", 1)
	if exists(filepath.Join(w.rec, ship.Session, "usage-error")) {
		t.Fatalf("the fake could not write its usage line: %s", w.readFile(filepath.Join(w.rec, ship.Session, "usage-error")))
	}
	if lines, _ := filepath.Glob(filepath.Join(w.userHom, ".claude", "projects", "*", "*.jsonl")); len(lines) != 1 || !strings.Contains(w.readFile(lines[0]), `"cost-state"`) {
		t.Fatalf("the fake wrote no cost-state line, only %v", lines)
	}
	log := w.waitEvent(ship.Session, "usage", 1)

	// The usage record sits beside `ended`, in either order.
	var rest []outRec
	var usageRec outRec
	for _, m := range log {
		if m["event"] == "usage" {
			usageRec = m
		} else {
			rest = append(rest, m)
		}
	}
	if got, want := eventsOf(rest), []string{"launched", "needs_you", "underway", "ended"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("records = %v, want %v and one usage", eventsOf(log), want)
	}
	if n := len(w.outcomeLog()); n != 5 {
		t.Errorf("the log has %d lines, want 5", n)
	}
	wantUsage(t, usageRec, "f", "claude", usageClaudeTokens, usageClaudeMoney)
	log = rest
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
	w.stayOneUsage(ship.Session)

	// A Codex and a Copilot ship each write one usage record at their exit, with their own figures.
	for _, c := range []struct {
		harness string
		tokens  map[string]any
		money   map[string]any
	}{
		{"codex", usageCodexTokens, nil},
		{"copilot", usageCopilotTokens, usageCopilotMoney},
	} {
		s := w.launch(map[string]any{"fleet": "u", "harness": c.harness, "dir": w.checkout("proj"), "skill": noSkill})
		w.record(s.Session)
		w.touch(s.Session, "go") // the fake's wait cue passes at once; its usage cue runs first
		w.waitEvent(s.Session, "ended", 1)
		if exists(filepath.Join(w.rec, s.Session, "usage-error")) {
			t.Fatalf("the %s fake could not write its usage line: %s", c.harness, w.readFile(filepath.Join(w.rec, s.Session, "usage-error")))
		}
		got := lastOf(w.waitEvent(s.Session, "usage", 1), "usage")
		wantUsage(t, got, "u", c.harness, c.tokens, c.money)
		w.stayOneUsage(s.Session)
	}
}

var (
	usageClaudeTokens  = map[string]any{"input": 100.0, "output": 40.0, "reasoning": 10.0, "cache_read": 300.0, "cache_write": 40.0}
	usageClaudeMoney   = map[string]any{"amount": 0.25, "basis": "estimate", "incomplete": false}
	usageCodexTokens   = map[string]any{"input": 300.0, "output": 40.0, "reasoning": 20.0, "cache_read": 200.0, "cache_write": 0.0}
	usageCopilotTokens = map[string]any{"input": 10.0, "output": 30.0, "reasoning": 0.0, "cache_read": 100.0, "cache_write": 40.0}
	usageCopilotMoney  = map[string]any{"amount": 2.5, "basis": "credits", "incomplete": false, "premium_requests": 3.0}
)

// wantUsage checks a usage record's fleet, harness, tokens and money (nil: null), and that it has no crew boats and no round.
func wantUsage(t *testing.T, m outRec, fleet, harness string, tokens map[string]any, money map[string]any) {
	t.Helper()
	if m["fleet"] != fleet || m["harness"] != harness {
		t.Errorf("usage = %v, want fleet %s and harness %s", m, fleet, harness)
	}
	if !reflect.DeepEqual(m["tokens"], tokens) {
		t.Errorf("usage tokens = %v, want %v", m["tokens"], tokens)
	}
	if money == nil {
		if !isNull(m, "money") {
			t.Errorf("usage money = %v, want null", m["money"])
		}
	} else if !reflect.DeepEqual(m["money"], money) {
		t.Errorf("usage money = %v, want %v", m["money"], money)
	}
	if _, has := m["round"]; has {
		t.Errorf("usage = %v, a ship that was never woken has no round", m)
	}
}

// stayOneUsage holds long enough for a second usage record to land after the first.
func (w *world) stayOneUsage(session string) {
	w.t.Helper()
	time.Sleep(1500 * time.Millisecond)
	n := 0
	for _, m := range w.shipLog(session) {
		if m["event"] == "usage" {
			n++
		}
	}
	if n != 1 {
		w.t.Errorf("%s has %d usage records, want exactly 1 per exit", session, n)
	}
}

func TestOutcomeReportInvalidMissingAndDismiss(t *testing.T) {
	t.Parallel()
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
		for w.stderrLines("its report") < lines && time.Now().Before(deadline) {
			time.Sleep(50 * time.Millisecond)
		}
		time.Sleep(300 * time.Millisecond)
		if got := w.stderrLines("its report"); got != lines {
			t.Errorf("%s: Admiral logged %d lines about the report so far, want %d (one per bad file, none for a missing one); stderr: %s",
				c.repo, got, lines, w.in.stderrBuf.String())
		}

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

func TestOutcomeRestartAddsNoLaunchedAndEndsADeadShip(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	w.outFleets(map[string][]string{"f": {"hook working"}})
	w.start()
	launchedAt := time.Now()
	ship := w.launchIn("f", "claude", "restart")
	w.waitFile(ship.Session, "cues-done")
	log := w.waitEvent(ship.Session, "launched", 1)
	if l := log[0]; l["source"] != "" || l["panel"] != "" || l["fleet"] != "f" {
		t.Errorf("launched = %v, want fleet f and an empty source and panel for a launch from the dock", l)
	}

	w.restart()
	w.in.waitStatusList(t, 10*time.Second, ship.Session, "underway")
	holdThat(t, "after a restart the ship has a second launched", func() bool {
		return reflect.DeepEqual(eventsOf(w.shipLog(ship.Session)), []string{"launched"})
	})

	w.in.Stop()
	w.killPane(ship.Session)
	ran := time.Since(launchedAt)
	const downtime = 2500 * time.Millisecond // must not count as the ship's run
	time.Sleep(downtime)
	restartedAt := time.Now()
	w.restart()
	log = w.waitEvent(ship.Session, "ended", 1)
	e := lastOf(log, "ended")
	if !isNull(e, "exit_code") || e["signal"] != "KILL" || !isNull(e, "report") {
		t.Errorf("ended = %v, want exit_code null, signal KILL, report null", e)
	}
	// launched is stamped to the second, so the run can read up to a second long.
	if d := num(t, e, "duration_s"); d > ran.Seconds()+1.25 {
		t.Errorf("duration_s = %v: the ship ran for %s before it was killed, %s before Admiral came back", d, ran, downtime)
	}
	if ts, err := time.Parse(time.RFC3339, e["ts"].(string)); err != nil || ts.Before(restartedAt.Add(-time.Second)) {
		t.Errorf("ended ts = %v, want the time it was written, after %s", e["ts"], restartedAt)
	}
	if got := eventsOf(log); !reflect.DeepEqual(got, []string{"launched", "ended"}) {
		t.Errorf("records = %v, want launched, ended", got)
	}

	w.restart()
	w.in.waitStatusList(t, 10*time.Second, ship.Session, "anchored")
	holdThat(t, "after another restart the ship has a second ended", func() bool {
		log := w.shipLog(ship.Session)
		return reflect.DeepEqual(eventsOf(log), []string{"launched", "ended"}) && usageCount(log) == 1
	})
}

// A ship that exited 0 while Admiral was stopped gets `ended` before the start-up kill.
func TestOutcomeRestartEndsAShipThatSailedOff(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	w.outFleets(map[string][]string{"f": {"hook working", "wait go", "exit 0"}})
	w.start()
	ship := w.launchIn("f", "claude", "zero")
	rec := w.record(ship.Session)
	w.waitEvent(ship.Session, "launched", 1)
	w.in.Stop()
	// The ship exits only now, so Admiral's stop can't race its exit and the files.
	reportPath, inboxPath := w.seedShipFiles(rec, `{"status":"done"}`)
	w.touch(ship.Session, "go")
	w.waitPaneDead(ship.Session)

	w.restart()
	waitGone(t, 15*time.Second, reportPath, inboxPath)
	if w.hasSession(ship.Session) {
		t.Errorf("the session of an exit-0 ship still exists after the restart")
	}
	log := w.shipLog(ship.Session)
	if got := eventsOf(log); !reflect.DeepEqual(got, []string{"launched", "ended"}) {
		t.Fatalf("records = %v, want launched, ended", got)
	}
	if n := usageCount(log); n != 1 {
		t.Errorf("the log has %d usage records, want 1", n)
	}
	e := log[1]
	if num(t, e, "exit_code") != 0 || !reflect.DeepEqual(e["report"], map[string]any{"status": "done"}) {
		t.Errorf("ended = %v, want exit_code 0 and the ship's report", e)
	}
}

func TestOutcomeUnwritableDirectoryDoesNotStopLaunching(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	w.outFleets(map[string][]string{"f": {"hook working", "sleep 1", "hook needs_you"}, "z": {"hook working", "wait go", "exit 0"},
		"y": {"hook working", "wait go", "exit 3"}})
	blocker := filepath.Join(w.home, "outcomes")
	if err := os.WriteFile(blocker, []byte("a file where the directory belongs"), 0o600); err != nil {
		t.Fatal(err)
	}
	w.start()

	a := w.launchIn("f", "claude", "unw1")
	b := w.launchIn("f", "claude", "unw2")
	w.in.waitStatusList(t, 10*time.Second, a.Session, "needs_you")
	w.in.waitStatusList(t, 10*time.Second, b.Session, "needs_you")
	if code, _ := send(t, http.MethodGet, w.in.LocalURL+"/api/ships", nil); code != 200 || !w.hasSession(b.Session) {
		t.Errorf("with an unwritable log GET /api/ships = %d, ship b present: %v", code, w.hasSession(b.Session))
	}
	for deadline := time.Now().Add(5 * time.Second); w.stderrLines("outcome log:") == 0; time.Sleep(50 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatalf("Admiral logged nothing about the unwritable outcome log; stderr: %s", w.in.stderrBuf.String())
		}
	}
	holdThat(t, "Admiral logged more than one line about the outcome log across four failed records", func() bool {
		return w.stderrLines("outcome log:") == 1
	})

	z := w.launchIn("z", "claude", "unw-sail")
	reportPath, inboxPath := w.seedShipFiles(w.record(z.Session), `{"status":"done"}`)
	w.touch(z.Session, "go")
	w.in.waitStatusList(t, 10*time.Second, z.Session, "sailing_off")
	for deadline := time.Now().Add(15 * time.Second); w.hasSession(z.Session); time.Sleep(100 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatalf("the exit-0 ship did not sail off while the log was unwritable")
		}
	}
	for deadline := time.Now().Add(time.Second); time.Now().Before(deadline); time.Sleep(100 * time.Millisecond) {
		if !exists(reportPath) || !exists(inboxPath) {
			t.Fatalf("a ship that sailed off without its ended in the log lost its report or inbox")
		}
	}

	y := w.launchIn("y", "claude", "unw-dismiss")
	_, yInbox := shipPaths(w.record(y.Session))
	w.touch(y.Session, "go")
	w.in.waitStatusList(t, 10*time.Second, y.Session, "anchored")
	time.Sleep(300 * time.Millisecond) // the writer has tried its queued `ended`, and failed

	if err := os.Remove(blocker); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(blocker, 0o700); err != nil {
		t.Fatal(err)
	}
	c := w.launchIn("f", "claude", "unw3")
	w.waitEvent(c.Session, "launched", 1)
	if got := eventsOf(w.shipLog(y.Session)); len(got) != 0 {
		t.Fatalf("the records of the anchored ship are %v while the log was unwritable, want none", got)
	}
	if code := w.dismiss(y.Session); code/100 != 2 {
		t.Fatalf("Dismiss = %d, want 2xx", code)
	}
	log := w.waitEvent(y.Session, "dismissed", 1)
	if got, want := eventsOf(log), []string{"ended", "dismissed"}; !reflect.DeepEqual(got, want) {
		t.Errorf("records after Dismiss = %v, want %v (its launched was lost with the log)", got, want)
	}
	waitGone(t, 5*time.Second, yInbox)
	if got := w.stderrLines("outcome log:"); got != 1 {
		t.Errorf("a record written after the repair added log lines: %d in all, want 1", got)
	}
}
