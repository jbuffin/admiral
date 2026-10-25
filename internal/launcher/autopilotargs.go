package launcher

// AutopilotArgs vets the values of `admiral crew launch --arg` against the
// per-harness allowlist for a caller with ADMIRAL_AUTOPILOT=1 (#175). dockArgs
// are the dock's own args for the harness; a --permission-mode may be no looser
// than the dock's. The error names the first value that doesn't fit.
func AutopilotArgs(harness string, dockArgs []string, args []string) error {
	return nil // stub: the implementer replaces this body
}
