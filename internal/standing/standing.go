// Package standing holds the Limit model of a fleet's standing orders and the pure functions that enforce it.
package standing

import "time"

// Preset names a set of Limit values; every fleet starts on Work.
type Preset string

const (
	Work Preset = "work"
	Home Preset = "home"
)

// Level is a rung of the action ladder. There is no A2.
type Level int

const (
	A0 Level = 0 // card only
	A1 Level = 1 // propose
	A3 Level = 3 // restricted ship
	A4 Level = 4 // normal ship
	A5 Level = 5 // Commodore team
)

func (l Level) String() string {
	switch l {
	case A0:
		return "A0"
	case A1:
		return "A1"
	case A3:
		return "A3"
	case A4:
		return "A4"
	case A5:
		return "A5"
	}
	return "A?"
}

// Restricted is the profile of an A3 ship.
type Restricted string

const (
	Investigate Restricted = "investigate"
	DraftFix    Restricted = "draft_fix"
)

type Tier int

const (
	T1 Tier = 1
	T2 Tier = 2
	T3 Tier = 3
	T4 Tier = 4
)

type Threshold struct {
	Events     int        `json:"events"`
	Days       int        `json:"days"`
	Level      Level      `json:"level"`
	Restricted Restricted `json:"restricted,omitempty"`
}

type Ceiling struct {
	Level      Level      `json:"level"`
	Restricted Restricted `json:"restricted,omitempty"`
	AsksFirst  bool       `json:"asks_first,omitempty"`
	Threshold  *Threshold `json:"threshold,omitempty"`
}

type Ceilings struct {
	T1    Ceiling `json:"t1"`
	T2    Ceiling `json:"t2"`
	T3    Ceiling `json:"t3"`
	T4    Ceiling `json:"t4"`
	Fleet Ceiling `json:"fleet"`
}

type Rates struct {
	LaunchesPerHour         int `json:"launches_per_hour"`
	LaunchesPerSenderPerDay int `json:"launches_per_sender_per_day"`
	Concurrent              int `json:"concurrent"`
}

type Spend struct {
	LookoutCallUSD  float64 `json:"lookout_call_usd"`
	DailyWorkerUSD  float64 `json:"daily_worker_usd"`
	ReviewerCallUSD float64 `json:"reviewer_call_usd"`
}

// Window holds launches as proposals from Start to End (HH:MM, fleet-local) on the listed weekdays ("mon".."sun").
// A window that crosses midnight belongs to the weekday it starts on.
type Window struct {
	Start    string   `json:"start"`
	End      string   `json:"end"`
	Weekdays []string `json:"weekdays"`
}

// Scoped is a skip-filter entry; an empty Routine applies to every routine.
type Scoped struct {
	Value   string `json:"value"`
	Routine string `json:"routine,omitempty"`
}

type Skip struct {
	Labels     []Scoped `json:"labels"`
	Senders    []Scoped `json:"senders"`
	DedupeKeys []Scoped `json:"dedupe_keys"`
}

type Limits struct {
	Ceilings         Ceilings `json:"ceilings"`
	SenderAllowlist  []string `json:"sender_allowlist"`
	Rates            Rates    `json:"rates"`
	QuietHours       []Window `json:"quiet_hours"`
	RepoAllowlist    []string `json:"repo_allowlist"`
	WallClockMinutes int      `json:"wall_clock_minutes"`
	Spend            Spend    `json:"spend"`
	QuotaHoldPercent int      `json:"quota_hold_percent"`
	WriteDeny        []string `json:"write_deny"`
	Skip             Skip     `json:"skip"`
}

// PresetLimits is the Limit values of a preset; quiet hours are empty in both.
func PresetLimits(p Preset) Limits { return Limits{} }

type Sentence struct {
	Field    string `json:"field"` // empty for the fixed sentences
	Sentence string `json:"sentence"`
}

// Sentences is one plain-English sentence per Limit, in catalog order, then the two fixed ones.
func Sentences(l Limits) []Sentence { return nil }

// Loosens lists the field paths (as in Sentences) the change from old to next loosens, in catalog order.
func Loosens(old, next Limits) []string { return nil }

// Verdict is the reviewer's call; the zero value lowers nothing.
type Verdict int

const (
	Allow Verdict = iota
	Ask
	Block
)

type Event struct {
	Tier              Tier
	Routine           Level // the routine's Before acting, mapped by the caller
	Reviewer          Verdict
	SameKeyEvents     int  // events with this dedupe_key in the last 14 days, this one included
	RestrictedHarness bool // some harness in the fleet can run a restricted ship
}

type Action struct {
	Level      Level
	Restricted Restricted // set only on A3
}

// Effective is min(routine, tier ceiling, fleet ceiling, reviewer verdict) with the T3, T1 and T4 rules and the A3 fallback.
func (l Limits) Effective(e Event) Action { return Action{Level: A4, Restricted: "not implemented"} }

// Held is whether now, in the fleet's local time, falls inside a quiet-hours window.
func (l Limits) Held(now time.Time) bool { return false }

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
	Reason string // the first Reason* exceeded; empty when OK
}

func (l Limits) CheckRates(c Counts) RateVerdict { return RateVerdict{OK: true} }

// DedupeHeld is whether a live ship already holds the dedupe_key.
func DedupeHeld(liveKeys []string, key string) bool { return false }

// RepoAllowed: an empty allowlist allows every repo.
func (l Limits) RepoAllowed(repo string) bool { return true }

// SenderAllowed: an empty allowlist allows no one.
func (l Limits) SenderAllowed(sender string) bool { return true }

type SkipInput struct {
	Routine   string
	Labels    []string
	Sender    string
	DedupeKey string
}

func (l Limits) Skipped(in SkipInput) bool { return false }

type Denial struct {
	Rules     []string // Claude Code deny rules: Edit(glob) and Write(glob) per glob
	DenyWrite []string // the sandbox denyWrite list
}

func (l Limits) WriteDenial() Denial { return Denial{} }

func (l Limits) WallClockExceeded(elapsed time.Duration) bool { return false }

// DailySpendReached is whether the day's worker spend has hit the cap.
func (l Limits) DailySpendReached(spentUSD float64) bool { return false }
