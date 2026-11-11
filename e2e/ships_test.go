package e2e

import (
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"regexp"
	"sort"
	"strings"
	"testing"
	"time"
)

// hookCmd is the one command text every hook runs: `admiral hook <event>` with
// the harness's own event name, found by bare name on the ship's PATH.
var hookCmd = regexp.MustCompile(`^admiral hook ([A-Za-z]+)$`)

var allOptions = []string{"@admiral-name", "@admiral-fleet", "@admiral-fleet-name", "@admiral-fleet-color",
	"@admiral-harness", "@admiral-dir", "@admiral-skill", "@admiral-source", "@admiral-order", "@admiral-launched"}

var codexHooks = []string{"Interrupt", "PermissionRequest", "PostToolUse", "SessionStart", "Stop", "SubagentStart", "SubagentStop", "UserPromptSubmit"}

// promptOf extracts the prompt argument from a harness's argv and checks the
// Admiral-owned part of the command line around it.
func promptOf(t *testing.T, w *world, r record) string {
	t.Helper()
	argv := r.argv
	last := argv[len(argv)-1]
	switch r.harness {
	case "claude":
		argv = r.sessionArgvBase()
		want := []string{"--fa-claude", "--settings", filepath.Join(w.home, "harness", "claude-settings.json"), "--"}
		if got := argv[:len(argv)-1]; !reflect.DeepEqual(got, want) {
			t.Fatalf("claude argv before the prompt = %q, want %q", got, want)
		}
		return last
	case "copilot":
		argv = r.sessionArgvBase()
		want := []string{"--fa-copilot", "--plugin-dir", filepath.Join(w.home, "harness", "copilot")}
		if got := argv[:len(argv)-1]; !reflect.DeepEqual(got, want) {
			t.Fatalf("copilot argv before the prompt = %q, want %q", got, want)
		}
		if !strings.HasPrefix(last, "--interactive=") {
			t.Fatalf("copilot's last argument must be --interactive=<prompt>, got %q", last)
		}
		return strings.TrimPrefix(last, "--interactive=")
	default:
		if tui := r.codexTUIArgv(); len(tui) < 3 || tui[0] != "--fa-codex" || tui[len(tui)-2] != "--" {
			t.Fatalf("codex argv must be: fleet args, -c flags, --, prompt; got %q", r.argv)
		}
		return last
	}
}

func TestLaunchCommandLine(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	w.start()
	tmuxPath, err := exec.LookPath("tmux")
	if err != nil {
		t.Fatal(err)
	}
	for _, harness := range []string{"claude", "codex", "copilot"} {
		t.Run(harness, func(t *testing.T) {
			w := w.at(t) // helpers must fail this subtest, not its parent
			ship := w.launch(w.order("work", harness, "cl-"+harness, commodore, "do the thing"))
			r := w.record(ship.Session)
			if r.harness != harness {
				t.Fatalf("ran %s, want %s", r.harness, harness)
			}
			if got := promptOf(t, w, r); got != map[string]string{"claude": "/commodore do the thing",
				"copilot": "/commodore do the thing", "codex": "$commodore do the thing"}[harness] {
				t.Errorf("prompt = %q", got)
			}
			if harness == "codex" {
				events := []string{}
				bell := false
				for i, a := range r.argv {
					if a != "-c" || i+1 >= len(r.argv) {
						continue
					}
					v := r.argv[i+1]
					if v == `tui.notification_method="bel"` {
						bell = true
					}
					if ev, rest, ok := strings.Cut(strings.TrimPrefix(v, "hooks."), "="); ok && strings.HasPrefix(v, "hooks.") {
						if !strings.Contains(rest, `command="admiral hook `+ev+`"`) {
							t.Errorf("codex -c %s = %s, want the command \"admiral hook %s\" with no path", ev, rest, ev)
						}
						events = append(events, ev)
					}
				}
				sort.Strings(events)
				if !reflect.DeepEqual(events, codexHooks) {
					t.Errorf("codex -c hooks.<Event> flags cover events %v, want exactly %v", events, codexHooks)
				}
				if !bell {
					t.Errorf(`codex argv lacks -c 'tui.notification_method="bel"': %q`, r.argv)
				}
			}
			if want, _ := filepath.EvalSymlinks(ship.Dir); r.cwd != want {
				t.Errorf("working directory = %q, want the chosen directory %q", r.cwd, want)
			}
			if r.env["FLEET_VAR"] != "fleet-value" {
				t.Errorf("fleet env FLEET_VAR = %q, want fleet-value", r.env["FLEET_VAR"])
			}
			if r.env["TILDE_VAR"] != filepath.Join(w.userHom, "tilde") {
				t.Errorf("fleet env `~/tilde` = %q, want it expanded to %q", r.env["TILDE_VAR"], filepath.Join(w.userHom, "tilde"))
			}
			if r.env["ADMIRAL_BASE"] != "base" || r.env["ADMIRAL_OVER"] != "fleet" {
				t.Errorf("env: Admiral's variable = %q (want base), fleet's override = %q (want fleet)", r.env["ADMIRAL_BASE"], r.env["ADMIRAL_OVER"])
			}
			if got, ok := r.env[compactWindow]; ok != (harness == "claude") || (ok && got != "200000") {
				t.Errorf("%s = %q (set %v), want 200000 on claude, unset on codex and copilot (ran %s)", compactWindow, got, ok, harness)
			}
			// Set before the harness runs, winning over the fleet's same-named variables.
			if r.env["ADMIRAL_SHIP"] != ship.Session {
				t.Errorf("ADMIRAL_SHIP = %q, want the session %s", r.env["ADMIRAL_SHIP"], ship.Session)
			}
			if want := filepath.Join(w.home, "reports", ship.Session+".json"); r.env["ADMIRAL_REPORT"] != want {
				t.Errorf("ADMIRAL_REPORT = %q, want %s", r.env["ADMIRAL_REPORT"], want)
			}
			if want := filepath.Join(w.home, "inbox", ship.Session) + "/"; r.env["ADMIRAL_INBOX"] != want {
				t.Errorf("ADMIRAL_INBOX = %q, want %s", r.env["ADMIRAL_INBOX"], want)
			}
			crewDir := filepath.Join(w.home, "fleets", "work", "crew")
			if r.env["ADMIRAL_CREW_DIR"] != crewDir {
				t.Errorf("ADMIRAL_CREW_DIR = %q, want the fleet's crew directory %s", r.env["ADMIRAL_CREW_DIR"], crewDir)
			}
			if st, err := os.Stat(crewDir); err != nil || !st.IsDir() || st.Mode().Perm() != 0o700 {
				t.Errorf("the fleet's crew directory %s: %v, want a directory with mode 0700", crewDir, err)
			}
			if got := strings.TrimSpace(readIf(filepath.Join(r.dir, "dirs"))); got != "inbox=yes reports=yes" {
				t.Errorf("before the harness ran: %q, want the inbox directory and reports/ to exist", got)
			}
			parts := strings.Split(r.env["PATH"], ":")
			if last := parts[len(parts)-1]; last != filepath.Dir(tmuxPath) {
				t.Errorf("PATH must end with the tmux directory %s, got last element %q", filepath.Dir(tmuxPath), last)
			}

			// Pinned members: a built-in one and a fleet one become the ship's run layer, and only Commodore launches take pins.
			member := func(dir, name, role string) string {
				text := "---\nname: " + name + "\nrole: " + role + "\npick-me-when: Pick " + name + ".\n---\n\nWork as " + name + ".\n"
				w.mkdir(dir)
				if err := os.WriteFile(filepath.Join(dir, name+".md"), []byte(text), 0o644); err != nil {
					t.Fatal(err)
				}
				return text
			}
			scout := member(filepath.Join(w.userHom, "."+harness, "skills", "commodore", "crew"), "scout", "scribe")
			mate := member(crewDir, "mate", "reviewer")
			plain := w.order("work", harness, "pin-plain-"+harness, noSkill, "no skill")
			plain["crew"] = []string{"scout"}
			code, body := w.post(plain)
			if code != http.StatusBadRequest || !strings.Contains(body, "Commodore") {
				t.Errorf("pins on a launch without Commodore = %d %s, want a 400 naming Commodore", code, body)
			}
			pinned := w.order("work", harness, "pin-"+harness, commodore, "do the pinned thing")
			pinned["crew"] = []string{"scout", "mate"}
			pr := w.record(w.launch(pinned).Session)
			run := pr.env["ADMIRAL_RUN_CREW"]
			if !filepath.IsAbs(run) || !strings.HasPrefix(run, w.home+string(os.PathSeparator)) {
				t.Fatalf("ADMIRAL_RUN_CREW = %q, want a directory under Admiral's home %s", run, w.home)
			}
			for name, want := range map[string]string{"scout.md": scout, "mate.md": mate} {
				if got, err := os.ReadFile(filepath.Join(run, name)); err != nil || string(got) != want {
					t.Errorf("run layer %s reads %q (%v), want the pinned member's file %q", name, got, err, want)
				}
			}
			if _, set := r.env["ADMIRAL_RUN_CREW"]; set {
				t.Errorf("the ship launched without pins has ADMIRAL_RUN_CREW = %q", r.env["ADMIRAL_RUN_CREW"])
			}
		})
	}
}

const compactWindow = "CLAUDE_CODE_AUTO_COMPACT_WINDOW"

// Admiral's own environment doesn't count as the fleet's.
func TestCommodoreCompactWindow(t *testing.T) {
	t.Parallel()
	t.Run("fleet env overrides", func(t *testing.T) {
		t.Parallel()
		w := newWorld(t)
		var cfg map[string]any
		if err := json.Unmarshal([]byte(w.config()), &cfg); err != nil {
			t.Fatal(err)
		}
		dock := cfg["fleets"].(map[string]any)["work"].(map[string]any)["claude"].(map[string]any)
		dock["env"].(map[string]any)[compactWindow] = "1000000"
		b, err := json.Marshal(cfg)
		if err != nil {
			t.Fatal(err)
		}
		writeConfig(t, w.home, string(b))
		w.start()
		r := w.record(w.launch(w.order("work", "claude", "ov", commodore, "x")).Session)
		if got := r.env[compactWindow]; got != "1000000" {
			t.Errorf("%s = %q, want the fleet's 1000000", compactWindow, got)
		}
	})
	t.Run("Admiral's environment does not override", func(t *testing.T) {
		t.Parallel()
		w := newWorld(t)
		w.extra = []string{compactWindow + "=1000000"}
		w.start()
		r := w.record(w.launch(w.order("work", "claude", "inh", commodore, "x")).Session)
		if got := r.env[compactWindow]; got != "200000" {
			t.Errorf("%s = %q, want 200000 (a value only Admiral inherited is not the fleet's)", compactWindow, got)
		}
	})
}

// requireRefusal: a client error with a JSON {"error": "..."} body that never
// says "flagged".
func requireRefusal(t *testing.T, code int, body string) string {
	t.Helper()
	if code < 400 || code > 499 {
		t.Fatalf("status = %d, want a 4xx: %s", code, body)
	}
	var e struct {
		Error string `json:"error"`
	}
	if json.Unmarshal([]byte(body), &e) != nil || e.Error == "" {
		t.Fatalf("refusal body must be JSON {\"error\": \"...\"}, got %s", body)
	}
	if strings.Contains(strings.ToLower(e.Error), "flagged") {
		t.Errorf("error copy says \"flagged\": %q (the words are \"marked\" and \"not found\")", e.Error)
	}
	return e.Error
}

func showOption(t *testing.T, w *world, session string, args ...string) string {
	t.Helper()
	return w.mustTmux(append([]string{"show-options", "-t", session}, args...)...)
}

func TestSessionOptionsAndHookBundle(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	writeConfig(t, w.home, w.config())
	w.addOrdersSource()
	w.start()
	w.waitSource("orders", 10*time.Second, "state ok", isOK)
	code, body := w.srcReq(http.MethodPost, "/orders/orders", map[string]any{"panel": w.panelID("orders"), "text": "go"})
	w.mustOK(code, body, "add an order")
	orderID := w.waitSource("orders", 5*time.Second, "the order", func(s sourceJSON) bool { return len(s.orderIDs()) == 1 }).orderIDs()[0]
	req := w.order("work", "claude", "optrepo", commodore, "go")
	req["source"], req["order_id"] = "orders", orderID
	ship := w.launch(req)
	r := w.record(ship.Session)

	want := map[string]string{"@admiral-name": ship.Name, "@admiral-fleet": "work", "@admiral-fleet-name": "Work",
		"@admiral-fleet-color": "#336699", "@admiral-harness": "claude", "@admiral-dir": ship.Dir,
		"@admiral-skill": "commodore", "@admiral-source": "orders", "@admiral-order": orderID}
	for k, v := range want {
		if got := showOption(t, w, ship.Session, "-v", k); got != v {
			t.Errorf("%s = %q, want %q", k, got, v)
		}
	}
	launched := showOption(t, w, ship.Session, "-v", "@admiral-launched")
	ts, err := time.Parse(time.RFC3339, launched)
	if err != nil || !strings.HasSuffix(launched, "Z") || time.Since(ts) > time.Minute || time.Since(ts) < -time.Minute {
		t.Errorf("@admiral-launched = %q, want a recent RFC 3339 UTC time", launched)
	}
	if ship.Launched != launched {
		t.Errorf("ship.launched = %q, want the same as @admiral-launched %q", ship.Launched, launched)
	}
	if got := showOption(t, w, ship.Session, "-v", "history-limit"); got != "10000" {
		t.Errorf("history-limit = %q, want 10000", got)
	}
	if got := showOption(t, w, ship.Session, "-p", "-v", "remain-on-exit"); got != "on" {
		t.Errorf("pane option remain-on-exit = %q, want on", got)
	}
	for _, k := range allOptions {
		if !strings.Contains(r.options, k) {
			t.Errorf("when the harness started, session option %s was not set yet; options seen: %s", k, r.options)
		}
	}
	if r.name != ship.Name {
		t.Errorf("harness read @admiral-name %q at start, want %q", r.name, ship.Name)
	}

	t.Run("just launch with no source sets all ten, empty where unused", func(t *testing.T) {
		w := w.at(t) // helpers must fail this subtest, not its parent
		req := w.order("work", "codex", "optrepo2", noSkill, "plain")
		delete(req, "source")
		delete(req, "order_id")
		s := w.launch(req)
		list := showOption(t, w, s.Session)
		for _, k := range allOptions {
			if !strings.Contains(list, k) {
				t.Errorf("option %s missing from show-options:\n%s", k, list)
			}
		}
		for _, k := range []string{"@admiral-skill", "@admiral-source", "@admiral-order"} {
			if got := showOption(t, w, s.Session, "-v", k); got != "" {
				t.Errorf("%s = %q, want empty", k, got)
			}
		}
		if s.Skill != nil {
			t.Errorf("ship.skill = %q for Just launch, want null", *s.Skill)
		}
	})

	t.Run("hook bundle", func(t *testing.T) {
		w := w.at(t) // helpers must fail this subtest, not its parent
		claude := readJSONFile(t, filepath.Join(w.home, "harness", "claude-settings.json"))
		checkClaudeBundle(t, claude)
		var copilot []any
		root := filepath.Join(w.home, "harness", "copilot")
		_ = filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
			if err == nil && !d.IsDir() && strings.HasSuffix(p, ".json") {
				copilot = append(copilot, readJSONFile(t, p))
			}
			return nil
		})
		if len(copilot) == 0 {
			t.Fatalf("no JSON files under %s: Copilot needs a plugin manifest and hooks.json", root)
		}
		checkCopilotBundle(t, copilot)
	})
}

func readJSONFile(t *testing.T, p string) any {
	t.Helper()
	data, err := os.ReadFile(p)
	if err != nil {
		t.Fatalf("hook bundle file missing: %v", err)
	}
	var v any
	if err := json.Unmarshal(data, &v); err != nil {
		t.Fatalf("%s is not JSON: %v", p, err)
	}
	return v
}

func leaves(v any, f func(string)) {
	switch x := v.(type) {
	case string:
		f(x)
	case []any:
		for _, e := range x {
			leaves(e, f)
		}
	case map[string]any:
		for _, e := range x {
			leaves(e, f)
		}
	}
}

func commandsUnder(v any, event string) []string {
	found := map[string]bool{}
	var walk func(any)
	walk = func(v any) {
		switch x := v.(type) {
		case []any:
			for _, e := range x {
				walk(e)
			}
		case map[string]any:
			for k, e := range x {
				if k == event {
					leaves(e, func(s string) {
						if hookCmd.MatchString(s) {
							found[s] = true
						}
					})
				}
				walk(e)
			}
		}
	}
	walk(v)
	out := []string{}
	for s := range found {
		out = append(out, s)
	}
	sort.Strings(out)
	return out
}

func requireExactHookCommands(t *testing.T, doc any) {
	t.Helper()
	count := 0
	leaves(doc, func(s string) {
		if strings.Contains(s, "tmux") || strings.Contains(s, "@admiral-state") || strings.Contains(s, "hook ") {
			count++
			if !hookCmd.MatchString(s) {
				t.Errorf("hook command %q is not exactly `admiral hook <event>`", s)
			}
		}
	})
	if count == 0 {
		t.Errorf("no hook commands found in the bundle")
	}
}

func checkClaudeBundle(t *testing.T, doc any) {
	t.Helper()
	requireExactHookCommands(t, doc)
	hooks, _ := doc.(map[string]any)["hooks"].(map[string]any)
	for _, ev := range []string{"PermissionRequest", "PreToolUse", "Notification", "Stop", "UserPromptSubmit", "PostToolUse"} {
		if got, want := commandsUnder(hooks, ev), []string{"admiral hook " + ev}; !reflect.DeepEqual(got, want) {
			t.Errorf("Claude Code %s hooks run %v, want %v", ev, got, want)
		}
	}
	// The AskUserQuestion split stays: a group matching AskUserQuestion, and no
	// group that matches every tool.
	entries, _ := hooks["PreToolUse"].([]any)
	askSeen := false
	for _, e := range entries {
		m, _ := e.(map[string]any)
		matcher, _ := m["matcher"].(string)
		if matcher == "" || matcher == "*" || matcher == ".*" {
			t.Errorf("PreToolUse group has matcher %q: it would match AskUserQuestion too", matcher)
		}
		if strings.Contains(matcher, "AskUserQuestion") && !strings.Contains(matcher, "?!") {
			askSeen = true
		}
	}
	if !askSeen {
		t.Errorf("no PreToolUse group with an AskUserQuestion matcher: %v", entries)
	}
	raw, _ := json.Marshal(hooks["Notification"])
	for _, m := range []string{"permission_prompt", "elicitation_dialog"} {
		if !strings.Contains(string(raw), m) {
			t.Errorf("Notification hooks lack the %s matcher: %s", m, raw)
		}
	}
}

func checkCopilotBundle(t *testing.T, docs []any) {
	t.Helper()
	requireExactHookCommands(t, docs)
	for _, ev := range []string{"notification", "preToolUse", "agentStop", "userPromptSubmitted", "postToolUse"} {
		if got, want := commandsUnder(docs, ev), []string{"admiral hook " + ev}; !reflect.DeepEqual(got, want) {
			t.Errorf("Copilot %s hooks run %v, want %v", ev, got, want)
		}
	}
	raw, _ := json.Marshal(docs)
	for _, m := range []string{"permission_prompt", "elicitation_dialog", "ask_user"} {
		if !strings.Contains(string(raw), m) {
			t.Errorf("Copilot bundle lacks %s: %s", m, raw)
		}
	}
}

func TestRestartRebuildsShips(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	w.start()
	a := w.launch(w.order("work", "claude", "ra", commodore, "x"))
	b := w.launch(w.order("home", "codex", "rb", customSkill("foo"), "y"))
	before := w.in.ships(t)
	if len(before) != 2 {
		t.Fatalf("ships before restart = %+v", before)
	}

	bundle := filepath.Join(w.home, "harness", "claude-settings.json")
	if err := os.WriteFile(bundle, []byte("garbage"), 0o600); err != nil {
		t.Fatal(err)
	}

	w.in.Stop()
	if got := len(w.sessions()); got != 2 {
		t.Fatalf("after stopping Admiral tmux has %d sessions, want 2 still running", got)
	}
	w.restart()
	after := w.in.waitShipsList(t, 5*time.Second, "both ships after restart", func(l []shipJSON) bool { return len(l) == 2 })
	if !reflect.DeepEqual(byName(before), byName(after)) {
		t.Errorf("ships changed over a restart:\nbefore %+v\nafter  %+v", before, after)
	}
	if got := byName(after)[a.Name]; got.Harness != "claude" || got.Fleet != "work" || got.Dir != a.Dir || got.Skill == nil || *got.Skill != "commodore" {
		t.Errorf("rebuilt ship %+v lost its launch settings", got)
	}
	readJSONFile(t, bundle)

	writeConfig(t, w.home, strings.NewReplacer(`"#336699"`, `"#00ff00"`).Replace(w.config()))
	waitAPI(t, w.in.instance, "work's new color", func(c cfgBody) bool { return c.fleet("work").Color == "#00ff00" })
	if got := byName(w.in.ships(t))[a.Name].FleetColor; got != "#336699" {
		t.Errorf("after the fleet's color changed, ship %s has fleet_color %q, want its launch color #336699", a.Name, got)
	}

	// The "home" fleet is removed from config: its ship stays, in its recorded color.
	cfg := map[string]any{}
	_ = json.Unmarshal([]byte(w.config()), &cfg)
	delete(cfg["fleets"].(map[string]any), "home")
	raw, _ := json.Marshal(cfg)
	writeConfig(t, w.home, string(raw))
	waitAPI(t, w.in.instance, "home removed", func(c cfgBody) bool { return c.fleet("home").Key == "" })
	got := byName(w.in.ships(t))[b.Name]
	if got.Session == "" || got.FleetColor != "#cc5533" || got.FleetName != "Home" {
		t.Errorf("ship %s of the removed fleet = %+v, want it kept with fleet_color #cc5533 and fleet_name Home", b.Name, got)
	}
	w.restart()
	got = byName(w.in.waitShipsList(t, 5*time.Second, "both ships", func(l []shipJSON) bool { return len(l) == 2 }))[b.Name]
	if got.FleetColor != "#cc5533" {
		t.Errorf("after a restart the removed fleet's ship has fleet_color %q, want #cc5533", got.FleetColor)
	}
}

func TestTmuxChangesReachAdmiral(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	w.start()
	a := w.launch(w.order("work", "claude", "wa", commodore, "x"))
	ws := dialWS(t, w.in.instance)
	waitShipsMsg(t, ws, 5*time.Second, "the first ship", func(l []shipJSON) bool { return len(l) == 1 })

	w.mustTmux("new-session", "-d", "-s", "stranger", "sleep", "3600")
	holdThat(t, "a tmux session without @admiral-name became a ship", func() bool { return len(w.in.ships(t)) == 1 })
	for _, s := range w.in.ships(t) {
		if s.Session == "_admiral" || s.Session == "stranger" {
			t.Errorf("%q reported as a ship", s.Session)
		}
	}

	// An option set on an existing session turns it into a ship.
	w.mustTmux("set-option", "-t", "stranger", "@admiral-name", "Outsider")
	w.mustTmux("set-option", "-t", "stranger", "@admiral-fleet", "work")
	w.mustTmux("set-option", "-t", "stranger", "@admiral-harness", "claude")
	waitShipsMsg(t, ws, 3*time.Second, "the session that got @admiral-name", func(l []shipJSON) bool {
		_, ok := byName(l)["Outsider"]
		return ok
	})

	w.mustTmux("kill-session", "-t", a.Session)
	waitShipsMsg(t, ws, 3*time.Second, "the killed ship gone", func(l []shipJSON) bool {
		_, ok := byName(l)[a.Name]
		return !ok
	})
	w.in.waitShipsList(t, 2*time.Second, "the killed ship gone", func(l []shipJSON) bool {
		_, ok := byName(l)[a.Name]
		return !ok
	})
}

func TestStateFile(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	w.start()
	a := w.launch(w.order("work", "claude", "sa", commodore, "x"))
	b := w.launch(w.order("work", "codex", "sb", commodore, "x"))

	other := dialWS(t, w.in.instance)
	waitShipsMsg(t, other, 5*time.Second, "both ships", func(l []shipJSON) bool { return len(l) == 2 })

	url := w.in.LocalURL + "/api/ships/" + a.Session + "/position"
	if code, body := send(t, http.MethodPut, url, map[string]float64{"x": 0.25, "y": 0.75}); code/100 != 2 {
		t.Fatalf("PUT position = %d: %s", code, body)
	}
	got := waitShipsMsg(t, other, 3*time.Second, "the new position", func(l []shipJSON) bool {
		p := byName(l)[a.Name].Position
		return p != nil && p.X == 0.25 && p.Y == 0.75
	})
	if p := byName(got)[b.Name].Position; p != nil {
		t.Errorf("an unplaced ship has position %+v, want null", p)
	}
	send(t, http.MethodPut, w.in.LocalURL+"/api/ships/"+b.Session+"/position", map[string]float64{"x": 0.5, "y": 0.5})

	if code, _ := send(t, http.MethodPut, url, map[string]float64{"x": 1.5, "y": 0.1}); code != http.StatusBadRequest {
		t.Errorf("PUT position outside 0..1 = %d, want 400", code)
	}
	if code, _ := send(t, http.MethodPut, w.in.LocalURL+"/api/ships/nosuch/position", map[string]float64{"x": 0.1, "y": 0.1}); code != http.StatusNotFound {
		t.Errorf("PUT position for an unknown ship = %d, want 404", code)
	}

	statePath := filepath.Join(w.home, "state.json")
	state := waitStateFile(t, statePath, func(s string) bool {
		return strings.Contains(s, a.Session) && strings.Contains(s, "0.75") && strings.Contains(s, "work") && strings.Contains(s, a.Dir)
	})
	var parsed map[string]any
	if err := json.Unmarshal([]byte(state), &parsed); err != nil || parsed["version"] != float64(1) {
		t.Errorf("state.json must be JSON with \"version\": 1, got %s", state)
	}
	dirs, _ := parsed["last_dirs"].(map[string]any)
	if dirs["work/claude"] != a.Dir || dirs["work/codex"] != b.Dir {
		t.Errorf("state.json last_dirs = %v, want work/claude → %s and work/codex → %s", dirs, a.Dir, b.Dir)
	}

	w.in.Stop()
	w.mustTmux("kill-session", "-t", b.Session)
	w.restart()
	list := w.in.waitShipsList(t, 5*time.Second, "the surviving ship", func(l []shipJSON) bool { return len(l) == 1 })
	if p := list[0].Position; list[0].Name != a.Name || p == nil || p.X != 0.25 || p.Y != 0.75 {
		t.Errorf("after restart ship = %+v, want %s at (0.25, 0.75)", list[0], a.Name)
	}
	w.mustTmux("new-session", "-d", "-s", b.Session, "sleep", "3600")
	for _, kv := range [][2]string{{"@admiral-name", b.Name}, {"@admiral-fleet", "work"}, {"@admiral-harness", "codex"}} {
		w.mustTmux("set-option", "-t", b.Session, kv[0], kv[1])
	}
	list = w.in.waitShipsList(t, 3*time.Second, "the recreated session as a ship", func(l []shipJSON) bool { return len(l) == 2 })
	if p := byName(list)[b.Name].Position; p != nil {
		t.Errorf("a session that was gone at start got its old position %+v back, want it dropped", p)
	}
}

func waitStateFile(t *testing.T, path string, pred func(string) bool) string {
	t.Helper()
	deadline := time.Now().Add(4 * time.Second)
	for {
		data, _ := os.ReadFile(path)
		if pred(string(data)) {
			return string(data)
		}
		if time.Now().After(deadline) {
			t.Fatalf("within 4s %s never held the expected content; it holds: %s", path, data)
		}
		time.Sleep(100 * time.Millisecond)
	}
}

func TestBadStateFileIsMovedAside(t *testing.T) {
	t.Parallel()
	for name, content := range map[string]string{"corrupt": "{not json", "unknown version": `{"version":2,"positions":{}}`} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			w := newWorld(t)
			if err := os.MkdirAll(w.home, 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(w.home, "state.json"), []byte(content), 0o600); err != nil {
				t.Fatal(err)
			}
			w.start()
			if data, err := os.ReadFile(filepath.Join(w.home, "state.json.bad")); err != nil || string(data) != content {
				t.Errorf("state.json.bad = %q (%v), want the old file's content %q", data, err, content)
			}
			s := w.launch(w.order("work", "claude", "bad", commodore, "x"))
			if len(w.in.ships(t)) != 1 || s.Position != nil {
				t.Errorf("Admiral did not start with default state")
			}
		})
	}
}

// Isolation, and a ship's hooks reach Admiral's socket whatever TMUX says.
func TestIsolationAndTmuxEnvironment(t *testing.T) {
	t.Parallel()
	one, two := newWorld(t), newWorld(t)
	two.tmuxTmp = one.tmuxTmp // both share one (test-owned) "default" tmux dir
	one.extra = []string{"TMUX=/nonexistent/bogus.sock,1,0", "TMUX_PANE=%99"}
	one.writeCue("hook needs_you")
	one.start()
	two.start()
	a := one.launch(one.order("work", "claude", "iso1", commodore, "x"))
	two.launch(two.order("work", "claude", "iso2", commodore, "x"))
	if n := len(one.in.ships(t)); n != 1 {
		t.Errorf("instance one sees %d ships, want only its own", n)
	}
	if n := len(two.in.ships(t)); n != 1 {
		t.Errorf("instance two sees %d ships, want only its own", n)
	}
	if s := one.sessions(); len(s) != 1 || s[0] != a.Session {
		t.Errorf("instance one's tmux has %v", s)
	}

	r := one.record(a.Session)
	if tm := r.env["TMUX"]; !strings.HasPrefix(tm, one.sock()+",") {
		t.Errorf("the ship's TMUX = %q, want tmux's own value starting with %s,", tm, one.sock())
	}
	if p := r.env["TMUX_PANE"]; !strings.HasPrefix(p, "%") || p == "%99" {
		t.Errorf("the ship's TMUX_PANE = %q, want its real pane id, not the bogus %%99", p)
	}
	one.waitFile(a.Session, "cues-done")
	if got := showOption(t, one, a.Session, "-p", "-v", "@admiral-state"); got != "needs_you" {
		t.Errorf("after the hook command ran, pane option @admiral-state = %q, want needs_you", got)
	}

	// (tmux gives every pane its own TMUX and TMUX_PANE whatever the client's
	// environment holds, so this proves the ship's values are right, not that
	// Admiral strips the bogus ones.)

	out, err := exec.Command("tmux", "-S", one.defaultSocket(), "list-sessions").CombinedOutput()
	if err == nil {
		t.Errorf("a default tmux server is running with sessions:\n%s", out)
	}
}

func TestMalformedSessionDoesNotHideShips(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	w.start()
	a := w.launch(w.order("work", "claude", "mf-a", commodore, "x"))
	b := w.launch(w.order("work", "codex", "mf-b", commodore, "x"))
	for _, s := range []shipJSON{a, b} {
		if code, body := send(t, http.MethodPut, w.in.LocalURL+"/api/ships/"+s.Session+"/position", map[string]float64{"x": 0.3, "y": 0.6}); code/100 != 2 {
			t.Fatalf("PUT position = %d: %s", code, body)
		}
	}
	w.mustTmux("new-session", "-d", "-s", "hand", "sleep", "3600")
	w.mustTmux("set-option", "-t", "hand:", "@admiral-name", "Hand")
	w.mustTmux("set-option", "-t", "hand:", "@admiral-order", "x\n%end 1 2 1\n%begin 1 2 1\nrow")
	w.mustTmux("set-option", "-t", a.Session+":", "@admiral-order", "y\n%end 1 2 1\nz")
	holdThat(t, "a known ship with a malformed option was dropped", func() bool {
		_, ok := byName(w.in.ships(t))[a.Name]
		return ok
	})

	list := byName(w.in.ships(t))
	for _, s := range []shipJSON{a, b} {
		got := list[s.Name]
		if got.Session == "" || got.Position == nil || got.Position.X != 0.3 {
			t.Errorf("ship %s after the malformed rows = %+v, want it listed at (0.3, 0.6)", s.Name, got)
		}
	}
	waitStateFile(t, filepath.Join(w.home, "state.json"), func(s string) bool {
		return strings.Contains(s, a.Session) && strings.Contains(s, b.Session)
	})
	w.launch(w.order("work", "claude", "mf-c", commodore, "x"))
}

// A new ship that draws a freed name must not inherit a gone ship's position.
func TestPositionsDroppedAfterTmuxRestarts(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	w.start()
	a := w.launch(w.order("work", "claude", "rs", commodore, "x"))
	if code, body := send(t, http.MethodPut, w.in.LocalURL+"/api/ships/"+a.Session+"/position", map[string]float64{"x": 0.4, "y": 0.4}); code/100 != 2 {
		t.Fatalf("PUT position = %d: %s", code, body)
	}
	w.mustTmux("kill-server")
	w.in.waitShipsList(t, 5*time.Second, "no ships once tmux is gone", func(l []shipJSON) bool { return len(l) == 0 })
	// Admiral reconnects, which starts a new server with its hidden session.
	for deadline := time.Now().Add(10 * time.Second); ; time.Sleep(100 * time.Millisecond) {
		if _, err := w.tmux("has-session", "-t", "=_admiral"); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("within 10s Admiral never started a new tmux server")
		}
	}
	w.mustTmux("new-session", "-d", "-s", a.Session, "sleep", "3600")
	w.mustTmux("set-option", "-t", a.Session+":", "@admiral-name", a.Name)
	list := w.in.waitShipsList(t, 3*time.Second, "the recreated session", func(l []shipJSON) bool { return len(l) == 1 })
	if list[0].Position != nil {
		t.Errorf("a ship that took a freed name has position %+v, want null", list[0].Position)
	}
}

func TestUnreadableShipKeepsItsPositionAcrossRestart(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	w.start()
	a := w.launch(w.order("work", "claude", "ur", commodore, "x"))
	if code, body := send(t, http.MethodPut, w.in.LocalURL+"/api/ships/"+a.Session+"/position", map[string]float64{"x": 0.3, "y": 0.6}); code/100 != 2 {
		t.Fatalf("PUT position = %d: %s", code, body)
	}
	statePath := filepath.Join(w.home, "state.json")
	waitStateFile(t, statePath, func(s string) bool { return strings.Contains(s, a.Session) })

	w.in.Stop()
	w.mustTmux("set-option", "-t", a.Session+":", "@admiral-order", "y\nz")
	w.restart()
	holdThat(t, "state.json lost the position of a ship that is alive but unreadable", func() bool {
		data, _ := os.ReadFile(statePath)
		return strings.Contains(string(data), a.Session)
	})

	w.mustTmux("set-option", "-t", a.Session+":", "@admiral-order", "o-ur")
	list := w.in.waitShipsList(t, 3*time.Second, "the repaired ship", func(l []shipJSON) bool { return len(l) == 1 })
	if p := list[0].Position; p == nil || p.X != 0.3 || p.Y != 0.6 {
		t.Errorf("repaired ship = %+v, want it back at (0.3, 0.6)", list[0])
	}
}

// A Codex ship's TUI runs on the app-server the pane wrapper started, on a socket in Admiral's
// private directory.
func TestCodexShipRunsOnItsOwnAppServer(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	w.server = "listen"
	w.start()
	ship := w.launch(w.order("work", "codex", "cl-codex", commodore, "do the thing"))
	r := w.record(ship.Session)
	if len(r.argv) < 3 || r.argv[0] != "--remote" || r.argv[2] != "--fa-codex" {
		t.Fatalf("codex argv = %q, want --remote unix://<socket> right after the exe, then the dock's args", r.argv)
	}
	sock := strings.TrimPrefix(r.argv[1], "unix://")
	if want := fmt.Sprintf("/tmp/admiral-%d/", os.Getuid()); !strings.HasPrefix(sock, want) {
		t.Fatalf("socket %q is not under %s", sock, want)
	}
	c, err := net.Dial("unix", sock)
	if err != nil {
		t.Fatalf("the ship's app-server does not accept connections: %v", err)
	}
	_ = c.Close()
}
