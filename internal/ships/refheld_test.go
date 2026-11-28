package ships

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/jbuffin/admiral/internal/launcher"
)

const heldIssue = "https://github.com/acme/api/issues/42"

func eventOn(url, routine string) func(*launcher.Request) {
	return func(q *launcher.Request) { q.RoutineKey, q.EventURL = "routine:"+routine, url }
}

func TestASecondEventLaunchOnTheSameIssueIsRefusedAsIssueHeldNamingTheFirstShip(t *testing.T) {
	t.Parallel()
	r := newWakeRig(t, 0)
	first := r.mustLaunch(t, eventOn(heldIssue, "r1"))
	before := len(r.s.List())
	_, lerr := r.s.Launch(context.Background(), r.keyReq(eventOn(heldIssue, "r2")))
	if lerr == nil || lerr.Cause != launcher.CauseIssueHeld || lerr.Status != http.StatusConflict {
		t.Fatalf("second event launch = %+v, want a 409 with Cause %q", lerr, launcher.CauseIssueHeld)
	}
	if lerr.HeldBy != first.Session {
		t.Errorf("HeldBy = %q, want the first ship's session %q", lerr.HeldBy, first.Session)
	}
	if !strings.Contains(lerr.Msg, "this issue already has a ship") || !strings.Contains(lerr.Msg, first.Name) {
		t.Errorf("message = %q, want the usual wording naming %q", lerr.Msg, first.Name)
	}
	if got := len(r.s.List()); got != before {
		t.Errorf("%d ships after the refusal, want %d", got, before)
	}
}

func TestAnEventLaunchOnAnotherIssueIsNotRefused(t *testing.T) {
	t.Parallel()
	r := newWakeRig(t, 0)
	r.mustLaunch(t, eventOn(heldIssue, "r1"))
	r.mustLaunch(t, eventOn("https://github.com/acme/api/issues/43", "r2"))
}
