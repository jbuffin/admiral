// Package commitments fires the promises AdmiralBot recorded: a launch or a reminder, with no model turn.
package commitments

import (
	"context"
	"time"

	"github.com/jbuffin/admiral/internal/launcher"
	"github.com/jbuffin/admiral/internal/ledger"
)

// Clock is the time seam; After is one timer to the next due time.
type Clock struct {
	Now   func() time.Time
	After func(d time.Duration) <-chan time.Time
}

// ShipRef is a ship as the runner sees it.
type ShipRef struct {
	Session string
	Name    string
}

// Ships is the part of the ships service the runner uses.
type Ships interface {
	Launch(ctx context.Context, req launcher.Request) (ShipRef, *launcher.Error)
	Holder(key string) (ShipRef, bool)
}

// SettleGrace is how long a clean `ended` waits for a `moored` before the ship counts as sailed off; the ships service moors a ship with open PRs a few seconds after it ends.
const SettleGrace = 15 * time.Second

// Card outcomes.
const (
	OutcomeLaunched = "launched"
	OutcomeReminded = "reminded"
	OutcomeCantRun  = "cant_run"
	OutcomeDropped  = "dropped"
)

// Card is what the chat shows for a commitment's outcome. ID is commitment-<commitment id>-<outcome>.
type Card struct {
	ID         string
	Commitment string // the commitment's id
	Text       string // the commitment's text
	Outcome    string
	Ship       string // the launched ship's session, for OutcomeLaunched
	Why        string // for OutcomeCantRun and OutcomeDropped
}

// Cards posts a card to a fleet's chat. A second post of one ID adds nothing.
type Cards interface {
	PostCard(fleet string, c Card) error
}

// Runner fires a fleet's commitments when their trigger comes.
type Runner struct {
	Home        string
	Ledger      *ledger.Ledger
	Fleets      func() []string
	SourceFleet func(source string) (string, bool)
	Clock       Clock
	Ships       Ships
	Cards       Cards
}

// Changed wakes the runner: a ship, a resource or the ledger changed.
func (r *Runner) Changed() {}

// Run fires what is due until ctx ends.
func (r *Runner) Run(ctx context.Context) {}

// Tick fires what is due now.
func (r *Runner) Tick(ctx context.Context) {}
