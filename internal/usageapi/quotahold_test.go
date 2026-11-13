package usageapi_test

import (
	"testing"
	"time"

	"github.com/jbuffin/admiral/internal/config"
	"github.com/jbuffin/admiral/internal/standing"
	"github.com/jbuffin/admiral/internal/usage"
)

// heldFleet is fleet f at the Work preset's line of 90 with one dock of the harness, whose login reads q.
func heldFleet(t *testing.T, harness string, q usage.Quota) *rig {
	t.Helper()
	dir := t.TempDir()
	f := fleetOf("f", dock(harness, dir))
	f.Limits = standing.PresetLimits(standing.Work)
	r := newRig(t, f)
	r.store.SetQuota(config.IdentityDir(harness, f.Harnesses[0].Env), q, now.Add(-time.Minute))
	return r
}

// held is the fleet's quota_hold object, or nil when the field is absent or says not held.
func held(t *testing.T, f map[string]any) map[string]any {
	t.Helper()
	h, ok := f["quota_hold"].(map[string]any)
	if !ok || h["held"] != true {
		return nil
	}
	return h
}

func TestUsageSaysUnattendedLaunchesAreHeldAndWhichWindowAndUntilWhen(t *testing.T) {
	reset := now.Add(90 * time.Minute)
	r := heldFleet(t, "claude", usage.Quota{
		Primary:   &usage.Window{UsedPercent: 40, WindowMinutes: 300, ResetsAt: reset.Unix()},
		Secondary: &usage.Window{UsedPercent: 93, WindowMinutes: 10080, ResetsAt: reset.Add(48 * time.Hour).Unix()},
	})
	h := held(t, r.fleetUsage("f"))
	if h == nil {
		t.Fatalf("quota_hold = %v, want held", r.fleetUsage("f")["quota_hold"])
	}
	want := reset.Add(48 * time.Hour).UTC().Format(time.RFC3339)
	if h["harness"] != "claude" || h["window"] != "secondary" || h["used_percent"] != 93.0 || h["line_percent"] != 90.0 || h["resets_at"] != want {
		t.Errorf("quota_hold = %v, want claude's secondary window at 93%% against line 90 resetting %s", h, want)
	}
}

func TestUsageHoldsNothingWhileEveryWindowIsUnderTheLine(t *testing.T) {
	r := heldFleet(t, "claude", usage.Quota{Primary: &usage.Window{UsedPercent: 89, ResetsAt: now.Add(time.Hour).Unix()}})
	if h := held(t, r.fleetUsage("f")); h != nil {
		t.Errorf("quota_hold = %v, want none under the line", h)
	}
}

func TestUsageHoldLiftsOnceTheWindowHasReset(t *testing.T) {
	r := heldFleet(t, "claude", usage.Quota{Primary: &usage.Window{UsedPercent: 95, ResetsAt: now.Add(-time.Minute).Unix()}})
	if h := held(t, r.fleetUsage("f")); h != nil {
		t.Errorf("quota_hold = %v, want none after the window's reset", h)
	}
}

func TestUsageSpendOverTheLineIsNotAHold(t *testing.T) {
	r := heldFleet(t, "claude", usage.Quota{
		Primary: &usage.Window{UsedPercent: 10, ResetsAt: now.Add(time.Hour).Unix()},
		Spend:   &usage.SpendLimit{UsedPercent: 100, ResetsAt: now.Add(24 * time.Hour).Unix(), UsedUSD: 20, LimitUSD: 20},
	})
	if h := held(t, r.fleetUsage("f")); h != nil {
		t.Errorf("quota_hold = %v, want none: spend has its own Limit", h)
	}
}

func TestUsageNeverHoldsACopilotFleet(t *testing.T) {
	r := heldFleet(t, "copilot", usage.Quota{PlanType: "copilot"})
	if h := held(t, r.fleetUsage("f")); h != nil {
		t.Errorf("quota_hold = %v, want none for a harness with no window figure", h)
	}
}
