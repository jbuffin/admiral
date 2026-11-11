package shipconv

import (
	"encoding/json"
	"fmt"
	"os"
	"testing"

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
		locate: f.svc.locate,
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
