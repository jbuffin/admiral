package standing

import (
	"slices"
	"time"
)

// ceilingRank orders what a ceiling lets through; an A3 draft fix sits between A3 investigate and A4.
func ceilingRank(c Ceiling) int {
	r := int(c.Level) * 2
	if c.Level == A3 && c.Restricted == DraftFix {
		r++
	}
	return r
}

func ceilingLoosens(old, next Ceiling) bool {
	if ceilingRank(next) > ceilingRank(old) || old.AsksFirst && !next.AsksFirst {
		return true
	}
	o, n := old.Threshold, next.Threshold
	if n == nil {
		return false
	}
	nr := ceilingRank(Ceiling{Level: n.Level, Restricted: n.Restricted})
	if o == nil {
		return nr > ceilingRank(next)
	}
	return nr > ceilingRank(Ceiling{Level: o.Level, Restricted: o.Restricted}) || n.Events < o.Events || n.Days > o.Days
}

// quietLoosens is whether some minute of the week held by old is free in next.
func quietLoosens(old, next []Window) bool {
	if len(old) == 0 {
		return false
	}
	zone := time.FixedZone("week", 0)
	o, n := Limits{QuietHours: old}, Limits{QuietHours: next}
	for t := time.Date(2026, 11, 1, 0, 0, 0, 0, zone); t.Day() < 8; t = t.Add(time.Minute) {
		if o.Held(t) && !n.Held(t) {
			return true
		}
	}
	return false
}

func dropsEntry[T comparable](old, next []T) bool {
	return slices.ContainsFunc(old, func(v T) bool { return !slices.Contains(next, v) })
}

func addsEntry[T comparable](old, next []T) bool { return dropsEntry(next, old) }

// Loosens lists the field paths (as in Sentences) the change from old to next loosens, in catalog order.
func Loosens(old, next Limits) []string {
	var out []string
	add := func(field string, loosened bool) {
		if loosened {
			out = append(out, field)
		}
	}
	add("ceilings.t1", ceilingLoosens(old.Ceilings.T1, next.Ceilings.T1))
	add("ceilings.t2", ceilingLoosens(old.Ceilings.T2, next.Ceilings.T2))
	add("ceilings.t3", ceilingLoosens(old.Ceilings.T3, next.Ceilings.T3))
	add("ceilings.t4", ceilingLoosens(old.Ceilings.T4, next.Ceilings.T4))
	add("ceilings.fleet", ceilingLoosens(old.Ceilings.Fleet, next.Ceilings.Fleet))
	// An empty sender allowlist allows no one, so only a new entry loosens it.
	add("sender_allowlist", addsEntry(old.SenderAllowlist, next.SenderAllowlist))
	add("rates.launches_per_hour", next.Rates.LaunchesPerHour > old.Rates.LaunchesPerHour)
	add("rates.launches_per_sender_per_day", next.Rates.LaunchesPerSenderPerDay > old.Rates.LaunchesPerSenderPerDay)
	add("rates.concurrent", next.Rates.Concurrent > old.Rates.Concurrent)
	add("quiet_hours", quietLoosens(old.QuietHours, next.QuietHours))
	// An empty repo allowlist allows any repo, so emptying it loosens and filling it narrows.
	add("repo_allowlist", len(old.RepoAllowlist) > 0 && (len(next.RepoAllowlist) == 0 || addsEntry(old.RepoAllowlist, next.RepoAllowlist)))
	add("wall_clock_minutes", next.WallClockMinutes > old.WallClockMinutes)
	add("spend.lookout_call_usd", next.Spend.LookoutCallUSD > old.Spend.LookoutCallUSD)
	add("spend.daily_worker_usd", next.Spend.DailyWorkerUSD > old.Spend.DailyWorkerUSD)
	add("spend.reviewer_call_usd", next.Spend.ReviewerCallUSD > old.Spend.ReviewerCallUSD)
	add("quota_hold_percent", next.QuotaHoldPercent > old.QuotaHoldPercent)
	add("write_deny", dropsEntry(old.WriteDeny, next.WriteDeny))
	add("skip.labels", dropsEntry(old.Skip.Labels, next.Skip.Labels))
	add("skip.senders", dropsEntry(old.Skip.Senders, next.Skip.Senders))
	add("skip.dedupe_keys", dropsEntry(old.Skip.DedupeKeys, next.Skip.DedupeKeys))
	return out
}
