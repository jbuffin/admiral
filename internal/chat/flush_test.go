package chat

import (
	"slices"
	"sync"
	"sync/atomic"
	"testing"
)

func u(id, text string) Message { return Message{ID: id, Kind: "message", Role: "user", Text: text} }
func a(id, text string) Message { return Message{ID: id, Kind: "message", Role: "assistant", Text: text} }

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

func TestFlushAnchor_HidesOnlyTheFlushTurn(t *testing.T) {
	an := flushAnchor{active: true, pre: "a1"}
	thread := []Message{u("u1", "hi"), a("a1", "hello"), u("f1", FlushPrompt), a("f2", "done")}
	wantIDs(t, "a turn still running", an.hide(thread), "u1", "a1")

	an.until, an.closed = "f2", true
	wantIDs(t, "a closed turn", an.hide(thread), "u1", "a1")
	wantIDs(t, "the next turn", an.hide(append(slices.Clone(thread), u("u2", "next"), a("a2", "ok"))), "u1", "a1", "u2", "a2")
}

func TestFlushAnchor_AnAbandonedFlushWithAQuestionShowsTheReplyAfterTheAnswer(t *testing.T) {
	an := flushAnchor{active: true, pre: "a1"}
	thread := []Message{a("a1", "hello"), u("f1", FlushPrompt), a("f2", "Let me ask first")}
	wantIDs(t, "before the question is seen", an.hide(thread), "a1")
	// The flush loop saw the question and gave up: the turn ends at its last message.
	an.until, an.closed = "f2", true
	thread = append(thread, a("f3", "Saved it, thanks"))
	wantIDs(t, "after the answer", an.hide(thread), "a1", "f3")
}

func TestFlushAnchor_StaysHiddenWhenThePromptSlidesOutOfTheWindow(t *testing.T) {
	an := flushAnchor{active: true, pre: "a1"}
	an.hide([]Message{a("a1", "hello"), u("f1", FlushPrompt), a("f2", "done")})
	// The reader's window moved on: the prompt is gone, its late reply is the head, and a user message follows.
	slid := []Message{a("f2", "done"), a("f3", "more"), u("u2", "next"), a("a2", "ok")}
	wantIDs(t, "an open turn", an.hide(slid), "u2", "a2")
	wantIDs(t, "once the user spoke again", an.hide(slid), "u2", "a2")
	if !an.closed || an.until != "f3" {
		t.Errorf("the anchor is %+v, want closed at f3", an)
	}
}

func TestFlushAnchor_AReplyAfterTheClearIsNotServed(t *testing.T) {
	an := flushAnchor{active: true, pre: "a1"}
	an.hide([]Message{a("a1", "hello"), u("f1", FlushPrompt)})
	// New chat timed out the flush and cleared: the reader restarts after the prompt, and the reply lands first.
	cleared := []Message{a("f2", "late reply")}
	wantIDs(t, "the late reply", an.hide(cleared))
	cleared = append(cleared, u("u3", "fresh"), a("a3", "hi"))
	wantIDs(t, "the fresh conversation", an.hide(cleared), "u3", "a3")
}

func TestFlushAnchor_AClosedTurnLeavesTheClearedChatAlone(t *testing.T) {
	an := flushAnchor{active: true, pre: "", from: "f1", until: "f2", closed: true}
	wantIDs(t, "after the clear", an.hide([]Message{a("a9", "fresh")}), "a9")
}

func TestFlushAnchor_SeesAPromptTheEngineerPartlyTyped(t *testing.T) {
	an := flushAnchor{active: true}
	an.until, an.closed = "f2", true
	wantIDs(t, "partial text before the paste", an.hide([]Message{u("f1", "half a thought "+FlushPrompt), a("f2", "done")}))
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
