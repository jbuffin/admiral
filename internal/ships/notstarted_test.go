package ships

import (
	"testing"
	"time"

	"github.com/jbuffin/admiral/internal/sessionhost"
)

var launchedAt = time.Date(2026, 11, 24, 10, 0, 0, 0, time.UTC)

func pastTheWait() time.Time   { return launchedAt.Add(NoHookWait + time.Second) }
func insideTheWait() time.Time { return launchedAt.Add(NoHookWait - time.Second) }

func launchedShip(state string, activity time.Time) sessionhost.Session {
	return sessionhost.Session{Name: "teacup-trawler", State: state, Activity: activity,
		Options: map[string]string{"name": "Teacup Trawler", "dir": "/work/teacup", "launched": launchedAt.Format(time.RFC3339)}}
}

func TestShipWithNoHookSinceLaunchNeedsYouAsNotStartedAndTheOutcomeSaysSo(t *testing.T) {
	sess := launchedShip("", time.Time{})

	if sh := FromSession(sess, insideTheWait()); sh.Status != StatusUnderway {
		t.Errorf("inside the wait: status %q, want underway", sh.Status)
	}
	sh := FromSession(sess, pastTheWait())
	if sh.Status != StatusNeedsYou || sh.Reason != "not_started" {
		t.Errorf("past the wait: status %q reason %q, want needs_you not_started", sh.Status, sh.Reason)
	}

	_, recs := advance(tally{Status: StatusUnderway}, status(sess, pastTheWait()), reasonOf(sess), pastTheWait())
	if len(recs) != 1 || recs[0].Event != "needs_you" || recs[0].Fields["reason"] != "not_started" {
		t.Errorf("outcome records = %+v, want one needs_you with reason not_started", recs)
	}
}

// A dock launch with no text runs SessionStart and then waits at its prompt with no state: it has started.
func TestShipThatRanSessionStartButHasNoStateStaysIdle(t *testing.T) {
	sess := launchedShip("", launchedAt.Add(2*time.Second))
	sh := FromSession(sess, pastTheWait())
	if sh.Status != StatusNeedsYou || sh.Reason != "idle" {
		t.Errorf("status %q reason %q, want needs_you idle", sh.Status, sh.Reason)
	}
}

func TestAStartedStampFromAnEarlierLaunchDoesNotCount(t *testing.T) {
	sess := launchedShip("", launchedAt.Add(-time.Hour))
	sh := FromSession(sess, pastTheWait())
	if sh.Status != StatusNeedsYou || sh.Reason != "not_started" {
		t.Errorf("status %q reason %q, want needs_you not_started", sh.Status, sh.Reason)
	}
}

func TestTheFirstStateWritingHookClearsNotStarted(t *testing.T) {
	if sh := FromSession(launchedShip("", time.Time{}), pastTheWait()); sh.Reason != "not_started" {
		t.Fatalf("before the hook: reason %q, want not_started", sh.Reason)
	}
	// Codex and Copilot have no SessionStart hook: their first state write is the first sign of life.
	sh := FromSession(launchedShip("working", time.Time{}), pastTheWait())
	if sh.Status != StatusUnderway || sh.Reason != "" {
		t.Errorf("after the hook: status %q reason %q, want underway and no reason", sh.Status, sh.Reason)
	}
}

// Answering "No, exit" at the trust prompt kills the pane.
func TestNotStartedShipThatExitsAnchorsAndItsBoatAnchors(t *testing.T) {
	sess := launchedShip("", time.Time{})
	sess.PaneDead, sess.ExitStatus = true, 1
	if sh := FromSession(sess, pastTheWait()); sh.Status != StatusAnchored {
		t.Errorf("ship status %q, want anchored", sh.Status)
	}
	boat := sessionhost.Crew{Name: "mate", State: sessionhost.CrewAnchored, Launched: launchedAt.Format(time.RFC3339)}
	if st, reason := crewStatus(boat, pastTheWait()); st != sessionhost.CrewAnchored || reason != "" {
		t.Errorf("boat status %q reason %q, want anchored", st, reason)
	}
}

func liveBoat(activity time.Time) sessionhost.Crew {
	return sessionhost.Crew{Name: "mate", Harness: "claude", Dir: "/work/teacup", State: sessionhost.CrewUnderway,
		Launched: launchedAt.Format(time.RFC3339), LastActivity: activity}
}

func TestCrewBoatWithNoHookPastTheWaitNeedsYouAsNotStartedAndLiftsItsShip(t *testing.T) {
	boats := crewBoats([]sessionhost.Crew{liveBoat(time.Time{})}, pastTheWait())
	if boats[0].Status != sessionhost.CrewNeedsYou || boats[0].Reason != "not_started" {
		t.Fatalf("boat status %q reason %q, want needs_you not_started", boats[0].Status, boats[0].Reason)
	}
	st, reason, since, ok := wireStatus(StatusUnderway, boats)
	if !ok || st != StatusNeedsYou || reason != ReasonCrew || since != launchedAt.Format(time.RFC3339) {
		t.Errorf("ship lifted to %q %q since %q (lifted %v), want needs_you crew since the boat's launch", st, reason, since, ok)
	}

	if b := crewBoats([]sessionhost.Crew{liveBoat(time.Time{})}, insideTheWait()); b[0].Status != sessionhost.CrewUnderway {
		t.Errorf("inside the wait: boat status %q, want underway", b[0].Status)
	}
}

func TestCrewBoatThatHasHadAHookIsNotNotStarted(t *testing.T) {
	boats := crewBoats([]sessionhost.Crew{liveBoat(launchedAt.Add(2 * time.Second))}, pastTheWait())
	if boats[0].Status != sessionhost.CrewUnderway || boats[0].Reason != "" {
		t.Errorf("boat status %q reason %q, want underway and no reason", boats[0].Status, boats[0].Reason)
	}
}
