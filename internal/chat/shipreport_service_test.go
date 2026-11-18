package chat

import (
	"slices"
	"strings"
	"testing"

	"github.com/jbuffin/admiral/internal/findings"
	"github.com/jbuffin/admiral/internal/ships"
)

// The report turn as the chat queues it: its id, and where its launch text comes from.

func queuedTexts(r *briefRig, fleet string) []string {
	var out []string
	r.chat(fleet).turns(func(t *turnState) {
		for _, q := range t.queue {
			out = append(out, q.Text)
		}
	})
	return out
}

func postLaunchLine(t *testing.T, r *briefRig, text, title string) {
	t.Helper()
	l := ships.LaunchLine{Ship: "calm-otter", Name: "Calm Otter", FirstLaunched: "2026-11-17T10:00:00Z", Kind: "quick", Reason: "the engineer asked", Text: text, Title: title}
	if err := r.PostLaunchLine("work", l); err != nil {
		t.Fatal(err)
	}
}

func TestAReportTurnIsQueuedUnderItsCardIdSoTheSameEventQueuesOnce(t *testing.T) {
	r := newBriefRig(t)
	for range 2 {
		if err := r.EnqueueShipReport("work", sampleCard(), "done"); err != nil {
			t.Fatalf("EnqueueShipReport = %v", err)
		}
	}
	id := "ship-calm-otter-2026-11-17T10:00:00Z-moored"
	if got := r.QueuedTurns("work"); !slices.Equal(got, []string{id}) {
		t.Errorf("queued turns = %v, want [%s]", got, id)
	}
	if got := r.QueuedTurns("home"); len(got) != 0 {
		t.Errorf("the home fleet queued %v, want nothing", got)
	}
}

func TestTheLaunchTextOfAReportTurnComesFromTheShipsLaunchLine(t *testing.T) {
	r := newBriefRig(t)
	postLaunchLine(t, r, "Rebase acme/api#12 onto main and report what conflicted", "Rebase")
	if got := r.LaunchTextOf("work", "calm-otter", "2026-11-17T10:00:00Z"); got != "Rebase acme/api#12 onto main and report what conflicted" {
		t.Errorf("LaunchTextOf = %q, want the launch line's text", got)
	}
	if got := r.LaunchTextOf("work", "calm-otter", "2026-11-17T11:00:00Z"); got != "" {
		t.Errorf("LaunchTextOf for another voyage of the name = %q, want none", got)
	}
	if err := r.EnqueueShipReport("work", sampleCard(), "done"); err != nil {
		t.Fatal(err)
	}
	if got := queuedTexts(r, "work"); len(got) != 1 || !strings.Contains(got[0], "Rebase acme/api#12 onto main and report what conflicted") {
		t.Errorf("queued turn = %q, want it to carry the launch line's text", got)
	}
}

func TestAReportTurnFallsBackToTheShipsTitleWhenItHasNoLaunchLine(t *testing.T) {
	r := newBriefRig(t)
	if err := r.EnqueueShipReport("work", sampleCard(), "done"); err != nil {
		t.Fatal(err)
	}
	if got := queuedTexts(r, "work"); len(got) != 1 || !strings.Contains(got[0], "Fix the flaky login test") {
		t.Errorf("queued turn = %q, want it to carry the ship's title", got)
	}
}

func findingsKeptInTurn(t *testing.T, r *briefRig, c ShipCard) bool {
	t.Helper()
	if err := r.EnqueueShipReport("work", c, "done"); err != nil {
		t.Fatalf("EnqueueShipReport = %v", err)
	}
	texts := queuedTexts(r, "work")
	if len(texts) != 1 {
		t.Fatalf("%d turns queued, want 1", len(texts))
	}
	line := lineWithPrefix(texts[0], "findings:")
	if line == "" {
		t.Fatalf("no findings line in the report turn:\n%s", texts[0])
	}
	return strings.HasPrefix(line, "findings: kept")
}

func TestTheReportTurnSaysFindingsWereKeptWhenTheCardsVoyageHasThem(t *testing.T) {
	r := newBriefRig(t)
	r.Service.Findings = findings.Open(r.home)
	c := sampleCard()
	keepFindings(t, r.home, c.Ship, c.FirstLaunched, "work")
	if !findingsKeptInTurn(t, r, c) {
		t.Error("the report turn says no findings were kept, want kept")
	}
}

func TestTheReportTurnSaysNoFindingsWhenTheVoyageKeptNone(t *testing.T) {
	r := newBriefRig(t)
	r.Service.Findings = findings.Open(r.home)
	if findingsKeptInTurn(t, r, sampleCard()) {
		t.Error("the report turn says findings were kept, want none")
	}
}

func TestTheReportTurnSaysNoFindingsWhenTheServiceHasNoFindingsStore(t *testing.T) {
	r := newBriefRig(t)
	c := sampleCard()
	keepFindings(t, r.home, c.Ship, c.FirstLaunched, "work")
	if findingsKeptInTurn(t, r, c) {
		t.Error("the report turn says findings were kept, want none with a nil store")
	}
}

func TestTheReportTurnIgnoresFindingsOfAnotherVoyageOfTheSameShipName(t *testing.T) {
	r := newBriefRig(t)
	r.Service.Findings = findings.Open(r.home)
	c := sampleCard()
	keepFindings(t, r.home, c.Ship, firstB, "work")
	if findingsKeptInTurn(t, r, c) {
		t.Error("the report turn says findings were kept, want none: only another voyage kept findings")
	}
}
