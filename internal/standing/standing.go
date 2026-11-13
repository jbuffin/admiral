// Package standing holds the Limit model of a fleet's standing orders and the pure functions that enforce it.
package standing

import (
	"encoding/json"
	"fmt"
	"slices"
)

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

// ParseLevel reads "A0" to "A5" except A2, which is not a rung.
func ParseLevel(s string) (Level, bool) {
	for _, l := range []Level{A0, A1, A3, A4, A5} {
		if l.String() == s {
			return l, true
		}
	}
	return 0, false
}

func (l Level) MarshalJSON() ([]byte, error) { return json.Marshal(l.String()) }

func (l *Level) UnmarshalJSON(b []byte) error {
	var s string
	if err := json.Unmarshal(b, &s); err != nil {
		return err
	}
	v, ok := ParseLevel(s)
	if !ok {
		return fmt.Errorf("%q is not an action ladder level", s)
	}
	*l = v
	return nil
}

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

// Clone copies every slice, so a caller can change the result freely.
func (l Limits) Clone() Limits {
	l.SenderAllowlist = slices.Clone(l.SenderAllowlist)
	l.RepoAllowlist = slices.Clone(l.RepoAllowlist)
	l.WriteDeny = slices.Clone(l.WriteDeny)
	l.QuietHours = slices.Clone(l.QuietHours)
	for i, w := range l.QuietHours {
		l.QuietHours[i].Weekdays = slices.Clone(w.Weekdays)
	}
	l.Skip.Labels = slices.Clone(l.Skip.Labels)
	l.Skip.Senders = slices.Clone(l.Skip.Senders)
	l.Skip.DedupeKeys = slices.Clone(l.Skip.DedupeKeys)
	for _, c := range []*Ceiling{&l.Ceilings.T1, &l.Ceilings.T2, &l.Ceilings.T3, &l.Ceilings.T4, &l.Ceilings.Fleet} {
		if c.Threshold != nil {
			t := *c.Threshold
			c.Threshold = &t
		}
	}
	return l
}
