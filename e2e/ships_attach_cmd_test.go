package e2e

import (
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// u12 S4: admiral ships and admiral attach. The CLI surface is documented at
// the top of cli_helpers_test.go.

type shipRow struct{ name, session, fleet, harness, status, repo string }

// shipTable parses `admiral ships`: tab-separated, a header line, then rows.
func shipTable(t *testing.T, out string) map[string]shipRow {
	t.Helper()
	rows := map[string]shipRow{}
	headed := false
	for _, l := range strings.Split(out, "\n") {
		f := strings.Split(l, "\t")
		if len(f) != 6 {
			continue
		}
		if !headed {
			if strings.Join(f, " ") != "NAME SESSION FLEET HARNESS STATUS REPO" {
				t.Fatalf("the ships table should start with the header NAME SESSION FLEET HARNESS STATUS REPO (tab-separated), got %q", f)
			}
			headed = true
			continue
		}
		rows[f[1]] = shipRow{f[0], f[1], f[2], f[3], f[4], f[5]}
	}
	if !headed {
		t.Fatalf("no tab-separated table in:\n%s", out)
	}
	return rows
}

// sail launches a Claude ship in a checkout called repo with the given cues and
// returns once the fake has run them.
func sail(t *testing.T, w *world, repo string, cues ...string) shipJSON {
	t.Helper()
	w.writeCue(cues...)
	s := w.launch(w.order("work", "claude", repo, noSkill, "go"))
	w.waitFile(s.Session, "cues-done")
	return s
}

func TestU12_S4_ShipsAndAttach(t *testing.T) {
	w := newWorld(t)
	w.start()
	live := sail(t, w, "alpha", "hook working")
	gone := sail(t, w, "bravo", "hook working", "exit 3")
	w.in.waitShipsList(t, 10*time.Second, "alpha underway and bravo anchored", func(l []shipJSON) bool {
		m := map[string]string{}
		for _, s := range l {
			m[s.Session] = s.Status
		}
		return m[live.Session] == "underway" && m[gone.Session] == "anchored"
	})
	// The setup terminal's session is not a ship.
	w.mustTmux("new-session", "-d", "-s", "_setup", "sleep 300")

	t.Run("ships asks the server and lists every ship, but not _setup", func(t *testing.T) {
		w := w.sub(t)
		r := w.admiral("ships", "--port", w.in.Port)
		w.expectExit(r, 0, "admiral ships")
		if strings.Contains(strings.ToLower(r.both()), "not running") {
			t.Errorf("the server is running, but ships says otherwise:\n%s", r.both())
		}
		rows := shipTable(t, r.stdout)
		if strings.Contains(r.stdout, "_setup") {
			t.Errorf("_setup is not a ship:\n%s", r.stdout)
		}
		a, ok := rows[live.Session]
		if !ok {
			t.Fatalf("no row for %s in:\n%s", live.Session, r.stdout)
		}
		if a.name != live.Name || a.fleet != "work" || a.harness != "claude" || a.status != "underway" || a.repo != "alpha" {
			t.Errorf("alpha row = %+v, want name %q, fleet work, harness claude, underway, repo alpha", a, live.Name)
		}
		if b := rows[gone.Session]; b.status != "anchored (exit 3)" || b.repo != "bravo" {
			t.Errorf("bravo row = %+v, want status `anchored (exit 3)` and repo bravo", b)
		}
	})

	t.Run("attach runs tmux with an exact target and ignore-size, read-only when anchored", func(t *testing.T) {
		w := w.sub(t)
		dir, rec := w.installFakeTmux()
		attach := func(name string) cliResult {
			return w.admiralWith(cliOpts{path: []string{dir}, env: []string{"FAKE_TMUX_RECORD=" + rec}}, "attach", name)
		}
		sock := filepath.Join(w.home, "tmux.sock")

		r := attach(live.Name)
		w.expectExit(r, 0, "attach by display name")
		calls := attachCalls(w, rec)
		if len(calls) != 1 {
			t.Fatalf("attach should run tmux attach once, recorded %v", calls)
		}
		argv := calls[0]
		if !hasPair(argv, "-S", sock) || !has(argv, "attach") || !hasPair(argv, "-f", "ignore-size") || !hasPair(argv, "-t", "="+live.Session) {
			t.Errorf("argv = %q, want -S %s attach -f ignore-size -t =%s", argv, sock, live.Session)
		}
		if has(argv, "-r") {
			t.Errorf("an underway ship attaches read-write, got %q", argv)
		}
		if n := strings.Count(r.both(), "Ctrl-b d to detach"); n != 1 {
			t.Errorf(`"Ctrl-b d to detach" should be printed once, got %d times in:`+"\n%s", n, r.both())
		}

		// By session name, the anchored ship, read-only.
		w.expectExit(attach(gone.Session), 0, "attach by session name")
		calls = attachCalls(w, rec)
		if len(calls) != 2 || !hasPair(calls[1], "-t", "="+gone.Session) || !has(calls[1], "-r") || !hasPair(calls[1], "-f", "ignore-size") {
			t.Errorf("anchored attach = %q, want -r, -f ignore-size and -t =%s", calls, gone.Session)
		}

		// Refusals: nothing reaches tmux, and the exit code is 1.
		for _, name := range []string{"_setup", "_admiral", "no such ship"} {
			r := attach(name)
			w.expectExit(r, 1, "attach "+name)
			if name[0] == '_' && !strings.Contains(r.both(), name) {
				t.Errorf("the refusal should name %s, got:\n%s", name, r.both())
			}
		}
		if got := attachCalls(w, rec); len(got) != 2 {
			t.Errorf("a refused attach still ran tmux attach: %q", got)
		}
	})

	t.Run("completion scripts and --names", func(t *testing.T) {
		w := w.sub(t)
		names := w.admiral("ships", "--names", "--port", w.in.Port)
		w.expectExit(names, 0, "ships --names")
		if got := strings.Split(strings.TrimSpace(names.stdout), "\n"); !has(got, live.Name) || !has(got, gone.Name) {
			t.Errorf("ships --names should print display names one per line, got %q", got)
		}
		for shell, marker := range map[string]string{"zsh": "compdef", "bash": "complete "} {
			r := w.admiral("completion", shell)
			w.expectExit(r, 0, "completion "+shell)
			if !strings.Contains(r.stdout, marker) || !strings.Contains(r.stdout, "ships --names") {
				t.Errorf("completion %s should register itself (%q) and ask `admiral ships --names`, got:\n%s", shell, marker, r.stdout)
			}
		}
	})

	t.Run("with the server stopped ships still lists, labelled, and attach still works", func(t *testing.T) {
		w := w.sub(t)
		w.in.Stop()
		r := w.admiral("ships", "--port", w.in.Port)
		w.expectExit(r, 0, "admiral ships without a server")
		first := strings.ToLower(strings.SplitN(strings.TrimSpace(r.stdout), "\n", 2)[0])
		if !strings.Contains(first, "not running") {
			t.Errorf("the first line should say Admiral is not running, got %q", first)
		}
		rows := shipTable(t, r.stdout)
		if rows[live.Session].name != live.Name || rows[gone.Session].name != gone.Name {
			t.Errorf("read from tmux, the table should still hold both ships:\n%s", r.stdout)
		}

		dir, rec := w.installFakeTmux()
		w.expectExit(w.admiralWith(cliOpts{path: []string{dir}, env: []string{"FAKE_TMUX_RECORD=" + rec}}, "attach", live.Name), 0, "attach with the server down")
		if calls := attachCalls(w, rec); len(calls) != 1 || !hasPair(calls[0], "-t", "="+live.Session) {
			t.Errorf("attach without a server recorded %q", calls)
		}
	})
}
