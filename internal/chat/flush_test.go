package chat

import (
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
)

func u(id, text string) Message { return Message{ID: id, Kind: "message", Role: "user", Text: text} }
func a(id, text string) Message {
	return Message{ID: id, Kind: "message", Role: "assistant", Text: text}
}

func ids(ms []Message) []string {
	out := []string{}
	for _, m := range ms {
		out = append(out, m.ID)
	}
	return out
}

func wantIDs(t *testing.T, what string, got []Message, want ...string) {
	t.Helper()
	if !slices.Equal(ids(got), want) {
		t.Errorf("%s: shows %v, want %v", what, ids(got), want)
	}
}

func anchors(an ...flushAnchor) *flushAnchors { as := flushAnchors(an); return &as }

func TestFlushAnchor_HidesOnlyTheFlushTurn(t *testing.T) {
	as := anchors(flushAnchor{pre: "a1"})
	thread := []Message{u("u1", "hi"), a("a1", "hello"), u("f1", FlushPrompt), a("f2", "done")}
	wantIDs(t, "a turn still running", as.hide(thread), "u1", "a1")

	(*as)[0].until, (*as)[0].closed = "f2", true
	wantIDs(t, "a closed turn", as.hide(thread), "u1", "a1")
	wantIDs(t, "the next turn", as.hide(append(slices.Clone(thread), u("u2", "next"), a("a2", "ok"))), "u1", "a1", "u2", "a2")
}

func TestFlushAnchor_AnAbandonedFlushWithAQuestionShowsTheReplyAfterTheAnswer(t *testing.T) {
	as := anchors(flushAnchor{pre: "a1"})
	thread := []Message{a("a1", "hello"), u("f1", FlushPrompt), a("f2", "Let me ask first")}
	wantIDs(t, "before the question is seen", as.hide(thread), "a1")
	// The flush loop saw the question and gave up: the turn ends at its last message.
	(*as)[0].until, (*as)[0].closed = "f2", true
	thread = append(thread, a("f3", "Saved it, thanks"))
	wantIDs(t, "after the answer", as.hide(thread), "a1", "f3")
}

func TestFlushAnchor_StaysHiddenWhenThePromptSlidesOutOfTheWindow(t *testing.T) {
	as := anchors(flushAnchor{pre: "a1"})
	as.hide([]Message{a("a1", "hello"), u("f1", FlushPrompt), a("f2", "done")})
	// The reader's window moved on: the prompt is gone, its late reply is the head, and a user message follows.
	slid := []Message{a("f2", "done"), a("f3", "more"), u("u2", "next"), a("a2", "ok")}
	wantIDs(t, "an open turn", as.hide(slid), "u2", "a2")
	wantIDs(t, "once the user spoke again", as.hide(slid), "u2", "a2")
}

func TestFlushAnchor_AReplyAfterTheClearIsNotServed(t *testing.T) {
	as := anchors(flushAnchor{pre: "a1"})
	as.hide([]Message{a("a1", "hello"), u("f1", FlushPrompt)})
	// New chat timed out the flush and cleared: the reader restarts after the prompt, and the reply lands first.
	cleared := []Message{a("f2", "late reply")}
	wantIDs(t, "the late reply", as.hide(cleared))
	cleared = append(cleared, u("u3", "fresh"), a("a3", "hi"))
	wantIDs(t, "the fresh conversation", as.hide(cleared), "u3", "a3")
}

// The turn's end reaches Admiral before the transcript's tail does: the reply is read after the marker.
func TestFlushAnchor_ALateReplyAfterTheTurnEndedAndTheClearIsHidden(t *testing.T) {
	as := anchors(flushAnchor{pre: "a1", from: "f1"})
	// The flush loop saw the turn end with only the prompt read, then Admiral cleared.
	wantIDs(t, "the prompt only", as.hide([]Message{a("a1", "hello"), u("f1", FlushPrompt)}), "a1")
	late := []Message{a("f2", "done.")}
	wantIDs(t, "the late reply", as.hide(late))
	wantIDs(t, "the engineer's next message", as.hide(append(slices.Clone(late), u("u2", "hi"), a("a2", "yes"))), "u2", "a2")
	wantIDs(t, "what follows", as.hide([]Message{u("u2", "hi"), a("a2", "yes"), a("a3", "more")}), "u2", "a2", "a3")
}

func TestFlushAnchor_AClosedTurnLeavesTheClearedChatAlone(t *testing.T) {
	as := anchors(flushAnchor{from: "f1", until: "f2", closed: true})
	wantIDs(t, "after the clear", as.hide([]Message{a("a9", "fresh")}), "a9")
	if len(*as) != 0 {
		t.Errorf("%d anchors left for a turn that is gone, want none", len(*as))
	}
}

func TestFlushAnchor_SeesAPromptTheEngineerPartlyTyped(t *testing.T) {
	as := anchors(flushAnchor{until: "f2", closed: true})
	wantIDs(t, "partial text before the paste", as.hide([]Message{u("f1", "half a thought "+FlushPrompt), a("f2", "done")}))
}

// A retried flush must not unhide the one abandoned before it.
func TestFlushAnchor_ARetryKeepsTheAbandonedFlushHidden(t *testing.T) {
	first := []Message{a("a1", "hello"), u("f1", FlushPrompt), a("f2", "Let me ask first")}
	for name, c := range map[string]struct {
		abandoned flushAnchor
		want      []string
	}{
		// What the abandoned turn said after the answer shows when it was closed by the question, not after a timeout.
		"closed by a question": {flushAnchor{pre: "a1", from: "f1", until: "f2", closed: true}, []string{"a1", "f3", "u2", "a2"}},
		"open after a timeout": {flushAnchor{pre: "a1", from: "f1"}, []string{"a1", "u2", "a2"}},
	} {
		abandoned := c.abandoned
		as := anchors(abandoned)
		wantIDs(t, name+": the first flush", as.hide(first), "a1")
		// The retry is pasted after the engineer spoke again.
		thread := append(slices.Clone(first), a("f3", "Saved it"), u("u2", "ok"), a("a2", "yes"))
		*as = append(*as, flushAnchor{pre: "a2"})
		thread = append(thread, u("f4", FlushPrompt), a("f5", "done"))
		wantIDs(t, name+": the retry", as.hide(thread), c.want...)
		// Even when the retry's anchor lost its place (the window slid past its pre), it takes its own prompt.
		as = anchors(abandoned, flushAnchor{pre: "gone"})
		wantIDs(t, name+": a retry that lost its place", as.hide(thread), c.want...)
	}
}

// One reset runs per fleet at a time: a claim holds until it is released, and it is the flag a send is refused by.
func TestClaimReset_OnlyOneAtATime(t *testing.T) {
	var s fleetChat
	var won atomic.Int32
	var wg sync.WaitGroup
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if s.claimReset() {
				won.Add(1)
			}
		}()
	}
	wg.Wait()
	if won.Load() != 1 {
		t.Fatalf("%d resets claimed the fleet, want 1", won.Load())
	}
	if !s.resetting.Load() {
		t.Error("a send would not be refused while a reset is claimed")
	}
	s.releaseReset()
	if s.resetting.Load() || !s.claimReset() {
		t.Error("the fleet cannot be claimed again after the release")
	}
}

func TestOnlyAClaudeChatIsFlushedAndResetOnASchedule(t *testing.T) {
	for harness, want := range map[string]bool{"claude": true, "codex": false, "copilot": false, "": false} {
		if got := flushesBeforeReset(harness); got != want {
			t.Errorf("flushesBeforeReset(%q) = %v, want %v", harness, got, want)
		}
	}
}

func TestTheFlushPromptAsksForAnyPromiseNotYetRecorded(t *testing.T) {
	for _, want := range []string{"save_note", "make_commitment"} {
		if !strings.Contains(FlushPrompt, want) {
			t.Errorf("FlushPrompt = %q, want it to name %s", FlushPrompt, want)
		}
	}
	if !strings.Contains(FlushPrompt, "asked") {
		t.Errorf("FlushPrompt = %q, want it limited to what the engineer asked for", FlushPrompt)
	}
}
