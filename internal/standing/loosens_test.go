package standing_test

import (
	"reflect"
	"testing"

	"github.com/jbuffin/admiral/internal/standing"
)

func TestLoosens(t *testing.T) {
	base := func() standing.Limits {
		l := standing.Limits{
			Ceilings: standing.Ceilings{
				T1:    standing.Ceiling{Level: standing.A5, AsksFirst: true},
				T2:    standing.Ceiling{Level: standing.A3, Restricted: standing.Investigate},
				T3:    standing.Ceiling{Level: standing.A1},
				T4:    standing.Ceiling{Level: standing.A3, Restricted: standing.Investigate, Threshold: &standing.Threshold{Events: 10, Days: 14, Level: standing.A3, Restricted: standing.DraftFix}},
				Fleet: standing.Ceiling{Level: standing.A4},
			},
			Rates:            standing.Rates{LaunchesPerHour: 4, LaunchesPerSenderPerDay: 3, Concurrent: 3},
			WallClockMinutes: 60,
			Spend:            standing.Spend{LookoutCallUSD: 0.25, DailyWorkerUSD: 5, ReviewerCallUSD: 0.10},
			QuotaHoldPercent: 90,
		}
		l.SenderAllowlist = []string{"alice@acme.test"}
		l.RepoAllowlist = []string{"acme/widget"}
		l.WriteDeny = []string{".github/**", "deploy/**"}
		l.QuietHours = []standing.Window{{Start: "22:00", End: "06:00", Weekdays: []string{"fri"}}}
		l.Skip.Labels = []standing.Scoped{{Value: "wontfix"}, {Value: "chore"}}
		l.Skip.Senders = []standing.Scoped{{Value: "bot@ci.test"}}
		l.Skip.DedupeKeys = []standing.Scoped{{Value: "sentry:1"}}
		return l
	}
	for _, c := range []struct {
		name   string
		change func(*standing.Limits)
		want   []string
	}{
		{"no change", func(*standing.Limits) {}, nil},
		{"a higher T2 ceiling", func(l *standing.Limits) { l.Ceilings.T2 = standing.Ceiling{Level: standing.A4} }, []string{"ceilings.t2"}},
		{"a higher restricted level", func(l *standing.Limits) { l.Ceilings.T2.Restricted = standing.DraftFix }, []string{"ceilings.t2"}},
		{"a lower T2 ceiling", func(l *standing.Limits) { l.Ceilings.T2 = standing.Ceiling{Level: standing.A1} }, nil},
		{"A5 asks first turned off", func(l *standing.Limits) { l.Ceilings.T1.AsksFirst = false }, []string{"ceilings.t1"}},
		{"A5 asks first turned on", func(l *standing.Limits) { l.Ceilings.T1.AsksFirst = true }, nil},
		{"a higher fleet ceiling", func(l *standing.Limits) { l.Ceilings.Fleet = standing.Ceiling{Level: standing.A5} }, []string{"ceilings.fleet"}},
		{"a lower fleet ceiling", func(l *standing.Limits) { l.Ceilings.Fleet = standing.Ceiling{Level: standing.A1} }, nil},
		{"a T4 threshold met sooner", func(l *standing.Limits) {
			th := *l.Ceilings.T4.Threshold
			th.Events = 5
			l.Ceilings.T4.Threshold = &th
		}, []string{"ceilings.t4"}},
		{"a T4 threshold met later", func(l *standing.Limits) {
			th := *l.Ceilings.T4.Threshold
			th.Events = 20
			l.Ceilings.T4.Threshold = &th
		}, nil},
		{"a higher hourly rate", func(l *standing.Limits) { l.Rates.LaunchesPerHour++ }, []string{"rates.launches_per_hour"}},
		{"a lower hourly rate", func(l *standing.Limits) { l.Rates.LaunchesPerHour-- }, nil},
		{"a higher per-sender rate", func(l *standing.Limits) { l.Rates.LaunchesPerSenderPerDay++ }, []string{"rates.launches_per_sender_per_day"}},
		{"more concurrent ships", func(l *standing.Limits) { l.Rates.Concurrent++ }, []string{"rates.concurrent"}},
		{"a longer wall clock", func(l *standing.Limits) { l.WallClockMinutes = 120 }, []string{"wall_clock_minutes"}},
		{"a shorter wall clock", func(l *standing.Limits) { l.WallClockMinutes = 30 }, nil},
		{"a higher Lookout budget", func(l *standing.Limits) { l.Spend.LookoutCallUSD = 1 }, []string{"spend.lookout_call_usd"}},
		{"a higher daily spend", func(l *standing.Limits) { l.Spend.DailyWorkerUSD = 50 }, []string{"spend.daily_worker_usd"}},
		{"a higher reviewer budget", func(l *standing.Limits) { l.Spend.ReviewerCallUSD = 1 }, []string{"spend.reviewer_call_usd"}},
		{"a higher quota hold", func(l *standing.Limits) { l.QuotaHoldPercent = 100 }, []string{"quota_hold_percent"}},
		{"a lower quota hold", func(l *standing.Limits) { l.QuotaHoldPercent = 50 }, nil},
		{"a quiet window removed", func(l *standing.Limits) { l.QuietHours = nil }, []string{"quiet_hours"}},
		{"a quiet window narrowed", func(l *standing.Limits) { l.QuietHours[0].End = "05:00" }, []string{"quiet_hours"}},
		{"a quiet window dropping a weekday", func(l *standing.Limits) {
			l.QuietHours = []standing.Window{{Start: "22:00", End: "06:00", Weekdays: []string{"fri", "sat"}}}
		}, nil},
		{"a quiet window widened", func(l *standing.Limits) { l.QuietHours[0].End = "07:00" }, nil},
		{"a quiet window added", func(l *standing.Limits) {
			l.QuietHours = append(l.QuietHours, standing.Window{Start: "12:00", End: "13:00", Weekdays: []string{"sun"}})
		}, nil},
		{"a shorter write-deny list", func(l *standing.Limits) { l.WriteDeny = l.WriteDeny[:1] }, []string{"write_deny"}},
		{"a longer write-deny list", func(l *standing.Limits) { l.WriteDeny = append(l.WriteDeny, "infra/**") }, nil},
		{"a shorter label skip list", func(l *standing.Limits) { l.Skip.Labels = l.Skip.Labels[:1] }, []string{"skip.labels"}},
		{"a shorter sender skip list", func(l *standing.Limits) { l.Skip.Senders = nil }, []string{"skip.senders"}},
		{"a shorter dedupe_key skip list", func(l *standing.Limits) { l.Skip.DedupeKeys = nil }, []string{"skip.dedupe_keys"}},
		{"a wider sender allowlist", func(l *standing.Limits) { l.SenderAllowlist = append(l.SenderAllowlist, "bob@acme.test") }, []string{"sender_allowlist"}},
		{"a narrower sender allowlist", func(l *standing.Limits) { l.SenderAllowlist = nil }, nil},
		{"a wider repo allowlist", func(l *standing.Limits) { l.RepoAllowlist = append(l.RepoAllowlist, "acme/other") }, []string{"repo_allowlist"}},
		{"an emptied repo allowlist", func(l *standing.Limits) { l.RepoAllowlist = nil }, []string{"repo_allowlist"}},
		{"a narrower repo allowlist", func(l *standing.Limits) {
			l.RepoAllowlist = []string{"acme/widget"}
		}, nil},
		{"two loosened fields come back in catalog order", func(l *standing.Limits) {
			l.WallClockMinutes = 120
			l.Rates.Concurrent++
		}, []string{"rates.concurrent", "wall_clock_minutes"}},
	} {
		t.Run(c.name, func(t *testing.T) {
			old, next := base(), base()
			c.change(&next)
			got := standing.Loosens(old, next)
			if len(got) == 0 && len(c.want) == 0 {
				return
			}
			if !reflect.DeepEqual(got, c.want) {
				t.Errorf("Loosens = %v, want %v", got, c.want)
			}
		})
	}
}
