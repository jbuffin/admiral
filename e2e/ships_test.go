package e2e

import (
	"encoding/json"
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

// Ship scenarios (u4): the real binary, a temporary ADMIRAL_HOME with its own
// tmux socket, and the fake harnesses in e2e/fakeharness. Launches go through
// POST /api/ships; ships are observed over GET /api/ships, the WebSocket
// `ships` message, tmux itself and what the fake harness recorded.

var hookCmd = regexp.MustCompile(`^tmux set -p @admiral-state (needs_you|working)$`)

var allOptions = []string{"@admiral-name", "@admiral-fleet", "@admiral-fleet-name", "@admiral-fleet-color",
	"@admiral-harness", "@admiral-dir", "@admiral-skill", "@admiral-source", "@admiral-order", "@admiral-launched"}

// codexHooks is the spec's Codex event → state mapping.
var codexHooks = map[string]string{"PermissionRequest": "needs_you", "Stop": "needs_you",
	"UserPromptSubmit": "working", "PostToolUse": "working", "Interrupt": "working"}

// promptOf extracts the prompt argument from a harness's argv and checks the
// Admiral-owned part of the command line around it.
func promptOf(t *testing.T, w *world, r record) string {
	t.Helper()
	last := r.argv[len(r.argv)-1]
	switch r.harness {
	case "claude":
		want := []string{"--fa-claude", "--settings", filepath.Join(w.home, "harness", "claude-settings.json"), "--"}
		if got := r.argv[:len(r.argv)-1]; !reflect.DeepEqual(got, want) {
			t.Fatalf("claude argv before the prompt = %q, want %q", got, want)
		}
		return last
	case "copilot":
		want := []string{"--fa-copilot", "--plugin-dir", filepath.Join(w.home, "harness", "copilot")}
		if got := r.argv[:len(r.argv)-1]; !reflect.DeepEqual(got, want) {
			t.Fatalf("copilot argv before the prompt = %q, want %q", got, want)
		}
		if !strings.HasPrefix(last, "--interactive=") {
			t.Fatalf("copilot's last argument must be --interactive=<prompt>, got %q", last)
		}
		return strings.TrimPrefix(last, "--interactive=")
	default:
		if len(r.argv) < 3 || r.argv[0] != "--fa-codex" || r.argv[len(r.argv)-2] != "--" {
			t.Fatalf("codex argv must be: fleet args, -c flags, --, prompt; got %q", r.argv)
		}
		return last
	}
}

// S1: the command line, working directory and environment per harness.
func TestLaunchCommandLine(t *testing.T) {
	w := newWorld(t)
	w.start()
	tmuxPath, err := exec.LookPath("tmux")
	if err != nil {
		t.Fatal(err)
	}
	for _, harness := range []string{"claude", "codex", "copilot"} {
		t.Run(harness, func(t *testing.T) {
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
				events := map[string]string{}
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
						for _, state := range []string{"needs_you", "working"} {
							if strings.Contains(rest, "tmux set -p @admiral-state "+state) {
								events[ev] += state
							}
						}
					}
				}
				if !reflect.DeepEqual(events, codexHooks) {
					t.Errorf("codex -c hooks.<Event> flags carry states %v, want exactly %v", events, codexHooks)
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
			parts := strings.Split(r.env["PATH"], ":")
			if last := parts[len(parts)-1]; last != filepath.Dir(tmuxPath) {
				t.Errorf("PATH must end with the tmux directory %s, got last element %q", filepath.Dir(tmuxPath), last)
			}
		})
	}
}

// S2: prompt forms.
func TestPromptForms(t *testing.T) {
	w := newWorld(t)
	w.start()
	type tc struct {
		name, text string
		skill      map[string]any
		want       map[string]string // harness → prompt
	}
	same := func(s string) map[string]string { return map[string]string{"claude": s, "codex": s, "copilot": s} }
	cases := []tc{
		{"commodore trims, keeps newlines", "\n  line one\nline two  \n\n", commodore,
			map[string]string{"claude": "/commodore line one\nline two", "copilot": "/commodore line one\nline two", "codex": "$commodore line one\nline two"}},
		{"custom slash name", "hello", customSkill("/foo"),
			map[string]string{"claude": "/foo hello", "copilot": "/foo hello", "codex": "$foo hello"}},
		{"custom bare name", "hello", customSkill("foo"),
			map[string]string{"claude": "/foo hello", "copilot": "/foo hello", "codex": "$foo hello"}},
		{"custom dollar name", "hello", customSkill("$foo"),
			map[string]string{"claude": "/foo hello", "copilot": "/foo hello", "codex": "$foo hello"}},
		{"only one leading slash is dropped", "hi", customSkill("//foo"),
			map[string]string{"claude": "//foo hi", "copilot": "//foo hi", "codex": "$/foo hi"}},
		{"just launch is exact", "  exactly\n as written \n\n", noSkill, same("  exactly\n as written \n\n")},
		{"just launch text starting with a dash", "--help -x", noSkill, same("--help -x")},
		{"commodore text starting with a dash", "-rf --foo", commodore,
			map[string]string{"claude": "/commodore -rf --foo", "copilot": "/commodore -rf --foo", "codex": "$commodore -rf --foo"}},
	}
	n := 0
	for _, c := range cases {
		for _, harness := range []string{"claude", "codex", "copilot"} {
			n++
			c, harness, repo := c, harness, "p"+string(rune('a'+n%26))+string(rune('a'+n/26))
			t.Run(c.name+"/"+harness, func(t *testing.T) {
				ship := w.launch(w.order("work", harness, repo, c.skill, c.text))
				if got := promptOf(t, w, w.record(ship.Session)); got != c.want[harness] {
					t.Errorf("prompt = %q, want %q", got, c.want[harness])
				}
			})
		}
	}
	t.Run("name with whitespace is refused", func(t *testing.T) {
		before := w.sessions()
		code, body := w.post(w.order("work", "claude", "ws", customSkill("my skill"), "x"))
		requireRefusal(t, code, body)
		if after := w.sessions(); len(after) != len(before) {
			t.Errorf("a refused launch created a session: before %v, after %v", before, after)
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
	return w.mustTmux(append([]string{"show-options"}, append(args, "-t", session)...)...)
}

// S3: session options, remain-on-exit, history-limit, options-before-hooks and
// the hook bundle.
func TestSessionOptionsAndHookBundle(t *testing.T) {
	w := newWorld(t)
	w.start()
	req := w.order("work", "claude", "optrepo", commodore, "go")
	ship := w.launch(req)
	r := w.record(ship.Session)

	want := map[string]string{"@admiral-name": ship.Name, "@admiral-fleet": "work", "@admiral-fleet-name": "Work",
		"@admiral-fleet-color": "#336699", "@admiral-harness": "claude", "@admiral-dir": ship.Dir,
		"@admiral-skill": "commodore", "@admiral-source": "todo", "@admiral-order": "o-optrepo"}
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

// leaves calls f for every string in v.
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

// statesUnder returns the hook states found under every map key named event.
func statesUnder(v any, event string) []string {
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
						if m := hookCmd.FindStringSubmatch(s); m != nil {
							found[m[1]] = true
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
		if strings.Contains(s, "tmux") || strings.Contains(s, "@admiral-state") {
			count++
			if !hookCmd.MatchString(s) {
				t.Errorf("hook command %q is not exactly `tmux set -p @admiral-state <needs_you|working>`", s)
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
	only := func(ev string, want ...string) {
		if got := statesUnder(hooks, ev); !reflect.DeepEqual(got, want) {
			t.Errorf("Claude Code %s hooks set states %v, want %v", ev, got, want)
		}
	}
	only("PermissionRequest", "needs_you")
	only("Stop", "needs_you")
	only("UserPromptSubmit", "working")
	only("PostToolUse", "working")
	only("Notification", "needs_you")
	only("PreToolUse", "needs_you", "working")
	// Matching hooks run in parallel, so a working PreToolUse hook must not
	// match AskUserQuestion's needs_you hook: it needs a matcher, never "" or "*".
	entries, _ := hooks["PreToolUse"].([]any)
	askSeen := false
	for _, e := range entries {
		m, _ := e.(map[string]any)
		matcher, _ := m["matcher"].(string)
		states := statesUnder(map[string]any{"x": m}, "x")
		switch {
		case len(states) == 1 && states[0] == "needs_you":
			if strings.Contains(matcher, "AskUserQuestion") {
				askSeen = true
			}
		case len(states) == 1 && states[0] == "working":
			if matcher == "" || matcher == "*" || matcher == ".*" {
				t.Errorf("PreToolUse working hook has matcher %q: it would race AskUserQuestion's needs_you", matcher)
			}
		}
	}
	if !askSeen {
		t.Errorf("no PreToolUse needs_you hook with an AskUserQuestion matcher: %v", entries)
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
	check := func(ev string, want ...string) {
		got := statesUnder(docs, ev)
		if !reflect.DeepEqual(got, want) {
			t.Errorf("Copilot %s hooks set states %v, want %v", ev, got, want)
		}
	}
	check("notification", "needs_you")
	check("preToolUse", "needs_you")
	check("agentStop", "needs_you")
	check("userPromptSubmitted", "working")
	check("postToolUse", "working")
	raw, _ := json.Marshal(docs)
	for _, m := range []string{"permission_prompt", "elicitation_dialog", "ask_user"} {
		if !strings.Contains(string(raw), m) {
			t.Errorf("Copilot bundle lacks %s: %s", m, raw)
		}
	}
}

// S4: names are distinct and the session name is their slug.
func TestShipNames(t *testing.T) {
	w := newWorld(t)
	w.start()
	slug := regexp.MustCompile(`^[a-z0-9]+(-[a-z0-9]+)*$`)
	seen := map[string]bool{}
	sessions := map[string]bool{}
	for i := 0; i < 12; i++ {
		s := w.launch(w.order("work", "claude", "nm"+string(rune('a'+i)), commodore, "x"))
		if seen[s.Name] {
			t.Fatalf("name %q handed out twice", s.Name)
		}
		seen[s.Name] = true
		sessions[s.Session] = true
		want := strings.Trim(regexp.MustCompile(`[^a-z0-9]+`).ReplaceAllString(strings.ToLower(s.Name), "-"), "-")
		if s.Session != want || !slug.MatchString(s.Session) || s.Session == "_admiral" {
			t.Errorf("session %q for ship %q, want the slug %q", s.Session, s.Name, want)
		}
	}
	live := w.sessions()
	sort.Strings(live)
	var got []string
	for s := range sessions {
		got = append(got, s)
	}
	sort.Strings(got)
	if !reflect.DeepEqual(live, got) {
		t.Errorf("tmux sessions = %v, want exactly the ships' sessions %v", live, got)
	}
}

// When every curated name is in use, a Roman numeral is added. The list holds
// about 150 names, so 220 ships must reach it.
func TestShipNamesGetNumerals(t *testing.T) {
	if testing.Short() {
		t.Skip("launches 220 ships")
	}
	w := newWorld(t)
	w.start()
	names := map[string]bool{}
	for i := 0; i < 220; i++ {
		req := w.order("work", "claude", "bulk", noSkill, "x")
		req["order_id"] = "o" + time.Now().Format("150405.000000000")
		names[w.launch(req).Name] = true
	}
	if len(names) != 220 {
		t.Fatalf("220 ships got %d distinct names", len(names))
	}
	numeral := regexp.MustCompile(`^(.+) (II|III|IV|V|VI|VII|VIII|IX|X)$`)
	found := 0
	for n := range names {
		if m := numeral.FindStringSubmatch(n); m != nil {
			found++
			if !names[m[1]] {
				t.Errorf("%q has a numeral but %q was never used", n, m[1])
			}
		}
	}
	if found == 0 {
		t.Errorf("no ship name got a numeral after 220 launches")
	}
}

// S5: refusals create no session and say why.
func TestLaunchRefusals(t *testing.T) {
	w := newWorld(t)
	w.start()
	if err := os.WriteFile(w.root+"/vanish-claude", nil, 0o755); err != nil {
		t.Fatal(err)
	}
	// Let the config reload see the executable, then remove it: the launch
	// re-resolves it.
	writeConfig(t, w.home, w.config())
	w.in.waitAPIFound(t, "vanish")
	if err := os.Remove(w.root + "/vanish-claude"); err != nil {
		t.Fatal(err)
	}

	// A control launch proves the endpoint works, so a refusal isn't a missing route.
	w.launch(w.order("work", "claude", "ok", commodore, "x"))
	file := filepath.Join(w.work, "afile")
	if err := os.WriteFile(file, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	missing := filepath.Join(w.work, "gone")
	cases := []struct {
		name  string
		mod   func(map[string]any)
		wants string // text the error must contain
	}{
		{"missing directory", func(r map[string]any) { r["dir"] = missing }, missing},
		{"file instead of directory", func(r map[string]any) { r["dir"] = file }, file},
		{"unknown fleet", func(r map[string]any) { r["fleet"] = "nope" }, "nope"},
		{"harness the fleet doesn't allow", func(r map[string]any) { r["fleet"] = "claudeonly"; r["harness"] = "codex" }, "codex"},
		{"fleet with no search roots", func(r map[string]any) { r["fleet"] = "nosearch" }, ""},
		{"executable not found", func(r map[string]any) { r["fleet"] = "broken" }, ""},
		{"executable gone since the last config load", func(r map[string]any) { r["fleet"] = "vanish" }, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			req := w.order("work", "claude", "refuse", commodore, "x")
			c.mod(req)
			before := w.sessions()
			code, body := w.post(req)
			msg := requireRefusal(t, code, body)
			if c.wants != "" && !strings.Contains(msg, c.wants) {
				t.Errorf("error %q should name %q", msg, c.wants)
			}
			if after := w.sessions(); !reflect.DeepEqual(before, after) {
				t.Errorf("a refused launch changed the sessions: before %v, after %v", before, after)
			}
			if n := len(w.in.ships(t)); n != 1 {
				t.Errorf("ships after a refusal = %d, want only the control ship", n)
			}
		})
	}
}

// waitAPIFound waits until /api/config marks the fleet's claude executable found.
func (in *fleetInst) waitAPIFound(t *testing.T, fleet string) {
	t.Helper()
	waitAPI(t, in.instance, "the "+fleet+" executable found", func(b cfgBody) bool {
		d, ok := b.fleet(fleet).dock("claude")
		return ok && d.ExecutableFound
	})
}

// S6: a restart rebuilds every ship from tmux, and config changes don't disturb them.
func TestRestartRebuildsShips(t *testing.T) {
	w := newWorld(t)
	w.start()
	a := w.launch(w.order("work", "claude", "ra", commodore, "x"))
	b := w.launch(w.order("home", "codex", "rb", customSkill("foo"), "y"))
	before := w.in.ships(t)
	if len(before) != 2 {
		t.Fatalf("ships before restart = %+v", before)
	}

	// The hook bundle is rewritten at every start.
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

	// Story 31: running ships keep their settings when config changes.
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

// Watching: changes made in tmux itself reach Admiral within about a second,
// and the hidden internal session and foreign sessions are never ships.
func TestTmuxChangesReachAdmiral(t *testing.T) {
	w := newWorld(t)
	w.start()
	a := w.launch(w.order("work", "claude", "wa", commodore, "x"))
	ws := dialWS(t, w.in.instance)
	waitShipsMsg(t, ws, 5*time.Second, "the first ship", func(l []shipJSON) bool { return len(l) == 1 })

	w.mustTmux("new-session", "-d", "-s", "stranger", "sleep", "3600")
	time.Sleep(1500 * time.Millisecond)
	if n := len(w.in.ships(t)); n != 1 {
		t.Errorf("a tmux session without @admiral-name must not be a ship, got %d ships", n)
	}
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

// S7: positions and the dock's last directory.
func TestStateFile(t *testing.T) {
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

	// Positions survive a restart; a gone session's position does not come back.
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
	for name, content := range map[string]string{"corrupt": "{not json", "unknown version": `{"version":2,"positions":{}}`} {
		t.Run(name, func(t *testing.T) {
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
			// Admiral works with defaults.
			s := w.launch(w.order("work", "claude", "bad", commodore, "x"))
			if len(w.in.ships(t)) != 1 || s.Position != nil {
				t.Errorf("Admiral did not start with default state")
			}
		})
	}
}

// S8: isolation, and a ship's hooks reach Admiral's socket whatever TMUX says.
func TestIsolationAndTmuxEnvironment(t *testing.T) {
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

	// Nothing reached the default server of TMUX_TMPDIR.
	out, err := exec.Command("tmux", "-S", one.defaultSocket(), "list-sessions").CombinedOutput()
	if err == nil {
		t.Errorf("a default tmux server is running with sessions:\n%s", out)
	}
}
