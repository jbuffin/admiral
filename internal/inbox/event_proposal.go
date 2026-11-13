package inbox

import (
	"context"
	"errors"
	"net/http"
	"time"
)

// KindEventProposal lives only in the AdmiralSession directory: an Ask first outside event, to launch, dismiss or mute before it expires.
const KindEventProposal = "event_proposal"

// EventProposal is the item's event_proposal object. It holds no event text: AdmiralBot reads every item field.
type EventProposal struct {
	Fleet       string    `json:"fleet"`
	Routine     string    `json:"routine"`
	RoutineName string    `json:"routine_name"`
	Trigger     string    `json:"trigger"`
	Tier        string    `json:"tier"`
	ExpiresAt   time.Time `json:"expires_at"`
}

// EventDetail is what Peek shows of the event, read from the detail route that the actor guard denies.
type EventDetail struct {
	Summary string `json:"summary"`
	Title   string `json:"title"`
	Sender  string `json:"sender"`
	Link    string `json:"link"`
	// Metadata is true when Summary is the metadata line, not a Lookout summary.
	Metadata bool `json:"metadata"`
	// MuteBy lists the Mute rule choices: sender and dedupe_key, each only when the event has one.
	MuteBy []string `json:"mute_by"`
}

// LaunchEventFunc launches an event proposal's routine. status is 0 on success, else the refusal's HTTP status with msg.
type LaunchEventFunc func(ctx context.Context, p EventProposal) (ship any, status int, msg string)

// PublishEventProposal writes the item event-<trigger> and keeps the routine's other open proposals.
func (s *Service) PublishEventProposal(p EventProposal, d EventDetail) (string, error) {
	return "", errors.New("not implemented")
}

func (s *Service) serveEventDetail(w http.ResponseWriter, r *http.Request) {
	writeError(w, http.StatusNotImplemented, "not implemented")
}
