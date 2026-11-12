package launcher

// ChatCopilotHome is a fleet's Copilot chat's own COPILOT_HOME: what Copilot writes, and nothing of the engineer's config.
func ChatCopilotHome(home, fleet string) string {
	_, _ = home, fleet
	return ""
}
