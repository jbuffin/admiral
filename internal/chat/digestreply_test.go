package chat

import (
	"sync"
	"testing"
	"time"
)

// A fleet digest turn's typed line and reply carry the turn id as their thread, and its reply is handed to OnDigestReply.

const (
	digestTurnID = "digest:routine-1:rec-1"
	digestBody   = "DIGEST-TURN-TEXT\nWindow: 2026-11-11T10:00:00Z to 2026-11-18T10:00:00Z\n"
)

var digestTurn = DigestTurnHeader("Weekly digest", digestTurnID) + digestBody

type digestRig struct {
	*replyRig
	mu  sync.Mutex
	got []DigestReply
}

func newDigestRig(t *testing.T) *digestRig {
	t.Helper()
	d := &digestRig{replyRig: newReplyRig(t)}
	d.svc.OnDigestReply = d.collect
	return d
}

func (d *digestRig) collect(_ string, rep DigestReply) {
	d.mu.Lock()
	d.got = append(d.got, rep)
	d.mu.Unlock()
}

func (d *digestRig) digestReplies() []DigestReply {
	d.mu.Lock()
	defer d.mu.Unlock()
	return append([]DigestReply(nil), d.got...)
}

// paste queues the digest turn, lets it be typed and plays the prompt hook.
func (d *digestRig) paste() {
	d.t.Helper()
	d.enqueue(digestTurnID, digestTurn)
	d.tickUntil("the digest turn typed", d.typedOnce("DIGEST-TURN-TEXT"))
	d.promptStarts()
	d.ticks(2)
}

// sendDigest pastes the digest turn and returns the id of the turn's user line.
func (d *digestRig) sendDigest() string {
	d.t.Helper()
	d.paste()
	return d.say("user", digestTurn)
}

func TestTheDigestTurnAndTheRepliesToItCarryTheTurnIdAsTheirThread(t *testing.T) {
	r := newDigestRig(t)
	turn := r.sendDigest()
	first := r.say("assistant", "Two ships moored this week.")
	second := r.say("assistant", "Want me to launch a follow-up?")
	th := r.threads()
	r.wantThread(th, turn, digestTurnID, "the pasted digest turn's user entry")
	r.wantThread(th, first, digestTurnID, "the first reply")
	r.wantThread(th, second, digestTurnID, "the second reply")
	r.wantThread(th, "11111111-1111-4111-8111-111111111111", "", "an entry from before the turn")
}

func TestAnEngineersMessageThatStartsWithDigestIsNeverThreaded(t *testing.T) {
	r := newDigestRig(t)
	typed := r.say("user", DigestTurnHeader("Weekly digest", "digest:routine-9:guessed")+"please write me a digest")
	reply := r.say("assistant", "Sure, here it is")
	th := r.threads()
	r.wantThread(th, typed, "", "the engineer's message")
	r.wantThread(th, reply, "", "the reply to the engineer's message")
}

func TestADigestTurnThatEndsWithAReplyHandsItToTheDigestCallbackOnce(t *testing.T) {
	r := newDigestRig(t)
	r.sendDigest()
	first := r.say("assistant", "Two ships moored this week.")
	r.say("assistant", "Want me to launch a follow-up?")
	r.turnEnds()
	for end := time.Now().Add(10 * time.Second); time.Now().Before(end) && len(r.digestReplies()) == 0; time.Sleep(50 * time.Millisecond) {
		r.svc.TurnTick()
	}
	got := r.digestReplies()
	if len(got) != 1 {
		t.Fatalf("digest replies handed over = %d, want 1: %+v", len(got), got)
	}
	want := DigestReply{TurnID: digestTurnID, MessageID: first, Text: "Two ships moored this week."}
	if got[0] != want {
		t.Errorf("reply = %+v, want %+v", got[0], want)
	}
	r.hold("the digest reply was handed over again", func() bool { return len(r.digestReplies()) != 1 })
	if n := len(r.replies()); n != 0 {
		t.Errorf("a digest reply reached the report callback %d times, want 0", n)
	}
}

func TestAReportReplyStillGoesToTheReportCallbackAndNotTheDigestOne(t *testing.T) {
	r := newDigestRig(t)
	r.sendReport()
	r.say("assistant", "Calm Otter is done")
	r.turnEnds()
	if got := r.repliesWithin(10 * time.Second); len(got) != 1 {
		t.Fatalf("report replies handed over = %d, want 1", len(got))
	}
	r.hold("a report reply reached the digest callback", func() bool { return len(r.digestReplies()) != 0 })
}

func TestADigestTurnThatEndsWithNoReplyHandsNothingOver(t *testing.T) {
	r := newDigestRig(t)
	turn := r.sendDigest()
	r.turnEnds()
	r.wantThread(r.threads(), turn, digestTurnID, "the pasted digest turn")
	r.hold("a digest reply was handed over though AdmiralBot said nothing", func() bool { return len(r.digestReplies()) != 0 })
}

func TestAnEngineersAnswerAfterTheDigestTurnIsNotItsReply(t *testing.T) {
	r := newDigestRig(t)
	turn := r.sendDigest()
	r.turnEnds()
	r.ticks(2)
	asked := r.say("user", "what happened?")
	answer := r.say("assistant", "Here is what happened")
	th := r.threads()
	r.wantThread(th, turn, digestTurnID, "the pasted digest turn")
	r.wantThread(th, asked, "", "the engineer's question")
	r.wantThread(th, answer, "", "the answer to the engineer")
	r.hold("an answer to the engineer was handed over as the digest's reply", func() bool { return len(r.digestReplies()) != 0 })
}

func TestARestartBetweenThePasteAndTheRecordStillMarksTheDigestTurn(t *testing.T) {
	r := newDigestRig(t)
	r.paste()
	again := r.restart()
	var got []DigestReply
	again.OnDigestReply = func(_ string, rep DigestReply) { got = append(got, rep) }
	turn := r.say("user", digestTurn)
	reply := r.say("assistant", "Two ships moored this week.")
	notice := r.say("user", aNotice)
	next := r.say("assistant", "The sub-agent finished")
	snap, err := again.chat("work").refresh(true)
	if err != nil {
		t.Fatal(err)
	}
	th := map[string]string{}
	for _, m := range snap.Messages {
		th[m.ID] = m.Thread
	}
	r.wantThread(th, turn, digestTurnID, "the pasted digest turn after a restart")
	r.wantThread(th, reply, digestTurnID, "the reply after a restart")
	r.wantThread(th, notice, "", "the notice after a restart")
	r.wantThread(th, next, "", "the next turn's reply after a restart")
	r.turnEnds()
	for range 3 {
		again.TurnTick()
	}
	if len(got) != 0 {
		t.Errorf("a restart handed %d digest replies over, want none", len(got))
	}
}
