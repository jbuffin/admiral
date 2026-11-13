package ledger

import (
	"bytes"
	"encoding/json"
	"fmt"
	"time"

	"github.com/jbuffin/admiral/internal/chatlog"
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
	// Launched is the launch stamp of the ship a ship trigger names: its session name is reused by a later ship.
	Launched string `json:"launched,omitempty"`
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
	// WaitReason is why a waiting launch has not started, as the refusal worded it.
	WaitReason string `json:"wait_reason,omitempty"`
	Shown      bool   `json:"shown,omitempty"`
}

var errNoCommitment = fmt.Errorf("%w: no such open commitment", ErrNotFound)

// AddCommitment records c.Text, c.Trigger, c.Action and c.Launch; the other fields are the ledger's.
func (l *Ledger) AddCommitment(fleet string, c Commitment, via string) (Commitment, error) {
	if !chatlog.Valid(fleet) {
		return Commitment{}, fmt.Errorf("ledger: %q is not a fleet key", fleet)
	}
	text, err := checkText(c.Text, "commitment")
	if err != nil {
		return Commitment{}, err
	}
	switch c.Trigger.Kind {
	case TriggerShipSailed, TriggerShipAnchored, TriggerPRMerged, TriggerPRClosed, TriggerIssueClosed, TriggerTime, TriggerNone:
	default:
		return Commitment{}, &RefusedError{fmt.Sprintf("the commitment trigger %q is not one of ship_sailed, ship_anchored, pr_merged, pr_closed, issue_closed, time, none", c.Trigger.Kind)}
	}
	var launch json.RawMessage
	switch c.Action {
	case ActionRemind:
	case ActionLaunch:
		if len(bytes.TrimSpace(c.Launch)) == 0 || !json.Valid(c.Launch) {
			return Commitment{}, &RefusedError{"a launch commitment needs its launch spec as a JSON object"}
		}
		if len(c.Launch) > MaxLaunchBytes {
			return Commitment{}, &RefusedError{fmt.Sprintf("the launch spec is %d bytes; at most %d are kept", len(c.Launch), MaxLaunchBytes)}
		}
		launch = c.Launch
	default:
		return Commitment{}, &RefusedError{fmt.Sprintf("the commitment action %q is not launch or remind", c.Action)}
	}
	id, err := newID()
	if err != nil {
		return Commitment{}, err
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	_, open, err := l.fold(fleet)
	if err != nil {
		return Commitment{}, err
	}
	if len(open) >= MaxCommitments {
		return Commitment{}, &RefusedError{fmt.Sprintf("the ledger is full: %d commitments are open. Tell the engineer they can cancel commitments on Preferences, then record this one again", MaxCommitments)}
	}
	trig := c.Trigger
	ev := event{V: version, TS: l.now().UTC().Truncate(time.Second), Event: EventAdded, ID: id, Kind: KindCommitment, Text: text, Via: via, Trigger: &trig, Action: c.Action, Launch: launch}
	if err := l.append(fleet, ev); err != nil {
		return Commitment{}, err
	}
	return Commitment{ID: id, Text: text, Trigger: trig, Action: c.Action, Launch: launch, TS: ev.TS, Via: via}, nil
}

// Commitments are the open and waiting ones, oldest first.
func (l *Ledger) Commitments(fleet string) ([]Commitment, error) {
	if !chatlog.Valid(fleet) {
		return nil, fmt.Errorf("ledger: %q is not a fleet key", fleet)
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	_, open, err := l.fold(fleet)
	if err != nil {
		return nil, err
	}
	if open == nil {
		open = []Commitment{}
	}
	return open, nil
}

// MarkFired moves an open launch commitment to waiting.
func (l *Ledger) MarkFired(fleet, id string) error {
	return l.commitmentEvent(fleet, id, EventFired, "")
}

func isRemind(c Commitment) bool { return c.Action == ActionRemind }

// Done closes an open or waiting commitment with a short reason.
func (l *Ledger) Done(fleet, id, reason string) error {
	return l.commitmentEvent(fleet, id, EventDone, reason)
}

// Drop closes an open or waiting commitment whose subject closed without the event.
func (l *Ledger) Drop(fleet, id, reason string) error {
	return l.commitmentEvent(fleet, id, EventDropped, reason)
}

// MarkShown records that the "still want this?" card was shown; the commitment stays open.
func (l *Ledger) MarkShown(fleet, id string) error {
	return l.commitmentEvent(fleet, id, EventShown, "")
}

// CancelCommitment closes an open or waiting commitment as the engineer.
func (l *Ledger) CancelCommitment(fleet, id string) error {
	return l.commitmentEvent(fleet, id, EventCancelled, "")
}

func (l *Ledger) commitmentEvent(fleet, id, name, reason string) error {
	if !chatlog.Valid(fleet) {
		return fmt.Errorf("ledger: %q is not a fleet key", fleet)
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	_, open, err := l.fold(fleet)
	if err != nil {
		return err
	}
	found := false
	for _, c := range open {
		if c.ID != id {
			continue
		}
		if name == EventFired && isRemind(c) {
			return &RefusedError{"a remind commitment never waits: only a launch commitment can be marked fired"}
		}
		found = true
	}
	if !found {
		return errNoCommitment
	}
	return l.append(fleet, event{V: version, TS: l.now().UTC().Truncate(time.Second), Event: name, ID: id, Reason: reason})
}
