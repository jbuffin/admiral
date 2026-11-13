package routines

import (
	"errors"
	"time"

	"github.com/jbuffin/admiral/internal/standing"
)

// EventKind is one kind a source's catalog lists; Panel is set for a kind scoped to one of the source's panels.
type EventKind struct {
	Kind  string
	Title string
	Panel string
}

// On is a routine's When as a source event, instead of a cron schedule.
type On struct {
	Source string `json:"source"`
	Kind   string `json:"kind"`
	Panel  string `json:"panel,omitempty"`
}

// Event is one outside event as the scheduler sees it. Tier is never a field: Go computes it.
type Event struct {
	Fleet    string
	Source   string
	Kind     string
	Panel    string
	Ref      string
	Actor    string
	Author   string
	Viewer   string // the engineer's login on the source; empty when the source can't say
	Verified bool
}

// Cost is one judge call's price on a trigger-log record.
type Cost struct {
	Call string  `json:"call"`
	USD  float64 `json:"usd"`
}

// TierOf is the less trusted of the actor's and the author's tiers; the stub returns a tier outside T1..T3.
func TierOf(ev Event, limits standing.Limits) standing.Tier { return standing.Tier(0) }

// EventTop is the highest action an event routine reaches until restricted and normal ships launch from events.
const EventTop = standing.A1

// Why a decision ended where it did.
const (
	ReasonSkipFilter = "skip_filter"
	ReasonRepo       = "repo_not_allowed"
	ReasonTop        = "top_action"
	ReasonSpend      = "daily_spend_reached"
	ReasonQuiet      = "quiet_hours"
	ReasonHold       = "hold_fire"
	ReasonDedupe     = "dedupe_key_held"
)

// DecideInput is everything the decision reads for one (event, routine) pair.
type DecideInput struct {
	Routine           Routine
	Tier              standing.Tier
	Limits            standing.Limits
	Top               standing.Level
	HoldOn            bool
	Now               time.Time // already in the fleet's zone
	SpentTodayUSD     float64
	Counts            standing.Counts
	LiveDedupeKeys    []string
	RestrictedHarness bool
	Sender            string
	Repo              string
	Labels            []string
	DedupeKey         string
}

// Decision is the outcome of one pair. Computed is the level before Top and the budgets; Level is the one acted on.
// Reasons lists, in decision order, each cause that held an action above A1 down, or the one that skipped the event.
type Decision struct {
	Outcome  Outcome
	Level    standing.Level
	Computed standing.Level
	Reasons  []string
}

// Decide runs the skip filters, the repo allowlist, the effective action, Top, spend, quiet hours, rates and dedupe, in that order.
func Decide(in DecideInput) Decision { return Decision{} }

// Enrichment is what a Lookout adds to an event before the decision.
type Enrichment struct {
	DedupeKey string
	Repo      string
	Labels    []string
	Costs     []Cost
}

// EventActs receives each decision that ends in a card (Reported) or a proposal (Proposed), with its saved record.
type EventActs interface {
	Act(fleet string, r Routine, ev Event, rec Trigger, d Decision)
}

// HandleEvent matches ev to the fleet's enabled event routines, decides each pair and records it.
func (s *Scheduler) HandleEvent(ev Event) {}

// Records is every trigger-log record of the fleet, newest first, with undo and failure marks applied.
func (s *Store) Records(fleet string) ([]Trigger, error) { return nil, errors.New("not implemented") }

// SpendToday sums the costs recorded on now's calendar day in zone.
func (s *Store) SpendToday(fleet string, now time.Time, zone *time.Location) (float64, error) {
	return 0, errors.New("not implemented")
}

// CountsFor reads the rate counts a launch would be checked against from the fleet's log.
func (s *Store) CountsFor(fleet, sender string, now time.Time, zone *time.Location) (standing.Counts, error) {
	return standing.Counts{}, errors.New("not implemented")
}
