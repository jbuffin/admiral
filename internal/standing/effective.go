package standing

import "time"

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
	SameKeyEvents     int  // events with this dedupe_key within the T4 threshold's days, this one included
	RestrictedHarness bool // some harness in the fleet can run a restricted ship
}

type Action struct {
	Level      Level
	Restricted Restricted // set only on A3
}

func (c Ceiling) action() Action {
	if c.Level == A3 {
		return Action{Level: A3, Restricted: c.Restricted}
	}
	return Action{Level: c.Level}
}

// lower picks the lower rung; two A3s keep the lower restricted level, an unset one not limiting.
func lower(a, b Action) Action {
	switch {
	case a.Level < b.Level:
		return a
	case b.Level < a.Level:
		return b
	case a.Level != A3:
		return a
	case a.Restricted == "" || a.Restricted == DraftFix && b.Restricted != "":
		return b
	}
	return a
}

// clampRoutine puts a caller's routine level on the ladder: off-ladder values drop to the rung below.
func clampRoutine(l Level) Action {
	switch {
	case l <= A0:
		return Action{Level: A0}
	case l == A1 || l == 2:
		return Action{Level: A1}
	case l == A3:
		return Action{Level: A3}
	case l == A4:
		return Action{Level: A4}
	}
	return Action{Level: A5}
}

// Effective is min(routine, tier ceiling, fleet ceiling, reviewer verdict) with the T3, T1 and T4 rules and the A3 fallback.
func (l Limits) Effective(e Event) Action {
	var tier Ceiling
	switch e.Tier {
	case T1:
		tier = l.Ceilings.T1
	case T2:
		tier = l.Ceilings.T2
	case T3:
		tier = l.Ceilings.T3
		if tier.Level > A1 { // no stored value raises T3
			tier = Ceiling{Level: A1}
		}
	case T4:
		tier = l.Ceilings.T4
		if t := tier.Threshold; t != nil && e.SameKeyEvents >= t.Events {
			tier = Ceiling{Level: t.Level, Restricted: t.Restricted}
		}
	}
	routine := clampRoutine(e.Routine)
	// Asks first decides on the request, so lowering the fleet ceiling can never raise the result.
	if e.Tier == T1 && l.Ceilings.T1.AsksFirst && l.Ceilings.T1.Level == A5 && routine.Level == A5 {
		routine = Action{Level: A1}
	}
	// An unknown tier has no ceiling: A0, never more than a card.
	got := lower(lower(routine, tier.action()), l.Ceilings.Fleet.action())
	switch e.Reviewer {
	case Allow:
	case Ask:
		got = lower(got, Action{Level: A1})
	default: // Block, and any value this code does not know, fails closed
		got = Action{Level: A0}
	}
	if got.Level == A3 && !e.RestrictedHarness {
		got = Action{Level: A1}
	}
	if got.Level == A3 && got.Restricted == "" { // a routine's A3 names no profile: the narrowest
		got.Restricted = Investigate
	}
	return got
}

var weekdays = [...]string{"sun", "mon", "tue", "wed", "thu", "fri", "sat"}

func minutes(hhmm string) int {
	if len(hhmm) != 5 {
		return 0
	}
	return int(hhmm[0]-'0')*600 + int(hhmm[1]-'0')*60 + int(hhmm[3]-'0')*10 + int(hhmm[4]-'0')
}

func (w Window) onDay(d time.Weekday) bool {
	for _, n := range w.Weekdays {
		if n == weekdays[d] {
			return true
		}
	}
	return false
}

// Held is whether now, read on its own wall clock (the caller passes the fleet's local time), falls inside a quiet-hours window.
func (l Limits) Held(now time.Time) bool {
	m := now.Hour()*60 + now.Minute()
	today, yesterday := now.Weekday(), (now.Weekday()+6)%7
	for _, w := range l.QuietHours {
		s, e := minutes(w.Start), minutes(w.End)
		switch {
		case s < e:
			if w.onDay(today) && m >= s && m < e {
				return true
			}
		case w.onDay(today) && m >= s, w.onDay(yesterday) && m < e:
			return true
		}
	}
	return false
}
