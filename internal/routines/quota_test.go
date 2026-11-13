package routines_test

import (
	"context"
	"strings"
	"testing"

	"github.com/jbuffin/admiral/internal/routines"
)

const quotaWhy = "Held for quota: Claude's 5-hour window is at 93% (line 90%), resets 14:00"

// overTheLine makes the scheduler's quota seam hold every unattended run of the fleet until it is switched off.
func (h *harness) overTheLine() (lift func()) {
	held := true
	h.sched.QuotaHold = func(fleet, harness string) (string, bool) {
		if fleet != "work" || !held {
			return "", false
		}
		return quotaWhy, true
	}
	return func() { held = false }
}

func TestAnActLaunchOverTheQuotaLineIsProposedWithTheQuotaReasonAndStartsNoShip(t *testing.T) {
	h := newHarness(t)
	r := h.act(nil)
	h.overTheLine()
	h.tickAt(at(0, 9, 0))
	h.wantLaunches(0)
	rec := h.latest(r)
	if rec.Outcome != routines.Proposed || !strings.Contains(rec.Reason, "quota") {
		t.Errorf("record = %+v, want proposed with a reason that says quota", rec)
	}
	got := h.proposer.proposals()
	if len(got) != 1 || !strings.Contains(got[0].trigger.Reason, "quota") {
		t.Errorf("proposals = %+v, want one whose trigger says it was held for quota", got)
	}
	if h.stored(r).Before != routines.Act {
		t.Error("the quota hold changed the stored setting")
	}
}

func TestARoutineHeldForQuotaLaunchesOnItsNextRunOnceTheWindowResets(t *testing.T) {
	h := newHarness(t)
	r := h.act(nil)
	lift := h.overTheLine()
	h.tickAt(at(0, 9, 0))
	h.wantLaunches(0)
	lift()
	h.tickAt(at(1, 9, 0))
	h.wantLaunches(1)
	if rec := h.latest(r); rec.Outcome != routines.Launched {
		t.Errorf("record = %+v, want launched with no change in config", rec)
	}
	if !h.ships.requests()[0].Unattended {
		t.Error("a scheduled launch is not marked unattended, so the quota hold cannot see it")
	}
}

func TestAPromptRoutineOverTheQuotaLineIsProposedAndSendsNoTurn(t *testing.T) {
	h := newHarness(t)
	r := h.act(func(r *routines.Routine) { r.Do = routines.Do{Prompt: "check the board"} })
	h.overTheLine()
	h.tickAt(at(0, 9, 0))
	if len(h.chat.sent) != 0 {
		t.Errorf("chat turns = %q, want none while held", h.chat.sent)
	}
	if rec := h.latest(r); rec.Outcome != routines.Proposed || len(h.proposer.proposals()) != 1 {
		t.Errorf("record = %+v with %d proposals, want one proposal", rec, len(h.proposer.proposals()))
	}
}

func TestAReportOnlyRoutineIsNotHeldForQuota(t *testing.T) {
	h := newHarness(t)
	r := h.act(func(r *routines.Routine) { r.Before = routines.ReportOnly })
	h.overTheLine()
	h.tickAt(at(0, 9, 0))
	if rec := h.latest(r); rec.Outcome != routines.Reported || len(h.proposer.proposals()) != 0 {
		t.Errorf("record = %+v with %d proposals, want reported and no proposal", rec, len(h.proposer.proposals()))
	}
}

func TestAcceptingAProposalHeldForQuotaLaunchesAndIsNotMarkedUnattended(t *testing.T) {
	h := newHarness(t)
	r := h.act(nil)
	h.overTheLine()
	h.tickAt(at(0, 9, 0))
	got := h.proposer.proposals()
	if len(got) != 1 {
		t.Fatalf("%d proposals, want 1 to accept", len(got))
	}
	trig := got[0].trigger
	h.clock.set(at(0, 9, 30))
	if _, err := h.sched.Accept(context.Background(), "work", r.ID, trig.ID); err != nil {
		t.Fatalf("Accept over the quota line: %v", err)
	}
	h.wantLaunches(1)
	req := h.ships.requests()[0]
	if req.Unattended || req.By == "proposal" {
		t.Errorf("request = %+v, want an accepted proposal not marked unattended, and with no By so it still counts toward the cap", req)
	}
}
