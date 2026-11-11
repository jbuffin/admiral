package ships

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jbuffin/admiral/internal/moor"
)

// polling gives the rig a GitHub that answers the poll's batched query.
func (r *wakeRig) polling() *pollGitHub {
	g := &pollGitHub{}
	r.s.GitHub = func(string) moor.GitHub { return g }
	return g
}

// aliceReview is a review from the fleet's wake_reviewers, submitted fifteen minutes ago.
func aliceReview() moor.Status {
	return moor.Status{State: "OPEN", Reviews: []moor.Review{{ID: "R1", Author: moor.Actor{Type: "User", Login: "alice"},
		State: "COMMENTED", Body: "rename the helper", SubmittedAt: time.Now().Add(-15 * time.Minute).UTC().Truncate(time.Second)}}}
}

func (r *wakeRig) state(t *testing.T) moor.WakeState {
	t.Helper()
	got, err := moor.ReadWake(r.home, "Saltpaw")
	if err != nil {
		t.Fatal(err)
	}
	return got
}

// waiting is the moored ship's waiting line, "" once it is not moored.
func (r *wakeRig) waiting() string {
	for _, sh := range r.s.List() {
		if sh.Name == "Saltpaw" {
			return sh.Waiting
		}
	}
	return ""
}

func (r *wakeRig) stillMoored(t *testing.T) bool {
	t.Helper()
	recs, err := moor.List(r.home)
	if err != nil {
		t.Fatal(err)
	}
	return len(recs) == 1
}

func TestAReviewFromAWakeReviewerWakesTheShipOnceWithThePRAndTheFeedbackFile(t *testing.T) {
	r := newWakeRig(t, 3)
	g := r.polling()
	g.set(wakePR, aliceReview())
	r.mooredResume(t, 0, "/commodore resume run-7", 30*time.Minute)

	r.s.PollMoored(context.Background())

	if r.stillMoored(t) {
		t.Fatal("the ship is still moored after a poll with a qualifying review that has waited out the debounce")
	}
	argv, env := r.started(t, 1)
	if got := prompt(argv); got != "/commodore resume run-7 feedback round 1 on "+wakePR {
		t.Errorf("prompt = %q, want the resume text, then feedback round 1 on the PR", got)
	}
	if !strings.Contains(env, "ADMIRAL_AUTOPILOT=1") {
		t.Error("env has no ADMIRAL_AUTOPILOT=1, want the round unattended")
	}
	b, _ := os.ReadFile(filepath.Join(r.checkout, ".admiral", "untrusted", "feedback-1.md"))
	if !strings.Contains(string(b), "rename the helper") || !strings.Contains(string(b), wakePR) {
		t.Errorf("feedback-1.md = %q, want the review's body and the PR", b)
	}
	st := r.state(t).PRs[wakePR]
	if st.Cursor.At.IsZero() || st.RoundStart.IsZero() {
		t.Errorf("wake state = %+v, want the cursor advanced and the round's start set", st)
	}
	if got := r.logged(t, "woken"); len(got) != 1 || got[0]["by"] != "feedback" || got[0]["pr"] != wakePR || got[0]["round"] != float64(1) {
		t.Errorf("woken events = %v, want one by feedback for round 1 on the PR", got)
	}
}

func TestTheNextPollAfterTheRoundDoesNotWakeTheShipOnTheSameReview(t *testing.T) {
	r := newWakeRig(t, 3)
	g := r.polling()
	g.set(wakePR, aliceReview())
	first := r.mooredResume(t, 0, "/commodore resume run-7", 30*time.Minute)
	// The cursor sits at the old mooring, before the review: only the round's saved state keeps the review from waking the next one.
	if err := moor.WriteWake(r.home, "Saltpaw", moor.WakeState{PRs: map[string]moor.PRWake{wakePR: {Cursor: moor.Cursor{At: first}}}}); err != nil {
		t.Fatal(err)
	}
	r.s.PollMoored(context.Background())
	r.started(t, 1)
	r.kill(t, "saltpaw")
	r.moored(t, 1) // the round ends and the ship moors again, now

	r.s.PollMoored(context.Background())

	if !r.stillMoored(t) {
		t.Error("the ship woke again on a review the first round already took")
	}
	if got := r.logged(t, "woken"); len(got) != 1 {
		t.Errorf("woken events = %v, want one", got)
	}
}

func TestAtTheCapTheStateIsUnchangedAndTheShipWaitsThenWakesOnceThereIsRoom(t *testing.T) {
	r := newWakeRig(t, 1)
	g := r.polling()
	g.set(wakePR, aliceReview())
	r.mooredResume(t, 0, "/commodore resume run-7", 30*time.Minute)
	key := OrderKey{"s", "other"}
	r.s.holdUnlisted(key, "ship-1", "p", nil)

	r.s.PollMoored(context.Background())

	if got := r.waiting(); got != "Waiting for room under the cap" {
		t.Errorf("waiting = %q, want the cap's line", got)
	}
	if st := r.state(t); len(st.PRs) != 0 {
		t.Errorf("wake state = %+v at the cap, want it unchanged", st)
	}
	if _, err := os.Stat(filepath.Join(r.checkout, ".admiral")); err == nil {
		t.Error("a refused round wrote under .admiral")
	}

	r.s.mu.Lock()
	delete(r.s.listing, key)
	r.s.mu.Unlock()
	r.s.PollMoored(context.Background())

	if r.stillMoored(t) {
		t.Fatal("the ship is still moored once there is room under the cap")
	}
	r.started(t, 1)
	if st := r.state(t).PRs[wakePR]; st.Cursor.At.IsZero() {
		t.Errorf("wake state = %+v, want the round's events handed over", st)
	}
}

func TestABusyResourceShowsItsNameAndLeavesTheStateThenTheShipWakesWhenItFrees(t *testing.T) {
	r := newWakeRig(t, 3)
	g := r.polling()
	g.set(wakePR, aliceReview())
	r.mooredResume(t, 0, "/commodore resume run-7", 30*time.Minute)

	for _, why := range []string{"busy", "missing", "pending"} {
		r.mu.Lock()
		r.refuseName, r.refuseWhy = "phone", why
		r.mu.Unlock()
		r.s.PollMoored(context.Background())
		if got, want := r.waiting(), "Waiting for phone ("+why+")"; got != want {
			t.Errorf("waiting = %q, want %q", got, want)
		}
		if st := r.state(t); len(st.PRs) != 0 {
			t.Errorf("wake state = %+v after a refusal for a %s resource, want it unchanged", st, why)
		}
	}

	r.mu.Lock()
	r.refuseWhy = ""
	r.mu.Unlock()
	r.s.PollMoored(context.Background())

	if r.stillMoored(t) {
		t.Error("the ship is still moored once its resource is free")
	}
}

func TestARefusalNoWaitingClearsIsShownAsCantWakeWithItsMessage(t *testing.T) {
	r := newWakeRig(t, 3)
	g := r.polling()
	g.set(wakePR, aliceReview())
	r.resources = []string{"nope"}
	r.mooredResume(t, 0, "/commodore resume run-7", 30*time.Minute)

	r.s.PollMoored(context.Background())

	if got, want := r.waiting(), `Can't wake: no resource named "nope"`; got != want {
		t.Errorf("waiting = %q, want %q", got, want)
	}
	if !r.stillMoored(t) {
		t.Error("the ship left its mooring on a refusal")
	}
}

// A file where the fleet's directory belongs fails the launch in shipContract, after the round-start hook ran.
func TestALaunchThatFailsAfterTheHookPutsTheWakeStateBack(t *testing.T) {
	r := newWakeRig(t, 3)
	g := r.polling()
	g.set(wakePR, aliceReview())
	r.mooredResume(t, 0, "/commodore resume run-7", 30*time.Minute)
	before := time.Date(2026, 11, 1, 9, 0, 0, 0, time.UTC)
	if err := moor.WriteWake(r.home, "Saltpaw", moor.WakeState{PRs: map[string]moor.PRWake{
		wakePR: {Seen: map[string]time.Time{"conflict": before}}}}); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(r.home, "fleets"), []byte("not a directory"), 0o600); err != nil {
		t.Fatal(err)
	}

	r.s.PollMoored(context.Background())

	if !r.stillMoored(t) {
		t.Fatal("the failed launch took the ship off its mooring")
	}
	st := r.state(t).PRs[wakePR]
	if !st.Seen["conflict"].Equal(before) || !st.Cursor.At.IsZero() || !st.RoundStart.IsZero() {
		t.Errorf("wake state = %+v after the failed launch, want the state from before the poll", st)
	}
	if got := r.waiting(); !strings.HasPrefix(got, "Can't wake: ") {
		t.Errorf("waiting = %q, want the can't-wake line for a refusal that does not clear", got)
	}
	if got := r.logged(t, "woken"); len(got) != 0 {
		t.Errorf("woken events = %v, want none", got)
	}
}

func TestAHandWakeWritesTheRoundStartAndTheBoundaryCursorAndKeepsTheRest(t *testing.T) {
	r := newWakeRig(t, 0)
	moored := r.mooredResume(t, 0, "/commodore resume run-7", 30*time.Minute)
	seen := time.Date(2026, 11, 1, 9, 0, 0, 0, time.UTC)
	if err := moor.WriteWake(r.home, "Saltpaw", moor.WakeState{PRs: map[string]moor.PRWake{
		wakePR: {Seen: map[string]time.Time{"conflict": seen}, Snapshot: moor.Snapshot{Head: "abc123"}}}}); err != nil {
		t.Fatal(err)
	}
	start := time.Now().Add(-time.Second)

	if _, lerr := r.s.Wake(context.Background(), "saltpaw", ""); lerr != nil {
		t.Fatal(lerr)
	}

	st := r.state(t).PRs[wakePR]
	if !st.Cursor.At.Equal(moored) {
		t.Errorf("cursor = %v, want the old mooring %v, so feedback that queues during the round survives the next mooring", st.Cursor.At, moored)
	}
	if st.RoundStart.Before(start) || st.RoundStart.After(time.Now().Add(time.Second)) {
		t.Errorf("round start = %v, want the time of the wake", st.RoundStart)
	}
	if !st.Seen["conflict"].Equal(seen) || st.Snapshot.Head != "abc123" {
		t.Errorf("state = %+v, want the first-seen times and snapshot as they were", st)
	}
}

func TestAFailedHandWakeLaunchPutsTheWakeStateBack(t *testing.T) {
	r := newWakeRig(t, 0)
	r.mooredResume(t, 0, "/commodore resume run-7", 30*time.Minute)
	seen := time.Date(2026, 11, 1, 9, 0, 0, 0, time.UTC)
	if err := moor.WriteWake(r.home, "Saltpaw", moor.WakeState{PRs: map[string]moor.PRWake{wakePR: {Seen: map[string]time.Time{"conflict": seen}}}}); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(r.home, "fleets"), []byte("not a directory"), 0o600); err != nil {
		t.Fatal(err)
	}

	if _, lerr := r.s.Wake(context.Background(), "saltpaw", ""); lerr == nil {
		t.Fatal("the wake launched, want it refused")
	}

	st := r.state(t).PRs[wakePR]
	if !st.Seen["conflict"].Equal(seen) || !st.Cursor.At.IsZero() || !st.RoundStart.IsZero() {
		t.Errorf("wake state = %+v after the failed hand wake, want the state from before", st)
	}
}

func TestAFailedLaunchWithNoEarlierWakeStateLeavesNoWakeStateFile(t *testing.T) {
	r := newWakeRig(t, 3)
	g := r.polling()
	g.set(wakePR, aliceReview())
	r.mooredResume(t, 0, "/commodore resume run-7", 30*time.Minute)
	if err := os.WriteFile(filepath.Join(r.home, "fleets"), []byte("not a directory"), 0o600); err != nil {
		t.Fatal(err)
	}

	r.s.PollMoored(context.Background())

	if _, err := os.Stat(filepath.Join(r.home, "moored", "wake", "Saltpaw.json")); err == nil {
		t.Errorf("a wake state file is left after a failed launch that had none before: %+v", r.state(t))
	}
}

func TestARoundIsRefusedWhenTheWakeStateChangedSinceItWasDecided(t *testing.T) {
	r := newWakeRig(t, 3)
	r.mooredResume(t, 0, "/commodore resume run-7", 30*time.Minute)
	decidedFrom := moor.WakeState{}
	other := moor.WakeState{PRs: map[string]moor.PRWake{wakePR: {Seen: map[string]time.Time{"conflict": time.Date(2026, 11, 1, 9, 0, 0, 0, time.UTC)}}}}
	if err := moor.WriteWake(r.home, "Saltpaw", other); err != nil {
		t.Fatal(err)
	}
	next := moor.WakeState{PRs: map[string]moor.PRWake{wakePR: {Cursor: moor.Cursor{At: time.Now().UTC()}}}}

	_, lerr := r.s.feedbackRound("saltpaw", wakePR, "text", &decidedFrom, next)

	if lerr == nil || lerr.Wait != WaitInFlight {
		t.Fatalf("round = %v, want a refusal that waits for the current wake", lerr)
	}
	if got := r.state(t); !sameWake(got, other) {
		t.Errorf("wake state = %+v, want the one on disk left alone", got)
	}
	if !r.stillMoored(t) {
		t.Error("the refused round took the ship off its mooring")
	}
	if _, err := os.Stat(filepath.Join(r.checkout, ".admiral")); err == nil {
		t.Error("a refused round wrote under .admiral")
	}
}
