package ships

import (
	"reflect"
	"testing"
	"time"

	"github.com/jbuffin/admiral/internal/outcomes"
	"github.com/jbuffin/admiral/internal/sessionhost"
)

func events(recs []outcomes.Record) []string {
	out := []string{}
	for _, r := range recs {
		out = append(out, r.Event)
	}
	return out
}

func TestReasonOf(t *testing.T) {
	for _, c := range []struct{ state, reason, want string }{
		{"", "", "idle"},                      // no hook has spoken: the 10 s rule
		{"needs_you", "question", "question"}, //
		{"needs_you", "", "unknown"},          // the state without its reason
		{"needs_you", "has space", "unknown"}, // a planted reason
		{"needs_you", "permission:Bash", "permission:Bash"},
	} {
		got := reasonOf(sessionhost.Session{State: c.state, Reason: c.reason})
		if got != c.want {
			t.Errorf("state %q reason %q: got %q, want %q", c.state, c.reason, got, c.want)
		}
	}
}

// Stops are counted and timed, a reason that changes inside one stop is not a
// second stop, and a stop the pane's death ends has no underway.
func TestAdvanceFollowsStops(t *testing.T) {
	t0 := time.Date(2026, 1, 1, 10, 0, 0, 0, time.UTC)
	at := func(s int) time.Time { return t0.Add(time.Duration(s) * time.Second) }
	var all []outcomes.Record
	tl := tally{Status: StatusUnderway}
	step := func(st, reason string, s int) {
		var recs []outcomes.Record
		tl, recs = advance(tl, st, reason, at(s))
		all = append(all, recs...)
	}
	step(StatusUnderway, "", 1)
	step(StatusNeedsYou, "question", 2)
	step(StatusNeedsYou, "stop", 4) // the reason changed: still one stop
	step(StatusUnderway, "", 6)
	step(StatusNeedsYou, "idle", 10)
	step(StatusAnchored, "", 15) // died while it needed you

	if got, want := events(all), []string{"needs_you", "underway", "needs_you"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("records = %v, want %v", got, want)
	}
	if w := all[1].Fields["waited_s"]; w != 4.0 {
		t.Errorf("waited_s = %v, want 4", w)
	}
	if tl.Count != 2 || tl.Seconds != 9 || !tl.Since.IsZero() {
		t.Errorf("tally = %+v, want 2 stops of 9 s in all and none open", tl)
	}
}

// After a restart an open stop is neither written again nor lost: its start
// comes back from tmux and the first pass that sees it end writes underway.
func TestBaselineKeepsAnOpenStop(t *testing.T) {
	since := time.Date(2026, 1, 1, 10, 0, 0, 0, time.UTC)
	now := since.Add(30 * time.Second)
	sess := sessionhost.Session{State: "needs_you", Reason: "stop", Options: map[string]string{
		"needs-count": "2", "needs-s": "5.000", "stop-since": since.Format(time.RFC3339Nano)}}

	tl := baseline(sess, now)
	tl, recs := advance(tl, status(sess, now), reasonOf(sess), now)
	if len(recs) != 0 || tl.Count != 2 || !tl.Since.Equal(since) {
		t.Errorf("a restart in an open stop wrote %v and left %+v, want nothing and the stop kept", recs, tl)
	}

	// The stop ended while Admiral was stopped.
	sess.State = "working"
	tl = baseline(sess, now)
	tl, recs = advance(tl, status(sess, now), "", now)
	if got := events(recs); !reflect.DeepEqual(got, []string{"underway"}) || tl.Seconds != 35 {
		t.Errorf("got %v and %+v, want one underway and 35 s", got, tl)
	}
}

// A ship can set its own tally options to anything: a non-finite seconds value
// would make `ended` unmarshalable, so it counts as unset.
func TestBaselineIgnoresNonFiniteSeconds(t *testing.T) {
	for _, v := range []string{"NaN", "+Inf", "-Inf", "inf", "1.7e308"} {
		sess := sessionhost.Session{Options: map[string]string{sessionhost.OptNeedsSecs: v}}
		if got := baseline(sess, time.Now()).Seconds; got != 0 {
			t.Errorf("%s: Seconds = %v, want 0", v, got)
		}
	}
	sess := sessionhost.Session{Options: map[string]string{sessionhost.OptNeedsSecs: "12.500"}}
	if got := baseline(sess, time.Now()).Seconds; got != 12.5 {
		t.Errorf("a finite value: Seconds = %v, want 12.5", got)
	}
}
