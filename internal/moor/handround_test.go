package moor

import (
	"reflect"
	"strings"
	"testing"
	"time"
)

func ids(es []Event) []string {
	var out []string
	for _, e := range es {
		out = append(out, e.ID)
	}
	return out
}

func TestAHandRoundHoldsThePRsWakingEventsOldestFirstWhateverTheDebounce(t *testing.T) {
	viewer := wUser("jbuffin")
	st := wOpen()
	// R2 was submitted before R1, and the newest event is a minute old: DecideWake would still be waiting.
	st.Reviews = []Review{wRev("R1", viewer, "CHANGES_REQUESTED", "second", wNow.Add(-time.Minute)), wRev("R2", viewer, "COMMENTED", "first", wNow.Add(-30*time.Minute))}
	st.ThreadComments = []Comment{wCmt("C1", wBot("coderabbitai"), "nit", wNow.Add(-10*time.Minute))}
	in := wInput(st)
	if d := DecideWake(in); d.Wake {
		t.Fatal("the premise fails: DecideWake woke inside the debounce")
	}

	got, ok := DecideHandRound(in, wpr1)

	if !ok {
		t.Fatal("no feedback, want the three waking events")
	}
	if want := []string{"R2", "C1", "R1"}; !reflect.DeepEqual(ids(got.Events), want) {
		t.Errorf("events = %v, want %v, oldest first", ids(got.Events), want)
	}
	for _, body := range []string{"first", "nit", "second", wpr1} {
		if !strings.Contains(got.Feedback, body) {
			t.Errorf("feedback lacks %q:\n%s", body, got.Feedback)
		}
	}
}

func TestAHandRoundSeesWhatThePollsEventsSeeAndNothingElse(t *testing.T) {
	viewer := wUser("jbuffin")
	cursorAt := wT0.Add(20 * time.Minute)
	for _, c := range []struct {
		name  string
		build func() WakeInput
		want  []string // event IDs; nil means no feedback
	}{
		{"a review before the mooring, with no cursor", func() WakeInput {
			st := wOpen()
			st.Reviews = []Review{wRev("R1", viewer, "CHANGES_REQUESTED", "old", wT0.Add(-5*time.Minute))}
			return wInput(st)
		}, nil},
		{"a review at the mooring, with no cursor", func() WakeInput {
			st := wOpen()
			st.Reviews = []Review{wRev("R1", viewer, "CHANGES_REQUESTED", "now", wT0)}
			return wInput(st)
		}, []string{"R1"}},
		{"a review the cursor has passed", func() WakeInput {
			st := wOpen()
			st.Reviews = []Review{wRev("R1", viewer, "CHANGES_REQUESTED", "x", cursorAt)}
			in := wInput(st)
			in.State = WakeState{PRs: map[string]PRWake{wpr1: {Cursor: Cursor{At: cursorAt, IDs: []string{"R1"}}}}}
			return in
		}, nil},
		{"a review a second after the cursor", func() WakeInput {
			st := wOpen()
			st.Reviews = []Review{wRev("R1", viewer, "CHANGES_REQUESTED", "x", cursorAt), wRev("R2", viewer, "CHANGES_REQUESTED", "y", cursorAt.Add(time.Second))}
			in := wInput(st)
			in.State = WakeState{PRs: map[string]PRWake{wpr1: {Cursor: Cursor{At: cursorAt, IDs: []string{"R1"}}}}}
			return in
		}, []string{"R2"}},
		{"an approval and a pending review", func() WakeInput {
			st := wOpen()
			st.Reviews = []Review{wRev("R1", viewer, "APPROVED", "", wEv), wRev("R2", viewer, "PENDING", "draft", wEv)}
			return wInput(st)
		}, nil},
		{"a failing check the snapshot already holds", func() WakeInput {
			st := wOpen()
			st.Contexts = []CheckContext{wCI("CI / test", "FAILURE")}
			in := wInput(st)
			in.State = WakeState{PRs: map[string]PRWake{wpr1: {Snapshot: Snapshot{Head: "abc", Failing: []string{"CI / test"}}}}}
			return in
		}, nil},
		{"a conflict the snapshot already holds", func() WakeInput {
			st := wOpen()
			st.Mergeable = "CONFLICTING"
			in := wInput(st)
			in.State = WakeState{PRs: map[string]PRWake{wpr1: {Snapshot: Snapshot{Conflicting: true}}}}
			return in
		}, nil},
		{"only a stranger's review and comment", func() WakeInput {
			st := wOpen()
			st.Reviews = []Review{wRev("R1", wUser("mallory"), "CHANGES_REQUESTED", "x", wEv)}
			st.Comments = []Comment{wCmt("C1", wUser("mallory"), "hi", wEv)}
			return wInput(st)
		}, nil},
		{"a stranger's comment beside the viewer's review", func() WakeInput {
			st := wOpen()
			st.Reviews = []Review{wRev("R1", viewer, "CHANGES_REQUESTED", "x", wEv)}
			st.Comments = []Comment{wCmt("C1", wUser("mallory"), "hi", wEv)}
			return wInput(st)
		}, []string{"R1"}},
		{"a bot's PR comment", func() WakeInput {
			st := wOpen()
			st.Comments = []Comment{wCmt("C1", wBot("github-actions"), "preview deployed", wEv)}
			return wInput(st)
		}, nil},
		{"only the viewer's replies made while the round ran", func() WakeInput {
			roundStart := wT0.Add(time.Hour)
			st := wOpen()
			st.Comments = []Comment{wCmt("C1", viewer, "replying", roundStart.Add(10*time.Minute))}
			in := wInput(st)
			in.Record.MooredAt = roundStart.Add(20 * time.Minute)
			in.State = WakeState{PRs: map[string]PRWake{wpr1: {Cursor: Cursor{At: wT0}, RoundStart: roundStart}}}
			in.Now = wT0.Add(3 * time.Hour)
			return in
		}, nil},
		{"a viewer reply the round listed as dropped", func() WakeInput {
			st := wOpen()
			st.Comments = []Comment{wCmt("C1", viewer, "replying", wEv)}
			in := wInput(st)
			in.State = WakeState{PRs: map[string]PRWake{wpr1: {Dropped: []string{"C1"}}}}
			return in
		}, nil},
		{"a merged PR with a review", func() WakeInput {
			st := wOpen()
			st.State = "MERGED"
			st.Reviews = []Review{wRev("R1", viewer, "CHANGES_REQUESTED", "x", wEv)}
			return wInput(st)
		}, nil},
		{"a closed PR with a review", func() WakeInput {
			st := wOpen()
			st.State = "CLOSED"
			st.Reviews = []Review{wRev("R1", viewer, "CHANGES_REQUESTED", "x", wEv)}
			return wInput(st)
		}, nil},
		{"a PR no poll has answered", func() WakeInput {
			st := wOpen()
			st.Reviews = []Review{wRev("R1", viewer, "CHANGES_REQUESTED", "x", wEv)}
			in := wInput(st)
			in.Answers = map[string]Status{}
			return in
		}, nil},
	} {
		t.Run(c.name, func(t *testing.T) {
			got, ok := DecideHandRound(c.build(), wpr1)
			if ok != (c.want != nil) {
				t.Fatalf("feedback = %v (events %v), want %v", ok, ids(got.Events), c.want != nil)
			}
			if c.want != nil && !reflect.DeepEqual(ids(got.Events), c.want) {
				t.Errorf("events = %v, want %v", ids(got.Events), c.want)
			}
		})
	}
}

func TestAHandRoundHoldsAConditionTheFirstPollSawWithoutWaitingForIt(t *testing.T) {
	st := wOpen()
	st.Mergeable = "CONFLICTING"

	got, ok := DecideHandRound(wInput(st), wpr1)

	if !ok || len(got.Events) != 1 || got.Events[0].Kind != EventConflict {
		t.Fatalf("round = %+v, %v; want the conflict", got.Events, ok)
	}
	if ps := got.Next.PRs[wpr1]; !ps.Snapshot.Conflicting || len(ps.Seen) != 0 {
		t.Errorf("next state = %+v, want the conflict in the snapshot and its first-seen time cleared", ps)
	}
}

func TestAHandRoundForAPRThatTheRecordDoesNotCarryHasNoFeedback(t *testing.T) {
	st := wOpen()
	st.Reviews = []Review{wRev("R1", wUser("jbuffin"), "CHANGES_REQUESTED", "x", wEv)}
	in := wInput(st)
	in.Answers[wpr2] = st

	if got, ok := DecideHandRound(in, wpr2); ok {
		t.Errorf("round on a PR outside the record = %+v, want none", got)
	}
}

// What the automatic round saves and what Answer review saves are one state, so a PR answered by hand is not answered again.
func TestAHandRoundSavesTheStateDecideWakeSavesForTheSamePR(t *testing.T) {
	viewer := wUser("jbuffin")
	seven, nine := wOpen(), wOpen()
	seven.Head = "def"
	seven.Reviews = []Review{wRev("R7", viewer, "CHANGES_REQUESTED", "seven needs work", wEv)}
	seven.Contexts = []CheckContext{wCI("CI / lint", "SUCCESS")}
	nine.Reviews = []Review{wRev("R9", viewer, "CHANGES_REQUESTED", "nine needs work", wEv)}
	nine.Comments = []Comment{wCmt("C9", viewer, "replying mid-round", wT0.Add(-time.Minute))}
	in := WakeInput{
		Record:    Record{Name: "Nelson", Session: "nelson", PRs: []string{wpr1, wpr2}, MooredAt: wT0},
		Answers:   map[string]Status{wpr1: seven, wpr2: nine},
		Reviewers: []string{"alice"},
		Now:       wNow,
		State: WakeState{PRs: map[string]PRWake{
			wpr1: {Seen: map[string]time.Time{"conflict": wT0}, Snapshot: Snapshot{Head: "abc", Failing: []string{"CI / lint"}, Conflicting: true}},
			wpr2: {Cursor: Cursor{At: wT0.Add(-time.Hour)}, RoundStart: wT0.Add(-2 * time.Minute)},
		}},
	}
	auto := DecideWake(in)
	if !auto.Wake || auto.PR != wpr1 {
		t.Fatalf("the premise fails: DecideWake chose %q (Wake %v), want %q", auto.PR, auto.Wake, wpr1)
	}

	got, ok := DecideHandRound(in, wpr1)

	if !ok {
		t.Fatal("no feedback on the PR DecideWake chose")
	}
	if !reflect.DeepEqual(got.Events, auto.Events) {
		t.Errorf("events = %+v, want DecideWake's %+v", got.Events, auto.Events)
	}
	if got.Feedback != auto.Feedback {
		t.Errorf("feedback = %q, want DecideWake's %q", got.Feedback, auto.Feedback)
	}
	if !reflect.DeepEqual(got.Next, auto.Next) {
		t.Errorf("next state = %+v, want DecideWake's %+v", got.Next, auto.Next)
	}
}

func TestAHandRoundOnTheSecondPRAdvancesOnlyItsCursorAndOpensTheWindowOnBoth(t *testing.T) {
	viewer := wUser("jbuffin")
	seven, nine := wOpen(), wOpen()
	seven.Reviews = []Review{wRev("R7", viewer, "CHANGES_REQUESTED", "seven needs work", wEv)}
	nine.Reviews = []Review{wRev("R9", viewer, "CHANGES_REQUESTED", "nine needs work", wEv)}
	in := WakeInput{
		Record:  Record{Name: "Nelson", Session: "nelson", PRs: []string{wpr1, wpr2}, MooredAt: wT0},
		Answers: map[string]Status{wpr1: seven, wpr2: nine},
		Now:     wNow,
	}

	got, ok := DecideHandRound(in, wpr2)

	if !ok || !reflect.DeepEqual(ids(got.Events), []string{"R9"}) {
		t.Fatalf("events = %v (ok %v), want only R9", ids(got.Events), ok)
	}
	if strings.Contains(got.Feedback, "seven needs work") {
		t.Errorf("the feedback holds the other PR's review:\n%s", got.Feedback)
	}
	want := WakeState{PRs: map[string]PRWake{
		wpr1: {Cursor: Cursor{At: wT0}, RoundStart: wNow},
		wpr2: {Cursor: Cursor{At: wEv, IDs: []string{"R9"}}, RoundStart: wNow, Snapshot: Snapshot{Head: "abc"}},
	}}
	if !reflect.DeepEqual(got.Next, want) {
		t.Errorf("next state = %+v, want %+v: the lower PR keeps its feedback queued for the next round", got.Next, want)
	}
}

func TestAHandRoundDoesNotChangeTheInputState(t *testing.T) {
	st := wOpen()
	st.Reviews = []Review{wRev("R1", wUser("jbuffin"), "CHANGES_REQUESTED", "x", wEv)}
	in := wInput(st)
	in.State = WakeState{PRs: map[string]PRWake{wpr1: {Seen: map[string]time.Time{"conflict": wT0}}}}
	before := cloneState(in.State)

	if _, ok := DecideHandRound(in, wpr1); !ok {
		t.Fatal("no feedback")
	}

	if !reflect.DeepEqual(in.State, before) {
		t.Errorf("the input state changed to %+v from %+v", in.State, before)
	}
}

func TestTheFeedbackCountsAgreeWithTheHandRoundOfEachPR(t *testing.T) {
	viewer := wUser("jbuffin")
	st := wOpen()
	st.Reviews = []Review{wRev("R1", viewer, "CHANGES_REQUESTED", "fix", wNow.Add(-time.Minute))}
	st.ThreadComments = []Comment{wCmt("C1", wBot("coderabbitai"), "nit", wNow.Add(-2*time.Minute))}
	in := wInput(st)

	counts := FeedbackCounts(in)

	for _, pr := range in.Record.PRs {
		round, ok := DecideHandRound(in, pr)
		if got := counts[pr]; got != len(round.Events) || (got > 0) != ok {
			t.Errorf("%s: count %d, hand round %d events (ok %v)", pr, got, len(round.Events), ok)
		}
	}
	if counts[wpr1] != 2 {
		t.Errorf("counts = %v, want 2 for %s", counts, wpr1)
	}
}
