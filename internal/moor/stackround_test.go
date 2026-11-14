package moor

import (
	"reflect"
	"testing"
	"time"
)

func stackInput() WakeInput {
	viewer := wUser("jbuffin")
	a, b := wOpen(), wOpen()
	a.Reviews = []Review{wRev("R1", viewer, "CHANGES_REQUESTED", "one", wEv)}
	b.Reviews = []Review{wRev("R2", viewer, "COMMENTED", "two", wEv.Add(time.Minute))}
	in := wInput(a)
	in.Record.PRs = []string{wpr1, wpr2}
	in.Answers = map[string]Status{wpr1: a, wpr2: b}
	return in
}

func TestAStackRoundAdvancesEveryOpenPRsCursorPastItsFeedback(t *testing.T) {
	next, ok := DecideStackRound(stackInput())

	if !ok {
		t.Fatal("no round, want one: both PRs have feedback")
	}
	if c := next.PRs[wpr1].Cursor; !c.At.Equal(wEv) || !reflect.DeepEqual(c.IDs, []string{"R1"}) {
		t.Errorf("first PR's cursor = %+v, want it at R1", c)
	}
	if c := next.PRs[wpr2].Cursor; !c.At.Equal(wEv.Add(time.Minute)) || !reflect.DeepEqual(c.IDs, []string{"R2"}) {
		t.Errorf("second PR's cursor = %+v, want it at R2", c)
	}
}

func TestAStackRoundGivesEachPRWhatAHandRoundOnItGives(t *testing.T) {
	in := stackInput()
	in.State = WakeState{PRs: map[string]PRWake{wpr2: {Seen: map[string]time.Time{"conflict": wT0}}}}

	next, ok := DecideStackRound(in)

	if !ok {
		t.Fatal("no round")
	}
	for _, pr := range []string{wpr1, wpr2} {
		hand, ok := DecideHandRound(in, pr)
		if !ok {
			t.Fatalf("no hand round on %s", pr)
		}
		if got, want := next.PRs[pr], hand.Next.PRs[pr]; !reflect.DeepEqual(got, want) {
			t.Errorf("%s: stack round gives %+v, hand round gives %+v", pr, got, want)
		}
	}
}

func TestAStackRoundLeavesMergedAndClosedPRsAlone(t *testing.T) {
	in := stackInput()
	merged := wOpen()
	merged.State = "MERGED"
	merged.Reviews = []Review{wRev("R3", wUser("jbuffin"), "CHANGES_REQUESTED", "old", wEv)}
	in.Record.PRs = []string{"https://github.com/acme/widgets/pull/5", wpr1, wpr2}
	in.Answers["https://github.com/acme/widgets/pull/5"] = merged

	next, ok := DecideStackRound(in)

	if !ok {
		t.Fatal("no round: a merged layer doesn't count against the open ones")
	}
	if ps, has := next.PRs["https://github.com/acme/widgets/pull/5"]; has {
		t.Errorf("merged PR got state %+v, want none", ps)
	}
	if next.PRs[wpr1].Cursor.At.IsZero() || next.PRs[wpr2].Cursor.At.IsZero() {
		t.Error("an open PR's cursor was not advanced")
	}
}

func TestAStackRoundReportsNoneWhenAnOpenPRHasNoFeedback(t *testing.T) {
	in := stackInput()
	in.Answers[wpr2] = wOpen()

	if _, ok := DecideStackRound(in); ok {
		t.Error("round reported, want none: the second PR has no feedback")
	}
}

func TestRoundsLeaveTheirInputsCursorIDsAlone(t *testing.T) {
	spare := make([]string, 1, 4)
	spare[0] = "R0"
	build := func() WakeInput {
		in := stackInput()
		in.Record.MooredAt = wT0
		in.State = WakeState{PRs: map[string]PRWake{
			wpr1: {Cursor: Cursor{At: wEv, IDs: spare}},
			wpr2: {Cursor: Cursor{At: wT0.Add(-time.Hour)}},
		}}
		// R1 lands at the cursor's own instant, so advance appends to its IDs.
		in.Answers[wpr1] = func() Status {
			s := wOpen()
			s.Reviews = []Review{wRev("R1", wUser("jbuffin"), "CHANGES_REQUESTED", "one", wEv)}
			return s
		}()
		return in
	}
	for name, run := range map[string]func(WakeInput){
		"a hand round":  func(in WakeInput) { DecideHandRound(in, wpr1) },
		"a stack round": func(in WakeInput) { DecideStackRound(in) },
	} {
		t.Run(name, func(t *testing.T) {
			spare = spare[:1]
			spare[:4][1] = ""
			run(build())
			if got := spare[:4][1]; got != "" {
				t.Errorf("the input's IDs backing array was written: %q", got)
			}
			if len(build().State.PRs[wpr1].Cursor.IDs) != 1 {
				t.Error("input cursor IDs changed length")
			}
		})
	}
}
