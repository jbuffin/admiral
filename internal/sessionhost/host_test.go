package sessionhost

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

// The events u5 depends on, from a real tmux on a private socket: every bell
// (not just the first), a pane's exit status, and a pane killed by a signal.
func TestBellsAndPaneDeath(t *testing.T) {
	tmux := needTmux(t)
	dir, err := os.MkdirTemp("/tmp", "sh")
	if err != nil {
		t.Fatal(err)
	}
	sock := filepath.Join(dir, "tmux.sock")
	t.Cleanup(func() {
		_ = exec.Command(tmux, "-S", sock, "kill-server").Run()
		_ = os.RemoveAll(dir)
	})
	t.Setenv("TMUX_TMPDIR", dir)
	run := func(args ...string) {
		t.Helper()
		if out, err := exec.Command(tmux, append([]string{"-S", sock}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("tmux %v: %v\n%s", args, err, out)
		}
	}

	h := New(tmux, sock)
	events, cancel := h.Subscribe()
	defer cancel()
	ctx, stop := context.WithCancel(context.Background())
	defer stop()
	go h.Run(ctx)
	select {
	case <-h.Ready():
	case <-time.After(10 * time.Second):
		t.Fatal("the host never read the sessions")
	}

	run("new-session", "-d", "-s", "bell", "sh", "-c", `sleep 2; printf '\a'; sleep 1; printf '\a'; sleep 1; exit 3`)
	run("set-option", "-p", "-t", "bell:", "remain-on-exit", "on")
	run("new-session", "-d", "-s", "killed", "sh", "-c", "sleep 3; kill -KILL $$")
	run("set-option", "-p", "-t", "killed:", "remain-on-exit", "on")
	run("set-option", "-t", "bell:", "@admiral-name", "Bell")

	bells, died, signalled := 0, false, false
	deadline := time.After(10 * time.Second)
	for !(bells == 2 && died && signalled) {
		select {
		case ev := <-events:
			switch {
			case ev.Kind == Bell && ev.Session == "bell":
				bells++
			case ev.Kind == PaneDied && ev.Session == "bell" && ev.Status == 3 && ev.Signal == "":
				died = true
			case ev.Kind == PaneDied && ev.Session == "killed":
				signalled = ev.Signal == "kill" && ev.Status == 0
			}
		case <-deadline:
			t.Fatalf("within 10s: bells = %d (want 2), exit status seen = %v, signal seen = %v", bells, died, signalled)
		}
	}
}

// needTmux finds tmux. Under -short (PR CI, which has no tmux) the test skips.
func needTmux(t *testing.T) string {
	t.Helper()
	if testing.Short() {
		t.Skip("needs tmux: skipped under -short")
	}
	tmux, err := exec.LookPath("tmux")
	if err != nil {
		t.Fatal("tmux is required:", err)
	}
	return tmux
}

// testHost is a Host connected to a real tmux on a private socket.
func testHost(t *testing.T) (*Host, context.Context) {
	t.Helper()
	tmux := needTmux(t)
	dir, err := os.MkdirTemp("/tmp", "sh")
	if err != nil {
		t.Fatal(err)
	}
	sock := filepath.Join(dir, "tmux.sock")
	t.Setenv("TMUX_TMPDIR", dir)
	t.Cleanup(func() {
		_ = exec.Command(tmux, "-S", sock, "kill-server").Run()
		_ = os.RemoveAll(dir)
	})
	h := New(tmux, sock)
	ctx, stop := context.WithCancel(context.Background())
	t.Cleanup(stop)
	go h.Run(ctx)
	select {
	case <-h.Ready():
	case <-time.After(10 * time.Second):
		t.Fatal("the host never read the sessions")
	}
	return h, ctx
}

// A command that gives up (here: a context already cancelled) must not leave
// its reply behind for the next command to read as its own.
func TestCancelledCommandDoesNotShiftReplies(t *testing.T) {
	h, ctx := testHost(t)
	for i := 0; i < 5; i++ {
		dead, cancel := context.WithCancel(context.Background())
		cancel()
		if _, err := h.command(dead, "display-message -p first"); err == nil {
			t.Fatal("a cancelled command should report the cancellation")
		}
		got, err := h.command(ctx, "display-message -p second")
		if err != nil || len(got) != 1 || got[0] != "second" {
			t.Fatalf("round %d: the next command got %q (%v), want its own reply [second]", i, got, err)
		}
		// A list answers once per command; the replies of a cancelled list and
		// of the next must each be counted, or SetNeedsYou shifts them.
		if _, err := h.commandList(dead, "display-message -p x", "display-message -p y"); err == nil {
			t.Fatal("a cancelled list should report the cancellation")
		}
		got, err = h.commandList(ctx, "display-message -p a", "display-message -p b")
		if err != nil || len(got) != 1 || got[0] != "b" {
			t.Fatalf("round %d: the list got %q (%v), want the last reply [b]", i, got, err)
		}
		got, err = h.command(ctx, "display-message -p third")
		if err != nil || len(got) != 1 || got[0] != "third" {
			t.Fatalf("round %d: the command after a list got %q (%v), want [third]", i, got, err)
		}
	}
}

// Forged and split rows: a session whose rows disagree, or which has a short
// row, is unreadable; a row that starts with no known session is dropped.
func TestParseRowsDistrustsForgedRows(t *testing.T) {
	row := func(name, order string) string {
		f := make([]string, fixedFields+len(optionNames)+tailFields)
		f[0] = name
		for i, n := range optionNames {
			f[fixedFields+i] = "v"
			if n == "order" {
				f[fixedFields+i] = order
			}
		}
		return strings.Join(f, sep)
	}
	known := map[string]bool{"aaa": true, "zeta": true, "split": true}
	got, unreadable := parseRows([]string{
		row("aaa", "1"), row("zeta", "real"), row("zeta", "forged"), // aaa's value planted a second zeta row
		"split" + sep + "short", "stray fragment", row("ghost", "1"),
	}, known)
	if _, ok := got["aaa"]; !ok || len(got) != 1 {
		t.Errorf("readable sessions = %v, want only aaa", got)
	}
	if want := []string{"split", "zeta"}; !slices.Equal(unreadable, want) {
		t.Errorf("unreadable = %v, want %v", unreadable, want)
	}
}

// A crew row never sets a ship field, whatever the order of the rows: the ship's
// row can come after its crew's, a session with only crew rows is no session, and
// a crew row's options (which differ from the session's) don't make it unreadable.
func TestParseRowsFoldsCrewWithoutLeaking(t *testing.T) {
	row := func(name, crew, state, dead string, opts string) string {
		f := make([]string, fixedFields+len(optionNames)+tailFields)
		f[0], f[1], f[6] = name, dead, state
		for i, n := range optionNames {
			f[fixedFields+i] = opts
			if n == "name" {
				f[fixedFields+i] = opts + "-" + n
			}
		}
		tail := fixedFields + len(optionNames)
		f[tail], f[tail+1], f[tail+2] = crew, "%"+name+crew, "@"+name+crew
		if crew == "" {
			f[tail+6] = `[{"id":"a1","type":"Explore","start":"2026-10-24T10:00:00Z"}]`
		}
		return strings.Join(f, sep)
	}
	known := map[string]bool{"ship": true, "orphan": true}
	got, unreadable := parseRows([]string{
		row("ship", "mate", "needs_you", "1", "crewopts"), // crew first, dead, other options
		row("ship", "", "working", "0", "shipopts"),
		row("orphan", "mate", "needs_you", "0", "crewopts"), // its ship window is gone
	}, known)
	if len(unreadable) != 0 {
		t.Errorf("unreadable = %v, want none", unreadable)
	}
	if _, ok := got["orphan"]; ok || len(got) != 1 {
		t.Errorf("sessions = %v, want only ship (a session of crew rows alone is no session)", got)
	}
	s := got["ship"]
	if s.PaneDead || s.State != "working" || s.Options["harness"] != "shipopts" || s.PaneID != "%ship" {
		t.Errorf("ship = %+v, want its own row's fields, none of the crew row's", s)
	}
	if len(s.Crew) != 1 || s.Crew[0].Name != "mate" || s.Crew[0].State != CrewAnchored || s.Crew[0].Harness != "crewopts" {
		t.Errorf("crew = %+v, want mate, anchored, read from its own row", s.Crew)
	}
	if len(s.Subagents) != 1 || s.Subagents[0].ID != "a1" {
		t.Errorf("subagents = %+v, want a1 from the ship's row", s.Subagents)
	}
}

// tmux stops a command list at its first failing command and answers it with
// one %error, so the list's replies are fewer than its commands. Neither a list
// that errored, nor one that was cancelled before its error arrived, may leave a
// reply count behind that makes the next command wait for one that never comes.
func TestFailedListDoesNotShiftReplies(t *testing.T) {
	h, ctx := testHost(t)
	const target = `-t "=nosuchsession:"`
	failing := []string{"set-option -p " + target + " @admiral-reason x", "set-option -p " + target + " @admiral-state y"}
	for i := 0; i < 3; i++ {
		start := time.Now()
		if _, err := h.commandList(ctx, failing...); err == nil {
			t.Fatalf("round %d: a list on a missing session should fail", i)
		}
		if d := time.Since(start); d > 8*time.Second {
			t.Fatalf("round %d: the failed list took %v, want its single %%error at once", i, d)
		}
		got, err := h.command(ctx, "display-message -p after-error")
		if err != nil || len(got) != 1 || got[0] != "after-error" {
			t.Fatalf("round %d: the next command got %q (%v), want [after-error]", i, got, err)
		}
		dead, cancel := context.WithCancel(context.Background())
		cancel()
		if _, err := h.commandList(dead, failing...); err == nil {
			t.Fatalf("round %d: a cancelled list should report an error", i)
		}
		got, err = h.command(ctx, "display-message -p after-cancel")
		if err != nil || len(got) != 1 || got[0] != "after-cancel" {
			t.Fatalf("round %d: the command after a cancelled failing list got %q (%v), want [after-cancel]", i, got, err)
		}
	}
}

// A ship with a crew window (even the current one) reads as before, its state
// writes land on its own pane, and a Codex crew boat's bell marks that boat
// needs_you without flagging the ship.
func TestShipWithCrewWindow(t *testing.T) {
	h, ctx := testHost(t)
	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command(h.tmux, append([]string{"-S", h.sock}, args...)...)
		cmd.Env = h.env
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("tmux %v: %v\n%s", args, err, out)
		}
	}
	run("new-session", "-d", "-s", "ship", "sleep", "60")
	run("set-option", "-t", "=ship:", "@admiral-name", "Ship")
	run("set-option", "-t", "=ship:", "@admiral-harness", "claude")
	run("new-window", "-t", "=ship:", "-n", "mate", "sh", "-c", `sleep 1; printf '\a'; sleep 60`) // not -d: the crew window is current
	run("set-option", "-w", "-t", "=ship:mate", "monitor-bell", "on")
	run("set-option", "-w", "-t", "=ship:mate", "@admiral-crew-name", "mate")
	run("set-option", "-w", "-t", "=ship:mate", "@admiral-harness", "codex")
	if err := h.Sync(ctx); err != nil {
		t.Fatal(err)
	}
	if err := h.SetNeedsYou(ctx, "ship", "stop"); err != nil {
		t.Fatal(err)
	}
	if err := h.Sync(ctx); err != nil {
		t.Fatal(err)
	}
	var ship Session
	for _, s := range h.Sessions() {
		if s.Name == "ship" {
			ship = s
		}
	}
	if ship.Options["harness"] != "claude" || ship.State != "needs_you" || len(h.Unreadable()) != 0 {
		t.Fatalf("ship = harness %q state %q, unreadable %v; want claude, needs_you (written to its own pane), none", ship.Options["harness"], ship.State, h.Unreadable())
	}
	deadline := time.Now().Add(10 * time.Second)
	for {
		crew, err := ReadCrew(h.tmux, h.sock, "ship")
		if err != nil {
			t.Fatal(err)
		}
		if len(crew) == 1 && crew[0].Name == "mate" && crew[0].State == CrewNeedsYou {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("crew = %+v after 10s, want mate needs_you from its own bell", crew)
		}
		time.Sleep(100 * time.Millisecond)
	}
	_ = h.Sync(ctx)
	for _, s := range h.Sessions() {
		if s.Name == "ship" && s.BellStamp != "" {
			t.Errorf("ship BellStamp = %q; the crew boat's bell must not flag the ship", s.BellStamp)
		}
	}
}
