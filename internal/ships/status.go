package ships

import (
	"strings"
	"time"

	"github.com/jbuffin/admiral/internal/delay"
	"github.com/jbuffin/admiral/internal/sessionhost"
)

// Anchored here means anchored and staying; the glossary's Anchored also covers
// the exit-0 ship that is leaving.
const (
	StatusUnderway   = "underway"
	StatusNeedsYou   = "needs_you"
	StatusAnchored   = "anchored"
	StatusSailingOff = "sailing_off"
)

var (
	// NoHookWait is how long a launched ship may go without any hook before it
	// counts as needing you.
	NoHookWait   = delay.From("ADMIRAL_NO_HOOK_WAIT", 10*time.Second)
	SailOffDelay = delay.From("ADMIRAL_SAIL_OFF_DELAY", 3*time.Second)
)

func exitedCleanly(s sessionhost.Session) bool {
	return s.PaneDead && s.Signal == "" && s.ExitStatus == 0
}

// status: pane death beats everything; otherwise the hooks' @admiral-state decides.
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

type OrderKey struct{ Source, OrderID string }

// HiddenOrders counts ships of any status.
func HiddenOrders(list []Ship) map[OrderKey]bool {
	out := map[OrderKey]bool{}
	for _, sh := range list {
		if sh.Source != "" && sh.OrderID != "" {
			out[OrderKey{sh.Source, sh.OrderID}] = true
		}
	}
	return out
}
