// EndHarness ends the harness process and keeps the pane, noting why; a process that ignores TERM is killed after the grace.
package sessionhost

import (
	"os/exec"
	"strings"
	"testing"
	"time"
)

func startShip(t *testing.T, h *Host, name, script string) {
	t.Helper()
	tmux := needTmux(t)
	for _, args := range [][]string{
		{"new-session", "-d", "-s", name, "sh", "-c", script},
		{"set-option", "-p", "-t", name + ":", "remain-on-exit", "on"},
		{"set-option", "-t", name + ":", "@admiral-name", name},
	} {
		if out, err := exec.Command(tmux, append([]string{"-S", h.Socket()}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("tmux %v: %v\n%s", args, err, out)
		}
	}
}

func sessionNamed(h *Host, name string) (Session, bool) {
	for _, s := range h.Sessions() {
		if s.Name == name {
			return s, true
		}
	}
	return Session{}, false
}

func waitDead(t *testing.T, h *Host, name string) Session {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if s, ok := sessionNamed(h, name); ok && s.PaneDead {
			return s
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("%s's pane never died", name)
	return Session{}
}

func TestEndHarnessTermsTheShipsProcessGroupKeepsThePaneAndNotesWhy(t *testing.T) {
	h, ctx := testHost(t)
	// the child sleeps in the pane's process group, so ending only the shell's pid would leave it running
	startShip(t, h, "polite", "sleep 600 & wait")
	if err := h.Sync(ctx); err != nil {
		t.Fatal(err)
	}
	if err := h.EndHarness(ctx, "polite", "wall_clock:60", 2*time.Second); err != nil {
		t.Fatalf("EndHarness: %v", err)
	}
	s := waitDead(t, h, "polite")
	if !strings.EqualFold(s.Signal, "term") {
		t.Errorf("the pane died of %q, want TERM for a process that obeys it", s.Signal)
	}
	if err := h.Sync(ctx); err != nil {
		t.Fatal(err)
	}
	if s, _ = sessionNamed(h, "polite"); s.Options[OptStoppedBy] != "wall_clock:60" {
		t.Errorf("stopped-by = %q, want wall_clock:60 so the page can say the wall clock stopped it", s.Options[OptStoppedBy])
	}
}

func TestEndHarnessKillsAProcessThatIgnoresTermOnceTheGraceIsUp(t *testing.T) {
	h, ctx := testHost(t)
	startShip(t, h, "stubborn", `trap '' TERM; while :; do sleep 1; done`)
	if err := h.Sync(ctx); err != nil {
		t.Fatal(err)
	}
	started := time.Now()
	if err := h.EndHarness(ctx, "stubborn", "wall_clock:60", 500*time.Millisecond); err != nil {
		t.Fatalf("EndHarness: %v", err)
	}
	s := waitDead(t, h, "stubborn")
	if !strings.EqualFold(s.Signal, "kill") {
		t.Errorf("the pane died of %q, want KILL after the grace", s.Signal)
	}
	if time.Since(started) < 400*time.Millisecond {
		t.Errorf("died after %v, before the 500ms grace was up", time.Since(started))
	}
}

func TestEndHarnessOnAShipThatIsNotListedIsAnError(t *testing.T) {
	h, ctx := testHost(t)
	if err := h.EndHarness(ctx, "nobody", "wall_clock:60", time.Second); err == nil {
		t.Error("EndHarness of an unlisted ship answered nil, want an error")
	}
}
