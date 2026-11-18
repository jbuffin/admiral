package chat

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/jbuffin/admiral/internal/actor"
	"github.com/jbuffin/admiral/internal/launcher"
	"github.com/jbuffin/admiral/internal/sessionhost"
)

// The report-turn queue against a fake pane under a live host: delivery, one turn at a time, the reset wait and the gate.

type turnRig struct {
	t   *testing.T
	svc *Service
	fc  *fleetChat
	n   int64
}

// newTurnRig is a chat that has started, so it can take a message.
func newTurnRig(t *testing.T, harness string) *turnRig {
	t.Helper()
	t.Setenv("ADMIRAL_CHAT_FLUSH_WAIT", "300ms")
	svc, fc := hostedChat(t, harness)
	record(t, svc, fc, harness)
	if harness == "claude" {
		claudeTranscript(t, svc, fc)
	} else {
		setPane(t, svc, "transcript", "1 startup /tmp/rollout.jsonl")
		_, _ = fc.refresh(true)
	}
	return &turnRig{t: t, svc: svc, fc: fc}
}

func (r *turnRig) screen() string {
	r.t.Helper()
	sess, _ := r.fc.session()
	out, err := exec.Command(r.svc.Tmux, "-S", r.svc.Socket, "capture-pane", "-p", "-J", "-t", sess.PaneID).CombinedOutput()
	if err != nil {
		r.t.Fatalf("capture-pane: %v: %s", err, out)
	}
	return string(out)
}

func (r *turnRig) typed(text string) int { return strings.Count(r.screen(), text) }

func (r *turnRig) setState(state, reason string) {
	r.t.Helper()
	setPane(r.t, r.svc, "state", state)
	setPane(r.t, r.svc, "reason", reason)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := r.svc.Host.Sync(ctx); err != nil {
		r.t.Fatal(err)
	}
}

// promptStarts plays the prompt hook: the stamp moves and the turn is working.
func (r *turnRig) promptStarts() {
	r.t.Helper()
	r.n++
	setPane(r.t, r.svc, sessionhost.ChatPromptOpt, strconv.FormatInt(time.Now().UnixNano()+r.n, 10))
	r.setState("working", "")
}

// turnEnds plays the stop hook.
func (r *turnRig) turnEnds() { r.t.Helper(); r.setState("needs_you", "stop") }

func (r *turnRig) enqueue(id, text string) {
	r.t.Helper()
	if err := r.svc.EnqueueTurn("work", id, text); err != nil {
		r.t.Fatalf("EnqueueTurn(%s): %v", id, err)
	}
}

func (r *turnRig) ticks(n int) {
	for range n {
		r.svc.TurnTick()
	}
}

// tickUntil runs the queue's loop until cond holds.
func (r *turnRig) tickUntil(what string, cond func() bool) {
	r.t.Helper()
	for end := time.Now().Add(10 * time.Second); time.Now().Before(end); time.Sleep(50 * time.Millisecond) {
		r.svc.TurnTick()
		if cond() {
			return
		}
	}
	r.t.Fatalf("never saw: %s\nthe pane shows:\n%s", what, r.screen())
}

func (r *turnRig) typedOnce(text string) func() bool {
	return func() bool { return r.typed(text) == 1 }
}

func (r *turnRig) queued() []string { return r.svc.QueuedTurns("work") }

func sameIDs(got []string, want ...string) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}

// restart is a second Admiral over the same home, host and tmux: nothing of the first one's memory carries over.
func (r *turnRig) restart() *Service {
	s := serviceFor(r.t, &launcher.Launcher{Tmux: r.svc.Tmux, Socket: r.svc.Socket, Home: r.svc.Home}, r.svc.Home)
	s.Config, s.Host = r.svc.Config, r.svc.Host
	return s
}

var harnesses = []string{"claude", "codex"}

func TestAnItemQueuedOnAnIdleChatIsTypedOnceAsItsOwnMessage(t *testing.T) {
	for _, h := range harnesses {
		t.Run(h, func(t *testing.T) {
			r := newTurnRig(t, h)
			r.enqueue("ship-1-done", "REPORT-A")
			if !sameIDs(r.queued(), "ship-1-done") {
				t.Fatalf("queued = %v, want [ship-1-done]", r.queued())
			}
			r.tickUntil("REPORT-A typed", r.typedOnce("REPORT-A"))
			if len(r.queued()) != 0 {
				t.Errorf("queued after delivery = %v, want none", r.queued())
			}
			r.promptStarts()
			r.ticks(3)
			r.turnEnds()
			r.ticks(3)
			if n := r.typed("REPORT-A"); n != 1 {
				t.Errorf("REPORT-A typed %d times, want 1", n)
			}
		})
	}
}

func TestAnItemWaitsWhileATurnRunsAndIsTypedOnceItEnds(t *testing.T) {
	for _, h := range harnesses {
		t.Run(h, func(t *testing.T) {
			r := newTurnRig(t, h)
			r.promptStarts()
			r.enqueue("a", "REPORT-A")
			r.ticks(4)
			if n := r.typed("REPORT-A"); n != 0 {
				t.Fatalf("typed %d times during the engineer's turn, want 0", n)
			}
			if !sameIDs(r.queued(), "a") {
				t.Errorf("queued = %v, want [a]: a waiting item is kept", r.queued())
			}
			r.turnEnds()
			r.tickUntil("REPORT-A typed after the turn", r.typedOnce("REPORT-A"))
		})
	}
}

func (r *turnRig) askQuestion() string {
	r.t.Helper()
	if err := os.MkdirAll(filepath.Join(r.svc.Home, "chat", "work", "questions"), 0o700); err != nil {
		r.t.Fatal(err)
	}
	mux := http.NewServeMux()
	r.svc.Routes(mux)
	req := httptest.NewRequest("POST", "/api/chat/questions", strings.NewReader(askBody)).WithContext(actor.WithFleet(context.Background(), "work"))
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	var got struct{ ID string }
	if rec.Code != http.StatusCreated || json.Unmarshal(rec.Body.Bytes(), &got) != nil || got.ID == "" {
		r.t.Fatalf("ask = %d %s", rec.Code, rec.Body)
	}
	return got.ID
}

func (r *turnRig) answerQuestion(id string) {
	r.t.Helper()
	req := httptest.NewRequest("POST", "/api/chat/work/questions/"+id+"/answer", strings.NewReader(`{"answers":{"q1":{"choice":"main"}}}`))
	req.SetPathValue("id", id)
	rec := httptest.NewRecorder()
	r.fc.serveAnswer(rec, req)
	if rec.Code != http.StatusOK {
		r.t.Fatalf("answer = %d %s", rec.Code, rec.Body)
	}
}

func TestAnItemWaitsForAnOpenChatQuestionAndIsTypedOnceItIsAnswered(t *testing.T) {
	r := newTurnRig(t, "claude")
	id := r.askQuestion()
	r.enqueue("a", "REPORT-A")
	r.ticks(4)
	if n := r.typed("REPORT-A"); n != 0 {
		t.Fatalf("typed %d times with a question open, want 0", n)
	}
	r.answerQuestion(id)
	r.tickUntil("REPORT-A typed after the answer", r.typedOnce("REPORT-A"))
}

func TestTheSecondItemWaitsUntilTheFirstReportTurnHasEnded(t *testing.T) {
	for _, h := range harnesses {
		t.Run(h, func(t *testing.T) {
			r := newTurnRig(t, h)
			r.enqueue("a", "REPORT-A")
			r.enqueue("b", "REPORT-B")
			r.tickUntil("REPORT-A typed", r.typedOnce("REPORT-A"))
			r.ticks(4)
			if n := r.typed("REPORT-B"); n != 0 {
				t.Fatalf("REPORT-B typed %d times before the first turn's prompt started, want 0", n)
			}
			r.promptStarts()
			r.ticks(4)
			if n := r.typed("REPORT-B"); n != 0 {
				t.Fatalf("REPORT-B typed %d times while the first report turn runs, want 0", n)
			}
			if !sameIDs(r.queued(), "b") {
				t.Errorf("queued = %v, want [b]", r.queued())
			}
			r.turnEnds()
			r.tickUntil("REPORT-B typed after the first turn", r.typedOnce("REPORT-B"))
			if n := r.typed("REPORT-A"); n != 1 {
				t.Errorf("REPORT-A typed %d times, want 1", n)
			}
		})
	}
}

func TestTheSameIDEnqueuedTwiceIsTypedOnceEvenAfterItWasDelivered(t *testing.T) {
	r := newTurnRig(t, "claude")
	r.enqueue("a", "REPORT-A")
	r.enqueue("a", "REPORT-A")
	if !sameIDs(r.queued(), "a") {
		t.Fatalf("queued = %v, want one [a]", r.queued())
	}
	r.tickUntil("REPORT-A typed", r.typedOnce("REPORT-A"))
	r.promptStarts()
	r.turnEnds()
	r.enqueue("a", "REPORT-A")
	r.ticks(4)
	if n := r.typed("REPORT-A"); n != 1 || len(r.queued()) != 0 {
		t.Errorf("after a re-post: typed %d times, queued %v; want 1 and none", n, r.queued())
	}
}

func TestTheGateIsClosedUntilTheReportTurnStartsAndOpensWhenItEnds(t *testing.T) {
	for _, h := range harnesses {
		t.Run(h, func(t *testing.T) {
			r := newTurnRig(t, h)
			if r.svc.TurnCantAct("work") {
				t.Fatal("the gate is closed with nothing queued")
			}
			r.enqueue("a", "REPORT-A")
			r.tickUntil("REPORT-A typed", r.typedOnce("REPORT-A"))
			r.promptStarts()
			r.ticks(1)
			if !r.svc.TurnCantAct("work") {
				t.Error("the gate is open while the report turn runs, want closed")
			}
			if r.svc.TurnCantAct("home") {
				t.Error("the gate is closed for a fleet with no report turn")
			}
			r.turnEnds()
			r.ticks(2)
			if r.svc.TurnCantAct("work") {
				t.Error("the gate stays closed after the report turn ended, want open")
			}
		})
	}
}

func TestAnEngineersMessageDuringAReportTurnIsPastedAndEndsTheGateWhenItsPromptStarts(t *testing.T) {
	r := newTurnRig(t, "claude")
	r.enqueue("a", "REPORT-A")
	r.tickUntil("REPORT-A typed", r.typedOnce("REPORT-A"))
	r.promptStarts()
	r.ticks(1)
	if !r.svc.TurnCantAct("work") {
		t.Fatal("the gate is open during the report turn, so the case proves nothing")
	}
	if err := r.fc.send("ENGINEER-HELLO"); err != nil {
		t.Fatalf("the engineer's send during a report turn: %v", err)
	}
	if n := r.typed("ENGINEER-HELLO"); n != 1 {
		t.Errorf("the engineer's message typed %d times, want 1", n)
	}
	r.promptStarts() // the engineer's prompt starts: the stamp moves past the report's, the pane stays working
	r.ticks(1)
	if r.svc.TurnCantAct("work") {
		t.Error("the gate stays closed after the engineer's prompt started, want open")
	}
}

func TestAnEngineerSendDuringAReportTurnAndStartAreNotBlockedByTheQueue(t *testing.T) {
	r := newTurnRig(t, "claude")
	r.enqueue("a", "REPORT-A")
	r.tickUntil("REPORT-A typed", r.typedOnce("REPORT-A"))
	r.promptStarts()
	r.ticks(1)
	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = r.fc.send("ENGINEER-HELLO")
		r.fc.serveStart(httptest.NewRecorder(), httptest.NewRequest("POST", "/api/chat/work/start", nil))
	}()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("the engineer's send and start hang during a report turn: the queue holds the chat's lock across the turn")
	}
}

func TestAnEngineerMessageRacingADeliveryIsNeverTypedIntoTheReportsMessage(t *testing.T) {
	r := newTurnRig(t, "claude")
	r.enqueue("a", "REPORT-A")
	sent := make(chan error, 1)
	go func() { sent <- r.fc.send("ENGINEER-HELLO") }()
	r.tickUntil("REPORT-A typed", r.typedOnce("REPORT-A"))
	if err := <-sent; err != nil {
		t.Fatalf("send: %v", err)
	}
	for _, line := range strings.Split(r.screen(), "\n") {
		if strings.Contains(line, "REPORT-A") && strings.Contains(line, "ENGINEER-HELLO") {
			t.Errorf("one line holds both messages: %q", line)
		}
	}
	if n := r.typed("ENGINEER-HELLO"); n != 1 {
		t.Errorf("the engineer's message typed %d times, want 1", n)
	}
}

func TestTheQueueSurvivesARestartAndADeliveredIDIsNotTypedAgain(t *testing.T) {
	r := newTurnRig(t, "claude")
	r.enqueue("kept", "REPORT-KEPT")
	if got := r.restart().QueuedTurns("work"); !sameIDs(got, "kept") {
		t.Fatalf("after a restart the queue holds %v, want [kept]", got)
	}
	r.tickUntil("REPORT-KEPT typed", r.typedOnce("REPORT-KEPT"))
	r.promptStarts()

	again := r.restart()
	if got := again.QueuedTurns("work"); len(got) != 0 {
		t.Errorf("after a restart mid-turn the queue holds %v, want none: the item left when it was pasted", got)
	}
	if again.TurnCantAct("work") {
		t.Error("a restarted Admiral starts with the gate closed, since its running state is in memory")
	}
	if err := again.EnqueueTurn("work", "kept", "REPORT-KEPT"); err != nil {
		t.Fatal(err)
	}
	r.turnEnds()
	for range 4 {
		again.TurnTick()
	}
	if n := r.typed("REPORT-KEPT"); n != 1 {
		t.Errorf("REPORT-KEPT typed %d times after the restart, want 1", n)
	}
	if got := again.QueuedTurns("work"); len(got) != 0 {
		t.Errorf("a delivered id was queued again after the restart: %v", got)
	}
}

func TestAnItemWaitsWhenTheChatCantTakeItAndIsTypedOnceItCan(t *testing.T) {
	t.Run("a chat that has not started", func(t *testing.T) {
		svc, fc := hostedChat(t, "claude")
		record(t, svc, fc, "claude")
		r := &turnRig{t: t, svc: svc, fc: fc}
		r.enqueue("a", "REPORT-A")
		r.ticks(4)
		if n := r.typed("REPORT-A"); n != 0 || !sameIDs(r.queued(), "a") {
			t.Fatalf("before its first SessionStart: typed %d times, queued %v; want 0 and [a]", n, r.queued())
		}
		claudeTranscript(t, svc, fc)
		r.tickUntil("REPORT-A typed once started", r.typedOnce("REPORT-A"))
	})
	t.Run("a chat that is resetting", func(t *testing.T) {
		r := newTurnRig(t, "claude")
		r.fc.resetting.Store(true)
		r.enqueue("a", "REPORT-A")
		r.ticks(4)
		if n := r.typed("REPORT-A"); n != 0 || !sameIDs(r.queued(), "a") {
			t.Fatalf("while resetting: typed %d times, queued %v; want 0 and [a]", n, r.queued())
		}
		r.fc.resetting.Store(false)
		r.tickUntil("REPORT-A typed after the reset", r.typedOnce("REPORT-A"))
	})
	t.Run("a Codex chat whose hooks are not trusted", func(t *testing.T) {
		svc, fc := hostedChat(t, "codex")
		record(t, svc, fc, "codex")
		r := &turnRig{t: t, svc: svc, fc: fc}
		sess, _ := fc.session()
		if out, err := exec.Command(svc.Tmux, "-S", svc.Socket, "send-keys", "-t", sess.PaneID, "-l", "› Ask Codex to do anything  ? for shortcuts").CombinedOutput(); err != nil {
			t.Fatalf("send-keys: %v: %s", err, out)
		}
		cfg := filepath.Join(launcher.ChatCodexHome(svc.Home, "work"), "config.toml")
		writeAt(t, cfg, nil)
		r.enqueue("a", "REPORT-A")
		r.ticks(4)
		if n := r.typed("REPORT-A"); n != 0 || !sameIDs(r.queued(), "a") {
			t.Fatalf("with untrusted hooks: typed %d times, queued %v; want 0 and [a]", n, r.queued())
		}
		writeAt(t, cfg, []byte("[hooks.state.\"/x/config.toml:session_start:0:0\"]\ntrusted_hash = \"sha256:1\"\n"))
		r.tickUntil("REPORT-A typed once trusted", r.typedOnce("REPORT-A"))
	})
}

func (r *turnRig) newChat() (code int, took time.Duration) {
	r.t.Helper()
	start := time.Now()
	rec := httptest.NewRecorder()
	done := make(chan struct{})
	go func() {
		defer close(done)
		r.fc.serveNew(rec, httptest.NewRequest("POST", "/api/chat/work/new", nil))
	}()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		r.t.Fatal("New chat did not return with a report queued: it waits instead of deferring the clear")
	}
	return rec.Code, time.Since(start)
}

func TestNewChatWithAnItemQueuedReturnsAtOnceAndClearsOnlyAfterTheQueueDrained(t *testing.T) {
	r := newTurnRig(t, "claude")
	r.enqueue("a", "REPORT-A")
	if code, _ := r.newChat(); code != http.StatusOK {
		t.Fatalf("New chat = %d, want 200", code)
	}
	if strings.Contains(r.screen(), "/clear") {
		t.Fatal("New chat cleared the chat with a report queued")
	}
	if !sameIDs(r.queued(), "a") {
		t.Errorf("queued = %v, want [a]: New chat keeps queued items", r.queued())
	}
	r.tickUntil("REPORT-A typed", r.typedOnce("REPORT-A"))
	r.promptStarts()
	r.ticks(4)
	if strings.Contains(r.screen(), "/clear") {
		t.Fatal("the chat was cleared while the report turn ran")
	}
	r.turnEnds()
	r.tickUntil("the deferred clear", func() bool { return strings.Contains(r.screen(), "/clear") })
	scr := r.screen()
	if strings.Index(scr, "/clear") < strings.Index(scr, "REPORT-A") {
		t.Errorf("the clear came before the report:\n%s", scr)
	}
}

func TestAScheduledResetSkipsAChatWithAnItemQueued(t *testing.T) {
	// due runs the scheduler's two ticks a day apart over an idle chat and says whether a reset began.
	due := func(t *testing.T, queue bool) bool {
		t.Helper()
		r := newTurnRig(t, "claude")
		if queue {
			r.enqueue("a", "REPORT-A")
		}
		now := time.Now()
		r.fc.resetTick(now)
		r.fc.resetTick(now.Add(25 * time.Hour))
		for end := time.Now().Add(3 * time.Second); time.Now().Before(end); time.Sleep(50 * time.Millisecond) {
			if pasted(t, r.svc, r.fc) {
				return true
			}
		}
		return false
	}
	if !due(t, false) {
		t.Fatal("with nothing queued the scheduled reset never began, so the case below proves nothing")
	}
	if due(t, true) {
		t.Error("a scheduled reset began with a report queued")
	}
}
