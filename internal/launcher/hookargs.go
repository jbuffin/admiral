package launcher

// RefuseHookArgs is a stub the red tests compile against; the implementation
// replaces it. It returns an error naming the first --arg value that overrides
// or disables Admiral's hooks for the harness, or nil.
func RefuseHookArgs(harness string, args []string) error { return nil }
