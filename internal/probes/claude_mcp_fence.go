package probes

import (
	"context"

	"github.com/jbuffin/admiral/internal/capabilities"
)

// ClaudeMCPFence is the probe for bot.mcp_fence on Claude Code.
func ClaudeMCPFence() Probe {
	return Probe{Harness: "claude", Capability: capabilities.BotMCPFence, Run: func(context.Context, Target) Outcome { return Outcome{} }}
}
