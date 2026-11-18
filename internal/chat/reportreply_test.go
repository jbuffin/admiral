package chat

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

// A report turn's reply threads under its ship card in the chat log and is handed to the push once.

const (
	replyCard = "ship-calm-otter-2026-11-17T10:00:00Z-moored"
	replyTurn = "[Report: Calm Otter] REPORT-TURN-TEXT"
)

type replyRig struct {
	*turnRig
	path string
	mu   sync.Mutex
	got  []ReportReply
	n    int
}

func newReplyRig(t *testing.T) *replyRig {
	t.Helper()
	r := &replyRig{turnRig: newTurnRig(t, "claude")}
	r.path = filepath.Join(os.Getenv("CLAUDE_CONFIG_DIR"), "projects", "p", claudeSession+".jsonl")
	r.svc.OnReply = func(_ string, rep ReportReply) {
		r.mu.Lock()
		r.got = append(r.got, rep)
		r.mu.Unlock()
	}
	return r
}

func (r *replyRig) replies() []ReportReply {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]ReportReply(nil), r.got...)
}

// say appends a transcript line and returns the id the chat log will know it by.
func (r *replyRig) say(kind, text string) string {
	r.t.Helper()
	r.n++
	id := fmt.Sprintf("44444444-4444-4444-8444-%012d", r.n)
	f, err := os.OpenFile(r.path, os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		r.t.Fatal(err)
	}
	defer f.Close()
	if _, err := f.WriteString(claudeLine(r.t, kind, id, text)); err != nil {
		r.t.Fatal(err)
	}
	return id
}

// threads is every logged message id and the thread it carries.
func (r *replyRig) threads() map[string]string {
	r.t.Helper()
	snap, err := r.fc.refresh(true)
	if err != nil {
		r.t.Fatal(err)
	}
	out := map[string]string{}
	for _, m := range snap.Messages {
		out[m.ID] = m.Thread
	}
	return out
}

// sendReport queues the report turn, lets it be typed and plays the prompt hook; it returns the id of the turn's user line.
func (r *replyRig) sendReport() string {
	r.t.Helper()
	r.enqueue(replyCard, replyTurn)
	r.tickUntil("the report turn typed", r.typedOnce("REPORT-TURN-TEXT"))
	r.promptStarts()
	r.ticks(2)
	return r.say("user", replyTurn)
}

func (r *replyRig) wantThread(th map[string]string, id, want, what string) {
	r.t.Helper()
	got, ok := th[id]
	if !ok {
		r.t.Fatalf("%s: the chat log never recorded it", what)
	}
	if got != want {
		r.t.Errorf("%s: thread = %q, want %q", what, got, want)
	}
}

func TestThePastedReportTurnAndTheRepliesToItCarryTheShipCardId(t *testing.T) {
	r := newReplyRig(t)
	turn := r.sendReport()
	first := r.say("assistant", "Calm Otter is done: [PR 7](https://github.com/acme/api/pull/7)")
	second := r.say("assistant", "Want me to launch a follow-up?")
	th := r.threads()
	r.wantThread(th, turn, replyCard, "the pasted turn's user entry")
	r.wantThread(th, first, replyCard, "the first reply")
	r.wantThread(th, second, replyCard, "the second reply")
	r.wantThread(th, "11111111-1111-4111-8111-111111111111", "", "an entry from before the turn")
}

func TestANoticeInsideTheReportTurnIsNotMarkedAndDoesNotCutTheThread(t *testing.T) {
	r := newReplyRig(t)
	r.sendReport()
	first := r.say("assistant", "Checking the PR")
	notice := r.say("user", "<task-notification>\n<summary>subagent finished</summary>\n</task-notification>")
	second := r.say("assistant", "Calm Otter is done")
	th := r.threads()
	r.wantThread(th, first, replyCard, "the reply before the notice")
	r.wantThread(th, notice, "", "the notice")
	r.wantThread(th, second, replyCard, "the reply after the notice")
}

func TestAnEngineersMessageThatStartsWithReportIsNeverThreaded(t *testing.T) {
	r := newReplyRig(t)
	typed := r.say("user", "[Report: Calm Otter] please write me a report")
	reply := r.say("assistant", "Sure, here it is")
	th := r.threads()
	r.wantThread(th, typed, "", "the engineer's message")
	r.wantThread(th, reply, "", "the reply to the engineer's message")
	turn := r.sendReport()
	answer := r.say("assistant", "Calm Otter is done")
	th = r.threads()
	r.wantThread(th, typed, "", "the engineer's message once a real report turn ran")
	r.wantThread(th, turn, replyCard, "the pasted turn")
	r.wantThread(th, answer, replyCard, "the reply to the pasted turn")
}

func TestARereadTranscriptAddsNoDuplicatesAndChangesNoMarksAcrossARestart(t *testing.T) {
	r := newReplyRig(t)
	turn := r.sendReport()
	reply := r.say("assistant", "Calm Otter is done")
	before := r.threads()
	r.turnEnds()
	r.ticks(2)
	later := r.say("user", "thanks")
	for range 2 {
		after := r.threads()
		r.wantThread(after, turn, replyCard, "the turn after a re-read")
		r.wantThread(after, reply, replyCard, "the reply after a re-read")
		r.wantThread(after, later, "", "the engineer's message after the turn")
		if len(after) != len(before)+1 {
			t.Fatalf("a re-read left %d entries, want %d", len(after), len(before)+1)
		}
	}
	snap, err := r.restart().chat("work").refresh(true)
	if err != nil {
		t.Fatal(err)
	}
	marks := map[string]string{}
	for _, m := range snap.Messages {
		marks[m.ID] = m.Thread
	}
	r.wantThread(marks, turn, replyCard, "the turn after a restart")
	r.wantThread(marks, reply, replyCard, "the reply after a restart")
}

func TestTheThreadIsServedOnTheSessionEndpointAndInTheChatMessage(t *testing.T) {
	r := newReplyRig(t)
	turn := r.sendReport()
	r.say("assistant", "Calm Otter is done")
	r.threads()
	mux := http.NewServeMux()
	r.svc.Routes(mux)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest("GET", "/api/chat/work/session", nil))
	var body struct {
		Messages []struct {
			ID     string `json:"id"`
			Thread string `json:"thread"`
		} `json:"messages"`
	}
	if rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &body) != nil {
		t.Fatalf("GET session = %d %s", rec.Code, rec.Body)
	}
	served := map[string]string{}
	for _, m := range body.Messages {
		served[m.ID] = m.Thread
	}
	r.wantThread(served, turn, replyCard, "the turn on GET /api/chat/work/session")
	raw, _ := json.Marshal(wire{Type: "chat", Snapshot: r.fc.currentFresh()})
	var ws struct {
		Messages []struct {
			ID     string `json:"id"`
			Thread string `json:"thread"`
		} `json:"messages"`
	}
	if err := json.Unmarshal(raw, &ws); err != nil {
		t.Fatal(err)
	}
	onSocket := map[string]string{}
	for _, m := range ws.Messages {
		onSocket[m.ID] = m.Thread
	}
	r.wantThread(onSocket, turn, replyCard, "the turn on the chat socket message")
}

// repliesWithin waits for the turn's end to hand over a reply, or for window to pass.
func (r *replyRig) repliesWithin(window time.Duration) []ReportReply {
	for end := time.Now().Add(window); time.Now().Before(end); time.Sleep(50 * time.Millisecond) {
		r.svc.TurnTick()
		if len(r.replies()) > 0 {
			break
		}
	}
	return r.replies()
}

func TestATurnThatEndsWithAReplyHandsItOverOnceAndReadsTheTranscriptItself(t *testing.T) {
	r := newReplyRig(t)
	r.sendReport()
	first := r.say("assistant", "Calm Otter is done: [PR 7](https://github.com/acme/api/pull/7)")
	r.say("assistant", "Want me to launch a follow-up?")
	// Nothing has read the transcript since the reply was written: the end forces that read.
	r.turnEnds()
	got := r.repliesWithin(10 * time.Second)
	if len(got) != 1 {
		t.Fatalf("replies handed over = %d, want 1: %+v", len(got), got)
	}
	want := ReportReply{CardID: replyCard, MessageID: first, Text: "Calm Otter is done: [PR 7](https://github.com/acme/api/pull/7)"}
	if got[0] != want {
		t.Errorf("reply = %+v, want %+v", got[0], want)
	}
	r.hold("the reply was handed over again", func() bool { return len(r.replies()) != 1 })
}

func TestATurnThatEndsWithNoReplyHandsNothingOver(t *testing.T) {
	r := newReplyRig(t)
	turn := r.sendReport()
	r.turnEnds()
	r.wantThread(r.threads(), turn, replyCard, "the pasted turn")
	r.hold("a reply was handed over though AdmiralBot said nothing", func() bool { return len(r.replies()) != 0 })
}

func TestAnEngineersAnswerAfterTheTurnIsNotTheReportsReply(t *testing.T) {
	r := newReplyRig(t)
	turn := r.sendReport()
	r.turnEnds()
	r.ticks(2)
	asked := r.say("user", "what happened?")
	answer := r.say("assistant", "Here is what happened")
	th := r.threads()
	r.wantThread(th, turn, replyCard, "the pasted turn")
	r.wantThread(th, asked, "", "the engineer's question")
	r.wantThread(th, answer, "", "the answer to the engineer")
	r.hold("an answer to the engineer was handed over as the report's reply", func() bool { return len(r.replies()) != 0 })
}

func TestADroppedTurnHandsNothingOverEvenWithAReplyLogged(t *testing.T) {
	r := newReplyRig(t)
	r.sendReport()
	reply := r.say("assistant", "Calm Otter is done")
	r.wantThread(r.threads(), reply, replyCard, "a reply logged in a turn that is then dropped")
	r.fc.endTurn(replyCard, TurnDropped)
	r.hold("a dropped turn handed over a reply", func() bool { return len(r.replies()) != 0 })
}

func TestARepeatedEndHandsOverOnceEvenAfterTheQueueFileIsReloaded(t *testing.T) {
	r := newReplyRig(t)
	r.sendReport()
	r.say("assistant", "Calm Otter is done")
	r.threads()
	r.fc.endTurn(replyCard, TurnEndedNormally)
	if got := r.replies(); len(got) != 1 {
		t.Fatalf("replies after the end = %d, want 1", len(got))
	}
	r.fc.endTurn(replyCard, TurnEndedNormally)
	r.fc.tq = turnState{}
	r.fc.endTurn(replyCard, TurnEndedNormally)
	if got := r.replies(); len(got) != 1 {
		t.Errorf("replies after repeated ends and a reload = %d, want 1", len(got))
	}
}
