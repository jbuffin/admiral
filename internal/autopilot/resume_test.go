package autopilot

import (
	"testing"

	"github.com/jbuffin/admiral/internal/config"
	"github.com/jbuffin/admiral/internal/ships"
	"github.com/jbuffin/admiral/internal/sources"
)

func TestPassHoldsUnrecordedOrdersWhileAResumeIsBeingAdded(t *testing.T) {
	r := newRig(t, "o1")
	done := r.engine.BeginResume("s", "p", "/w/o1", "", "")
	want(t, "while the add is in flight", r.pass(), nil)
	if n := len(r.recordsOf("skipped")); n != 0 {
		t.Errorf("skipped records = %d, want none while held", n)
	}
	done("") // the add gave no id: the order is an ordinary one
	want(t, "after the hold", r.pass(), []string{"s/p/o1"})
}

func TestResumeRecordLaunchesInItsDirAndSurvivesUntilTheOrderLeavesTheListing(t *testing.T) {
	r := newRig(t, "o1")
	checkout := func(_ config.Fleet, dir string) bool { return dir == "/w/ship" || dir == "/w/x" }
	r.engine.Checkout = checkout
	r.engine.BeginResume("s", "p", "/w/ship", "", "")("o1")
	wantDir := func(what, dir string) {
		t.Helper()
		if n := len(r.launches); n == 0 || r.launches[n-1].Dir != dir {
			t.Errorf("%s: launches %+v, want the last in %s", what, r.launches, dir)
		}
	}

	// The record outlives a restart.
	r.engine = r.newEngine()
	r.engine.Checkout = checkout
	want(t, "after a restart", r.pass(), []string{"s/p/o1"})
	wantDir("after a restart", "/w/ship")

	// It is dropped once the order has been listed and is not any more: back
	// in the listing, the order is an ordinary one.
	r.setShips()
	r.src[0].Panels[0].Orders = nil
	r.pass()
	r.src[0].Panels = []sources.Panel{panel("p", "o1")}
	want(t, "the order back", r.pass(), []string{"s/p/o1"})
	wantDir("the order back", "/w/o1")

	// A listing taken before the add's refresh must not drop the record.
	r.engine.BeginResume("s", "p", "/w/x", "", "")("o2")
	r.pass()
	r.src[0].Panels = []sources.Panel{panel("p", "o1", "o2")}
	want(t, "o2 listed", r.pass(), []string{"s/p/o2"})
	wantDir("o2 listed", "/w/x")
}

func TestPassTreatsAnOrderRecordedDuringThePassAsAResumeOrder(t *testing.T) {
	r := newRig(t, "o1")
	r.dirs = map[string]string{} // an ordinary order would be skipped: no repository in its text
	r.engine.Checkout = func(_ config.Fleet, dir string) bool { return dir == "/w/ship" }
	done := r.engine.BeginResume("s", "p", "/w/ship", "", "")
	list := r.engine.Ships
	r.engine.Ships = func() []ships.Ship { done("o1"); return list() } // the add ends as the pass begins
	want(t, "a pass beside the end of the add", r.pass(), []string{"s/p/o1"})
	if n := len(r.recordsOf("skipped")); n != 0 {
		t.Errorf("skipped records = %d, want none", n)
	}
}
