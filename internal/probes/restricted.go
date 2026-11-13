package probes

import (
	"context"

	"github.com/jbuffin/admiral/internal/capabilities"
)

func notBuilt(context.Context, Target) Outcome { return Outcome{Detail: "not implemented"} }

// ClaudeRestrictedSandbox is the probe for restricted.sandbox_fails_closed on Claude Code.
func ClaudeRestrictedSandbox() Probe {
	return Probe{Harness: "claude", Capability: capabilities.RestrictedSandbox, Run: notBuilt}
}

// ClaudeRestrictedNetwork is the probe for restricted.network_allowlist on Claude Code.
func ClaudeRestrictedNetwork() Probe {
	return Probe{Harness: "claude", Capability: capabilities.RestrictedAllow, Run: notBuilt}
}

// RestrictedHarnesses lists the harnesses with a registered probe for both restricted capabilities: a stored pass for any other proves nothing.
func RestrictedHarnesses() []string {
	var out []string
	for _, h := range []string{"claude", "codex", "copilot"} {
		sandbox, network := false, false
		for _, p := range defaultProbes() {
			if p.Harness != h {
				continue
			}
			sandbox = sandbox || p.Capability == capabilities.RestrictedSandbox
			network = network || p.Capability == capabilities.RestrictedAllow
		}
		if sandbox && network {
			out = append(out, h)
		}
	}
	return out
}
