package inbox

import (
	"context"
	"errors"
	"time"
)

// KindRoutineProposal lives only in the AdmiralSession directory: an Ask first run of a routine, to launch or dismiss before it expires.
const KindRoutineProposal = "routine_proposal"

// RoutineProposal is the item's routine_proposal object.
type RoutineProposal struct {
	Fleet       string    `json:"fleet"`
	Routine     string    `json:"routine"`
	RoutineName string    `json:"routine_name"`
	Trigger     string    `json:"trigger"`
	Action      string    `json:"action"`
	ExpiresAt   time.Time `json:"expires_at"`
}

// LaunchRoutineFunc runs the proposal's routine once. status is 0 on success, else the refusal's HTTP status with msg.
type LaunchRoutineFunc func(ctx context.Context, p RoutineProposal) (ship any, status int, msg string)

// PublishRoutineProposal writes the item routine-<fleet>-<trigger> once and closes the routine's earlier open one.
func (s *Service) PublishRoutineProposal(p RoutineProposal) (string, error) {
	return "", errors.New("not implemented")
}

// RoutineProposal is the proposal of a routine_proposal item.
func (it Item) RoutineProposal() (RoutineProposal, bool) { return RoutineProposal{}, false }
