package moor

import "time"

// ProposedEvent is a T3 event already put in a proposal, kept until the PR's cursor passes it.
type ProposedEvent struct {
	ID string    `json:"id"`
	At time.Time `json:"at"`
}

// ProposalBatch is the not-yet-proposed T3 events of one author on one PR.
type ProposalBatch struct {
	PR     string
	Login  string
	ID     string // the proposal's deterministic id
	Events []Event
}

// ProposeT3 groups d.T3 into batches ready to propose and returns d.Next with their IDs recorded and the stale entries pruned.
func ProposeT3(ship string, d Decision, now time.Time, validLogin func(string) bool) ([]ProposalBatch, WakeState) {
	return nil, d.Next
}

// ProposalID is "wake-" and the first 20 hex characters of a SHA-256 over the ship, PR, login and sorted event IDs.
func ProposalID(ship, pr, login string, eventIDs []string) string {
	return ""
}
