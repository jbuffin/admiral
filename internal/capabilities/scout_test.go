package capabilities

import (
	"slices"
	"testing"
)

func TestScoutGuardHookIsAStableCapabilityInTheScoutFeature(t *testing.T) {
	if string(ScoutGuard) != "scout.guard_hook" {
		t.Errorf("ScoutGuard = %q, want scout.guard_hook", ScoutGuard)
	}
	for _, f := range Table() {
		if f.ID != "scout" {
			continue
		}
		if f.Label != "Scout" {
			t.Errorf("feature label = %q, want Scout", f.Label)
		}
		if len(f.Capabilities) != 1 || f.Capabilities[0].ID != ScoutGuard || f.Capabilities[0].Label != "Scout guard hook" {
			t.Errorf("capabilities = %+v, want one row, Scout guard hook", f.Capabilities)
		}
		return
	}
	t.Error("the table has no scout feature")
}

func TestScoutGuardNeedsTheSameClaudeVersionAsTheAutopilotGuard(t *testing.T) {
	auto, _ := Claims("claude", AutopilotGuard)
	got, ok := Claims("claude", ScoutGuard)
	if !ok || got.From != "2.1.295" || got.From != auto.From {
		t.Errorf("claude claims scout.guard_hook from %q (ok %v), want %q, the autopilot guard's", got.From, ok, auto.From)
	}
	if ClaimedBy("claude", ScoutGuard, "2.1.294") || !ClaimedBy("claude", ScoutGuard, "2.1.295") {
		t.Error("claude's claim does not start at 2.1.295")
	}
	for _, h := range []string{"codex", "copilot"} {
		if _, ok := Claims(h, ScoutGuard); !ok {
			t.Errorf("%s makes no claim for scout.guard_hook", h)
		}
	}
}

func TestProbeAndJudgeRunsNeverInheritTheScoutCheckout(t *testing.T) {
	if !slices.Contains(ScrubbedEnv, "ADMIRAL_SCOUT") {
		t.Error("ScrubbedEnv lacks ADMIRAL_SCOUT: a probe or judge run would inherit a scout's guard mode")
	}
}
