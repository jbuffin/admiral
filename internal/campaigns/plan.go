package campaigns

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
	return Plan{}
}
