package lookout

import (
	"context"
	"time"

	"github.com/jbuffin/admiral/internal/chat"
	"github.com/jbuffin/admiral/internal/inbox"
	"github.com/jbuffin/admiral/internal/judge"
	"github.com/jbuffin/admiral/internal/routines"
	"github.com/jbuffin/admiral/internal/standing"
)

// Timeout bounds one Lookout call.
const Timeout = 45 * time.Second

// Lookout reads an outside event on the fleet's judge harness and returns fixed fields only.
type Lookout struct {
	// Choose is the fleet's judge harness; an error means none is proven.
	Choose func(fleet string) (judge.Choice, error)
	// Judge runs the call; judge.Run when nil.
	Judge func(context.Context, judge.Choice, judge.Call) (judge.Reply, error)
	// Limits is the fleet's Limits, for the per-call budget.
	Limits func(fleet string) standing.Limits
	// Repos are the repos of the fleet's search roots.
	Repos func(fleet string) []string
	// Item is the title and body of the event's item, fetched for a verified GitHub event.
	Item func(ctx context.Context, ev routines.Event) (title, body string, err error)
}

// Enrich is the scheduler's Enrich seam.
func (l *Lookout) Enrich(ev routines.Event) routines.Enrichment { return routines.Enrichment{} }

// Metadata is the one line an event card or proposal shows when there is no Lookout summary: title, sender, repo and link.
func Metadata(ev routines.Event, repo string) string { return "" }

// CardPoster posts an A0 card to a fleet's chat.
type CardPoster interface {
	PostEventCard(fleet string, c chat.EventCard) error
}

// ProposalPublisher publishes an A1 event proposal to the Inbox.
type ProposalPublisher interface {
	PublishEventProposal(p inbox.EventProposal, d inbox.EventDetail) (string, error)
}

// Acts is the scheduler's Acts seam: a Reported decision becomes a card, a Proposed one an Inbox proposal.
type Acts struct {
	Cards     CardPoster
	Proposals ProposalPublisher
}

func (a *Acts) Act(fleet string, r routines.Routine, ev routines.Event, rec routines.Trigger, d routines.Decision, en routines.Enrichment) {
}
