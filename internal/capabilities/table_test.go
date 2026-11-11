package capabilities

import (
	"slices"
	"testing"
)

func TestTableGroupsExactlyTheADRCapabilities(t *testing.T) {
	want := map[string][]ID{
		"judge":      {JudgeNoTools, JudgeNoNetwork, JudgeJSONSchema, JudgeCallCap, JudgeUsage},
		"bot":        {BotMCPFence, BotConfigHome},
		"restricted": {RestrictedSandbox, RestrictedAllow},
		"autopilot":  {AutopilotGuard, AutopilotArgs, AutopilotAdvisor},
	}
	got := map[string][]ID{}
	seen := map[ID]bool{}
	for _, f := range Table() {
		if f.Label == "" {
			t.Errorf("feature %q has no label", f.ID)
		}
		for _, c := range f.Capabilities {
			if c.Label == "" {
				t.Errorf("capability %q has no label", c.ID)
			}
			if seen[c.ID] {
				t.Errorf("capability %q listed twice", c.ID)
			}
			seen[c.ID] = true
			got[f.ID] = append(got[f.ID], c.ID)
		}
	}
	if len(got) != len(want) {
		t.Fatalf("features = %v, want %v", got, want)
	}
	for feature, ids := range want {
		if !slices.Equal(got[feature], ids) {
			t.Errorf("feature %s = %v, want %v", feature, got[feature], ids)
		}
	}
}

func TestCapabilityIDsAreStable(t *testing.T) {
	for id, want := range map[ID]string{
		JudgeNoTools:      "judge.no_tools",
		JudgeNoNetwork:    "judge.no_network",
		JudgeJSONSchema:   "judge.json_schema",
		JudgeCallCap:      "judge.call_cap",
		JudgeUsage:        "judge.usage_reports",
		BotMCPFence:       "bot.mcp_fence",
		BotConfigHome:     "bot.config_home",
		RestrictedSandbox: "restricted.sandbox_fails_closed",
		RestrictedAllow:   "restricted.network_allowlist",
		AutopilotGuard:    "autopilot.guard_hook",
		AutopilotArgs:     "autopilot.args_allowlist",
		AutopilotAdvisor:  "autopilot.advisor",
	} {
		if string(id) != want {
			t.Errorf("ID %q, want %q", id, want)
		}
	}
	if string(Passed) != "passed" || string(Failed) != "failed" || string(NotChecked) != "not_checked" {
		t.Errorf("statuses = %q %q %q", Passed, Failed, NotChecked)
	}
}

func TestEveryHarnessClaimsEveryCapability(t *testing.T) {
	if len(Table()) == 0 {
		t.Fatal("the table is empty")
	}
	for _, h := range []string{"claude", "codex", "copilot"} {
		for _, f := range Table() {
			for _, c := range f.Capabilities {
				if _, ok := Claims(h, c.ID); !ok {
					t.Errorf("%s makes no claim for %s", h, c.ID)
				}
			}
		}
	}
	if _, ok := Claims("nope", AutopilotGuard); ok {
		t.Error("an unknown harness claims a capability")
	}
}

func TestClaudeGuardHookNeedsVersion2_1_295(t *testing.T) {
	cl, ok := Claims("claude", AutopilotGuard)
	if !ok || cl.From != "2.1.295" {
		t.Fatalf("claim = %+v, %v; want From 2.1.295", cl, ok)
	}
	for v, want := range map[string]bool{
		"2.1.294":               false,
		"2.1.295":               true,
		"2.1.295 (Claude Code)": true,
		"2.1.300":               true,
		"2.2.0":                 true,
		"2.1.1000":              true,
		"2.0.999":               false,
		"1.9.9999":              false,
		"":                      false,
		"not a version":         false,
	} {
		if got := ClaimedBy("claude", AutopilotGuard, v); got != want {
			t.Errorf("ClaimedBy(claude, guard, %q) = %v, want %v", v, got, want)
		}
	}
}

func TestOtherClaimsHoldForAllVersions(t *testing.T) {
	for _, h := range []string{"codex", "copilot"} {
		if cl, _ := Claims(h, AutopilotGuard); cl.From != "" {
			t.Errorf("%s guard claim From = %q, want all versions", h, cl.From)
		}
	}
	if cl, _ := Claims("claude", JudgeNoTools); cl.From != "" {
		t.Errorf("claude judge.no_tools From = %q, want all versions", cl.From)
	}
	if !ClaimedBy("codex", AutopilotGuard, "0.1.0") {
		t.Error("an all-versions claim must hold for any version")
	}
}
