package routines

import (
	"context"
	"errors"
	"time"
)

// EventProposalExpiry is how long an event proposal stays open.
const EventProposalExpiry = 24 * time.Hour

var (
	// ErrNotIssued is a launch of a trigger that is not an open event proposal for this fleet and routine.
	ErrNotIssued = errors.New("not an open event proposal for this routine")
	// ErrExpired is a launch of an event proposal past its expiry.
	ErrExpired = errors.New("the proposal expired")
)

// LaunchProposed launches the routine's stored launch spec for an event proposal this scheduler issued.
func (s *Scheduler) LaunchProposed(ctx context.Context, fleet, routineID, triggerID string) (Trigger, error) {
	return Trigger{}, errors.New("not implemented")
}

// LiveShip is a listed ship a routine launched on an event, with the dedupe_key of its trigger record.
type LiveShip struct{ Session, DedupeKey string }

// EventLive is the EventLoad over the listed event-launched ships.
type EventLive struct {
	Ships func(fleet string) []LiveShip
}

func (l EventLive) WorkerShips(fleet string) int { return 0 }

func (l EventLive) DedupeKeys(fleet string) []string { return nil }
