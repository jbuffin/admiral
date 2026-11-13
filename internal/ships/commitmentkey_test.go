package ships

import (
	"context"
	"net/http"
	"path/filepath"
	"testing"

	"github.com/jbuffin/admiral/internal/config"
	"github.com/jbuffin/admiral/internal/launcher"
	"github.com/jbuffin/admiral/internal/sessionhost"
)

// The quiet gate refuses an unproven autopilot launch the way the normal gate does, but leaves the Inbox alone.
func TestAQuietGateRefusesAnUnprovenHarnessWithoutAnInboxNotice(t *testing.T) {
	home := guardedHome(t) // proven for claude in work only
	s := &Service{
		Host:     sessionhost.New("tmux", t.TempDir()+"/tmux.sock"), // never run: a launch that got through would be a 503
		Launcher: &launcher.Launcher{Home: home, Config: func() *config.Config { return &config.Config{Fleets: fleets()} }},
	}
	notices := func() int {
		names, _ := filepath.Glob(filepath.Join(home, "inbox", "_admiral", "notice-*.json"))
		return len(names)
	}
	req := launcher.Request{Fleet: "work", Harness: "codex", Dir: t.TempDir(), Skill: launcher.Skill{Kind: "none"}, Autopilot: true, QuietGate: true}

	_, lerr := s.Launch(context.Background(), req)
	if lerr == nil || lerr.Status != http.StatusBadRequest {
		t.Fatalf("a quiet autopilot launch of an unproven harness = %v, want a 400", lerr)
	}
	if got := notices(); got != 0 {
		t.Errorf("a quiet refusal left %d Inbox notices, want 0", got)
	}

	req.QuietGate = false
	if _, lerr := s.Launch(context.Background(), req); lerr == nil || lerr.Status != http.StatusBadRequest {
		t.Fatalf("the same launch without the quiet gate = %v, want a 400", lerr)
	}
	if got := notices(); got != 1 {
		t.Errorf("the normal refusal left %d Inbox notices, want 1: the gate itself still notifies", got)
	}
}

func TestACommitmentKeyHoldsASecondLaunchAndNamesNoRoutine(t *testing.T) {
	r := newWakeRig(t, 0)
	const key = "commitment:c1"
	first := r.mustLaunch(t, func(q *launcher.Request) { q.CommitmentKey = key })

	got, ok := r.s.Holder(key)
	if !ok || got.Session != first.Session {
		t.Errorf("Holder(%q) = %v, %v, want the launched ship %q", key, got.Session, ok, first.Session)
	}
	if first.Routine != "" || got.Routine != "" {
		t.Errorf("Routine = %q, want empty: a commitment ship is not a routine ship", got.Routine)
	}
	if _, ok := r.s.Holder("routine:c1"); ok {
		t.Errorf("a routine key found the commitment's ship")
	}
	r.wantHeld(t, func(q *launcher.Request) { q.CommitmentKey = key }, first)
}
