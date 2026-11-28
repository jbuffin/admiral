package chat

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/jbuffin/admiral/internal/server"
)

// A report turn's reply threads under its ship card in the chat log and is handed to the push once.

const (
	replyCard = "ship-calm-otter-2026-11-17T10:00:00Z-moored"
	replyTurn = "[Report: Calm Otter] REPORT-TURN-TEXT\n\ncard: " + replyCard
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
	r.path = filepath.Join(r.fc.loginDirFor("claude"), "projects", "p", claudeSession+".jsonl")
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
	if _, err := f.WriteString(claudeLineAt(r.t, kind, id, text, time.Now().UTC().Format(time.RFC3339Nano))); err != nil {
		r.t.Fatal(err)
	}
	return id
}

// sayAt is say with the line stamped at.
func (r *replyRig) sayAt(kind, text string, at time.Time) string {
	r.t.Helper()
	r.n++
	id := fmt.Sprintf("44444444-4444-4444-8444-%012d", r.n)
	f, err := os.OpenFile(r.path, os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		r.t.Fatal(err)
	}
	defer f.Close()
	if _, err := f.WriteString(claudeLineAt(r.t, kind, id, text, at.UTC().Format(time.RFC3339Nano))); err != nil {
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
	t.Parallel()
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
	t.Parallel()
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
	t.Parallel()
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
	t.Parallel()
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
	t.Parallel()
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
	onSocket := r.socketThreads()
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
	t.Parallel()
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
	t.Parallel()
	r := newReplyRig(t)
	turn := r.sendReport()
	r.turnEnds()
	r.wantThread(r.threads(), turn, replyCard, "the pasted turn")
	r.hold("a reply was handed over though AdmiralBot said nothing", func() bool { return len(r.replies()) != 0 })
}

func TestAnEngineersAnswerAfterTheTurnIsNotTheReportsReply(t *testing.T) {
	t.Parallel()
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
	t.Parallel()
	r := newReplyRig(t)
	r.sendReport()
	reply := r.say("assistant", "Calm Otter is done")
	r.wantThread(r.threads(), reply, replyCard, "a reply logged in a turn that is then dropped")
	r.sessionGoesAway()
	r.hold("a dropped turn handed over a reply", func() bool { return len(r.replies()) != 0 })
}

func TestARepeatedEndHandsOverOnceEvenAfterTheQueueFileIsReloaded(t *testing.T) {
	t.Parallel()
	r := newReplyRig(t)
	r.sendReport()
	r.say("assistant", "Calm Otter is done")
	r.threads()
	r.fc.endTurn(replyCard, TurnEndedNormally)
	if got := r.replies(); len(got) != 1 {
		t.Fatalf("replies after the end = %d, want 1", len(got))
	}
	r.fc.endTurn(replyCard, TurnEndedNormally)
	again := r.restart()
	again.OnReply = r.svc.OnReply
	again.chat("work").endTurn(replyCard, TurnEndedNormally)
	if got := r.replies(); len(got) != 1 {
		t.Errorf("replies after repeated ends and a restart = %d, want 1", len(got))
	}
}

func TestAnEngineersMessageDuringTheTurnEndsTheThread(t *testing.T) {
	t.Parallel()
	r := newReplyRig(t)
	r.sendReport()
	before := r.say("assistant", "Checking the PR")
	asked := r.say("user", "stop, do something else")
	after := r.say("assistant", "Sure")
	th := r.threads()
	r.wantThread(th, before, replyCard, "the reply before the engineer spoke")
	r.wantThread(th, asked, "", "the engineer's message")
	r.wantThread(th, after, "", "the reply after the engineer spoke")
}

func TestAReplyLoggedAfterTheTurnEndsIsThreadedButPushesNothing(t *testing.T) {
	t.Parallel()
	r := newReplyRig(t)
	turn := r.sendReport()
	r.turnEnds()
	r.ticks(2)
	late := r.say("assistant", "Calm Otter is done")
	r.wantThread(r.threads(), late, replyCard, "a reply logged after the end")
	r.wantThread(r.threads(), turn, replyCard, "the pasted turn")
	r.hold("a reply logged after the end pushed late", func() bool { return len(r.replies()) != 0 })
}

// sessionGoesAway kills the chat's tmux session, the way a crash does, and lets the turn queue see it.
func (r *replyRig) sessionGoesAway() {
	r.t.Helper()
	if out, err := exec.Command(r.svc.Tmux, "-S", r.svc.Socket, "kill-session", "-t", "="+r.fc.name()).CombinedOutput(); err != nil {
		r.t.Fatalf("kill-session: %v: %s", err, out)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := r.svc.Host.Sync(ctx); err != nil {
		r.t.Fatal(err)
	}
	r.ticks(2)
}

// socketThreads is the thread of each message on the chat message a new browser connection is sent.
func (r *replyRig) socketThreads() map[string]string {
	r.t.Helper()
	srv := httptest.NewServer((&server.Server{Hub: server.NewHub(), Modules: []server.Module{r.svc}}).Handler())
	defer srv.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	conn, _, err := websocket.Dial(ctx, "ws"+srv.URL[len("http"):]+"/ws", nil)
	if err != nil {
		r.t.Fatal(err)
	}
	defer conn.CloseNow()
	for {
		_, raw, err := conn.Read(ctx)
		if err != nil {
			r.t.Fatalf("no chat message came on the socket: %v", err)
		}
		var m struct {
			Type     string `json:"type"`
			Messages []struct {
				ID     string `json:"id"`
				Thread string `json:"thread"`
			} `json:"messages"`
		}
		if json.Unmarshal(raw, &m) != nil || m.Type != "chat" {
			continue
		}
		out := map[string]string{}
		for _, x := range m.Messages {
			out[x.ID] = x.Thread
		}
		return out
	}
}

const aNotice = "<task-notification>\n<summary>subagent finished</summary>\n</task-notification>"

func TestANoticeAfterTheTurnEndsClosesTheThread(t *testing.T) {
	t.Parallel()
	r := newReplyRig(t)
	r.sendReport()
	reply := r.say("assistant", "Calm Otter is done")
	r.turnEnds()
	r.ticks(2)
	notice := r.say("user", aNotice)
	next := r.say("assistant", "The sub-agent finished")
	th := r.threads()
	r.wantThread(th, reply, replyCard, "the reply before the end")
	r.wantThread(th, notice, "", "the notice after the end")
	r.wantThread(th, next, "", "the new turn's reply after the notice")
}

func TestANoticeThatStartedTheNextTurnClosesTheThreadEvenWhenNothingReadItBeforeTheEnd(t *testing.T) {
	t.Parallel()
	r := newReplyRig(t)
	r.sendReport()
	reply := r.say("assistant", "Calm Otter is done")
	notice := r.say("user", aNotice)
	r.promptStarts()
	r.ticks(3)
	next := r.say("assistant", "The sub-agent finished")
	th := r.threads()
	r.wantThread(th, reply, replyCard, "the reply before the notice")
	r.wantThread(th, notice, "", "the notice that started the next turn")
	r.wantThread(th, next, "", "the next turn's reply")
}

func TestOnlyTheTurnsOwnCardLineAfterThePasteIsTakenForTheTurn(t *testing.T) {
	t.Parallel()
	r := newReplyRig(t)
	r.enqueue(replyCard, replyTurn)
	r.tickUntil("the report turn typed", r.typedOnce("REPORT-TURN-TEXT"))
	r.promptStarts()
	r.ticks(2)
	bare := r.say("user", "[Report: Calm Otter]")
	open := r.say("user", "[")
	other := r.say("user", "[Report: Calm Otter] REPORT-TURN-TEXT\n\ncard: ship-someone-else-2026-11-17T10:00:00Z-moored")
	stale := r.sayAt("user", replyTurn, time.Now().Add(-time.Hour))
	turn := r.say("user", replyTurn)
	reply := r.say("assistant", "Calm Otter is done")
	th := r.threads()
	r.wantThread(th, bare, "", "a line that is only the report marker")
	r.wantThread(th, open, "", "a line that is only a bracket")
	r.wantThread(th, other, "", "a report turn for another ship card")
	r.wantThread(th, stale, "", "the turn's text logged before the paste")
	r.wantThread(th, turn, replyCard, "the pasted turn")
	r.wantThread(th, reply, replyCard, "the reply")
}

func TestARestartBetweenThePasteAndTheRecordStillMarksTheTurnAndPushesNothing(t *testing.T) {
	t.Parallel()
	r := newReplyRig(t)
	r.enqueue(replyCard, replyTurn)
	r.tickUntil("the report turn typed", r.typedOnce("REPORT-TURN-TEXT"))
	r.promptStarts()
	r.ticks(2)
	again := r.restart()
	var got []ReportReply
	again.OnReply = func(_ string, rep ReportReply) { got = append(got, rep) }
	turn := r.say("user", replyTurn)
	reply := r.say("assistant", "Calm Otter is done")
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
	r.wantThread(th, turn, replyCard, "the pasted turn after a restart")
	r.wantThread(th, reply, replyCard, "the reply after a restart")
	r.wantThread(th, notice, "", "the notice after a restart")
	r.wantThread(th, next, "", "the next turn's reply after a restart")
	r.turnEnds()
	for range 3 {
		again.TurnTick()
	}
	if len(got) != 0 {
		t.Errorf("a restart handed %d replies over, want none", len(got))
	}
}
