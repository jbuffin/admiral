package standing_test

import (
	"reflect"
	"testing"
	"time"

	"github.com/jbuffin/admiral/internal/standing"
)

func TestQuietHoursHeld(t *testing.T) {
	zone := time.FixedZone("test", -5*3600)
	at := func(day, hour, min int) time.Time { // November 2026: the 13th is a Friday
		return time.Date(2026, 11, day, hour, min, 0, 0, zone)
	}
	if wd := at(13, 0, 0).Weekday(); wd != time.Friday {
		t.Fatalf("the 13th is a %s, want Friday", wd)
	}
	night := standing.Limits{QuietHours: []standing.Window{{Start: "22:00", End: "06:00", Weekdays: []string{"fri"}}}}
	workday := standing.Limits{QuietHours: []standing.Window{{Start: "08:00", End: "19:00", Weekdays: []string{"mon", "tue", "wed", "thu", "fri"}}}}
	two := standing.Limits{QuietHours: []standing.Window{
		{Start: "22:00", End: "06:00", Weekdays: []string{"fri"}},
		{Start: "12:00", End: "13:00", Weekdays: []string{"sun"}},
	}}
	for _, c := range []struct {
		name string
		l    standing.Limits
		now  time.Time
		want bool
	}{
		{"no windows never hold", standing.Limits{}, at(13, 23, 0), false},
		{"before a window that crosses midnight", night, at(13, 21, 59), false},
		{"a window's start is held", night, at(13, 22, 0), true},
		{"the evening side of a window that crosses midnight", night, at(13, 23, 30), true},
		{"the morning side belongs to the day the window starts", night, at(14, 5, 59), true},
		{"a window's end is not held", night, at(14, 6, 0), false},
		{"the next evening is another weekday", night, at(14, 23, 0), false},
		{"the small hours before the listed weekday are another window", night, at(13, 3, 0), false},
		{"inside a same-day window", workday, at(16, 8, 0), true},
		{"just before a same-day window", workday, at(16, 7, 59), false},
		{"a same-day window's end is not held", workday, at(16, 19, 0), false},
		{"a weekday the window does not list", workday, at(14, 12, 0), false},
		{"any one of several windows holds", two, at(15, 12, 30), true},
	} {
		t.Run(c.name, func(t *testing.T) {
			if got := c.l.Held(c.now); got != c.want {
				t.Errorf("Held(%s) = %v, want %v", c.now.Format("Mon 15:04"), got, c.want)
			}
		})
	}
}

func TestCheckRates(t *testing.T) {
	work := standing.PresetLimits(standing.Work) // 2 an hour, 1 per sender a day, 2 at once
	for _, c := range []struct {
		name   string
		counts standing.Counts
		ok     bool
		reason string
	}{
		{"under every cap", standing.Counts{LaunchesLastHour: 1, SenderLaunchesToday: 0, WorkerShips: 1}, true, ""},
		{"the hourly cap is reached", standing.Counts{LaunchesLastHour: 2}, false, standing.ReasonPerHour},
		{"the sender's daily cap is reached", standing.Counts{SenderLaunchesToday: 1}, false, standing.ReasonPerSender},
		{"the concurrent cap is reached", standing.Counts{WorkerShips: 2}, false, standing.ReasonConcurrent},
	} {
		t.Run(c.name, func(t *testing.T) {
			got := work.CheckRates(c.counts)
			if got.OK != c.ok || got.Reason != c.reason {
				t.Errorf("CheckRates(%+v) = %+v, want OK=%v reason %q", c.counts, got, c.ok, c.reason)
			}
		})
	}
}

func TestDedupeHeld(t *testing.T) {
	live := []string{"sentry:123", "sentry:456"}
	if !standing.DedupeHeld(live, "sentry:456") {
		t.Error("a live ship holds sentry:456, but DedupeHeld said no")
	}
	if standing.DedupeHeld(live, "sentry:789") {
		t.Error("no live ship holds sentry:789, but DedupeHeld said yes")
	}
	if standing.DedupeHeld(live, "") {
		t.Error("an event with no dedupe_key was held by another")
	}
}

func TestRepoAndSenderAllowlists(t *testing.T) {
	l := standing.Limits{RepoAllowlist: []string{"acme/widget"}, SenderAllowlist: []string{"alice@acme.test"}}
	if !l.RepoAllowed("acme/widget") || l.RepoAllowed("acme/other") {
		t.Errorf("RepoAllowed: acme/widget = %v, acme/other = %v; want true, false", l.RepoAllowed("acme/widget"), l.RepoAllowed("acme/other"))
	}
	if !l.SenderAllowed("alice@acme.test") || l.SenderAllowed("mallory@evil.test") {
		t.Errorf("SenderAllowed: alice = %v, mallory = %v; want true, false", l.SenderAllowed("alice@acme.test"), l.SenderAllowed("mallory@evil.test"))
	}
	none := standing.Limits{}
	if !none.RepoAllowed("any/repo") {
		t.Error("an empty repo allowlist refused a repo, want any repo the fleet finds")
	}
	if none.SenderAllowed("alice@acme.test") {
		t.Error("an empty sender allowlist allowed a sender, want no one")
	}
}

func TestSkipFilters(t *testing.T) {
	l := standing.Limits{Skip: standing.Skip{
		Labels:     []standing.Scoped{{Value: "wontfix"}, {Value: "chore", Routine: "triage"}},
		Senders:    []standing.Scoped{{Value: "bot@ci.test", Routine: "triage"}},
		DedupeKeys: []standing.Scoped{{Value: "sentry:123"}},
	}}
	for _, c := range []struct {
		name string
		in   standing.SkipInput
		want bool
	}{
		{"a fleet-wide label", standing.SkipInput{Routine: "nightly", Labels: []string{"bug", "wontfix"}}, true},
		{"a label scoped to this routine", standing.SkipInput{Routine: "triage", Labels: []string{"chore"}}, true},
		{"a label scoped to another routine", standing.SkipInput{Routine: "nightly", Labels: []string{"chore"}}, false},
		{"a sender scoped to this routine", standing.SkipInput{Routine: "triage", Sender: "bot@ci.test"}, true},
		{"a sender scoped to another routine", standing.SkipInput{Routine: "nightly", Sender: "bot@ci.test"}, false},
		{"a muted dedupe_key", standing.SkipInput{Routine: "nightly", DedupeKey: "sentry:123"}, true},
		{"nothing matches", standing.SkipInput{Routine: "triage", Labels: []string{"bug"}, Sender: "alice@acme.test", DedupeKey: "sentry:9"}, false},
	} {
		t.Run(c.name, func(t *testing.T) {
			if got := l.Skipped(c.in); got != c.want {
				t.Errorf("Skipped(%+v) = %v, want %v", c.in, got, c.want)
			}
		})
	}
}

func TestWriteDenyBecomesEditWriteRulesAndASandboxList(t *testing.T) {
	l := standing.Limits{WriteDeny: []string{".github/workflows/**", "deploy/*.yml"}}
	want := standing.Denial{
		Rules:     []string{"Edit(.github/workflows/**)", "Write(.github/workflows/**)", "Edit(deploy/*.yml)", "Write(deploy/*.yml)"},
		DenyWrite: []string{".github/workflows/**", "deploy/*.yml"},
	}
	if got := l.WriteDenial(); !reflect.DeepEqual(got, want) {
		t.Errorf("WriteDenial = %+v, want %+v", got, want)
	}
}

func TestWallClockAndSpendCaps(t *testing.T) {
	work := standing.PresetLimits(standing.Work) // 60 minutes, $10 a day
	if work.WallClockExceeded(59 * time.Minute) {
		t.Error("59 minutes exceeded a 60 minute wall clock")
	}
	if !work.WallClockExceeded(61 * time.Minute) {
		t.Error("61 minutes did not exceed a 60 minute wall clock")
	}
	if work.DailySpendReached(9.99) {
		t.Error("$9.99 reached a $10 daily cap")
	}
	if !work.DailySpendReached(10) {
		t.Error("$10 did not reach a $10 daily cap")
	}
}
