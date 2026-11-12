package routines_test

import (
	"testing"
	"time"

	"github.com/jbuffin/admiral/internal/routines"
)

func TestCronNextRunAfterATime(t *testing.T) {
	cases := []struct {
		expr  string
		after time.Time
		want  time.Time
	}{
		{"30 8 * * 1-5", time.Date(2026, 11, 12, 8, 0, 0, 0, time.UTC), time.Date(2026, 11, 12, 8, 30, 0, 0, time.UTC)},
		{"30 8 * * 1-5", time.Date(2026, 11, 12, 8, 30, 0, 0, time.UTC), time.Date(2026, 11, 13, 8, 30, 0, 0, time.UTC)},
		{"30 8 * * 1-5", time.Date(2026, 11, 13, 9, 0, 0, 0, time.UTC), time.Date(2026, 11, 16, 8, 30, 0, 0, time.UTC)},
		{"*/15 * * * *", time.Date(2026, 11, 12, 8, 50, 30, 0, time.UTC), time.Date(2026, 11, 12, 9, 0, 0, 0, time.UTC)},
		{"0 2 * * *", time.Date(2026, 12, 31, 3, 0, 0, 0, time.UTC), time.Date(2027, 1, 1, 2, 0, 0, 0, time.UTC)},
		{"0 0 29 2 *", time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC), time.Date(2028, 2, 29, 0, 0, 0, 0, time.UTC)},
		{"0 9 * * sun", time.Date(2026, 11, 12, 0, 0, 0, 0, time.UTC), time.Date(2026, 11, 15, 9, 0, 0, 0, time.UTC)},
		{"0 9 * * 7", time.Date(2026, 11, 12, 0, 0, 0, 0, time.UTC), time.Date(2026, 11, 15, 9, 0, 0, 0, time.UTC)},
		// both day fields restricted: either matches
		{"0 9 13 * mon", time.Date(2026, 11, 12, 10, 0, 0, 0, time.UTC), time.Date(2026, 11, 13, 9, 0, 0, 0, time.UTC)},
	}
	for _, c := range cases {
		s, err := routines.ParseCron(c.expr)
		if err != nil {
			t.Fatalf("%q: %v", c.expr, err)
		}
		if got := s.Next(c.after); !got.Equal(c.want) {
			t.Errorf("%q after %v = %v, want %v", c.expr, c.after, got, c.want)
		}
	}
}

func TestCronRefusesWhatIsNotFiveValidFields(t *testing.T) {
	for _, expr := range []string{"", "* * * *", "0 30 8 * * 1-5", "60 * * * *", "* 24 * * *", "* * 0 * *", "* * * 13 *", "*/0 * * * *", "5-1 * * * *", "a * * * *", "-1 * * * *"} {
		if _, err := routines.ParseCron(expr); err == nil {
			t.Errorf("%q parsed", expr)
		}
	}
}

// nextWithin fails the test instead of hanging when Next never returns.
func nextWithin(t *testing.T, expr string, after time.Time) time.Time {
	t.Helper()
	s, err := routines.ParseCron(expr)
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan time.Time, 1)
	go func() { done <- s.Next(after) }()
	select {
	case got := <-done:
		return got
	case <-time.After(5 * time.Second):
		t.Fatalf("Next(%q) after %v did not return", expr, after)
		return time.Time{}
	}
}

func zone(t *testing.T, name string) *time.Location {
	t.Helper()
	loc, err := time.LoadLocation(name)
	if err != nil {
		t.Skipf("no tzdata for %s: %v", name, err)
	}
	return loc
}

func TestCronNextAcrossClocksSpringingForward(t *testing.T) {
	ny := zone(t, "America/New_York")
	cases := []struct {
		expr  string
		after time.Time
		want  time.Time
	}{
		{"0 3 * * *", time.Date(2026, 3, 8, 0, 0, 0, 0, ny), time.Date(2026, 3, 8, 3, 0, 0, 0, ny)},
		{"0 2 * * *", time.Date(2026, 3, 7, 12, 0, 0, 0, ny), time.Date(2026, 3, 8, 3, 0, 0, 0, ny)},
		// a fixed time inside the gap runs once, at the first instant after it
		{"30 2 * * *", time.Date(2026, 3, 7, 12, 0, 0, 0, ny), time.Date(2026, 3, 8, 3, 0, 0, 0, ny)},
		{"30 2 * * *", time.Date(2026, 3, 8, 3, 0, 0, 0, ny), time.Date(2026, 3, 9, 2, 30, 0, 0, ny)},
		{"30 8 * * *", time.Date(2026, 3, 7, 9, 0, 0, 0, ny), time.Date(2026, 3, 8, 8, 30, 0, 0, ny)},
		{"0 * * * *", time.Date(2026, 3, 8, 1, 30, 0, 0, ny), time.Date(2026, 3, 8, 3, 0, 0, 0, ny)},
	}
	for _, c := range cases {
		if got := nextWithin(t, c.expr, c.after); !got.Equal(c.want) {
			t.Errorf("%q after %v = %v, want %v", c.expr, c.after, got, c.want)
		}
	}
}

func TestCronNextAcrossClocksGoingBack(t *testing.T) {
	ny := zone(t, "America/New_York")
	first := time.Date(2026, 11, 1, 1, 30, 0, 0, ny) // 01:30 EDT; the same wall time comes again an hour later
	if first.Add(time.Hour).Hour() != 1 {
		t.Fatal("the zone data has no repeated hour on 2026-11-01")
	}
	// a fixed time runs once; a wildcard hour runs in both passes
	if got, want := nextWithin(t, "30 1 * * *", first), time.Date(2026, 11, 2, 1, 30, 0, 0, ny); !got.Equal(want) {
		t.Errorf("fixed hour after its first pass = %v, want %v", got, want)
	}
	if got := nextWithin(t, "30 * * * *", first); !got.Equal(first.Add(time.Hour)) {
		t.Errorf("wildcard hour after its first pass = %v, want %v", got, first.Add(time.Hour))
	}
}

func TestCronNextInAZoneThatSkipsMidnight(t *testing.T) {
	santiago := zone(t, "America/Santiago")
	// clocks spring forward at midnight on 2022-09-11, so 00:00 that day does not exist
	after := time.Date(2022, 9, 10, 12, 0, 0, 0, santiago)
	for _, expr := range []string{"0 0 * * *", "30 0 * * *", "0 3 * * *", "0 8 * * 1-5"} {
		got := nextWithin(t, expr, after)
		if got.IsZero() || !got.After(after) {
			t.Errorf("%q after %v = %v", expr, after, got)
		}
	}
}
