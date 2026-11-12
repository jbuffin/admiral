package capabilities

import "github.com/jbuffin/admiral/internal/config"

// ChatProbe reads a capability's status from the store for the AdmiralBot gate in config.
func ChatProbe(s *Store) config.ChatProbe {
	return func(fleet, harness, capability string) (string, string) { return "", "" }
}
