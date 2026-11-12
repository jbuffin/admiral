package probes

import (
	"context"

	"github.com/jbuffin/admiral/internal/capabilities"
)

// CopilotMCPFence is the probe for bot.mcp_fence on Copilot: a fenced `copilot -p … --output-format json` run in a throwaway COPILOT_HOME.
func CopilotMCPFence() Probe {
	return Probe{Harness: "copilot", Capability: capabilities.BotMCPFence, Run: notBuilt}
}

// CopilotConfigHome is the probe for bot.config_home on Copilot: the login works in a fresh COPILOT_HOME and no plugin or MCP server loads.
func CopilotConfigHome() Probe {
	return Probe{Harness: "copilot", Capability: capabilities.BotConfigHome, Run: notBuilt}
}

func notBuilt(context.Context, Target) Outcome { return Outcome{Detail: "not built yet"} }
