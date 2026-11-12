package capabilities

import "github.com/jbuffin/admiral/internal/config"

// ChatProbe reads a capability's status from the store for the AdmiralBot gate in config.
func ChatProbe(s *Store) config.ChatProbe {
	return func(fleet, harness, capability string) (string, string) {
		r, ok, err := s.Result(fleet, harness, ID(capability))
		switch {
		case err != nil || !ok:
			return string(NotChecked), ""
		case r.Passed:
			return string(Passed), ""
		}
		return string(Failed), r.Detail
	}
}
