package ships

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jbuffin/admiral/internal/moor"
)

// baseConflicted moors the two-PR ship, every layer ready, with the checker answering as given.
func (a *answerRig) baseConflicted(t *testing.T, ck *fakeStackChecker) {
	t.Helper()
	a.s.StackChecker = ck
	a.gh.set(wakePR, readyAt("h7"))
	a.gh.set(wakePR2, readyAt("top-1"))
	a.gh.mu.Lock()
	a.gh.detail = map[string]moor.Detail{
		wakePR:  {Number: 7, URL: wakePR, Head: "layer-7", Base: "main", DefaultBranch: "main", Stack: baseStack()},
		wakePR2: {Number: 8, URL: wakePR2, Head: "layer-8", Base: "layer-7", DefaultBranch: "main", Stack: baseStack()},
	}
	a.gh.mu.Unlock()
	a.mooredStack(t, 2, "/commodore resume run-7", 30*time.Minute)
	a.s.PollMoored(context.Background())
	if !a.stillMoored(t) {
		t.Fatal("the premise fails: the poll woke the ship on its own")
	}
}

func baseStack() *moor.DetailStack {
	return &moor.DetailStack{Number: 12, Size: 2, Base: "main", URL: wakeStack, Entries: []moor.DetailStackEntry{
		{Position: 1, Number: 7, URL: wakePR, State: "OPEN", Head: "layer-7"}, {Position: 2, Number: 8, URL: wakePR2, State: "OPEN", Head: "layer-8"}}}
}

func TestFixOnAStackThatConflictsWithItsBaseWakesTheShipAndTellsItToRebaseOntoTheBase(t *testing.T) {
	a := newAnswerRig(t, 3)
	a.baseConflicted(t, &fakeStackChecker{conflicts: true})

	rec := a.fixRoute("saltpaw")

	if rec.Code != http.StatusCreated {
		t.Fatalf("fix = %d %s, want 201: the stack has a problem to fix", rec.Code, rec.Body)
	}
	argv, _ := a.started(t, 1)
	if got, want := prompt(argv), "/commodore resume run-7 feedback round 3 on "+wakePR2; got != want {
		t.Errorf("prompt = %q, want %q", got, want)
	}
	b, _ := os.ReadFile(filepath.Join(a.checkout, ".admiral", "untrusted", "feedback-3.md"))
	if !strings.Contains(string(b), wakePR2) || !strings.Contains(string(b), "main") || !strings.Contains(strings.ToLower(string(b)), "rebase") {
		t.Errorf("feedback-3.md = %q, want it to say the stack conflicts with main and to rebase onto it", b)
	}
}
