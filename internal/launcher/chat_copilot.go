package launcher

// ChatCopilotHome is a fleet's Copilot chat's own COPILOT_HOME: what Copilot writes, and nothing of the engineer's config.
func ChatCopilotHome(home, fleet string) string {
	_, _ = home, fleet
	return ""
}

// CopilotFenceArgs is the fence flags for a run whose only MCP server is `server`: the chat's launch and the probes both take them.
func CopilotFenceArgs(server string) []string {
	_ = server
	return nil
}
