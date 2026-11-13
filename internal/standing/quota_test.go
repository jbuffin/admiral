package standing_test

import (
	"testing"
	"time"

	"github.com/jbuffin/admiral/internal/standing"
	"github.com/jbuffin/admiral/internal/usage"
)

func TestQuotaHoldDecision(t *testing.T) {
	line := standing.PresetLimits(standing.Work) // 90
	reset := time.Date(2026, 11, 13, 14, 0, 0, 0, time.UTC)
	win := func(used float64) *usage.Window {
		return &usage.Window{UsedPercent: used, WindowMinutes: 300, ResetsAt: reset.Unix()}
	}
	cases := []struct {
		name   string
		q      usage.Quota
		have   bool
		held   bool
		window string
		used   float64
	}{
		{"primary under the line", usage.Quota{Primary: win(89.9)}, true, false, "", 0},
		{"primary at the line", usage.Quota{Primary: win(90)}, true, true, "primary", 90},
		{"primary over the line", usage.Quota{Primary: win(93)}, true, true, "primary", 93},
		{"secondary alone over the line", usage.Quota{Primary: win(10), Secondary: win(95)}, true, true, "secondary", 95},
		{"secondary at the line, primary under", usage.Quota{Primary: win(40), Secondary: win(90)}, true, true, "secondary", 90},
		{"both under", usage.Quota{Primary: win(89), Secondary: win(89)}, true, false, "", 0},
		{"a window past its reset reads unused", usage.Quota{Primary: win(0), Secondary: win(20)}, true, false, "", 0},
		{"spend at 100 with the windows under", usage.Quota{Primary: win(10), Secondary: win(10), Spend: &usage.SpendLimit{UsedPercent: 100, ResetsAt: reset.Unix()}}, true, false, "", 0},
		{"spend alone", usage.Quota{Spend: &usage.SpendLimit{UsedPercent: 100}}, true, false, "", 0},
		{"a login with no quota", usage.Quota{}, false, false, "", 0},
		{"Copilot: a quota with no windows", usage.Quota{PlanType: "copilot"}, true, false, "", 0},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := standing.CheckQuotaHold(line, c.q, c.have)
			if got.Held != c.held {
				t.Fatalf("Held = %v, want %v (%+v)", got.Held, c.held, got)
			}
			if !c.held {
				return
			}
			if got.Window != c.window || got.UsedPercent != c.used || got.LinePercent != 90 || !got.ResetsAt.Equal(reset) {
				t.Errorf("hold = %+v, want window %s at %v%% against line 90 resetting %v", got, c.window, c.used, reset)
			}
		})
	}
}

func TestQuotaHoldFollowsTheFleetsLine(t *testing.T) {
	q := usage.Quota{Primary: &usage.Window{UsedPercent: 75, ResetsAt: 1}}
	l := standing.PresetLimits(standing.Work)
	l.QuotaHoldPercent = 75
	if got := standing.CheckQuotaHold(l, q, true); !got.Held || got.LinePercent != 75 {
		t.Errorf("at a line of 75 and 75%% used: %+v, want held against 75", got)
	}
	l.QuotaHoldPercent = 100
	if got := standing.CheckQuotaHold(l, q, true); got.Held {
		t.Errorf("at a line of 100 and 75%% used: %+v, want not held", got)
	}
}

func TestQuotaHoldWhyWordsTheWindowAndTheReset(t *testing.T) {
	loc := time.FixedZone("test", -5*3600)
	now := time.Date(2026, 11, 13, 9, 0, 0, 0, loc)
	cases := []struct {
		name string
		hold standing.QuotaHold
		want string
	}{
		{"5-hour window resetting today", standing.QuotaHold{Window: "primary", WindowMinutes: 300, UsedPercent: 93.4, LinePercent: 90, ResetsAt: time.Date(2026, 11, 13, 15, 4, 0, 0, loc)},
			"Held for quota: Claude's 5-hour window is at 93% (line 90%), resets 15:04"},
		{"weekly window resetting in days", standing.QuotaHold{Window: "secondary", WindowMinutes: 10080, UsedPercent: 95, LinePercent: 90, ResetsAt: time.Date(2026, 11, 16, 15, 4, 0, 0, loc)},
			"Held for quota: Claude's weekly window is at 95% (line 90%), resets Mon 15:04"},
		{"an unknown length on the secondary window", standing.QuotaHold{Window: "secondary", UsedPercent: 91, LinePercent: 90, ResetsAt: time.Date(2026, 11, 13, 15, 4, 0, 0, loc)},
			"Held for quota: Claude's longer plan window is at 91% (line 90%), resets 15:04"},
		{"an unknown length on the primary window", standing.QuotaHold{Window: "primary", UsedPercent: 91, LinePercent: 90, ResetsAt: time.Date(2026, 11, 13, 15, 4, 0, 0, loc)},
			"Held for quota: Claude's plan window is at 91% (line 90%), resets 15:04"},
		{"a reset just under 24 hours away", standing.QuotaHold{Window: "primary", WindowMinutes: 300, UsedPercent: 90, LinePercent: 90, ResetsAt: now.Add(24*time.Hour - time.Minute)},
			"Held for quota: Claude's 5-hour window is at 90% (line 90%), resets 08:59"},
		{"a reset 24 hours away", standing.QuotaHold{Window: "primary", WindowMinutes: 300, UsedPercent: 90, LinePercent: 90, ResetsAt: now.Add(24 * time.Hour)},
			"Held for quota: Claude's 5-hour window is at 90% (line 90%), resets Sat 09:00"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := c.hold.Why("Claude", now, loc); got != c.want {
				t.Errorf("Why = %q, want %q", got, c.want)
			}
		})
	}
}
