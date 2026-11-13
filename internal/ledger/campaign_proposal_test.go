package ledger

import (
	"errors"
	"testing"
)

const campURL2 = "https://github.com/acme/widgets/issues/22"

func TestAProposalIsFoldedAsTheOpenQuestionAndTheChildAsProposed(t *testing.T) {
	l, _ := newLedger(t)
	c := mustWork(t, l, "camp-1")
	if err := l.ChildProposed("home", c.ID, campURL, "campaign-x-1"); err != nil {
		t.Fatalf("ChildProposed: %v", err)
	}
	got, _ := find(t, l, "home", c.ID)
	if got.Proposal == nil || got.Proposal.URL != campURL || got.Proposal.QuestionID != "campaign-x-1" {
		t.Errorf("Proposal = %+v; want the open question about %s", got.Proposal, campURL)
	}
	if ch := childOf(t, l, c.ID, campURL); ch.State != ChildProposed || ch.Proposals != 1 {
		t.Errorf("child = %+v; want proposed, asked once", ch)
	}
	if got.Launched != 0 || got.WaitingChildren != 0 {
		t.Errorf("a proposed child counts as launched or waiting: %+v", got)
	}
}

func TestAWithYouLaunchRecordsItsModeAndClearsTheProposal(t *testing.T) {
	l, _ := newLedger(t)
	c := mustWork(t, l, "camp-1")
	if err := l.ChildProposed("home", c.ID, campURL, "campaign-x-1"); err != nil {
		t.Fatalf("ChildProposed: %v", err)
	}
	if err := l.ChildLaunchedAs("home", c.ID, campURL, "saltpaw-2", ModeWithYou); err != nil {
		t.Fatalf("ChildLaunchedAs: %v", err)
	}
	got, _ := find(t, l, "home", c.ID)
	if got.Proposal != nil {
		t.Errorf("Proposal = %+v after the launch; want none", got.Proposal)
	}
	if ch := childOf(t, l, c.ID, campURL); ch.State != ChildLaunched || ch.Mode != ModeWithYou || ch.Ship != "saltpaw-2" || ch.Proposals != 1 {
		t.Errorf("child = %+v; want launched with you on saltpaw-2, asked once", ch)
	}
	if got.Launched != 1 {
		t.Errorf("Launched = %d, want 1", got.Launched)
	}
}

func TestAChildLaunchedTheOldWayReadsAsWithoutYou(t *testing.T) {
	l, _ := newLedger(t)
	c := mustWork(t, l, "camp-1")
	if err := l.ChildLaunched("home", c.ID, campURL, "saltpaw-2"); err != nil {
		t.Fatalf("ChildLaunched: %v", err)
	}
	if ch := childOf(t, l, c.ID, campURL); ch.Mode != ModeWithoutYou {
		t.Errorf("Mode = %q; want %q for a launch recorded without a mode", ch.Mode, ModeWithoutYou)
	}
}

func TestAWithoutYouLaunchRecordsItsMode(t *testing.T) {
	l, _ := newLedger(t)
	c := mustWork(t, l, "camp-1")
	if err := l.ChildLaunchedAs("home", c.ID, campURL, "saltpaw-2", ModeWithoutYou); err != nil {
		t.Fatalf("ChildLaunchedAs: %v", err)
	}
	if ch := childOf(t, l, c.ID, campURL); ch.Mode != ModeWithoutYou {
		t.Errorf("Mode = %q, want %q", ch.Mode, ModeWithoutYou)
	}
}

func TestAWithdrawnProposalClearsTheQuestionAndKeepsItsReason(t *testing.T) {
	l, _ := newLedger(t)
	c := mustWork(t, l, "camp-1")
	if err := l.ChildProposed("home", c.ID, campURL, "campaign-x-1"); err != nil {
		t.Fatalf("ChildProposed: %v", err)
	}
	if err := l.ChildWithdrawn("home", c.ID, campURL, "not ready"); err != nil {
		t.Fatalf("ChildWithdrawn: %v", err)
	}
	got, _ := find(t, l, "home", c.ID)
	if got.Proposal != nil {
		t.Errorf("Proposal = %+v after withdrawal; want none", got.Proposal)
	}
	if ch := childOf(t, l, c.ID, campURL); ch.State != ChildWithdrawn || ch.Reason != "not ready" {
		t.Errorf("child = %+v; want withdrawn: not ready", ch)
	}
}

func TestAWithdrawnChildProposedAgainCountsItsQuestions(t *testing.T) {
	l, _ := newLedger(t)
	c := mustWork(t, l, "camp-1")
	for i, q := range []string{"campaign-x-1", "campaign-x-2"} {
		if err := l.ChildProposed("home", c.ID, campURL, q); err != nil {
			t.Fatalf("ChildProposed %d: %v", i, err)
		}
		if i == 0 {
			if err := l.ChildWithdrawn("home", c.ID, campURL, "question removed"); err != nil {
				t.Fatalf("ChildWithdrawn: %v", err)
			}
		}
	}
	got, _ := find(t, l, "home", c.ID)
	if got.Proposal == nil || got.Proposal.QuestionID != "campaign-x-2" {
		t.Errorf("Proposal = %+v; want the second question open", got.Proposal)
	}
	if ch := childOf(t, l, c.ID, campURL); ch.State != ChildProposed || ch.Proposals != 2 {
		t.Errorf("child = %+v; want proposed, asked twice", ch)
	}
}

func TestADeclinedProposalLeavesNoOpenQuestion(t *testing.T) {
	l, _ := newLedger(t)
	c := mustWork(t, l, "camp-1")
	if err := l.ChildProposed("home", c.ID, campURL, "campaign-x-1"); err != nil {
		t.Fatalf("ChildProposed: %v", err)
	}
	if err := l.ChildDeclined("home", c.ID, campURL); err != nil {
		t.Fatalf("ChildDeclined: %v", err)
	}
	got, _ := find(t, l, "home", c.ID)
	if got.Proposal != nil {
		t.Errorf("Proposal = %+v after a No; want none", got.Proposal)
	}
	if ch := childOf(t, l, c.ID, campURL); ch.State != ChildDeclined {
		t.Errorf("child = %+v; want declined", ch)
	}
}

func TestOnlyOneProposalIsOpenAtATime(t *testing.T) {
	l, _ := newLedger(t)
	c := mustWork(t, l, "camp-1")
	if err := l.ChildProposed("home", c.ID, campURL, "campaign-x-1"); err != nil {
		t.Fatalf("ChildProposed: %v", err)
	}
	if err := l.ChildProposed("home", c.ID, campURL2, "campaign-y-1"); err == nil {
		t.Error("a second proposal while one is open was recorded; want it refused")
	}
	if got, _ := find(t, l, "home", c.ID); got.Proposal == nil || got.Proposal.URL != campURL {
		t.Errorf("Proposal = %+v; want the first still open", got.Proposal)
	}
}

func TestProposalEventsFoldFromHandWrittenLinesAndCountTowardTheBound(t *testing.T) {
	l, home := newLedger(t)
	seed(t, home, "home", `{"v":1,"ts":"2026-11-12T09:00:00Z","event":"added","id":"c1","kind":"commitment","text":"work on acme/widgets#20","trigger":{"kind":"campaign","subject":"camp-1"},"action":"work","harness":"claude"}`+"\n"+
		`{"v":1,"ts":"2026-11-12T09:01:00Z","event":"child_proposed","id":"c1","url":"`+campURL+`","question":"campaign-x-1"}`+"\n")
	got, ok := find(t, l, "home", "c1")
	if !ok || got.Proposal == nil || got.Proposal.QuestionID != "campaign-x-1" || got.ChildEvents != 1 {
		t.Fatalf("commitment = %+v; want the proposal folded and one child event counted", got)
	}
}

func TestProposalEventsAreRefusedAtTheChildEventBound(t *testing.T) {
	l, _ := newLedger(t)
	c := mustWork(t, l, "camp-1")
	for i := 0; i < MaxChildEvents; i++ {
		if err := l.ChildWaiting("home", c.ID, campURL2); err != nil {
			t.Fatalf("ChildWaiting %d: %v", i, err)
		}
	}
	if err := l.ChildProposed("home", c.ID, campURL, "campaign-x-1"); !errors.Is(err, ErrChildLimit) {
		t.Errorf("ChildProposed at the bound = %v; want ErrChildLimit", err)
	}
}
