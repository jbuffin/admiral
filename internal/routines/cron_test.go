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
