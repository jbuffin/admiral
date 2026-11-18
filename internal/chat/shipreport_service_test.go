package chat

import (
	"slices"
	"strings"
	"testing"

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
