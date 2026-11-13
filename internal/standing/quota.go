package standing

import (
	"fmt"
	"time"

	"github.com/jbuffin/admiral/internal/usage"
)

// QuotaHold is whether unattended launches on one login are held, and the window that holds them.
type QuotaHold struct {
	Held          bool
	Window        string // "primary" or "secondary"
	UsedPercent   float64
	WindowMinutes int // 0 when the harness did not say
	LinePercent   int
	ResetsAt      time.Time
}

// CheckQuotaHold decides from the fleet's Limits and a login's quota; have is false when the login has no figure.
// Only the plan windows count: the monthly spend cap has its own Limit.
func CheckQuotaHold(l Limits, q usage.Quota, have bool) QuotaHold {
	if !have {
		return QuotaHold{}
	}
	var hold QuotaHold
	for _, w := range []struct {
		name string
		win  *usage.Window
	}{{"primary", q.Primary}, {"secondary", q.Secondary}} {
		if w.win == nil || w.win.UsedPercent < float64(l.QuotaHoldPercent) {
			continue
		}
		if hold.Held && w.win.UsedPercent <= hold.UsedPercent {
			continue
		}
		hold = QuotaHold{
			Held:          true,
			Window:        w.name,
			UsedPercent:   w.win.UsedPercent,
			WindowMinutes: w.win.WindowMinutes,
			LinePercent:   l.QuotaHoldPercent,
		}
		if w.win.ResetsAt > 0 {
			hold.ResetsAt = time.Unix(w.win.ResetsAt, 0)
		}
	}
	return hold
}

// Why words the hold for a refusal or a proposal: the harness's name, the window, how far it is over the line and when it resets in loc.
func (h QuotaHold) Why(name string, now time.Time, loc *time.Location) string {
	s := fmt.Sprintf("Held for quota: %s's %s is at %.0f%% (line %d%%)", name, h.windowWords(), h.UsedPercent, h.LinePercent)
	if h.ResetsAt.IsZero() {
		return s
	}
	at := h.ResetsAt.In(loc)
	layout := "15:04"
	if h.ResetsAt.Sub(now) >= 24*time.Hour {
		layout = "Mon 15:04"
	}
	return s + ", resets " + at.Format(layout)
}

func (h QuotaHold) windowWords() string {
	switch h.WindowMinutes {
	case 300:
		return "5-hour window"
	case 10080:
		return "weekly window"
	}
	if h.Window == "secondary" {
		return "longer plan window"
	}
	return "plan window"
}
