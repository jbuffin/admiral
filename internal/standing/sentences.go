package standing

import (
	"fmt"
	"strings"
)

type Sentence struct {
	Field    string `json:"field"` // a path the UI links to; empty for the fixed sentences
	Sentence string `json:"sentence"`
}

// outcome says what a ceiling lets an event do.
func outcome(c Ceiling) string {
	switch c.Level {
	case A0:
		return "at most a card"
	case A1:
		return "at most a proposal"
	case A3:
		if c.Restricted == DraftFix {
			return "at most a restricted ship that drafts a fix"
		}
		return "at most a restricted ship that investigates"
	case A4:
		return "at most a ship"
	}
	return "a Commodore team"
}

func tierSentence(who string, c Ceiling) string {
	s := fmt.Sprintf("%s can get %s", who, outcome(c))
	if c.AsksFirst && c.Level == A5 {
		s += ", but Commodore asks first: a request for a team becomes a proposal"
	}
	if t := c.Threshold; t != nil {
		s += fmt.Sprintf("; at %d or more events with the same key in %d days, %s", t.Events, t.Days, outcome(Ceiling{Level: t.Level, Restricted: t.Restricted}))
	}
	return s + "."
}

func plural(n int, one, many string) string {
	if n == 1 {
		return fmt.Sprintf("%d %s", n, one)
	}
	return fmt.Sprintf("%d %s", n, many)
}

func list(label string, none string, items []string) string {
	if len(items) == 0 {
		return none
	}
	return label + " " + strings.Join(items, ", ") + "."
}

// RoutineNamer names a routine by its ID; ok is false when the routine no longer exists.
type RoutineNamer func(id string) (name string, ok bool)

func scopedNames(in []Scoped, name RoutineNamer) []string {
	out := make([]string, len(in))
	for i, s := range in {
		out[i] = s.Value
		if s.Routine == "" {
			continue
		}
		who := s.Routine
		if name != nil {
			if n, ok := name(s.Routine); ok {
				who = n
			} else {
				who = "a deleted routine"
			}
		}
		out[i] += " (" + who + " only)"
	}
	return out
}

func quietSentence(ws []Window) string {
	if len(ws) == 0 {
		return "Launches are never held for the time of day."
	}
	parts := make([]string, len(ws))
	for i, w := range ws {
		parts[i] = fmt.Sprintf("%s to %s on %s", w.Start, w.End, strings.Join(w.Weekdays, ", "))
	}
	return "Launches are held as proposals from " + strings.Join(parts, "; and from ") + "."
}

// Sentences is one plain-English sentence per Limit, in catalog order, then the two fixed ones.
// A scoped skip names its routine by ID.
func Sentences(l Limits) []Sentence { return SentencesNamed(l, nil) }

// SentencesNamed is Sentences with a scoped skip naming its routine through name; a nil name keeps the ID.
func SentencesNamed(l Limits, name RoutineNamer) []Sentence {
	c := l.Ceilings
	repo := "Ships may start in any repo the fleet's search roots find."
	if len(l.RepoAllowlist) > 0 {
		repo = "Ships may start only in " + strings.Join(l.RepoAllowlist, ", ") + "."
	}
	return []Sentence{
		{"ceilings.t1", tierSentence("You (T1)", c.T1)},
		{"ceilings.t2", tierSentence("Allowed people and trackers (T2)", c.T2)},
		{"ceilings.t3", tierSentence("Strangers (T3)", c.T3)},
		{"ceilings.t4", tierSentence("Monitor alerts (T4)", c.T4)},
		{"ceilings.fleet", "Whoever the sender, this fleet allows " + outcome(c.Fleet) + "."},
		{"sender_allowlist", list("Only these senders count as allowed (T2):", "No sender counts as allowed (T2).", l.SenderAllowlist)},
		{"rates.launches_per_hour", fmt.Sprintf("The fleet starts at most %s an hour, scheduled runs included.", plural(l.Rates.LaunchesPerHour, "ship", "ships"))},
		{"rates.launches_per_sender_per_day", fmt.Sprintf("One sender can start at most %s a day.", plural(l.Rates.LaunchesPerSenderPerDay, "ship", "ships"))},
		{"rates.concurrent", fmt.Sprintf("At most %s started by an outside event can run at once.", plural(l.Rates.Concurrent, "ship", "ships"))},
		{"quiet_hours", quietSentence(l.QuietHours)},
		{"repo_allowlist", repo},
		{"wall_clock_minutes", fmt.Sprintf("A ship started by an outside event is stopped after %s.", plural(l.WallClockMinutes, "minute", "minutes"))},
		{"spend.lookout_call_usd", fmt.Sprintf("One Lookout call may cost at most $%.2f.", l.Spend.LookoutCallUSD)},
		{"spend.daily_worker_usd", fmt.Sprintf("Ships started by outside events may spend at most $%.2f a day.", l.Spend.DailyWorkerUSD)},
		{"spend.reviewer_call_usd", fmt.Sprintf("One reviewer call may cost at most $%.2f.", l.Spend.ReviewerCallUSD)},
		{"quota_hold_percent", fmt.Sprintf("Commitments and review wakes wait, and routines ask first, while any plan window is at %d%% or more.", l.QuotaHoldPercent)},
		{"write_deny", list("Ships may not write to", "Ships may write anywhere in their repo.", l.WriteDeny)},
		{"skip.labels", list("Events with these labels are skipped:", "No label is skipped.", scopedNames(l.Skip.Labels, name))},
		{"skip.senders", list("Events from these senders are skipped:", "No sender is skipped.", scopedNames(l.Skip.Senders, name))},
		{"skip.dedupe_keys", list("Events with these keys are skipped:", "No key is skipped.", scopedNames(l.Skip.DedupeKeys, name))},
		{"", "A restricted ship never pushes."},
		{"", "A restricted ship's network reaches package registries only."},
	}
}
