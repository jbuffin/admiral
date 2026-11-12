package ships

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jbuffin/admiral/internal/launcher"
	"github.com/jbuffin/admiral/internal/moor"
	"github.com/jbuffin/admiral/internal/sessionhost"
)

// launchRound starts Saltpaw as a fresh Claude session in the given round, as a wake would.
func (r *wakeRig) launchRound(t *testing.T, round int) {
	t.Helper()
	_, lerr := r.s.LaunchOrder(launcher.Request{Fleet: "work", Harness: "claude", Dir: r.checkout, Model: "opus",
		Name: "Saltpaw", Session: "saltpaw", Round: round, Prompt: "go", Skill: launcher.Skill{Kind: "none"}})
	if lerr != nil {
		t.Fatalf("LaunchOrder = %v", lerr)
	}
}

func sessionIDOf(t *testing.T, argv []string) string {
	t.Helper()
	for i, a := range argv {
		if a == "--session-id" && i+1 < len(argv) {
			return argv[i+1]
		}
	}
	t.Fatalf("argv %v has no --session-id", argv)
	return ""
}

// exitAndMoor ends the harness and moors the dead pane's ship, as the poll does once its PR is open.
func (r *wakeRig) exitAndMoor(t *testing.T) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(r.rec, "exit"), []byte("0"), 0o600); err != nil {
		t.Fatal(err)
	}
	var dead sessionhost.Session
	waitUntil(t, "the pane to die", func() bool {
		_ = r.s.Host.Sync(context.Background())
		for _, sess := range r.s.Host.Sessions() {
			if sess.Name == "saltpaw" && sess.PaneDead {
				dead = sess
				return true
			}
		}
		return false
	})
	if !r.s.moor(context.Background(), dead, []string{wakePR}) {
		t.Fatal("moor reported no record")
	}
}

func TestMooringRecordsTheRoundWithItsSessionAndTranscript(t *testing.T) {
	r := newWakeRig(t, 0)
	before := time.Now().Add(-time.Minute)
	r.launchRound(t, 0)
	argv, _ := r.started(t, 1)
	id := sessionIDOf(t, argv)
	r.exitAndMoor(t)

	got, err := moor.ReadRounds(r.home, "Saltpaw")
	if err != nil || len(got) != 1 {
		t.Fatalf("ReadRounds = %+v, %v; want round 0 recorded under the ship's name", got, err)
	}
	h := got[0]
	if h.Round != 0 || h.SessionID != id || h.Fleet != "work" || !strings.HasSuffix(h.Transcript, "/"+id+".jsonl") ||
		!sessionhost.ValidTranscriptPath(h.Transcript) {
		t.Errorf("round = %+v, want round 0 of session %s in fleet work with a valid transcript path ending in its id", h, id)
	}
	if h.Started.Before(before) || h.Ended.Before(h.Started) {
		t.Errorf("started %s, ended %s; want the pane's launch, then the mooring", h.Started, h.Ended)
	}
}

func TestAWokenRoundIsRecordedBesideTheEarlierOneUnderItsOwnSession(t *testing.T) {
	r := newWakeRig(t, 0)
	r.launchRound(t, 0)
	argv1, _ := r.started(t, 1)
	r.exitAndMoor(t)
	r.kill(t, "saltpaw")
	if err := os.Remove(filepath.Join(r.rec, "exit")); err != nil {
		t.Fatal(err)
	}
	r.moored(t, 0)
	if _, lerr := r.s.Wake(context.Background(), "saltpaw", ""); lerr != nil {
		t.Fatalf("Wake = %v", lerr)
	}
	argv2, _ := r.started(t, 2)
	if got, _ := moor.ReadRounds(r.home, "Saltpaw"); len(got) != 1 {
		t.Fatalf("history after the wake = %+v, want round 0 kept: a wake drops the record, not the history", got)
	}
	r.exitAndMoor(t)

	got, _ := moor.ReadRounds(r.home, "Saltpaw")
	if len(got) != 2 || got[0].Round != 0 || got[1].Round != 1 {
		t.Fatalf("history = %+v, want rounds 0 and 1", got)
	}
	if got[0].SessionID != sessionIDOf(t, argv1) || got[1].SessionID != sessionIDOf(t, argv2) || got[0].SessionID == got[1].SessionID {
		t.Errorf("session ids = %q, %q; want each round's own --session-id (%s, %s)",
			got[0].SessionID, got[1].SessionID, sessionIDOf(t, argv1), sessionIDOf(t, argv2))
	}
}

func TestAForgedTranscriptOptionIsRecordedAsNoTranscript(t *testing.T) {
	r := newWakeRig(t, 0)
	r.launchRound(t, 0)
	r.started(t, 1)
	forged := filepath.Join(r.checkout, "..", "cfg", "projects", "zz", "11111111-2222-4333-8444-555555555555.jsonl")
	forged = filepath.Clean(forged)
	if err := r.s.Host.Sync(context.Background()); err != nil {
		t.Fatal(err)
	}
	var pane string
	for _, sess := range r.s.Host.Sessions() {
		if sess.Name == "saltpaw" {
			pane = sess.PaneID
		}
	}
	if pane == "" {
		t.Fatal("no pane for saltpaw")
	}
	opt := sessionhost.TranscriptOption
	if out, err := exec.Command(r.s.Launcher.Tmux, "-S", r.s.Launcher.Socket, "set-option", "-p", "-t", pane, opt,
		"1700000000000000000 launch "+forged).CombinedOutput(); err != nil {
		t.Fatalf("set-option: %v: %s", err, out)
	}
	r.exitAndMoor(t)

	got, _ := moor.ReadRounds(r.home, "Saltpaw")
	if len(got) != 1 || got[0].Transcript != "" {
		t.Errorf("history = %+v, want round 0 with no transcript: the option names another session than the start command", got)
	}
}

func TestEndingAMooringForGoodRemovesTheRoundHistory(t *testing.T) {
	r := newWakeRig(t, 0)
	r.launchRound(t, 0)
	r.started(t, 1)
	r.exitAndMoor(t)
	if got, _ := moor.ReadRounds(r.home, "Saltpaw"); len(got) != 1 {
		t.Fatalf("history = %+v, want round 0 before the dismiss", got)
	}
	if ended, err := r.s.endMoored("saltpaw", "dismissed"); !ended || err != nil {
		t.Fatalf("endMoored = %v, %v", ended, err)
	}
	if got, _ := moor.ReadRounds(r.home, "Saltpaw"); len(got) != 0 {
		t.Errorf("history = %+v after the dismiss, want none", got)
	}
}
