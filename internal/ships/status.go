package ships

import (
	"strings"
	"time"

	"github.com/jbuffin/admiral/internal/sessionhost"
)

// A ship's status on the wire. Anchored here means anchored and staying (the
// glossary's Anchored also covers the exit-0 ship that is leaving).
const (
	StatusUnderway   = "underway"
	StatusNeedsYou   = "needs_you"
	StatusAnchored   = "anchored"
	StatusSailingOff = "sailing_off"
)

const (
	// NoHookWait is how long a launched ship may go without any hook before it
	// counts as needing you (ADR 0007).
	NoHookWait = 10 * time.Second
	// SailOffDelay is how long an exit-0 ship shows sailing_off before its
	// session is killed.
	SailOffDelay = 3 * time.Second
)

// exitedCleanly is true for a ship whose process ended with status 0.
func exitedCleanly(s sessionhost.Session) bool {
	return s.PaneDead && s.Signal == "" && s.ExitStatus == 0
}

// status reads a ship's status from its session. Pane death beats everything;
// otherwise the hooks' @admiral-state decides, and with none yet the ship needs
// you once NoHookWait has passed since it launched.
func status(s sessionhost.Session, now time.Time) string {
	switch {
	case exitedCleanly(s):
		return StatusSailingOff
	case s.PaneDead:
		return StatusAnchored
	case s.State == "needs_you":
		return StatusNeedsYou
	case s.State != "":
		return StatusUnderway
	}
	if t, err := time.Parse(time.RFC3339, s.Options["launched"]); err == nil && now.Sub(t) >= NoHookWait {
		return StatusNeedsYou
	}
	return StatusUnderway
}

// exit is what an anchored ship carries: its exit code, or the signal that
// killed it (then there is no code).
func exit(s sessionhost.Session) (code *int, sig *string) {
	if !s.PaneDead || exitedCleanly(s) {
		return nil, nil
	}
	if s.Signal != "" {
		n := strings.TrimPrefix(strings.ToUpper(s.Signal), "SIG")
		return nil, &n
	}
	c := s.ExitStatus
	return &c, nil
}

// OrderKey names an order by its source and id.
type OrderKey struct{ Source, OrderID string }

// HiddenOrders is the orders hidden by ships: one pair for every ship in the
// list, whatever its status, that has both a source and an order id (ADR 0012).
func HiddenOrders(list []Ship) map[OrderKey]bool {
	out := map[OrderKey]bool{}
	for _, sh := range list {
		if sh.Source != "" && sh.OrderID != "" {
			out[OrderKey{sh.Source, sh.OrderID}] = true
		}
	}
	return out
}
