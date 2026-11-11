package shipconv

import (
	"encoding/json"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/jbuffin/admiral/internal/ships"
)

func assistantLine(i int, text string) string {
	b, _ := json.Marshal(map[string]any{"type": "assistant", "uuid": fmt.Sprintf("a-%03d", i), "timestamp": "2026-01-02T03:04:05.000Z",
		"message": map[string]any{"role": "assistant", "content": []map[string]any{{"type": "text", "text": text}}}})
	return string(b) + "\n"
}

func tailsOf(f *fixture) *Tails {
	return &Tails{
		list: func() []ships.Ship {
			f.mu.Lock()
			defer f.mu.Unlock()
			var out []ships.Ship
			for name, h := range f.harness {
				out = append(out, ships.Ship{Session: name, Harness: h, Launched: "L1"})
			}
			return out
		},
		locate: f.svc.locate, open: openAt,
	}
}

func TestTailsFollowAClaudeTranscriptAndReadItOnlyWhenItChanges(t *testing.T) {
	f := newFixture(t)
	path := f.path("zz")
	appendTo(t, path, assistantLine(1, "First thought.\nmore"))
	f.ship("alpha", path)
	tails := tailsOf(f)

	if !tails.Poll() || tails.Tail("alpha") != "First thought." {
		t.Fatalf("tail = %q, want the first line of the assistant text", tails.Tail("alpha"))
	}
	reads := tails.reads
	if tails.Poll() || tails.reads != reads {
		t.Errorf("an unchanged transcript was read again (%d reads, was %d)", tails.reads, reads)
	}

	appendTo(t, path, assistantLine(2, "Second thought."))
	if !tails.Poll() || tails.Tail("alpha") != "Second thought." {
		t.Errorf("tail after an append = %q, want %q", tails.Tail("alpha"), "Second thought.")
	}
}

func TestTailsLeaveOtherHarnessesBlankWithoutReadingAnything(t *testing.T) {
	f := newFixture(t)
	f.mu.Lock()
	f.harness["codex-ship"] = "codex"
	f.mu.Unlock()
	tails := tailsOf(f)
	tails.locate = func(string) (where, bool, bool) {
		t.Error("a non-Claude ship's transcript was looked up")
		return where{}, false, true
	}
	if tails.Poll() || tails.Tail("codex-ship") != "" {
		t.Errorf("tail = %q, want blank", tails.Tail("codex-ship"))
	}
	if len(tails.follows) != 0 {
		t.Errorf("%d ships followed, want none", len(tails.follows))
	}
}

func TestTailsStopFollowingAShipOnceItIsGone(t *testing.T) {
	f := newFixture(t)
	path := f.path("zz")
	appendTo(t, path, assistantLine(1, "Hello."))
	f.ship("alpha", path)
	tails := tailsOf(f)
	tails.Poll()
	f.mu.Lock()
	delete(f.harness, "alpha")
	f.mu.Unlock()
	tails.Poll()
	if len(tails.follows) != 0 || tails.Tail("alpha") != "" {
		t.Errorf("follows = %d, tail = %q, want none and blank once the ship is gone", len(tails.follows), tails.Tail("alpha"))
	}
	_ = os.Remove(path)
}

func TestTailsOfAShipWithNoTranscriptYetStayBlank(t *testing.T) {
	f := newFixture(t)
	f.ship("alpha", f.path("zz")) // the launch recorded a path; nothing is written there yet
	tails := tailsOf(f)
	if tails.Poll() || tails.Tail("alpha") != "" {
		t.Errorf("tail = %q, want blank", tails.Tail("alpha"))
	}
}

func TestTailsMoveOffAQuestionOnceItIsAnswered(t *testing.T) {
	f := newFixture(t)
	path := f.path("zz")
	appendTo(t, path, assistantLine(1, "Earlier.")+
		`{"type":"assistant","uuid":"q1","message":{"content":[{"type":"tool_use","id":"tq","name":"AskUserQuestion","input":{"questions":[{"question":"Which database?","options":[{"label":"pg"}]}]}}]}}`+"\n")
	f.ship("alpha", path)
	tails := tailsOf(f)
	tails.Poll()
	if got := tails.Tail("alpha"); got != "Which database?" {
		t.Fatalf("tail = %q, want the pending question", got)
	}
	appendTo(t, path, `{"type":"user","uuid":"r1","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"tq","content":"pg"}]}}`+"\n")
	tails.Poll()
	if got := tails.Tail("alpha"); got != "Earlier." {
		t.Errorf("tail after the answer = %q, want %q", got, "Earlier.")
	}
}

func TestTailsStartOverWhenAShipIsRelaunched(t *testing.T) {
	f := newFixture(t)
	path := f.path("zz")
	appendTo(t, path, assistantLine(1, "Old run."))
	f.ship("alpha", path)
	launched := "L1"
	tails := tailsOf(f)
	list := tails.list
	tails.list = func() []ships.Ship {
		out := list()
		for i := range out {
			out[i].Launched = launched
		}
		return out
	}
	tails.Poll()
	if tails.Tail("alpha") != "Old run." {
		t.Fatalf("tail = %q", tails.Tail("alpha"))
	}
	launched = "L2"
	f.mu.Lock()
	f.harness["alpha"] = "codex" // the new launch has nothing readable
	f.mu.Unlock()
	tails.Poll()
	if tails.Tail("alpha") != "" || len(tails.follows) != 0 {
		t.Errorf("tail = %q with %d follows, want both reset", tails.Tail("alpha"), len(tails.follows))
	}
}

func TestTailsLookForAMissingFileOnlyOncePerFindInterval(t *testing.T) {
	f := newFixture(t)
	path := f.path("zz")
	appendTo(t, path, assistantLine(1, "Hello."))
	f.ship("alpha", path)
	tails := tailsOf(f)
	tails.Poll()
	if tails.Tail("alpha") != "Hello." {
		t.Fatal("tail not read")
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	finds := 0
	tails.open = func(w where, p string, find bool) (*os.File, os.FileInfo, string, error) {
		if find {
			finds++
		}
		return openAt(w, p, find)
	}
	for range 20 {
		tails.Poll()
	}
	if finds > 1 {
		t.Errorf("%d searches of the project dirs in a burst of polls, want at most 1", finds)
	}
	if tails.Tail("alpha") != "" {
		t.Errorf("tail = %q after the transcript vanished, want blank", tails.Tail("alpha"))
	}
}

func TestTailsLookUpAShipWithNoConversationLessAndLess(t *testing.T) {
	f := newFixture(t)
	f.mu.Lock()
	f.harness["alpha"] = "claude" // no option recorded: locate says none
	f.mu.Unlock()
	tails := tailsOf(f)
	looks := 0
	locate := tails.locate
	tails.locate = func(s string) (where, bool, bool) { looks++; return locate(s) }
	tails.Poll()
	if looks != 1 {
		t.Fatalf("%d lookups on the first poll, want 1", looks)
	}
	fl := tails.follows["alpha"]
	for i := 0; i < 4; i++ {
		fl.lastTry = time.Time{} // the wait is over
		tails.Poll()
	}
	if fl.retry < 16*retryEvery/2 && fl.retry != maxRetry {
		t.Errorf("retry after 5 lookups = %v, want it backed off", fl.retry)
	}
	before := looks
	for range 10 {
		tails.Poll()
	}
	if looks != before {
		t.Errorf("%d lookups inside the backoff, want none", looks-before)
	}
}
