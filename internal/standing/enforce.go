package standing

import (
	"slices"
	"strings"
	"time"
)

type Counts struct {
	LaunchesLastHour    int // includes scheduled launches
	SenderLaunchesToday int
	WorkerShips         int
}

const (
	ReasonPerHour    = "launches_per_hour"
	ReasonPerSender  = "launches_per_sender_per_day"
	ReasonConcurrent = "concurrent"
)

type RateVerdict struct {
	OK     bool
	Reason string // the first Reason* reached; empty when OK
}

// CheckRates refuses a launch once a count has reached its cap.
func (l Limits) CheckRates(c Counts) RateVerdict {
	switch {
	case c.LaunchesLastHour >= l.Rates.LaunchesPerHour:
		return RateVerdict{Reason: ReasonPerHour}
	case c.SenderLaunchesToday >= l.Rates.LaunchesPerSenderPerDay:
		return RateVerdict{Reason: ReasonPerSender}
	case c.WorkerShips >= l.Rates.Concurrent:
		return RateVerdict{Reason: ReasonConcurrent}
	}
	return RateVerdict{OK: true}
}

// DedupeHeld is whether a live ship already holds the dedupe_key; an event with no key is never held.
func DedupeHeld(liveKeys []string, key string) bool {
	return key != "" && slices.Contains(liveKeys, key)
}

func containsFold(list []string, s string) bool {
	return slices.ContainsFunc(list, func(v string) bool { return strings.EqualFold(v, s) })
}

// RepoAllowed: an empty allowlist allows every repo.
func (l Limits) RepoAllowed(repo string) bool {
	return len(l.RepoAllowlist) == 0 || containsFold(l.RepoAllowlist, repo)
}

// SenderAllowed: an empty allowlist allows no one.
func (l Limits) SenderAllowed(sender string) bool { return containsFold(l.SenderAllowlist, sender) }

type SkipInput struct {
	Routine   string
	Labels    []string
	Sender    string
	DedupeKey string
}

func scopedMatch(list []Scoped, routine string, match func(value string) bool) bool {
	return slices.ContainsFunc(list, func(s Scoped) bool {
		return (s.Routine == "" || s.Routine == routine) && match(s.Value)
	})
}

// Skipped is whether a filter drops the event before any launch.
func (l Limits) Skipped(in SkipInput) bool {
	return scopedMatch(l.Skip.Labels, in.Routine, func(v string) bool { return containsFold(in.Labels, v) }) ||
		scopedMatch(l.Skip.Senders, in.Routine, func(v string) bool { return in.Sender != "" && strings.EqualFold(v, in.Sender) }) ||
		scopedMatch(l.Skip.DedupeKeys, in.Routine, func(v string) bool { return in.DedupeKey != "" && v == in.DedupeKey })
}

type Denial struct {
	Rules     []string // Claude Code deny rules: Edit(glob) and Write(glob) per glob
	DenyWrite []string // the sandbox denyWrite list
}

func (l Limits) WriteDenial() Denial {
	var d Denial
	for _, g := range l.WriteDeny {
		d.Rules = append(d.Rules, "Edit("+g+")", "Write("+g+")")
		d.DenyWrite = append(d.DenyWrite, g)
	}
	return d
}

func (l Limits) WallClockExceeded(elapsed time.Duration) bool {
	return elapsed > time.Duration(l.WallClockMinutes)*time.Minute
}

// DailySpendReached is whether the day's worker spend has hit the cap.
func (l Limits) DailySpendReached(spentUSD float64) bool { return spentUSD >= l.Spend.DailyWorkerUSD }
