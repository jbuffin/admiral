package ships

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/jbuffin/admiral/internal/moor"
)

const resumeText = "/commodore resume run-7"

// moorAged moors a ship that can be woken, a half hour ago.
func (f *mooredFixture) moorAged(t *testing.T, name, fleet string, prs ...string) string {
	t.Helper()
	session := f.moor(t, name, fleet, prs...)
	rec := moor.Record{Name: name, Session: session, Fleet: fleet, Harness: "claude", Checkout: "/src/widgets", PRs: prs,
		Resume: resumeText, MooredAt: time.Now().Add(-30 * time.Minute).UTC().Truncate(time.Second)}
	if err := moor.Write(f.home, rec); err != nil {
		t.Fatal(err)
	}
	return session
}

// reviewed is an open PR with a review from a bot, which wakes a ship, submitted fifteen minutes ago.
func reviewed() moor.Status {
	return moor.Status{State: "OPEN", Reviews: []moor.Review{{ID: "R1", Author: moor.Actor{Type: "Bot", Login: "reviewbot[bot]"},
		State: "COMMENTED", Body: "rename it", SubmittedAt: time.Now().Add(-15 * time.Minute).UTC().Truncate(time.Second)}}}
}

func conflicting() moor.Status { return moor.Status{State: "OPEN", Mergeable: "CONFLICTING"} }

func (f *mooredFixture) wakeState(t *testing.T, name string) moor.WakeState {
	t.Helper()
	got, err := moor.ReadWake(f.home, name)
	if err != nil {
		t.Fatal(err)
	}
	return got
}

func TestAConditionFirstSeenIsSavedAndTheNextPollKeepsItsTime(t *testing.T) {
	g := &pollGitHub{}
	g.set(prSeven, conflicting())
	g.set(prNine, moor.Status{State: "OPEN"})
	f := mooredService(t, map[string]*pollGitHub{"work": g})
	f.moorAged(t, "Nelson", "work", prSeven)
	f.moorAged(t, "Hood", "work", prNine)

	f.s.PollMoored(context.Background())

	first := f.wakeState(t, "Nelson").PRs[prSeven].Seen["conflict"]
	if first.IsZero() {
		t.Fatalf("Nelson's wake state after the poll = %+v, want the conflict's first-seen time saved", f.wakeState(t, "Nelson"))
	}
	if _, err := os.Stat(wakeFile(f, "Hood")); err == nil {
		t.Error("a ship with nothing first seen got a wake state file, want it written only when the state changes")
	}
	if sh, _ := f.ship(t, "Nelson"); sh.Waiting != "" {
		t.Errorf("waiting = %q for a ship still inside the debounce, want none", sh.Waiting)
	}

	time.Sleep(1100 * time.Millisecond)
	f.s.PollMoored(context.Background())

	if got := f.wakeState(t, "Nelson").PRs[prSeven].Seen["conflict"]; !got.Equal(first) {
		t.Errorf("conflict first seen = %v after a second poll, want %v kept", got, first)
	}
}

func TestAMergedPRSailsTheShipOffAndNothingIsDecided(t *testing.T) {
	g := &pollGitHub{}
	merged := reviewed()
	merged.State = "MERGED"
	g.set(prSeven, merged)
	g.set(prNine, conflicting())
	f := mooredService(t, map[string]*pollGitHub{"work": g})
	merging := f.moorAged(t, "Nelson", "work", prSeven)
	f.moorAged(t, "Hood", "work", prNine)

	f.s.PollMoored(context.Background())

	if len(f.events(t, merging, "unmoored")) != 1 {
		t.Errorf("unmoored events = %v, want the merged ship ended", f.events(t, merging, "unmoored"))
	}
	if _, err := os.Stat(wakeFile(f, "Nelson")); err == nil {
		t.Error("the merged ship has a wake state file")
	}
	if f.wakeState(t, "Hood").PRs[prNine].Seen["conflict"].IsZero() {
		t.Error("the ship beside it, still open, was not decided")
	}
}

func TestAShipWithNoValidResumeTextIsNotDecidedAndShowsNoWaiting(t *testing.T) {
	g := &pollGitHub{}
	g.set(prSeven, reviewed())
	g.set(prNine, reviewed())
	f := mooredService(t, map[string]*pollGitHub{"work": g})
	f.moorAged(t, "Nelson", "work", prSeven)
	f.moor(t, "Hood", "work", prNine) // no resume text

	f.s.PollMoored(context.Background())

	if sh, _ := f.ship(t, "Nelson"); sh.Waiting != "Waiting for Admiral's terminal host" {
		t.Errorf("a ship due a round with no terminal host: waiting = %q, want the terminal host line", sh.Waiting)
	}
	if sh, _ := f.ship(t, "Hood"); sh.Waiting != "" || !sh.NoResume {
		t.Errorf("a ship with no resume text: waiting = %q, no_resume = %v, want no waiting line beside its no-resume note", sh.Waiting, sh.NoResume)
	}
}

func TestWaitingClearsOnAPollThatNoLongerDecidesToWake(t *testing.T) {
	g := &pollGitHub{}
	g.set(prSeven, reviewed())
	f := mooredService(t, map[string]*pollGitHub{"work": g})
	f.moorAged(t, "Nelson", "work", prSeven)
	f.s.PollMoored(context.Background())
	if sh, _ := f.ship(t, "Nelson"); sh.Waiting == "" {
		t.Fatal("the ship due a round shows no waiting line")
	}

	g.set(prSeven, moor.Status{State: "OPEN"})
	f.s.PollMoored(context.Background())

	if sh, _ := f.ship(t, "Nelson"); sh.Waiting != "" {
		t.Errorf("waiting = %q after the review went away, want it cleared", sh.Waiting)
	}
}

func TestANextWriteIsSkippedForAShipWhoseWakeHasCommitted(t *testing.T) {
	g := &pollGitHub{}
	g.set(prSeven, conflicting())
	g.set(prNine, conflicting())
	f := mooredService(t, map[string]*pollGitHub{"work": g})
	f.moorAged(t, "Nelson", "work", prSeven)
	f.moorAged(t, "Hood", "work", prNine)
	f.s.List() // loads the records
	f.s.mooredMu.Lock()
	f.s.committed = map[string]bool{"nelson": true}
	f.s.mooredMu.Unlock()

	f.s.PollMoored(context.Background())

	if f.wakeState(t, "Hood").PRs[prNine].Seen["conflict"].IsZero() {
		t.Fatal("the uncommitted ship was not decided")
	}
	if _, err := os.Stat(wakeFile(f, "Nelson")); err == nil {
		t.Errorf("the poll wrote the wake state of a ship whose wake had committed: %+v", f.wakeState(t, "Nelson"))
	}
}
