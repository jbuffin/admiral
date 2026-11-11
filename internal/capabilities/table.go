package capabilities

import (
	"strconv"
	"strings"
)

var table = []Feature{
	{ID: "judge", Label: "Judge calls", Capabilities: []Capability{
		{JudgeNoTools, "No tools"},
		{JudgeNoNetwork, "No network"},
		{JudgeJSONSchema, "JSON schema output"},
		{JudgeCallCap, "Per-call cap"},
		{JudgeUsage, "Usage reports"},
	}},
	{ID: "bot", Label: "AdmiralBot", Capabilities: []Capability{
		{BotMCPFence, "MCP-only fence"},
		{BotConfigHome, "Separate config home"},
	}},
	{ID: "restricted", Label: "Restricted ship", Capabilities: []Capability{
		{RestrictedSandbox, "Sandbox that fails closed"},
		{RestrictedAllow, "Network allowlist"},
	}},
	{ID: "autopilot", Label: "Autopilot", Capabilities: []Capability{
		{AutopilotGuard, "Pre-tool guard hook"},
		{AutopilotArgs, "Args allowlist"},
		{AutopilotAdvisor, "Advisor"},
	}},
}

var harnesses = []string{"claude", "codex", "copilot"}

// minimums holds the only sourced minimum versions; every other claim holds for all versions.
var minimums = map[string]map[ID]string{
	// onFailure "block" in the hook settings needs it (internal/launcher/bundle.go).
	"claude": {AutopilotGuard: "2.1.295"},
}

// Table returns the built-in table, grouped by feature.
func Table() []Feature {
	out := make([]Feature, len(table))
	for i, f := range table {
		f.Capabilities = append([]Capability(nil), f.Capabilities...)
		out[i] = f
	}
	return out
}

// Claims reports the claim a harness makes for a capability, and false when it makes none.
func Claims(harness string, id ID) (Claim, bool) {
	known := false
	for _, h := range harnesses {
		known = known || h == harness
	}
	if !known {
		return Claim{}, false
	}
	for _, f := range table {
		for _, c := range f.Capabilities {
			if c.ID == id {
				return Claim{From: minimums[harness][id]}, true
			}
		}
	}
	return Claim{}, false
}

// ClaimedBy reports whether a harness at the given version claims the capability.
// It reads the first X.Y.Z token of the version, which may carry trailing text ("2.1.295 (Claude Code)").
func ClaimedBy(harness string, id ID, version string) bool {
	cl, ok := Claims(harness, id)
	if !ok {
		return false
	}
	if cl.From == "" {
		return true
	}
	have, ok := parseVersion(version)
	if !ok {
		return false
	}
	min, _ := parseVersion(cl.From)
	for i := range have {
		if have[i] != min[i] {
			return have[i] > min[i]
		}
	}
	return true
}

// parseVersion reads the leading major.minor.patch of s.
func parseVersion(s string) ([3]int, bool) {
	var v [3]int
	fields := strings.Fields(s)
	if len(fields) == 0 {
		return v, false
	}
	parts := strings.Split(strings.TrimPrefix(fields[0], "v"), ".")
	if len(parts) != 3 {
		return v, false
	}
	for i, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil || n < 0 {
			return v, false
		}
		v[i] = n
	}
	return v, true
}
