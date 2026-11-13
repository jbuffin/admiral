package ledger

import (
	"encoding/json"
	"time"
)

const (
	KindCommitment = "commitment"
	EventFired     = "fired"
	EventDone      = "done"
	EventDropped   = "dropped"
	EventShown     = "shown"

	MaxCommitments = 30
	// MaxLaunchBytes bounds a stored launch spec, so the ledger stays under what PreCompact reads.
	MaxLaunchBytes = 16 << 10

	TriggerShipSailed   = "ship_sailed"
	TriggerShipAnchored = "ship_anchored"
	TriggerPRMerged     = "pr_merged"
	TriggerPRClosed     = "pr_closed"
	TriggerIssueClosed  = "issue_closed"
	TriggerTime         = "time"
	TriggerNone         = "none"

	ActionLaunch = "launch"
	ActionRemind = "remind"
)

// Trigger is what a commitment waits for; Subject is a ship session id, a PR or issue URL, or an RFC 3339 time.
type Trigger struct {
	Kind    string `json:"kind"`
	Subject string `json:"subject,omitempty"`
}

// Commitment is a promise as it stands after folding the log.
type Commitment struct {
	ID      string          `json:"id"`
	Text    string          `json:"text"`
	Trigger Trigger         `json:"trigger"`
	Action  string          `json:"action"`
	Launch  json.RawMessage `json:"launch,omitempty"`
	TS      time.Time       `json:"ts"`
	Via     string          `json:"via,omitempty"`
	Waiting bool            `json:"waiting,omitempty"`
	Shown   bool            `json:"shown,omitempty"`
}

// AddCommitment records c.Text, c.Trigger, c.Action and c.Launch; the other fields are the ledger's.
func (l *Ledger) AddCommitment(fleet string, c Commitment, via string) (Commitment, error) {
	return Commitment{}, nil
}

// Commitments are the open and waiting ones, oldest first.
func (l *Ledger) Commitments(fleet string) ([]Commitment, error) { return nil, nil }

// MarkFired moves an open launch commitment to waiting.
func (l *Ledger) MarkFired(fleet, id string) error { return nil }

// Done closes an open or waiting commitment with a short reason.
func (l *Ledger) Done(fleet, id, reason string) error { return nil }

// Drop closes an open or waiting commitment whose subject closed without the event.
func (l *Ledger) Drop(fleet, id, reason string) error { return nil }

// MarkShown records that the "still want this?" card was shown; the commitment stays open.
func (l *Ledger) MarkShown(fleet, id string) error { return nil }

// CancelCommitment closes an open or waiting commitment as the engineer.
func (l *Ledger) CancelCommitment(fleet, id string) error { return nil }
