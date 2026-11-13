package standing_test

import (
	"reflect"
	"testing"

	"github.com/jbuffin/admiral/internal/standing"
)

func TestPresetValues(t *testing.T) {
	home := standing.PresetLimits(standing.Home)
	work := standing.PresetLimits(standing.Work)

	ceil := func(l standing.Level, r standing.Restricted) standing.Ceiling {
		return standing.Ceiling{Level: l, Restricted: r}
	}
	rows := []struct {
		row        string
		home, work any
		gotHome    any
		gotWork    any
	}{
		{"T1 ceiling", standing.Ceiling{Level: standing.A5}, standing.Ceiling{Level: standing.A5, AsksFirst: true}, home.Ceilings.T1, work.Ceilings.T1},
		{"T2 ceiling", ceil(standing.A3, standing.DraftFix), ceil(standing.A1, ""), home.Ceilings.T2, work.Ceilings.T2},
		{"T3 ceiling", ceil(standing.A1, ""), ceil(standing.A0, ""), home.Ceilings.T3, work.Ceilings.T3},
		{"T4 ceiling",
			standing.Ceiling{Level: standing.A3, Restricted: standing.Investigate, Threshold: &standing.Threshold{Events: 10, Days: 14, Level: standing.A3, Restricted: standing.DraftFix}},
			ceil(standing.A3, standing.Investigate), home.Ceilings.T4, work.Ceilings.T4},
		{"Fleet ceiling", ceil(standing.A5, ""), ceil(standing.A5, ""), home.Ceilings.Fleet, work.Ceilings.Fleet},
		{"Launches per hour per fleet", 4, 2, home.Rates.LaunchesPerHour, work.Rates.LaunchesPerHour},
		{"Launches per sender per day", 3, 1, home.Rates.LaunchesPerSenderPerDay, work.Rates.LaunchesPerSenderPerDay},
		{"Concurrent worker-launched ships", 3, 2, home.Rates.Concurrent, work.Rates.Concurrent},
		{"Ship wall clock", 60, 60, home.WallClockMinutes, work.WallClockMinutes},
		{"Lookout call", 0.25, 0.25, home.Spend.LookoutCallUSD, work.Spend.LookoutCallUSD},
		{"Daily worker spend", 5.0, 10.0, home.Spend.DailyWorkerUSD, work.Spend.DailyWorkerUSD},
		{"Reviewer call", 0.10, 0.10, home.Spend.ReviewerCallUSD, work.Spend.ReviewerCallUSD},
		{"Quota hold percent", 90, 90, home.QuotaHoldPercent, work.QuotaHoldPercent},
	}
	for _, r := range rows {
		t.Run(r.row+" at Home", func(t *testing.T) {
			if !reflect.DeepEqual(r.gotHome, r.home) {
				t.Errorf("Home %s = %+v, want %+v", r.row, r.gotHome, r.home)
			}
		})
		t.Run(r.row+" at Work", func(t *testing.T) {
			if !reflect.DeepEqual(r.gotWork, r.work) {
				t.Errorf("Work %s = %+v, want %+v", r.row, r.gotWork, r.work)
			}
		})
	}

	for name, l := range map[string]standing.Limits{"Home": home, "Work": work} {
		t.Run("quiet hours at "+name+" are none", func(t *testing.T) {
			if len(l.QuietHours) != 0 {
				t.Errorf("%s quiet hours = %+v, want none: the engineer sets them per fleet", name, l.QuietHours)
			}
		})
		t.Run("lists at "+name+" are empty", func(t *testing.T) {
			if n := len(l.SenderAllowlist) + len(l.RepoAllowlist) + len(l.WriteDeny) +
				len(l.Skip.Labels) + len(l.Skip.Senders) + len(l.Skip.DedupeKeys); n != 0 {
				t.Errorf("%s has %d list entries, want none", name, n)
			}
		})
	}
}
