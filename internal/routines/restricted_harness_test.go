package routines_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jbuffin/admiral/internal/capabilities"
	"github.com/jbuffin/admiral/internal/routines"
	"github.com/jbuffin/admiral/internal/standing"
)

type decisionActs struct{ got []routines.Decision }

func (a *decisionActs) Act(_ string, _ routines.Routine, _ routines.Event, _ routines.Trigger, d routines.Decision, _ routines.Enrichment) {
	a.got = append(a.got, d)
}

// a3Limits puts the engineer's own tier at A3 so the restricted-harness rule alone decides what Computed is.
func a3Limits() standing.Limits {
	l := standing.PresetLimits(standing.Home)
	l.Ceilings.T1 = standing.Ceiling{Level: standing.A3, Restricted: standing.DraftFix}
	return l
}

func (h *harness) proveRestricted(version string, sandbox, network bool) {
	h.t.Helper()
	s := capabilities.Open(h.home)
	if err := s.RecordVersion("work", "claude", version, t0); err != nil {
		h.t.Fatal(err)
	}
	for id, pass := range map[capabilities.ID]bool{capabilities.RestrictedSandbox: sandbox, capabilities.RestrictedAllow: network,
		capabilities.AutopilotGuard: true, capabilities.JudgeNoTools: true, capabilities.JudgeNoNetwork: true} {
		if err := s.Record("work", "claude", version, id, capabilities.Result{Passed: pass, At: t0}); err != nil {
			h.t.Fatal(err)
		}
	}
}

// actRoutine saves the Act event routine while the rig's guard is still proven, since a store that proves less refuses to save one.
func (h *harness) actRoutine() routines.Routine { return h.onRoutine(routines.Act, nil) }

// decide sends one event from the engineer through r and returns the decision and the record's reason.
func (h *harness) decide(r routines.Routine) (routines.Decision, string) {
	h.t.Helper()
	acts := &decisionActs{}
	h.sched.Acts = acts
	h.sched.HandleEvent(evFrom("Eng"))
	if len(acts.got) != 1 {
		h.t.Fatalf("%d decisions reached the acts, want 1", len(acts.got))
	}
	return acts.got[0], h.latest(r).Reason
}

func (h *harness) wantComputed(d routines.Decision, reason string, want standing.Level, wantReason bool) {
	h.t.Helper()
	if d.Computed != want {
		h.t.Errorf("Computed = A%d, want A%d (reasons %q)", d.Computed, want, reason)
	}
	if got := strings.Contains(reason, "no_restricted_harness"); got != wantReason {
		h.t.Errorf("reason = %q, no_restricted_harness present = %v, want %v", reason, got, wantReason)
	}
}

func TestAnA3TierDropsToA1WhenNoHarnessHasProvenRestrictedShips(t *testing.T) {
	h := newHarness(t)
	h.withEvents(a3Limits())
	r := h.actRoutine()
	d, reason := h.decide(r)
	h.wantComputed(d, reason, standing.A1, true)
}

func TestAnA3TierDropsToA1WithOnlyOneRestrictedCapabilityProven(t *testing.T) {
	for name, c := range map[string][2]bool{"sandbox only": {true, false}, "network only": {false, true}} {
		t.Run(name, func(t *testing.T) {
			h := newHarness(t)
			h.withEvents(a3Limits())
			r := h.actRoutine()
			h.proveRestricted("2.1.300", c[0], c[1])
			d, reason := h.decide(r)
			h.wantComputed(d, reason, standing.A1, true)
		})
	}
}

func TestAnA3TierStaysA3WithBothRestrictedCapabilitiesAndTheGuardProvenAtTheDetectedVersion(t *testing.T) {
	h := newHarness(t)
	h.withEvents(a3Limits())
	r := h.actRoutine()
	h.proveRestricted("2.1.300", true, true)
	d, reason := h.decide(r)
	h.wantComputed(d, reason, standing.A3, false)
}

func TestAnA3TierDropsToA1WhenTheGuardIsNotProven(t *testing.T) {
	h := newHarness(t)
	h.withEvents(a3Limits())
	r := h.actRoutine()
	h.proveRestricted("2.1.300", true, true)
	if err := capabilities.Open(h.home).Record("work", "claude", "2.1.300", capabilities.AutopilotGuard, capabilities.Result{Passed: false, At: t0}); err != nil {
		t.Fatal(err)
	}
	d, reason := h.decide(r)
	h.wantComputed(d, reason, standing.A1, true)
}

func TestAnA3TierDropsToA1AfterAVersionChangeAndRisesAgainWhenReproven(t *testing.T) {
	h := newHarness(t)
	h.withEvents(a3Limits())
	r := h.actRoutine()
	h.proveRestricted("2.1.300", true, true)
	if err := capabilities.Open(h.home).RecordVersion("work", "claude", "2.1.400", t0); err != nil {
		t.Fatal(err)
	}
	d, reason := h.decide(r)
	h.wantComputed(d, reason, standing.A1, true)

	h.proveRestricted("2.1.400", true, true)
	d, reason = h.decide(r)
	h.wantComputed(d, reason, standing.A3, false)
}

func TestAMissingOrCorruptCapabilityStoreIsNotACapableHarness(t *testing.T) {
	h := newHarness(t)
	h.withEvents(a3Limits())
	r := h.actRoutine()
	h.proveRestricted("2.1.300", true, true)
	if err := os.WriteFile(filepath.Join(h.home, capabilities.FileName), []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	d, reason := h.decide(r)
	h.wantComputed(d, reason, standing.A1, true)
}

func TestAFleetWithChecksDropsToA1EvenWithAProvenRestrictedHarness(t *testing.T) {
	h := newHarness(t)
	h.withEvents(a3Limits())
	r := h.actRoutine()
	h.proveRestricted("2.1.300", true, true)
	checks := []string{"Always run make check before a PR."}
	if _, err := h.store.SetStandingOrders("work", routines.StandingOrdersPatch{Checks: &checks}); err != nil {
		t.Fatal(err)
	}
	d, _ := h.decide(r)
	if d.Computed != standing.A1 {
		t.Errorf("Computed = A%d, want A1: the step reviewer counts as ask in a fleet with Checks", d.Computed)
	}
}

func TestAFleetWithoutChecksKeepsA3OnlyOnceItHasACapableHarness(t *testing.T) {
	h := newHarness(t)
	h.withEvents(a3Limits())
	r := h.actRoutine()
	empty := []string{}
	if _, err := h.store.SetStandingOrders("work", routines.StandingOrdersPatch{Checks: &empty}); err != nil {
		t.Fatal(err)
	}
	h.proveRestricted("2.1.300", true, true)
	if d, _ := h.decide(r); d.Computed != standing.A3 {
		t.Errorf("Computed = A%d, want A3 for a fleet with no Checks and a capable harness", d.Computed)
	}
}
