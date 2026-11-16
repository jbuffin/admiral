package routines_test

import (
	"testing"

	"github.com/jbuffin/admiral/internal/routines"
	"github.com/jbuffin/admiral/internal/standing"
)

func inCheckout(dir string) func(*routines.Routine) {
	return func(r *routines.Routine) { r.Do.Launch.Dir = dir }
}

func scopeHarness(t *testing.T) *harness {
	h := newHarness(t)
	h.withEvents(standing.PresetLimits(standing.Home))
	return h
}

func TestAVerifiedEventOnTheCheckoutsRepoLaunches(t *testing.T) {
	h := scopeHarness(t)
	r := h.onRoutine(routines.Act, inCheckout(checkoutWith(t, [2]string{"origin", "https://github.com/acme/api.git"})))
	h.sched.HandleEvent(evFrom("Eng"))
	if n := len(h.records(r)); n != 1 {
		t.Fatalf("%d records, want 1", n)
	}
	h.wantLaunches(1)
}

func TestAVerifiedEventOnAnotherRepoOfTheSameOwnerDoesNothing(t *testing.T) {
	h := scopeHarness(t)
	r := h.onRoutine(routines.Act, inCheckout(checkoutWith(t, [2]string{"origin", "https://github.com/acme/web.git"})))
	h.sched.HandleEvent(evFrom("Eng"))
	if recs := h.records(r); len(recs) != 0 {
		t.Fatalf("records = %+v, want none: the event is on acme/api, the checkout is acme/web", recs)
	}
	h.wantLaunches(0)
	if n := len(h.proposer.proposals()); n != 0 {
		t.Errorf("%d proposals, want 0", n)
	}
}

func TestACheckoutCloneThroughAnSSHAliasMatchesTheEventsRepo(t *testing.T) {
	h := scopeHarness(t)
	r := h.onRoutine(routines.Act, inCheckout(checkoutWith(t, [2]string{"origin", "git@github-work:acme/api.git"})))
	h.sched.HandleEvent(evFrom("Eng"))
	if n := len(h.records(r)); n != 1 {
		t.Fatalf("%d records, want 1: the host is ignored", n)
	}
}

func TestAForkCheckoutWhoseUpstreamIsTheEventsRepoMatches(t *testing.T) {
	h := scopeHarness(t)
	r := h.onRoutine(routines.Act, inCheckout(checkoutWith(t,
		[2]string{"origin", "https://github.com/me/api.git"}, [2]string{"upstream", "https://github.com/acme/api.git"})))
	h.sched.HandleEvent(evFrom("Eng"))
	if n := len(h.records(r)); n != 1 {
		t.Fatalf("%d records, want 1: any remote counts", n)
	}
}

func TestTheRepoComparisonIgnoresCase(t *testing.T) {
	h := scopeHarness(t)
	r := h.onRoutine(routines.Act, inCheckout(checkoutWith(t, [2]string{"origin", "https://github.com/Acme/API.git"})))
	h.sched.HandleEvent(evFrom("Eng"))
	if n := len(h.records(r)); n != 1 {
		t.Fatalf("%d records, want 1", n)
	}
	r2 := h.onRoutine(routines.Act, inCheckout(sharedCheckout))
	ev := evFrom("Eng")
	ev.Ref = "https://github.com/ACME/Api/pull/9"
	h.sched.HandleEvent(ev)
	if n := len(h.records(r2)); n != 1 {
		t.Fatalf("shared checkout routine has %d records, want 1", n)
	}
}

func TestWithTwoRoutinesOnDifferentCheckoutsOnlyTheInScopeOneActs(t *testing.T) {
	h := scopeHarness(t)
	in := h.onRoutine(routines.AskFirst, inCheckout(sharedCheckout))
	out := h.onRoutine(routines.AskFirst, inCheckout(checkoutWith(t, [2]string{"origin", "https://github.com/acme/web.git"})))
	h.sched.HandleEvent(evFrom("Eng"))
	if n := len(h.records(in)); n != 1 {
		t.Errorf("the in-scope routine has %d records, want 1", n)
	}
	if n := len(h.records(out)); n != 0 {
		t.Errorf("the out-of-scope routine has %d records, want 0", n)
	}
}

func TestACheckoutWithNoRemoteNeverFires(t *testing.T) {
	h := scopeHarness(t)
	r := h.onRoutine(routines.Act, inCheckout(checkoutWith(t)))
	h.sched.HandleEvent(evFrom("Eng"))
	if n := len(h.records(r)); n != 0 {
		t.Fatalf("%d records, want 0", n)
	}
	h.wantLaunches(0)
}

func TestAPluginEventIsNotScopedWhateverItsRefLooksLike(t *testing.T) {
	h := scopeHarness(t)
	r := h.onRoutine(routines.Act, inCheckout(sharedCheckout))
	for _, ref := range []string{"https://github.com/other/thing/pull/1", "https://linear.app/acme/issue/ENG-4"} {
		ev := evFrom("Eng")
		ev.Verified, ev.Ref = false, ref
		h.sched.HandleEvent(ev)
	}
	if n := len(h.records(r)); n != 2 {
		t.Fatalf("%d records, want 2: plugin events match as before", n)
	}
}
