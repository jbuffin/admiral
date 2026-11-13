package ledger

import (
	"errors"
	"fmt"
	"sync"
	"testing"
)

const campURL = "https://github.com/acme/widgets/issues/21"

func work(campaign string) Commitment {
	return Commitment{Text: "work on acme/widgets#20", Trigger: Trigger{Kind: TriggerCampaign, Subject: campaign}, Action: ActionWork, Harness: "claude"}
}

func mustWork(t *testing.T, l *Ledger, campaign string) Commitment {
	t.Helper()
	c, created, err := l.AddCampaignCommitment("home", work(campaign), "admiralbot")
	if err != nil || !created || c.ID == "" {
		t.Fatalf("AddCampaignCommitment = %+v, created=%v, %v; want a new commitment", c, created, err)
	}
	return c
}

func TestACampaignCommitmentIsStoredOpenWithItsKindActionHarnessAndCampaign(t *testing.T) {
	l, _ := newLedger(t)
	c := mustWork(t, l, "camp-1")
	got, ok := find(t, l, "home", c.ID)
	if !ok {
		t.Fatalf("commitment %s is not listed", c.ID)
	}
	if got.Trigger.Kind != TriggerCampaign || got.Trigger.Subject != "camp-1" || got.Action != ActionWork || got.Harness != "claude" || got.Via != "admiralbot" {
		t.Errorf("stored %+v; want kind campaign, subject camp-1, action work, harness claude, via admiralbot", got)
	}
	if got.Waiting || len(got.Children) != 0 || got.Launched != 0 {
		t.Errorf("a new campaign commitment has children or waits: %+v", got)
	}
}

func TestASecondWorkOnForTheSameCampaignReturnsTheOpenCommitmentAndRecordsNothing(t *testing.T) {
	l, home := newLedger(t)
	first := mustWork(t, l, "camp-1")
	again, created, err := l.AddCampaignCommitment("home", work("camp-1"), "admiralbot")
	if err != nil || created || again.ID != first.ID {
		t.Fatalf("second AddCampaignCommitment = %+v, created=%v, %v; want the first commitment, created false", again, created, err)
	}
	if n := len(lines(t, home, "home")); n != 1 {
		t.Errorf("ledger has %d lines, want the one added event", n)
	}
}

func TestAnotherCampaignGetsItsOwnCommitment(t *testing.T) {
	l, _ := newLedger(t)
	a := mustWork(t, l, "camp-1")
	b := mustWork(t, l, "camp-2")
	if a.ID == b.ID || len(openIDs(t, l, "home")) != 2 {
		t.Errorf("ids %s and %s, open %v; want two commitments", a.ID, b.ID, openIDs(t, l, "home"))
	}
}

func TestACampaignWorkedOnAgainAfterItsCommitmentClosedGetsANewOne(t *testing.T) {
	l, _ := newLedger(t)
	first := mustWork(t, l, "camp-1")
	if err := l.Done("home", first.ID, "the campaign ended"); err != nil {
		t.Fatal(err)
	}
	second := mustWork(t, l, "camp-1")
	if second.ID == first.ID {
		t.Errorf("a closed commitment was returned again: %s", second.ID)
	}
}

func TestConcurrentWorkOnsForOneCampaignRecordOneCommitment(t *testing.T) {
	l, home := newLedger(t)
	var wg sync.WaitGroup
	var mu sync.Mutex
	created, ids := 0, map[string]bool{}
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			c, made, err := l.AddCampaignCommitment("home", work("camp-1"), "admiralbot")
			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				t.Errorf("AddCampaignCommitment: %v", err)
				return
			}
			ids[c.ID] = true
			if made {
				created++
			}
		}()
	}
	wg.Wait()
	if created != 1 || len(ids) != 1 || len(lines(t, home, "home")) != 1 {
		t.Errorf("created %d, distinct ids %d, lines %d; want one each", created, len(ids), len(lines(t, home, "home")))
	}
}

func TestACampaignCommitmentNeedsACampaignAndTextAndCountsTowardTheCap(t *testing.T) {
	l, home := newLedger(t)
	var re *RefusedError
	noSubject := work("")
	if _, _, err := l.AddCampaignCommitment("home", noSubject, ""); !errors.As(err, &re) {
		t.Errorf("a commitment with no campaign: %v; want a RefusedError", err)
	}
	seed(t, home, "home", rawCommitments(MaxCommitments))
	if _, _, err := l.AddCampaignCommitment("home", work("camp-1"), ""); !errors.As(err, &re) {
		t.Errorf("the thirty-first commitment: %v; want a RefusedError", err)
	}
}

func TestTheGenericAddRefusesTheCampaignKindAndAction(t *testing.T) {
	l, home := newLedger(t)
	var re *RefusedError
	if _, err := l.AddCommitment("home", Commitment{Text: "x", Trigger: Trigger{Kind: TriggerCampaign, Subject: "camp-1"}, Action: ActionRemind}, ""); !errors.As(err, &re) {
		t.Errorf("AddCommitment with the campaign trigger: %v; want a RefusedError", err)
	}
	if _, err := l.AddCommitment("home", Commitment{Text: "x", Trigger: Trigger{Kind: TriggerNone}, Action: ActionWork}, ""); !errors.As(err, &re) {
		t.Errorf("AddCommitment with the work action: %v; want a RefusedError", err)
	}
	if n := len(lines(t, home, "home")); n != 0 {
		t.Errorf("%d lines written, want none", n)
	}
}

func childOf(t *testing.T, l *Ledger, id, url string) Child {
	t.Helper()
	c, ok := find(t, l, "home", id)
	if !ok {
		t.Fatalf("commitment %s is not open", id)
	}
	for _, ch := range c.Children {
		if ch.URL == url {
			return ch
		}
	}
	t.Fatalf("commitment %+v has no child %s", c, url)
	return Child{}
}

func TestAChildLaunchIsFoldedWithItsShipAndTheCommitmentStaysOpen(t *testing.T) {
	l, _ := newLedger(t)
	c := mustWork(t, l, "camp-1")
	if err := l.ChildLaunched("home", c.ID, campURL, "saltpaw-2"); err != nil {
		t.Fatalf("ChildLaunched: %v", err)
	}
	if ch := childOf(t, l, c.ID, campURL); ch.State != ChildLaunched || ch.Ship != "saltpaw-2" {
		t.Errorf("child = %+v; want launched on saltpaw-2", ch)
	}
	got, _ := find(t, l, "home", c.ID)
	if got.Launched != 1 || got.WaitingChildren != 0 || got.Waiting {
		t.Errorf("commitment = %+v; want 1 launched, 0 waiting children, not waiting", got)
	}
}

func TestAWaitingChildBecomesLaunchedAndIsCountedOnceUnderItsLastState(t *testing.T) {
	l, _ := newLedger(t)
	c := mustWork(t, l, "camp-1")
	if err := l.ChildWaiting("home", c.ID, campURL); err != nil {
		t.Fatalf("ChildWaiting: %v", err)
	}
	if got, _ := find(t, l, "home", c.ID); got.WaitingChildren != 1 || got.Launched != 0 || len(got.Children) != 1 || got.Children[0].State != ChildWaiting {
		t.Fatalf("after waiting: %+v; want one waiting child", got)
	}
	if err := l.ChildLaunched("home", c.ID, campURL, "saltpaw-2"); err != nil {
		t.Fatalf("ChildLaunched: %v", err)
	}
	got, _ := find(t, l, "home", c.ID)
	if got.WaitingChildren != 0 || got.Launched != 1 || len(got.Children) != 1 || got.Children[0].State != ChildLaunched {
		t.Errorf("after launch: %+v; want one launched child and no waiting", got)
	}
}

func TestAFailedChildKeepsItsReasonAndADeclinedChildItsState(t *testing.T) {
	l, _ := newLedger(t)
	c := mustWork(t, l, "camp-1")
	other := "https://github.com/acme/widgets/issues/22"
	if err := l.ChildFailed("home", c.ID, campURL, "autopilot is not proven for claude"); err != nil {
		t.Fatalf("ChildFailed: %v", err)
	}
	if err := l.ChildDeclined("home", c.ID, other); err != nil {
		t.Fatalf("ChildDeclined: %v", err)
	}
	if ch := childOf(t, l, c.ID, campURL); ch.State != ChildFailed || ch.Reason != "autopilot is not proven for claude" {
		t.Errorf("failed child = %+v", ch)
	}
	if ch := childOf(t, l, c.ID, other); ch.State != ChildDeclined {
		t.Errorf("declined child = %+v", ch)
	}
	if got, _ := find(t, l, "home", c.ID); got.Launched != 0 || got.WaitingChildren != 0 {
		t.Errorf("failed and declined children count as launched or waiting: %+v", got)
	}
}

func TestChildEventsFoldFromHandWrittenLinesAndSurviveAFreshLedger(t *testing.T) {
	l, home := newLedger(t)
	seed(t, home, "home", `{"v":1,"ts":"2026-11-12T09:00:00Z","event":"added","id":"c1","kind":"commitment","text":"work on acme/widgets#20","trigger":{"kind":"campaign","subject":"camp-1"},"action":"work","harness":"claude"}`+"\n"+
		`{"v":1,"ts":"2026-11-12T09:01:00Z","event":"child_launched","id":"c1","url":"`+campURL+`","ship":"saltpaw-2"}`+"\n")
	got, ok := find(t, l, "home", "c1")
	if !ok || got.Harness != "claude" || got.Launched != 1 || len(got.Children) != 1 || got.Children[0].URL != campURL || got.Children[0].Ship != "saltpaw-2" {
		t.Errorf("folded %+v, %v; want harness claude and one launched child on saltpaw-2", got, ok)
	}
}

func TestAChildEventAfterTheCommitmentClosedIsRefusedAndNotRecorded(t *testing.T) {
	for name, closeIt := range map[string]func(*Ledger, string) error{
		"done":      func(l *Ledger, id string) error { return l.Done("home", id, "the campaign ended") },
		"cancelled": func(l *Ledger, id string) error { return l.CancelCommitment("home", id) },
	} {
		t.Run(name, func(t *testing.T) {
			l, home := newLedger(t)
			c := mustWork(t, l, "camp-1")
			if err := closeIt(l, c.ID); err != nil {
				t.Fatal(err)
			}
			before := len(lines(t, home, "home"))
			if err := l.ChildLaunched("home", c.ID, campURL, "saltpaw-2"); !errors.Is(err, ErrNotFound) {
				t.Errorf("ChildLaunched after close = %v; want ErrNotFound", err)
			}
			if n := len(lines(t, home, "home")); n != before {
				t.Errorf("a child event was written after the close: %d lines, was %d", n, before)
			}
		})
	}
}

func TestAChildEventForANonCampaignCommitmentIsRefused(t *testing.T) {
	l, home := newLedger(t)
	c := mustCommit(t, l, "home", remind("later", Trigger{Kind: TriggerNone}), "")
	var re *RefusedError
	if err := l.ChildLaunched("home", c.ID, campURL, "saltpaw-2"); !errors.As(err, &re) {
		t.Errorf("ChildLaunched on a remind commitment = %v; want a RefusedError", err)
	}
	if n := len(lines(t, home, "home")); n != 1 {
		t.Errorf("%d lines, want only the added event", n)
	}
}

func TestACampaignCommitmentTakesTwoHundredChildEventsAndRefusesTheNext(t *testing.T) {
	l, home := newLedger(t)
	c := mustWork(t, l, "camp-1")
	for i := 0; i < MaxChildEvents; i++ {
		if err := l.ChildLaunched("home", c.ID, fmt.Sprintf("https://github.com/acme/widgets/issues/%d", 100+i), "ship"); err != nil {
			t.Fatalf("child event %d: %v", i+1, err)
		}
	}
	err := l.ChildLaunched("home", c.ID, "https://github.com/acme/widgets/issues/999", "ship")
	if !errors.Is(err, ErrChildLimit) {
		t.Fatalf("child event %d = %v; want ErrChildLimit", MaxChildEvents+1, err)
	}
	if n := len(lines(t, home, "home")); n != 1+MaxChildEvents {
		t.Errorf("%d lines, want %d: the refused event must not be recorded", n, 1+MaxChildEvents)
	}
	if _, ok := find(t, l, "home", c.ID); !ok {
		t.Error("the commitment closed at the bound; it should stay open for the runner to say so")
	}
}

func TestTheChildEventBoundCountsEveryKindOfChildEvent(t *testing.T) {
	l, _ := newLedger(t)
	c := mustWork(t, l, "camp-1")
	for i := 0; i < MaxChildEvents/2; i++ {
		u := fmt.Sprintf("https://github.com/acme/widgets/issues/%d", 100+i)
		if err := l.ChildWaiting("home", c.ID, u); err != nil {
			t.Fatalf("waiting %d: %v", i, err)
		}
		if err := l.ChildLaunched("home", c.ID, u, "ship"); err != nil {
			t.Fatalf("launched %d: %v", i, err)
		}
	}
	if err := l.ChildFailed("home", c.ID, campURL, "no"); !errors.Is(err, ErrChildLimit) {
		t.Errorf("event past the bound = %v; want ErrChildLimit", err)
	}
}
