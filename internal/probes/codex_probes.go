package probes

import (
	"context"

	"github.com/jbuffin/admiral/internal/capabilities"
)

// CodexMCPFence is the probe for bot.mcp_fence on Codex: a fenced `codex exec --json` run in a throwaway CODEX_HOME.
func CodexMCPFence() Probe {
	return Probe{Harness: "codex", Capability: capabilities.BotMCPFence, Run: func(context.Context, Target) Outcome { return Outcome{} }}
}

// CodexConfigHome is the probe for bot.config_home on Codex: the login works in a home holding only the auth.json link, and no plugin or other MCP server loads.
func CodexConfigHome() Probe {
	return Probe{Harness: "codex", Capability: capabilities.BotConfigHome, Run: func(context.Context, Target) Outcome { return Outcome{} }}
}
