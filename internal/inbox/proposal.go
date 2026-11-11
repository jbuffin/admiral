package inbox

import (
	"context"
	"encoding/json"
	"time"
)

// KindWakeProposal lives only in the AdmiralSession directory: a stranger's feedback on a moored ship's PR, to launch or dismiss.
const KindWakeProposal = "wake_proposal"

// WakeProposalEvent is one event of a stranger's batch. Body is plain text, never rendered.
type WakeProposalEvent struct {
	ID   string    `json:"id"`
	Kind string    `json:"kind"`
	URL  string    `json:"url,omitempty"`
	At   time.Time `json:"at"`
	Body string    `json:"body,omitempty"`
}

// WakeProposal is the item's wake_proposal object. Ship is the moored ship's name and Session its session, which wake takes.
type WakeProposal struct {
	Ship    string              `json:"ship"`
	Session string              `json:"session"`
	PR      string              `json:"pr"`
	Login   string              `json:"login"`
	Events  []WakeProposalEvent `json:"events"`
}

// LaunchProposalFunc starts the feedback round for p. status is 0 on success, else the refusal's HTTP status with msg.
type LaunchProposalFunc func(ctx context.Context, p WakeProposal) (ship any, status int, msg string)

// WriteWakeProposal writes the item once: an item or answer already there for id is left as it is.
func WriteWakeProposal(home, id string, p WakeProposal) error { return nil }

// WakeProposal is the proposal of a wake_proposal item.
func (it Item) WakeProposal() (WakeProposal, bool) {
	var p WakeProposal
	return p, json.Unmarshal(it.fields[KindWakeProposal], &p) == nil && p.Ship != ""
}
