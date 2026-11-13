package commitments

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"slices"
	"time"

	"github.com/jbuffin/admiral/internal/campaigns"
	"github.com/jbuffin/admiral/internal/config"
	"github.com/jbuffin/admiral/internal/launcher"
	"github.com/jbuffin/admiral/internal/ledger"
)

const (
	reasonEnded      = "the campaign ended"
	reasonUnfollowed = "stopped following"
)

// workCampaign is one pass over a campaign commitment. It never calls fire or the close-on-outcome helpers:
// the commitment stays open while its children launch and closes only when the campaign ends.
func (r *Runner) workCampaign(ctx context.Context, fleet string, c ledger.Commitment, now time.Time, earlier func(time.Time)) {
	if r.Campaign == nil {
		return
	}
	rd := r.Campaign(c.Trigger.Subject)
	if !rd.Found {
		r.withdraw(fleet, c, "ended")
		r.end(fleet, c, reasonUnfollowed)
		return
	}
	var launched, declined []string
	waiting := map[string]bool{}
	for _, ch := range c.Children {
		switch ch.State {
		case ledger.ChildLaunched, ledger.ChildFailed:
			launched = append(launched, ch.URL)
		case ledger.ChildDeclined:
			declined = append(declined, ch.URL)
		case ledger.ChildWaiting:
			waiting[ch.URL] = true
		}
	}
	proposing := r.Proposals != nil && r.Proposals.ChatOn(fleet)
	in := campaigns.PlanInput{CampaignID: rd.ID, Campaign: rd.Campaign, Followed: rd.Followed, Launched: launched, Declined: declined, Held: !proposing || r.fleetHeld(fleet)}
	if c.Proposal != nil {
		in.Proposed = c.Proposal.URL
	}
	plan := campaigns.Decide(in)
	switch plan.End {
	case campaigns.Ended:
		r.withdraw(fleet, c, "ended")
		r.end(fleet, c, reasonEnded)
		return
	case campaigns.Unfollowed:
		r.withdraw(fleet, c, "ended")
		r.end(fleet, c, reasonUnfollowed)
		return
	}
	events := c.ChildEvents
	if plan.Withdraw != "" {
		if r.withdraw(fleet, c, "not ready") {
			events++
		}
	} else if c.Proposal != nil && proposing {
		events += r.answerProposal(ctx, fleet, c, rd, now, earlier)
	}
	for _, ch := range plan.Launch {
		if ctx.Err() != nil {
			return
		}
		// A launch can record a wait and then the launch itself.
		if events+2 > ledger.MaxChildEvents {
			r.tooLarge(fleet, c)
			return
		}
		events += r.launchChild(ctx, fleet, c, rd, ch, waiting[ch.URL], false, now, earlier)
	}
	if plan.Propose != nil && c.Proposal == nil {
		r.propose(ctx, fleet, c, rd.Campaign.Root.Title, *plan.Propose, events)
	}
}

// fleetHeld reports whether a With-you child is being worked or asked about in the fleet, across all its campaign commitments.
func (r *Runner) fleetHeld(fleet string) bool {
	list, err := r.Ledger.Commitments(fleet)
	if err != nil {
		return true
	}
	for _, o := range list {
		if o.Trigger.Kind != ledger.TriggerCampaign {
			continue
		}
		if o.Proposal != nil {
			return true
		}
		for _, ch := range o.Children {
			if ch.State != ledger.ChildLaunched || ch.Mode != ledger.ModeWithYou {
				continue
			}
			if ship, ok := r.Ships.Holder("commitment:" + o.ID + ":" + ch.URL); ok && (ship.Status == "underway" || ship.Status == "needs_you") {
				return true
			}
		}
	}
	return false
}

// propose asks the engineer about a With-you child; events is how many child events the commitment holds.
func (r *Runner) propose(ctx context.Context, fleet string, c ledger.Commitment, campaign string, ch campaigns.Child, events int) {
	if ctx.Err() != nil {
		return
	}
	// The proposal and its resolution each record an event.
	if events+2 > ledger.MaxChildEvents {
		r.tooLarge(fleet, c)
		return
	}
	n := 1
	for _, k := range c.Children {
		if k.URL == ch.URL {
			n = k.Proposals + 1
		}
	}
	sum := sha256.Sum256([]byte(ch.URL))
	p := Proposal{ID: fmt.Sprintf("campaign-%s-%s-%d", c.ID, hex.EncodeToString(sum[:2]), n), Title: fmt.Sprintf("Start %s with you? (%s)", childName(ch), campaign), Child: ch.URL}
	if r.Proposals.Propose(fleet, p) != nil {
		return
	}
	if r.Ledger.ChildProposed(fleet, c.ID, ch.URL, p.ID) != nil {
		r.Proposals.Withdraw(fleet, p.ID)
	}
}

func childName(ch campaigns.Child) string {
	if ch.Title == "" {
		return ch.URL
	}
	return ch.Title
}

func (r *Runner) tooLarge(fleet string, c ledger.Commitment) {
	if !c.Shown && r.post(fleet, c, OutcomeTooLarge, "", "this campaign has too many children to work on unattended; start the rest by hand") == nil {
		_ = r.Ledger.MarkShown(fleet, c.ID)
	}
}

// withdraw removes the commitment's open question and records why; it reports whether it recorded an event.
func (r *Runner) withdraw(fleet string, c ledger.Commitment, reason string) bool {
	if c.Proposal == nil || r.Proposals == nil {
		return false
	}
	r.Proposals.Withdraw(fleet, c.Proposal.QuestionID)
	return r.Ledger.ChildWithdrawn(fleet, c.ID, c.Proposal.URL, reason) == nil
}

// answerProposal acts on the engineer's answer to the open proposal and returns how many child events it recorded.
func (r *Runner) answerProposal(ctx context.Context, fleet string, c ledger.Commitment, rd campaigns.Read, now time.Time, earlier func(time.Time)) int {
	url := c.Proposal.URL
	switch r.Proposals.Reply(fleet, c.Proposal.QuestionID) {
	case ProposalNo:
		if r.Ledger.ChildDeclined(fleet, c.ID, url) != nil {
			return 0
		}
		return 1
	case ProposalGone:
		if r.Ledger.ChildWithdrawn(fleet, c.ID, url, "question removed") != nil {
			return 0
		}
		return 1
	case ProposalYes:
		i := slices.IndexFunc(rd.Campaign.Ready, func(n *campaigns.Node) bool { return n.URL == url })
		if i < 0 {
			return 0
		}
		n := rd.Campaign.Ready[i]
		return r.launchChild(ctx, fleet, c, rd, campaigns.Child{Campaign: rd.ID, URL: n.URL, Title: n.Title, Skill: n.Skill}, false, true, now, earlier)
	}
	return 0
}

// launchChild launches one child and returns how many child events it recorded: a Without-you child in autopilot, or a With-you child the engineer said Yes to.
func (r *Runner) launchChild(ctx context.Context, fleet string, c ledger.Commitment, rd campaigns.Read, ch campaigns.Child, wasWaiting, withYou bool, now time.Time, earlier func(time.Time)) int {
	key := "commitment:" + c.ID + ":" + ch.URL
	mode := ledger.ModeWithoutYou
	if withYou {
		mode = ledger.ModeWithYou
	}
	if held, ok := r.Ships.Holder(key); ok {
		return r.childLaunched(fleet, c, ch.URL, held, mode)
	}
	o := rd.Orders[ch.URL]
	skill := launcher.Skill{Kind: ch.Skill.Kind, Name: ch.Skill.Name}
	if skill.Kind == "" {
		skill.Kind = "none"
	}
	req := launcher.Request{
		Fleet: fleet, Harness: c.Harness, Clone: true, CloneRepo: o.CloneRepo,
		Skill:  skill,
		Source: config.ReservedSourceKey, OrderID: rd.ID + "/" + ch.URL, Text: o.Text,
		Via: c.Via, CommitmentKey: key, Autopilot: !withYou, QuietGate: !withYou,
		// To AdmiralBot a ship of another fleet is only that fleet, so a refusal's text names no foreign ship.
		ByFleet: fleet,
	}
	// The launcher runs this under its lock, so a cancel that lands before it launches nothing.
	cancelled := false
	req.Before = func(string) error {
		if r.open(fleet, c.ID) {
			return nil
		}
		cancelled = true
		return errors.New("the commitment was cancelled")
	}
	ship, lerr := r.Ships.Launch(ctx, req)
	switch {
	case lerr != nil && cancelled:
		return 0
	case lerr != nil && lerr.Wait != "":
		earlier(now.Add(pollEvery))
		// The answer stays on the open proposal, which a waiting event would clear.
		if wasWaiting || withYou {
			return 0
		}
		if r.Ledger.ChildWaiting(fleet, c.ID, ch.URL) != nil {
			return 0
		}
		return 1
	case lerr != nil:
		// The child is recorded as failed so it is never retried; the engineer can start it by hand.
		if r.postChild(fleet, c, OutcomeCantRun, ch.URL, "", lerr.Msg) != nil {
			return 0
		}
		if r.Ledger.ChildFailed(fleet, c.ID, ch.URL, lerr.Msg) != nil {
			return 0
		}
		return 1
	}
	return r.childLaunched(fleet, c, ch.URL, ship, mode)
}

func (r *Runner) childLaunched(fleet string, c ledger.Commitment, url string, ship ShipRef, mode string) int {
	// The ship exists, so the child is recorded even when its card cannot be written: otherwise the next pass launches again.
	if err := r.postChild(fleet, c, OutcomeLaunched, url, ship.Session, ""); err != nil {
		fmt.Fprintf(os.Stderr, "admiral: commitment %s launched %s but its chat card cannot be written: %v\n", c.ID, ship.Session, err)
	}
	if r.Ledger.ChildLaunchedAs(fleet, c.ID, url, ship.Session, mode) != nil {
		return 0
	}
	return 1
}

func (r *Runner) postChild(fleet string, c ledger.Commitment, outcome, url, ship, why string) error {
	sum := sha256.Sum256([]byte(url))
	id := "commitment-" + c.ID + "-" + outcome + "-" + hex.EncodeToString(sum[:4])
	return r.Cards.PostCard(fleet, Card{ID: id, Commitment: c.ID, Text: c.Text, Outcome: outcome, Ship: ship, Why: why, Child: url})
}

func (r *Runner) end(fleet string, c ledger.Commitment, why string) {
	if r.post(fleet, c, OutcomeEnded, "", why) == nil {
		_ = r.Ledger.Done(fleet, c.ID, why)
	}
}
