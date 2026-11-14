// The event text a launched ship reads: built in the Lookout, and kept out of every card and proposal.
package lookout_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/jbuffin/admiral/internal/judge"
	"github.com/jbuffin/admiral/internal/routines"
)

func TestTheEnrichmentTextIsTheFetchedTitleThenTheFetchedBody(t *testing.T) {
	r := newRig(t)
	en := r.l.Enrich(ghEvent())
	if en.Text != "Fix the quarry lantern\n\n"+bodyToken {
		t.Errorf("Text = %q, want the fetched title, a blank line and the fetched body", en.Text)
	}
}

func TestWithNoJudgeHarnessTheTextIsTheTitleAndTheMetadataLine(t *testing.T) {
	r := newRig(t)
	r.none = &judge.NoJudgeHarness{Reason: "claude has not proven no tools"}
	ev := ghEvent()
	en := r.l.Enrich(ev)
	for _, want := range []string{"Fix the quarry lantern", "alice", "https://github.com/acme/api/pull/7"} {
		if !strings.Contains(en.Text, want) {
			t.Errorf("Text = %q, want it to hold %q", en.Text, want)
		}
	}
	if strings.Contains(en.Text, bodyToken) {
		t.Errorf("Text = %q holds a body that was never fetched", en.Text)
	}
}

func TestAFailedItemFetchLeavesTheTitleAndTheMetadataLine(t *testing.T) {
	r := newRig(t)
	r.l.Item = func(context.Context, routines.Event) (string, string, error) { return "", "", errors.New("no network") }
	en := r.l.Enrich(ghEvent())
	if !strings.Contains(en.Text, "Fix the quarry lantern") || !strings.Contains(en.Text, "https://github.com/acme/api/pull/7") {
		t.Errorf("Text = %q, want the event's title and the metadata line", en.Text)
	}
}

func TestAFailedLookoutCallStillCarriesTheEventText(t *testing.T) {
	r := newRig(t)
	r.err = errors.New("the judge broke")
	en := r.l.Enrich(ghEvent())
	if en.Failure == "" || !strings.Contains(en.Text, bodyToken) {
		t.Errorf("enrichment = %+v, want the failure and the fetched body in Text", en)
	}
}

func TestTheEventTextReachesNoCardAndNoProposal(t *testing.T) {
	for _, outcome := range []routines.Outcome{routines.Reported, routines.Proposed} {
		t.Run(string(outcome), func(t *testing.T) {
			a, cards, props := actRig()
			actOn(a, outcome, "alice", routines.Enrichment{Summary: "A review is asked for.", Text: "Fix the quarry lantern\n\n" + bodyToken})
			all := fmt.Sprintf("%+v %+v %+v", cards.cards, props.props, props.details)
			if strings.Contains(all, bodyToken) {
				t.Errorf("the event body reached a card or proposal: %s", all)
			}
		})
	}
}
