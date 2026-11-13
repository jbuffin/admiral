package standing

import (
	"time"

	"github.com/jbuffin/admiral/internal/usage"
)

// QuotaHold is whether unattended launches on one login are held, and the window that holds them.
type QuotaHold struct {
	Held        bool
	Window      string // "primary" or "secondary"
	UsedPercent float64
	LinePercent int
	ResetsAt    time.Time
}

// CheckQuotaHold decides from the fleet's Limits and a login's quota; have is false when the login has no figure.
func CheckQuotaHold(l Limits, q usage.Quota, have bool) QuotaHold {
	return QuotaHold{}
}
