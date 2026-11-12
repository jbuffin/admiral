package routines

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

// Schedule is a parsed 5-field cron expression: minute hour day-of-month month day-of-week.
// It is a small parser of our own rather than a dependency: the standard fields are all a routine's When takes.
type Schedule struct {
	min, hour, dom, month, dow uint64 // bit i set when value i matches
	domStar, dowStar           bool
}

type fieldSpec struct {
	name   string
	lo, hi int
	names  []string // names[0] is the name of value lo
}

var (
	monthNames = []string{"jan", "feb", "mar", "apr", "may", "jun", "jul", "aug", "sep", "oct", "nov", "dec"}
	dowNames   = []string{"sun", "mon", "tue", "wed", "thu", "fri", "sat"}
	cronFields = []fieldSpec{
		{name: "minute", lo: 0, hi: 59},
		{name: "hour", lo: 0, hi: 23},
		{name: "day of month", lo: 1, hi: 31},
		{name: "month", lo: 1, hi: 12, names: monthNames},
		{name: "day of week", lo: 0, hi: 7, names: dowNames},
	}
)

// ParseCron parses a standard 5-field expression: *, lists, ranges and steps, with month and weekday names.
func ParseCron(expr string) (Schedule, error) {
	parts := strings.Fields(expr)
	if len(parts) != 5 {
		return Schedule{}, fmt.Errorf("not a cron expression: want 5 fields (minute hour day-of-month month day-of-week), got %d", len(parts))
	}
	var bits [5]uint64
	for i, p := range parts {
		b, err := parseField(p, cronFields[i])
		if err != nil {
			return Schedule{}, fmt.Errorf("cron %s %q: %w", cronFields[i].name, p, err)
		}
		bits[i] = b
	}
	// 7 is Sunday too
	if bits[4]&(1<<7) != 0 {
		bits[4] |= 1
	}
	return Schedule{min: bits[0], hour: bits[1], dom: bits[2], month: bits[3], dow: bits[4],
		domStar: strings.HasPrefix(parts[2], "*"), dowStar: strings.HasPrefix(parts[4], "*")}, nil
}

func parseField(s string, f fieldSpec) (uint64, error) {
	var out uint64
	for _, item := range strings.Split(s, ",") {
		rng, stepText, hasStep := strings.Cut(item, "/")
		step := 1
		if hasStep {
			n, err := strconv.Atoi(stepText)
			if err != nil || n < 1 {
				return 0, fmt.Errorf("bad step %q", stepText)
			}
			step = n
		}
		lo, hi := f.lo, f.hi
		if f.name == "day of week" {
			hi = 6 // a bare * is Sunday to Saturday; 7 only as an explicit value
		}
		if rng != "*" {
			a, b, isRange := strings.Cut(rng, "-")
			var err error
			if lo, err = value(a, f); err != nil {
				return 0, err
			}
			hi = lo
			if isRange {
				if hi, err = value(b, f); err != nil {
					return 0, err
				}
			} else if hasStep {
				hi = f.hi
			}
			if lo > hi {
				return 0, fmt.Errorf("range %q runs backwards", rng)
			}
		}
		for v := lo; v <= hi; v += step {
			out |= 1 << uint(v)
		}
	}
	return out, nil
}

func value(s string, f fieldSpec) (int, error) {
	for i, n := range f.names {
		if strings.EqualFold(s, n) {
			return f.lo + i, nil
		}
	}
	n, err := strconv.Atoi(s)
	if err != nil || s == "" || s[0] == '+' || s[0] == '-' {
		return 0, fmt.Errorf("%q is not a number", s)
	}
	if n < f.lo || n > f.hi {
		return 0, fmt.Errorf("%d is outside %d-%d", n, f.lo, f.hi)
	}
	return n, nil
}

func (s Schedule) dayMatches(t time.Time) bool {
	dom, dow := s.dom&(1<<uint(t.Day())) != 0, s.dow&(1<<uint(t.Weekday())) != 0
	// as Vixie cron: when both day fields are restricted, either one matches
	if !s.domStar && !s.dowStar {
		return dom || dow
	}
	return dom && dow
}

// Next is the first matching minute strictly after t, in t's location; zero when none falls within five years.
func (s Schedule) Next(after time.Time) time.Time {
	t := after.Truncate(time.Minute).Add(time.Minute)
	limit := after.AddDate(5, 0, 0)
	for t.Before(limit) {
		switch {
		case s.month&(1<<uint(t.Month())) == 0:
			t = time.Date(t.Year(), t.Month()+1, 1, 0, 0, 0, 0, t.Location())
		case !s.dayMatches(t):
			t = time.Date(t.Year(), t.Month(), t.Day()+1, 0, 0, 0, 0, t.Location())
		case s.hour&(1<<uint(t.Hour())) == 0:
			t = time.Date(t.Year(), t.Month(), t.Day(), t.Hour()+1, 0, 0, 0, t.Location())
		case s.min&(1<<uint(t.Minute())) == 0:
			t = t.Add(time.Minute)
		default:
			return t
		}
	}
	return time.Time{}
}
