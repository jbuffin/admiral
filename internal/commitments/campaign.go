package commitments

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
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
	// Held stops every proposal: With-you children are not worked here.
	plan := campaigns.Decide(campaigns.PlanInput{CampaignID: rd.ID, Campaign: rd.Campaign, Followed: rd.Followed, Launched: launched, Declined: declined, Held: true})
	switch plan.End {
	case campaigns.Ended:
		r.end(fleet, c, reasonEnded)
		return
	case campaigns.Unfollowed:
		r.end(fleet, c, reasonUnfollowed)
		return
	}
	events := c.ChildEvents
	for _, ch := range plan.Launch {
		if ctx.Err() != nil {
			return
		}
		// A launch can record a wait and then the launch itself.
		if events+2 > ledger.MaxChildEvents {
			if !c.Shown && r.post(fleet, c, OutcomeTooLarge, "", "this campaign has too many children to work on unattended; start the rest by hand") == nil {
				_ = r.Ledger.MarkShown(fleet, c.ID)
			}
			return
		}
		events += r.launchChild(ctx, fleet, c, rd, ch, waiting[ch.URL], now, earlier)
	}
}

// launchChild launches one Without-you child in autopilot and returns how many child events it recorded.
func (r *Runner) launchChild(ctx context.Context, fleet string, c ledger.Commitment, rd campaigns.Read, ch campaigns.Child, wasWaiting bool, now time.Time, earlier func(time.Time)) int {
	key := "commitment:" + c.ID + ":" + ch.URL
	if held, ok := r.Ships.Holder(key); ok {
		return r.childLaunched(fleet, c, ch.URL, held)
	}
	o := rd.Orders[ch.URL]
	req := launcher.Request{
		Fleet: fleet, Harness: c.Harness, Clone: true, CloneRepo: o.CloneRepo,
		Skill:  launcher.Skill{Kind: ch.Skill.Kind, Name: ch.Skill.Name},
		Source: config.ReservedSourceKey, OrderID: rd.ID + "/" + ch.URL, Text: o.Text,
		Via: c.Via, CommitmentKey: key, Autopilot: true, QuietGate: true,
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
		if wasWaiting {
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
	return r.childLaunched(fleet, c, ch.URL, ship)
}

func (r *Runner) childLaunched(fleet string, c ledger.Commitment, url string, ship ShipRef) int {
	// The ship exists, so the child is recorded even when its card cannot be written: otherwise the next pass launches again.
	if err := r.postChild(fleet, c, OutcomeLaunched, url, ship.Session, ""); err != nil {
		fmt.Fprintf(os.Stderr, "admiral: commitment %s launched %s but its chat card cannot be written: %v\n", c.ID, ship.Session, err)
	}
	if r.Ledger.ChildLaunched(fleet, c.ID, url, ship.Session) != nil {
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
