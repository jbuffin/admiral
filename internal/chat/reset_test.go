package chat

import (
	"os"
	"testing"
	"time"
)

func local(t *testing.T, name string) *time.Location {
	t.Helper()
	loc, err := time.LoadLocation(name)
	if err != nil {
		t.Skipf("no zone data for %s: %v", name, err)
	}
	return loc
}

var (
	short = 10 * time.Minute
	long  = 60 * time.Minute
)

// due is an input every rule's test starts from: the chat is idle at 14:00 with nothing else due, then each
// case sets what makes its rule fire and flips one input to show it stops.
func due(now time.Time) ResetInput {
	return ResetInput{
		Now: now, Running: true, State: "idle", IdleSince: now, Threshold: 150_000,
		LastReset: now.Add(-time.Hour), ContextSeen: now, ShortIdle: short, LongIdle: long, TurnSinceReset: true,
	}
}

func TestDueReset_ThresholdRule(t *testing.T) {
	now := time.Date(2026, 11, 12, 14, 0, 0, 0, time.UTC)
	fire := func(in ResetInput) ResetInput {
		in.HaveContext, in.Context, in.IdleSince = true, 150_001, now.Add(-short)
		return in
	}
	if got := DueReset(fire(due(now))); got != ResetThreshold {
		t.Fatalf("150001 tokens after 10 idle minutes = %q, want threshold", got)
	}
	for name, edit := range map[string]func(*ResetInput){
		"idle one second short of 10 minutes": func(in *ResetInput) { in.IdleSince = now.Add(-short + time.Second) },
		"context at the threshold":            func(in *ResetInput) { in.Context = 150_000 },
		"no context reading":                  func(in *ResetInput) { in.HaveContext = false },
		"a reading older than the last reset": func(in *ResetInput) { in.ContextSeen = in.LastReset.Add(-time.Second) },
		"the chat's threshold is never":       func(in *ResetInput) { in.Threshold = 0 },
		"no turn since the last reset":        func(in *ResetInput) { in.TurnSinceReset = false },
	} {
		in := fire(due(now))
		edit(&in)
		if got := DueReset(in); got != ResetNone {
			t.Errorf("%s: due %q, want none", name, got)
		}
	}
	in := fire(due(now))
	in.Threshold = 100_000
	in.Context = 100_001
	if got := DueReset(in); got != ResetThreshold {
		t.Errorf("a 100K threshold with 100001 tokens = %q, want threshold", got)
	}
}

func TestDueReset_IdleRule(t *testing.T) {
	now := time.Date(2026, 11, 12, 14, 0, 0, 0, time.UTC)
	fire := func(in ResetInput) ResetInput {
		// Under the threshold, so only the idle-hour rule can be the one that fires.
		in.HaveContext, in.Context, in.IdleSince, in.Threshold = true, 100_001, now.Add(-long), 300_000
		return in
	}
	if got := DueReset(fire(due(now))); got != ResetIdle {
		t.Fatalf("100001 tokens after an idle hour = %q, want idle", got)
	}
	for name, edit := range map[string]func(*ResetInput){
		"idle one second short of an hour":    func(in *ResetInput) { in.IdleSince = now.Add(-long + time.Second) },
		"context at 100000":                   func(in *ResetInput) { in.Context = 100_000 },
		"no context reading":                  func(in *ResetInput) { in.HaveContext = false },
		"a reading older than the last reset": func(in *ResetInput) { in.ContextSeen = in.LastReset.Add(-time.Second) },
		"no turn since the last reset":        func(in *ResetInput) { in.TurnSinceReset = false },
	} {
		in := fire(due(now))
		edit(&in)
		if got := DueReset(in); got != ResetNone {
			t.Errorf("%s: due %q, want none", name, got)
		}
	}
	// "never" is the engineer opting out of Admiral's resets, the idle hour's too.
	in := fire(due(now))
	in.Threshold = 0
	if got := DueReset(in); got != ResetNone {
		t.Errorf("threshold never with an idle hour: due %q, want none", got)
	}
}

func TestDueReset_DailyRule(t *testing.T) {
	ny := local(t, "America/New_York")
	at := func(h, m int) time.Time { return time.Date(2026, 11, 12, h, m, 0, 0, ny) }
	fire := func(now time.Time) ResetInput {
		in := due(now)
		in.LastReset, in.TurnSinceReset, in.IdleSince = at(0, 30), true, now.Add(-short)
		return in
	}
	if got := DueReset(fire(at(4, 0))); got != ResetDaily {
		t.Fatalf("at 04:00 local with a turn since the last reset = %q, want daily", got)
	}
	if got := DueReset(fire(at(9, 15))); got != ResetDaily {
		t.Errorf("at 09:15 local, nothing reset since midnight = %q, want daily", got)
	}
	// Quiet means idle: a turn that just ended is the engineer still talking.
	in := fire(at(9, 15))
	in.IdleSince = in.Now.Add(-time.Second)
	if got := DueReset(in); got != ResetNone {
		t.Errorf("at 09:15, idle for one second = %q, want none", got)
	}
	in.IdleSince = in.Now.Add(-short + time.Second)
	if got := DueReset(in); got != ResetNone {
		t.Errorf("at 09:15, idle one second short of the window = %q, want none", got)
	}
	in.IdleSince = in.Now.Add(-short)
	if got := DueReset(in); got != ResetDaily {
		t.Errorf("at 09:15, idle for the short window = %q, want daily", got)
	}
	for name, edit := range map[string]func(*ResetInput){
		"one minute before 04:00":       func(in *ResetInput) { in.Now = at(3, 59) },
		"reset exactly at 04:00":        func(in *ResetInput) { in.LastReset = at(4, 0) },
		"reset after 04:00":             func(in *ResetInput) { in.LastReset = at(5, 0) },
		"no turn since the last reset":  func(in *ResetInput) { in.TurnSinceReset = false },
		"the chat's threshold is never": func(in *ResetInput) { in.Threshold = 0 },
	} {
		in := fire(at(6, 0))
		edit(&in)
		if got := DueReset(in); got != ResetNone {
			t.Errorf("%s: due %q, want none", name, got)
		}
	}
	// 04:00 is the wall-clock hour in the chat's zone, so the 25-hour and 23-hour days still reset at 04:00.
	// New York's clocks went back on 2026-11-01 (01:00 happens twice) and forward on 2026-03-08 (02:00 is skipped).
	for _, day := range []struct {
		name string
		date [3]int
	}{{"the clocks go back", [3]int{2026, 11, 1}}, {"the clocks go forward", [3]int{2026, 3, 8}}} {
		d := func(h, m int) time.Time {
			return time.Date(day.date[0], time.Month(day.date[1]), day.date[2], h, m, 0, 0, ny)
		}
		in := fire(d(4, 0))
		in.LastReset = d(0, 30)
		if got := DueReset(in); got != ResetDaily {
			t.Errorf("%s: at 04:00 local = %q, want daily", day.name, got)
		}
		in = fire(d(3, 59))
		in.LastReset = d(0, 30)
		if got := DueReset(in); got != ResetNone {
			t.Errorf("%s: at 03:59 local = %q, want none", day.name, got)
		}
		// A reset at 04:00 sharp on that day stays the day's reset.
		in = fire(d(9, 0))
		in.LastReset = d(4, 0)
		if got := DueReset(in); got != ResetNone {
			t.Errorf("%s: reset at 04:00, asked at 09:00 = %q, want none", day.name, got)
		}
	}
}

// Whatever a rule would say, only an idle, running chat with no question open is reset.
func TestDueReset_OnlyAnIdleChatIsReset(t *testing.T) {
	now := time.Date(2026, 11, 12, 14, 0, 0, 0, time.UTC)
	rules := map[ResetRule]func(*ResetInput){
		ResetThreshold: func(in *ResetInput) {
			in.HaveContext, in.Context, in.IdleSince = true, 150_001, now.Add(-short)
		},
		ResetIdle: func(in *ResetInput) {
			in.HaveContext, in.Context, in.IdleSince, in.Threshold = true, 100_001, now.Add(-long), 300_000
		},
		ResetDaily: func(in *ResetInput) {
			in.Now = time.Date(2026, 11, 12, 5, 0, 0, 0, time.UTC)
			in.LastReset, in.IdleSince = time.Date(2026, 11, 11, 20, 0, 0, 0, time.UTC), in.Now.Add(-short)
		},
	}
	blocks := map[string]func(*ResetInput){
		"a turn is running":            func(in *ResetInput) { in.State = "working" },
		"a question waits":             func(in *ResetInput) { in.State, in.QuestionWaiting = "waiting", true },
		"a question waits, state idle": func(in *ResetInput) { in.QuestionWaiting = true },
		"the chat is not running":      func(in *ResetInput) { in.Running, in.State = false, "" },
	}
	for rule, set := range rules {
		in := due(now)
		set(&in)
		if got := DueReset(in); got != rule {
			t.Fatalf("%s rule's input = %q, want %q", rule, got, rule)
		}
		for name, block := range blocks {
			in := due(now)
			set(&in)
			block(&in)
			if got := DueReset(in); got != ResetNone {
				t.Errorf("%s rule while %s: due %q, want none", rule, name, got)
			}
		}
	}
}

func TestResetIdleWindows(t *testing.T) {
	t.Setenv("ADMIRAL_CHAT_RESET_IDLE", "")
	if s, l := ResetIdleWindows(); s != 10*time.Minute || l != time.Hour {
		t.Errorf("default windows = %v and %v, want 10m and 1h", s, l)
	}
	t.Setenv("ADMIRAL_CHAT_RESET_IDLE", "2s")
	if s, l := ResetIdleWindows(); s != 2*time.Second || l != 12*time.Second {
		t.Errorf("windows with ADMIRAL_CHAT_RESET_IDLE=2s = %v and %v, want 2s and 12s", s, l)
	}
}

func TestContextTokens(t *testing.T) {
	kept, err := os.ReadFile("../usage/testdata/claude-statusline-kept.json")
	if err != nil {
		t.Fatal(err)
	}
	got, seen, ok := ContextTokens(kept)
	// current_usage: 8500 input + 5000 cache creation + 2000 cache read; output tokens are not context.
	if !ok || got != 15_500 {
		t.Errorf("ContextTokens(kept sample) = %d, %v, want 15500, true", got, ok)
	}
	if want := time.Date(2026, 11, 12, 10, 0, 0, 0, time.UTC); !seen.Equal(want) {
		t.Errorf("seen = %v, want the line's time %v", seen, want)
	}
	for name, line := range map[string]string{
		"no current_usage":  `{"time":"2026-11-12T10:00:00Z","context_window":{"total_input_tokens":15234,"total_output_tokens":4521}}`,
		"no context_window": `{"time":"2026-11-12T10:00:00Z","session_id":"s1","cost":{"total_cost_usd":1}}`,
		"not json":          `nope`,
	} {
		if n, _, ok := ContextTokens([]byte(line)); ok {
			t.Errorf("%s: ContextTokens = %d, true, want no reading", name, n)
		}
	}
}

func TestMarkResetCarriesItsText(t *testing.T) {
	fc := newBriefRig(t).chat("home")
	fc.markReset(MarkerRefreshed)
	var got []string
	for _, m := range fc.recent() {
		if m.Kind == "reset" {
			got = append(got, m.Text)
		}
	}
	if len(got) != 1 || got[0] != "Context refreshed" {
		t.Errorf("reset entries after markReset(%q) = %q, want one reading Context refreshed", MarkerRefreshed, got)
	}
}
