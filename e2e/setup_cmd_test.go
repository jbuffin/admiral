package e2e

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// u12 S1: admiral setup, unattended. The CLI surface is documented at the top
// of cli_helpers_test.go.

// setupWorld is a world whose HOME holds what a real one does: identity
// directories the process may not open, a git checkout inside one (which must
// never be suggested), and some that must be.
func setupWorld(t *testing.T) *world {
	t.Helper()
	w := newWorld(t)
	home := w.userHom
	mk := func(rel string) string {
		d := filepath.Join(home, rel)
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
		return d
	}
	// Checkouts first, then the permissions: a directory with mode 000 can't be filled.
	w.git(mk(".claude-x/proj"), "init", "-q")
	mk(".codex-y")
	w.git(mk("code/alpha"), "init", "-q") // the positive control: ~/code must be suggested
	w.git(mk("Library/Thing/proj"), "init", "-q")
	w.git(mk("deep/a/b/c/d/proj"), "init", "-q")
	for _, d := range []string{".claude-x", ".codex-y"} {
		p := filepath.Join(home, d)
		if err := os.Chmod(p, 0); err != nil {
			t.Fatal(err)
		}
		// Registered after newWorld's cleanup, so it runs before RemoveAll.
		t.Cleanup(func() { _ = os.Chmod(p, 0o755) })
	}
	return w
}

// answers builds an answers file from fleet objects written as raw JSON, so the
// fleet order in the text is the order under test.
func (w *world) answers(t *testing.T, fleets ...string) string {
	t.Helper()
	p := filepath.Join(w.root, fmt.Sprintf("answers-%d.json", time.Now().UnixNano()))
	w.writeRaw(p, `{"fleets":{`+strings.Join(fleets, ",")+`}}`, 0o600)
	return p
}

func (w *world) fakeExe2(h string) string { return filepath.Join(fakeDir(w.t), h) }

func TestU12_S1_SetupUnattended(t *testing.T) {
	t.Run("writes the fleets, detects without reading inside identity directories", func(t *testing.T) {
		w := setupWorld(t)
		wrappers := filepath.Join(w.root, "wrappers")
		w.writeRaw(filepath.Join(wrappers, "claude-work"), "#!/bin/sh\nexit 0\n", 0o755)
		file := w.answers(t,
			`"zulu":{"name":"Zulu","color":"#112233","search_roots":["~/code"],
				"claude":{"env":{"CLAUDE_CONFIG_DIR":"~/.claude-x","EXTRA_VAR":"1"}},
				"codex":{}}`,
			`"alpha":{"name":"Alpha","search_roots":["~/code"],
				"claude":{"executable":`+quote(w.fakeExe2("claude"))+`,"args":["--fa"],"env":{"CLAUDE_CONFIG_DIR":"~/.claude-alpha"}},
				"codex":{"env":{"CODEX_HOME":"~/.codex-y"}}}`)

		r := w.admiralWith(cliOpts{path: []string{wrappers}}, "setup", "--answers", file, "--yes")
		w.expectExit(r, 0, "setup --answers --yes (a chmod 000 identity directory must not fail it)")

		c := w.readConfig()
		if string(c.top["version"]) != "1" {
			t.Fatalf(`version = %s, want 1`, c.top["version"])
		}
		if strings.Join(c.fleetOrder, ",") != "zulu,alpha" {
			t.Fatalf("fleet order = %v, want the answers file's order [zulu alpha]", c.fleetOrder)
		}
		if got := c.str(t, "zulu", "color"); got != "#112233" {
			t.Fatalf("zulu color = %q", got)
		}
		if roots := c.roots(t, "zulu"); len(roots) != 1 || w.tilde(roots[0]) != filepath.Join(w.userHom, "code") {
			t.Fatalf("zulu search_roots = %v, want [~/code]", roots)
		}
		zc := c.dock(t, "zulu", "claude")
		if zc.Executable != w.fakeExe2("claude") {
			t.Fatalf("zulu claude executable = %q, want the absolute path found on PATH, %q", zc.Executable, w.fakeExe2("claude"))
		}
		if w.tilde(zc.Env["CLAUDE_CONFIG_DIR"]) != filepath.Join(w.userHom, ".claude-x") || zc.Env["EXTRA_VAR"] != "1" {
			t.Fatalf("zulu claude env = %v", zc.Env)
		}
		// The identity variable is always written, here as the harness default.
		zx := c.dock(t, "zulu", "codex")
		if zx.Executable != w.fakeExe2("codex") {
			t.Fatalf("zulu codex executable = %q, want %q", zx.Executable, w.fakeExe2("codex"))
		}
		if got := w.tilde(zx.Env["CODEX_HOME"]); zx.Env["CODEX_HOME"] == "" || got != filepath.Join(w.userHom, ".codex") {
			t.Fatalf("zulu codex env = %v: CODEX_HOME must be written, naming %s/.codex (got %q)", zx.Env, w.userHom, got)
		}
		if ac := c.dock(t, "alpha", "claude"); len(ac.Args) != 1 || ac.Args[0] != "--fa" {
			t.Fatalf("alpha claude args = %v", ac.Args)
		}

		// What it found, by name only.
		out := r.stdout
		if !strings.Contains(strings.Join(linesWith(out, "Harness claude: "), "\n"), w.fakeExe2("claude")) {
			t.Errorf("no `Harness claude: %s` line in:\n%s", w.fakeExe2("claude"), out)
		}
		if !strings.Contains(strings.Join(linesWith(out, "Wrapper: "), "\n"), "claude-work") {
			t.Errorf("no `Wrapper: .../claude-work` line in:\n%s", out)
		}
		ids := strings.Join(linesWith(out, "Identity directory: "), "\n")
		for _, name := range []string{".claude-x", ".codex-y"} {
			if !strings.Contains(ids, name) {
				t.Errorf("no `Identity directory:` line naming %s in:\n%s", name, out)
			}
		}
		suggested := linesWith(out, "Suggested search root: ")
		if !strings.Contains(strings.Join(suggested, "\n"), filepath.Join(w.userHom, "code")) && !strings.Contains(strings.Join(suggested, "\n"), "~/code") {
			t.Errorf("~/code holds a checkout and should be suggested; suggestions: %q", suggested)
		}
		for _, l := range suggested {
			for _, bad := range []string{".claude", ".codex", ".copilot", "Library", "/deep"} {
				if strings.Contains(l, bad) {
					t.Errorf("suggested %q, but the walk skips dot-directories, ~/Library and anything deeper than 3: %s", bad, l)
				}
			}
		}
	})

	t.Run("a second run adds and updates, keeps hand edits and unknown keys, and shows the diff", func(t *testing.T) {
		w := setupWorld(t)
		oldFleet := `"old":{"name":"Old","search_roots":["~/code"],"claude":{"executable":` + quote(w.fakeExe2("claude")) + `,"env":{"CLAUDE_CONFIG_DIR":"~/.claude-old"}}}`
		w.writeRaw(configPath(w.home), `{
  "version": 1,
  "x_future": {"keep": [1, 2, {"deep": true}]},
  "fleets": {`+oldFleet+`}
}
`, 0o600)
		before := w.readConfig()

		zulu := func(name string) string {
			return `"zulu":{"name":` + quote(name) + `,"search_roots":["~/code"],"claude":{"env":{"CLAUDE_CONFIG_DIR":"~/.claude-z"}}}`
		}
		r := w.admiral("setup", "--answers", w.answers(t, zulu("Zulu")), "--yes")
		w.expectExit(r, 0, "first run")
		c := w.readConfig()
		if strings.Join(c.fleetOrder, ",") != "old,zulu" {
			t.Fatalf("fleet order = %v, want [old zulu]: a re-run adds and never wipes", c.fleetOrder)
		}
		if c.fleetRaw(t, "old") != before.fleetRaw(t, "old") {
			t.Fatalf("the old fleet changed:\n%s\nwas\n%s", c.fleetRaw(t, "old"), before.fleetRaw(t, "old"))
		}
		if compactJSON(t, c.top["x_future"]) != compactJSON(t, before.top["x_future"]) {
			t.Fatalf("unknown top-level key x_future = %s, want it kept as it was", c.top["x_future"])
		}
		added := false
		for _, l := range strings.Split(r.stdout, "\n") {
			added = added || (strings.HasPrefix(l, "+") && strings.Contains(l, "zulu"))
		}
		if !added {
			t.Errorf("the diff should show the new fleet as a `+` line naming zulu:\n%s", r.stdout)
		}

		r = w.admiral("setup", "--answers", w.answers(t, zulu("Zulu Two")), "--yes")
		w.expectExit(r, 0, "second run")
		c = w.readConfig()
		if strings.Join(c.fleetOrder, ",") != "old,zulu" || c.str(t, "zulu", "name") != "Zulu Two" {
			t.Fatalf("after the update: order %v, zulu name %q", c.fleetOrder, c.str(t, "zulu", "name"))
		}
		if compactJSON(t, c.top["x_future"]) != compactJSON(t, before.top["x_future"]) {
			t.Fatalf("x_future after the second run = %s", c.top["x_future"])
		}
		var minus, plus bool
		for _, l := range strings.Split(r.stdout, "\n") {
			minus = minus || (strings.HasPrefix(l, "-") && strings.Contains(l, `"Zulu"`))
			plus = plus || (strings.HasPrefix(l, "+") && strings.Contains(l, `"Zulu Two"`))
		}
		if !minus || !plus {
			t.Errorf("the diff should remove the name Zulu and add Zulu Two:\n%s", r.stdout)
		}
	})

	t.Run("--dry-run prints the diff and writes nothing", func(t *testing.T) {
		w := setupWorld(t)
		file := w.answers(t, `"zulu":{"name":"Zulu","search_roots":["~/code"],"claude":{"env":{"CLAUDE_CONFIG_DIR":"~/.claude-z"}}}`)

		r := w.admiral("setup", "--answers", file, "--dry-run")
		w.expectExit(r, 0, "dry run without a config")
		w.requireNoConfig()
		added := false
		for _, l := range strings.Split(r.stdout, "\n") {
			added = added || (strings.HasPrefix(l, "+") && strings.Contains(l, "zulu"))
		}
		if !added {
			t.Errorf("--dry-run should print the diff (a `+` line naming zulu):\n%s", r.stdout)
		}

		existing := `{"version":1,"x_future":true,"fleets":{}}` + "\n"
		w.writeRaw(configPath(w.home), existing, 0o600)
		r = w.admiral("setup", "--answers", file, "--dry-run", "--yes")
		w.expectExit(r, 0, "dry run with a config")
		if got := string(w.configBytes()); got != existing {
			t.Fatalf("--dry-run changed config.json to:\n%s", got)
		}
	})

	t.Run("two fleets sharing an identity directory are refused and nothing is written", func(t *testing.T) {
		w := setupWorld(t)
		share := func(a, b string) string {
			return w.answers(t,
				`"one":{"name":"One","search_roots":["~/code"],"claude":`+a+`}`,
				`"two":{"name":"Two","search_roots":["~/code"],"claude":`+b+`}`)
		}
		env := func(dir string) string { return `{"env":{"CLAUDE_CONFIG_DIR":` + quote(dir) + `}}` }

		for name, file := range map[string]string{
			"the same variable":          share(env("~/.claude-p"), env("~/.claude-p")),
			"~ and the absolute spelling": share(env("~/.claude-p"), env(filepath.Join(w.userHom, ".claude-p"))),
			"neither sets it (both use the default)": share(`{}`, `{}`),
		} {
			r := w.admiral("setup", "--answers", file, "--yes")
			w.expectExit(r, 1, "two fleets with "+name)
			low := strings.ToLower(r.both())
			if !strings.Contains(low, "identity directory") || !strings.Contains(low, "one") || !strings.Contains(low, "two") {
				t.Errorf("%s: the refusal should name the identity directory and both fleets, got:\n%s", name, r.both())
			}
			w.requireNoConfig()
		}
	})

	t.Run("a fleet sharing an identity directory with one already in the file is refused", func(t *testing.T) {
		w := setupWorld(t)
		existing := `{"version":1,"fleets":{"old":{"name":"Old","search_roots":["~/code"],"claude":{"env":{"CLAUDE_CONFIG_DIR":"~/.claude-old"}}}}}` + "\n"
		w.writeRaw(configPath(w.home), existing, 0o600)
		file := w.answers(t, `"new":{"name":"New","search_roots":["~/code"],"claude":{"env":{"CLAUDE_CONFIG_DIR":"~/.claude-old"}}}`)

		r := w.admiral("setup", "--answers", file, "--yes")
		w.expectExit(r, 1, "a fleet that shares the old fleet's identity directory")
		if got := string(w.configBytes()); got != existing {
			t.Fatalf("config.json changed to:\n%s", got)
		}
	})

	t.Run("answers that don't validate are refused with the path and write nothing", func(t *testing.T) {
		w := setupWorld(t)
		file := w.answers(t, `"zulu":{"name":"Zulu","search_roots":["~/code"],"claud":{}}`)

		r := w.admiral("setup", "--answers", file, "--yes")
		w.expectExit(r, 1, "an unknown key in the answers")
		if !strings.Contains(r.both(), "fleets.zulu.claud") {
			t.Errorf("the error should name fleets.zulu.claud, got:\n%s", r.both())
		}
		w.requireNoConfig()
	})
}

// Exit codes: 0 ok, 1 error, 2 usage.
func TestU12_ExitCodes(t *testing.T) {
	w := newWorld(t)
	w.expectExit(w.admiral("nosuchcommand"), 2, "an unknown command")
	w.expectExit(w.admiral("setup", "--yes"), 2, "--yes without --answers")
	w.expectExit(w.admiral("fleet", "check", "nosuchfleet"), 1, "fleet check for a fleet that isn't there")
}

// Story 12: `admiral` on a terminal with no fleets offers setup. A terminal is
// a tmux pane; without one (launchd, the other e2e tests) the server just
// starts, which every other test already relies on.
func TestU12_NoFleetsOffersSetupOnATerminal(t *testing.T) {
	w := newWorld(t)
	sock := filepath.Join(w.root, "p")
	tmux := func(args ...string) string {
		cmd := exec.Command("tmux", append([]string{"-S", sock}, args...)...)
		cmd.Env = w.baseEnv()
		out, _ := cmd.CombinedOutput()
		return string(out)
	}
	t.Cleanup(func() { tmux("kill-server") })
	tmux("new-session", "-d", "-x", "100", "-y", "30", binaryPath(t)+" --listen 127.0.0.1:0")

	wait := func(want string) string {
		t.Helper()
		var pane string
		deadline := time.Now().Add(10 * time.Second)
		for time.Now().Before(deadline) {
			if pane = tmux("capture-pane", "-p", "-t", "=0:"); strings.Contains(pane, want) {
				return pane
			}
			time.Sleep(100 * time.Millisecond)
		}
		t.Fatalf("within 10s the terminal never showed %q; it shows:\n%s", want, pane)
		return ""
	}
	pane := wait("[y/N]")
	if !strings.Contains(strings.ToLower(pane), "setup") {
		t.Fatalf("the offer should mention setup:\n%s", pane)
	}
	tmux("send-keys", "-t", "=0:", "n", "Enter")
	wait("Admiral is running at http://127.0.0.1:")
}
