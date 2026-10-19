package e2e

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// u12 S2: admiral fleet list | add | edit | check and admiral config show |
// validate | edit. The CLI surface is documented at the top of cli_helpers_test.go.

// cmdWorld is a world whose config.json holds two fleets and a top-level key
// Admiral doesn't know.
func cmdWorld(t *testing.T) (*world, string) {
	t.Helper()
	w := newWorld(t)
	claude, codex := w.fakeExe2("claude"), w.fakeExe2("codex")
	text := `{
  "version": 1,
  "x_future": {"keep": [1, 2, {"deep": true}]},
  "fleets": {
    "zulu": {"name": "Zulu", "search_roots": ["~/code"], "claude": {"executable": ` + quote(claude) + `, "env": {"CLAUDE_CONFIG_DIR": "~/.claude-z"}}},
    "alpha": {"name": "Alpha", "search_roots": ["~/code"], "codex": {"executable": ` + quote(codex) + `, "env": {"CODEX_HOME": "~/.codex-a"}}}
  }
}
`
	w.writeRaw(configPath(w.home), text, 0o600)
	return w, text
}

func TestU12_S2_FleetList(t *testing.T) {
	w, _ := cmdWorld(t)
	r := w.admiral("fleet", "list")
	w.expectExit(r, 0, "fleet list")
	var rows []string
	for _, l := range strings.Split(r.stdout, "\n") {
		if strings.TrimSpace(l) != "" {
			rows = append(rows, l)
		}
	}
	if len(rows) != 2 {
		t.Fatalf("fleet list should print one line per fleet, got:\n%s", r.stdout)
	}
	for i, want := range [][]string{{"zulu", "Zulu", "claude"}, {"alpha", "Alpha", "codex"}} {
		for _, s := range want {
			if !strings.Contains(rows[i], s) {
				t.Errorf("line %d (file order: zulu, then alpha) should carry %q: %q", i+1, s, rows[i])
			}
		}
	}
}

func TestU12_S2_FleetAddAndEditKeepWhatTheyDoNotTouch(t *testing.T) {
	w, original := cmdWorld(t)
	before := w.readConfig()
	unchanged := func(c savedConfig, what string) {
		t.Helper()
		if compactJSON(t, c.top["x_future"]) != compactJSON(t, before.top["x_future"]) {
			t.Errorf("%s: x_future = %s, want it kept", what, c.top["x_future"])
		}
		for _, k := range []string{"zulu", "alpha"} {
			if c.fleetRaw(t, k) != before.fleetRaw(t, k) {
				t.Errorf("%s: fleet %s changed:\n%s", what, k, c.fleetRaw(t, k))
			}
		}
	}

	r := w.admiral("fleet", "add", "bravo", "--name", "Bravo", "--color", "#00AA55",
		"--harness", "claude", "--harness", "codex",
		"--executable", "claude="+w.fakeExe2("claude"),
		"--env", "claude=CLAUDE_CONFIG_DIR=~/.claude-b", "--env", "codex=CODEX_HOME=~/.codex-b",
		"--search-root", "~/src")
	w.expectExit(r, 0, "fleet add")
	c := w.readConfig()
	if strings.Join(c.fleetOrder, ",") != "zulu,alpha,bravo" {
		t.Fatalf("fleet order = %v, want the new fleet appended", c.fleetOrder)
	}
	unchanged(c, "after add")
	if c.str(t, "bravo", "name") != "Bravo" || c.str(t, "bravo", "color") != "#00AA55" {
		t.Errorf("bravo = %s", c.top["fleets"])
	}
	if d := c.dock(t, "bravo", "claude"); d.Executable != w.fakeExe2("claude") || d.Env["CLAUDE_CONFIG_DIR"] != "~/.claude-b" {
		t.Errorf("bravo claude = %+v", d)
	}
	if d := c.dock(t, "bravo", "codex"); d.Env["CODEX_HOME"] != "~/.codex-b" {
		t.Errorf("bravo codex = %+v", d)
	}
	if roots := c.roots(t, "bravo"); len(roots) != 1 || roots[0] != "~/src" {
		t.Errorf("bravo search_roots = %v", roots)
	}

	afterAdd := string(w.configBytes())
	w.expectExit(w.admiral("fleet", "add", "bravo", "--name", "Again", "--harness", "claude",
		"--env", "claude=CLAUDE_CONFIG_DIR=~/.claude-again"), 1, "adding a key that exists")
	if string(w.configBytes()) != afterAdd {
		t.Fatalf("a refused add changed config.json")
	}

	r = w.admiral("fleet", "edit", "bravo", "--name", "Bravo Two", "--arg", "claude=--verbose",
		"--env", "claude=FOO=bar", "--remove-search-root", "~/src", "--search-root", "~/more")
	w.expectExit(r, 0, "fleet edit")
	c = w.readConfig()
	unchanged(c, "after edit")
	if c.str(t, "bravo", "name") != "Bravo Two" {
		t.Errorf("bravo name = %q", c.str(t, "bravo", "name"))
	}
	if d := c.dock(t, "bravo", "claude"); len(d.Args) != 1 || d.Args[0] != "--verbose" || d.Env["FOO"] != "bar" || d.Env["CLAUDE_CONFIG_DIR"] != "~/.claude-b" {
		t.Errorf("bravo claude after edit = %+v", d)
	}
	if roots := c.roots(t, "bravo"); len(roots) != 1 || roots[0] != "~/more" {
		t.Errorf("bravo search_roots after edit = %v, want [~/more]", roots)
	}

	w.expectExit(w.admiral("fleet", "edit", "bravo", "--unset-env", "claude=FOO"), 0, "fleet edit --unset-env")
	if d := w.readConfig().dock(t, "bravo", "claude"); d.Env["FOO"] != "" || d.Env["CLAUDE_CONFIG_DIR"] != "~/.claude-b" {
		t.Errorf("bravo claude env after --unset-env = %v, want FOO gone and the identity kept", d.Env)
	}
	w.expectExit(w.admiral("fleet", "edit", "nosuch", "--name", "X"), 1, "editing a fleet that isn't there")

	// Story 26: add and edit refuse a shared identity directory.
	settled := string(w.configBytes())
	r = w.admiral("fleet", "add", "charlie", "--name", "Charlie", "--harness", "claude", "--env", "claude=CLAUDE_CONFIG_DIR=~/.claude-z")
	w.expectExit(r, 1, "fleet add sharing zulu's identity directory")
	if !strings.Contains(strings.ToLower(r.both()), "identity directory") {
		t.Errorf("the refusal should say identity directory, got:\n%s", r.both())
	}
	r = w.admiral("fleet", "edit", "bravo", "--env", "claude=CLAUDE_CONFIG_DIR=~/.claude-z")
	w.expectExit(r, 1, "fleet edit onto zulu's identity directory")
	if string(w.configBytes()) != settled {
		t.Fatalf("a refused add or edit changed config.json")
	}
	_ = original
}

func TestU12_S2_FleetCheckResolvesAndNeverRuns(t *testing.T) {
	w := newWorld(t)
	marker := filepath.Join(w.root, "ran")
	w.writeRaw(filepath.Join(w.root, "bin3", "claude"), "#!/bin/sh\ntouch "+marker+"\n", 0o755)
	w.writeRaw(configPath(w.home), `{"version":1,"fleets":{
  "fine":   {"name":"Fine","search_roots":["~/code"],"claude":{"executable":`+quote(w.fakeExe2("claude"))+`}},
  "bare":   {"name":"Bare","search_roots":["~/code"],"codex":{}},
  "gone":   {"name":"Gone","search_roots":["~/code"],"claude":{"executable":"/nonexistent/dir/claude"}},
  "marker": {"name":"Marker","search_roots":["~/code"],"claude":{"executable":`+quote(filepath.Join(w.root, "bin3", "claude"))+`}}
}}`, 0o600)

	r := w.admiral("fleet", "check")
	w.expectExit(r, 1, "fleet check with a missing executable")
	status := func(key, harness string) []string {
		for _, l := range strings.Split(r.stdout, "\n") {
			if f := strings.Fields(l); len(f) >= 4 && f[0] == key && f[1] == harness {
				return f
			}
		}
		t.Fatalf("no `%s %s ok|missing ...` line in:\n%s", key, harness, r.stdout)
		return nil
	}
	if f := status("fine", "claude"); f[2] != "ok" || f[3] != w.fakeExe2("claude") {
		t.Errorf("fine claude = %v, want ok and its path", f)
	}
	if f := status("bare", "codex"); f[2] != "ok" || f[3] != w.fakeExe2("codex") {
		t.Errorf("bare codex = %v, want the name resolved on PATH to %s", f, w.fakeExe2("codex"))
	}
	if f := status("gone", "claude"); f[2] != "missing" {
		t.Errorf("gone claude = %v, want missing", f)
	}
	status("marker", "claude")
	if _, err := os.Stat(marker); err == nil {
		t.Fatalf("fleet check ran the executable (it touched %s)", marker)
	}

	w.expectExit(w.admiral("fleet", "check", "fine"), 0, "fleet check of one good fleet")
}

func TestU12_S2_ConfigShowValidateEdit(t *testing.T) {
	w, original := cmdWorld(t)

	r := w.admiral("config", "show")
	w.expectExit(r, 0, "config show")
	if r.stdout != original {
		t.Fatalf("config show should print the file as it is, got:\n%s", r.stdout)
	}
	r = w.admiral("config", "validate")
	w.expectExit(r, 0, "config validate on a valid file")
	if strings.TrimSpace(r.stderr) != "" {
		t.Errorf("a valid file prints nothing on stderr, got %q", r.stderr)
	}

	// $EDITOR is a script that copies EDITOR_PAYLOAD over the file it is given.
	editor := filepath.Join(w.root, "editor.sh")
	w.writeRaw(editor, "#!/bin/sh\n[ -n \"$1\" ] || exit 9\ncp \"$EDITOR_PAYLOAD\" \"$1\"\n", 0o755)
	edit := func(t *testing.T, payload string) cliResult {
		t.Helper()
		w := w.sub(t)
		p := filepath.Join(w.root, "payload.json")
		w.writeRaw(p, payload, 0o600)
		return w.admiralWith(cliOpts{env: []string{"EDITOR=" + editor, "EDITOR_PAYLOAD=" + p}}, "config", "edit")
	}

	t.Run("a result that validates is saved, with its unknown keys", func(t *testing.T) {
		w := w.sub(t)
		before := w.readConfig()
		edited := `{"version":1,"added_by_editor":{"n":1},"x_future":{"keep":[1,2,{"deep":true}]},"fleets":{"zulu":{"name":"Zulu Edited","search_roots":["~/code"],"claude":{"env":{"CLAUDE_CONFIG_DIR":"~/.claude-z"}}}}}`
		w.expectExit(edit(t, edited), 0, "config edit with a valid result")
		c := w.readConfig()
		if c.str(t, "zulu", "name") != "Zulu Edited" || len(c.fleetOrder) != 1 {
			t.Fatalf("the edit was not saved: %s", w.configBytes())
		}
		if string(c.top["added_by_editor"]) == "" || compactJSON(t, c.top["x_future"]) != compactJSON(t, before.top["x_future"]) {
			t.Fatalf("unknown top-level keys must survive an edit: %s", w.configBytes())
		}
	})

	t.Run("a result that doesn't validate is refused and the file stays", func(t *testing.T) {
		w := w.sub(t)
		kept := string(w.configBytes())
		r := edit(t, `{"version":1,"fleets":{"zulu":{"name":"Zulu","claud":{}}}}`)
		w.expectExit(r, 1, "config edit with an invalid result")
		if !strings.Contains(r.both(), "fleets.zulu.claud") {
			t.Errorf("the refusal should name fleets.zulu.claud, got:\n%s", r.both())
		}
		if string(w.configBytes()) != kept {
			t.Fatalf("an invalid edit was saved")
		}
	})

	t.Run("an invalid file can still be opened, and fixed", func(t *testing.T) {
		w := w.sub(t)
		w.writeRaw(configPath(w.home), `{"version":1,"fleets":{"zulu":{"name":"Zulu","claud":{}}}}`, 0o600)
		bad := w.admiral("config", "validate")
		w.expectExit(bad, 1, "config validate on an invalid file")
		if !strings.Contains(bad.both(), "fleets.zulu.claud") {
			t.Errorf("validate should name fleets.zulu.claud, got:\n%s", bad.both())
		}
		w.expectExit(edit(t, `{"version":1,"fleets":{"zulu":{"name":"Zulu","search_roots":["~/code"],"claude":{}}}}`), 0, "config edit that fixes the file")
		w.expectExit(w.admiral("config", "validate"), 0, "config validate after the fix")
	})
}
