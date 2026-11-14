// Package reviewer is the step reviewer: one judge call that weighs an event routine's launch against the fleet's Checks.
package reviewer

import (
	"context"

	"github.com/jbuffin/admiral/internal/judge"
	"github.com/jbuffin/admiral/internal/routines"
	"github.com/jbuffin/admiral/internal/standing"
)

// Reviewer judges one routine's provisional launch on the fleet's judge harness.
type Reviewer struct {
	// Choose is the fleet's judge harness; an error means none is proven.
	Choose func(fleet string) (judge.Choice, error)
	// Judge runs the call; judge.Run when nil.
	Judge func(context.Context, judge.Choice, judge.Call) (judge.Reply, error)
	// Limits is the fleet's Limits, for the per-call budget.
	Limits func(fleet string) standing.Limits
}

// CheckID is the first 8 lowercase hex characters of the sha256 of a Check's text.
func CheckID(text string) string { return "" }

// Review is the scheduler's Review seam.
func (r *Reviewer) Review(in routines.ReviewInput) routines.Review {
	return routines.Review{Verdict: standing.Ask, Reason: "not implemented"}
}
