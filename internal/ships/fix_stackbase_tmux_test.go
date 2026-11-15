package ships

import (
	"context"
	"errors"
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

func TestAStackThatConflictsWithItsBaseServesAFixStackOfferOnItsTopPR(t *testing.T) {
	a := newAnswerRig(t, 3)
	a.baseConflicted(t, &fakeStackChecker{conflicts: true})

	fix, has := a.listedFix(t)

	if !has {
		t.Fatal("GET /api/ships serves no fix for a ready stack that conflicts with its base")
	}
	if fix["pr"] != wakePR2 || fix["stack"] != true {
		t.Errorf("fix = %v, want a stack round on the top PR %s", fix, wakePR2)
	}
	problems, _ := fix["problems"].([]any)
	if len(problems) != 1 {
		t.Fatalf("problems = %v, want one entry, on the top PR", fix["problems"])
	}
	if p, _ := problems[0].(map[string]any); p["pr"] != wakePR2 || p["conflicts_with_base"] != "main" || p["conflicts"] != nil {
		t.Errorf("problem = %v, want conflicts_with_base main on %s alone", problems[0], wakePR2)
	}
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

func TestAStackBaseConflictThatClearsTakesTheFixOfferAndTheFixRouteAway(t *testing.T) {
	a := newAnswerRig(t, 3)
	ck := &fakeStackChecker{conflicts: true}
	a.baseConflicted(t, ck)
	if _, has := a.listedFix(t); !has {
		t.Fatal("the premise fails: no offer was served")
	}

	ck.set(false, nil)
	a.s.PollMoored(context.Background())

	if fix, has := a.listedFix(t); has {
		t.Errorf("fix = %v, want none once the stack merges cleanly", fix)
	}
	if rec := a.fixRoute("saltpaw"); rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), "no merge problem") {
		t.Errorf("fix = %d %s, want 409 saying there is no merge problem", rec.Code, rec.Body)
	}
	a.nothingStarted(t)
}

func TestAStackBaseCheckThatCannotRunOffersNoFix(t *testing.T) {
	a := newAnswerRig(t, 3)
	ck := &fakeStackChecker{err: errors.New("no gh auth")}
	a.baseConflicted(t, ck)
	if len(ck.requests()) == 0 {
		t.Fatal("the premise fails: the check never ran")
	}

	if fix, has := a.listedFix(t); has {
		t.Errorf("fix = %v, want none when the check cannot tell", fix)
	}
	if rec := a.fixRoute("saltpaw"); rec.Code != http.StatusConflict {
		t.Errorf("fix = %d %s, want 409", rec.Code, rec.Body)
	}
}

func TestAMergeableStackBlockedOnReviewThatConflictsWithItsBaseIsStillOfferedFixStack(t *testing.T) {
	a := newAnswerRig(t, 3)
	a.baseConflicted(t, &fakeStackChecker{conflicts: true})
	blocked := readyAt("top-1")
	blocked.Review, blocked.MergeState = "REVIEW_REQUIRED", "BLOCKED"
	a.gh.set(wakePR2, blocked)

	a.s.PollMoored(context.Background())

	fix, has := a.listedFix(t)
	if !has || fix["pr"] != wakePR2 || fix["stack"] != true {
		t.Fatalf("fix = %v, %v, want a stack round on %s: every layer is mergeable", fix, has, wakePR2)
	}
	problems, _ := fix["problems"].([]any)
	if p, _ := problems[0].(map[string]any); len(problems) != 1 || p["conflicts_with_base"] != "main" {
		t.Errorf("problems = %v, want conflicts_with_base main", problems)
	}
}
