package autopilot

import (
	"testing"

	"github.com/jbuffin/admiral/internal/config"
)

// Resume orders (#120): the in-flight hold on a panel's unrecorded orders, and
// the record's life. The launch itself is covered end to end.

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
	r.engine.Checkout = func(_ config.Fleet, dir string) bool { return dir == "/w/ship" }
	r.engine.BeginResume("s", "p", "/w/ship", "", "")("o1")
	want(t, "a recorded dir that is a checkout", r.pass(), []string{"s/p/o1"})
	if got := r.launches[0].Dir; got != "/w/ship" {
		t.Errorf("dir = %q, want the recorded one", got)
	}

	// The record outlives a restart, and is dropped only once the order has been
	// listed and is not any more.
	r.engine = r.newEngine()
	if _, ok := r.engine.Store.resumeOf("s", "o1"); !ok {
		t.Fatal("the record did not survive a restart")
	}
	r.src[0].Panels[0].Orders = nil
	r.pass()
	if _, ok := r.engine.Store.resumeOf("s", "o1"); ok {
		t.Error("the record stayed after its order left the listing")
	}

	// An order that was never listed does not lose its record to a listing taken
	// before the add's refresh.
	r.engine.BeginResume("s", "p", "/w/x", "", "")("o2")
	r.pass()
	if _, ok := r.engine.Store.resumeOf("s", "o2"); !ok {
		t.Error("the record of an order not listed yet was pruned")
	}
}
