package campaigns

import "slices"

// Child is one campaign child the planner acts on.
type Child struct {
	Campaign string // the campaign's id
	URL      string
	Title    string
	Skill    Skill
}

// End says why a campaign commitment is over.
type End string

const (
	NotEnded   End = ""
	Ended      End = "ended"      // the parent and every child are closed
	Unfollowed End = "unfollowed" // the engineer stopped following the campaign
)

// PlanInput is what the commitment knows when it wakes.
type PlanInput struct {
	CampaignID string
	Campaign   Campaign // from the latest good tree read
	Followed   bool
	Launched   []string // child URLs this commitment launched or fired, in either mode
	Declined   []string // With-you child URLs the engineer said No to
	Proposed   string   // the With-you child URL with an open proposal, or ""
	Held       bool     // a With-you ship AdmiralBot started in the fleet is underway or needs you
}

// Plan is what to do now.
type Plan struct {
	Launch   []Child // Without-you children to launch, in tree order
	Propose  *Child  // at most one With-you child
	Withdraw string  // the open proposal to withdraw, or ""
	End      End
}

// Decide works out a campaign commitment's next steps from a tree read.
func Decide(in PlanInput) Plan {
	root := in.Campaign.Root
	if root == nil {
		return Plan{}
	}
	var p Plan
	// All-closed is checked before the followed flag: the follow store also ends itself when the tree closes.
	switch {
	case allClosed(root):
		p.End = Ended
	case !in.Followed:
		p.End = Unfollowed
	}
	if p.End != NotEnded {
		p.Withdraw = in.Proposed
		return p
	}

	var proposable *Node
	proposedStillReady := false
	for _, n := range in.Campaign.Ready {
		launched := slices.Contains(in.Launched, n.URL)
		switch n.Mode {
		case WithoutYou:
			if !launched && !slices.ContainsFunc(p.Launch, func(c Child) bool { return c.URL == n.URL }) {
				p.Launch = append(p.Launch, in.child(n))
			}
		case WithYou:
			if launched {
				continue
			}
			if n.URL == in.Proposed {
				proposedStillReady = true
			}
			if proposable == nil && !slices.Contains(in.Declined, n.URL) {
				proposable = n
			}
		}
	}

	switch {
	case in.Proposed != "":
		// A withdrawn child is proposed on a later wake, not in this plan.
		if !proposedStillReady {
			p.Withdraw = in.Proposed
		}
	case proposable != nil && !in.Held:
		c := in.child(proposable)
		p.Propose = &c
	}
	return p
}

func (in PlanInput) child(n *Node) Child {
	return Child{Campaign: in.CampaignID, URL: n.URL, Title: n.Title, Skill: n.Skill}
}

// allClosed mirrors follow.allClosed: false for any open node, or one with unreadable sub-issues, at any depth.
func allClosed(n *Node) bool {
	if !n.Closed || n.Unreadable > 0 {
		return false
	}
	for _, c := range n.Children {
		if !allClosed(c) {
			return false
		}
	}
	return true
}
