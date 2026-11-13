package ledger

import "errors"

const (
	TriggerCampaign = "campaign"
	ActionWork      = "work"

	EventChildLaunched = "child_launched"
	EventChildWaiting  = "child_waiting"
	EventChildDeclined = "child_declined"
	EventChildFailed   = "child_failed"

	ChildLaunched = "launched"
	ChildWaiting  = "waiting"
	ChildDeclined = "declined"
	ChildFailed   = "failed"

	// MaxChildEvents bounds a campaign commitment's child events, so the ledger stays small.
	MaxChildEvents = 200
)

// ErrChildLimit is a child event refused because the commitment already holds MaxChildEvents.
var ErrChildLimit = errors.New("ledger: the commitment holds too many child events")

var errNotImplemented = errors.New("not implemented")

// Child is one campaign child as a commitment's events left it: one entry per URL.
type Child struct {
	URL    string `json:"url"`
	State  string `json:"state"`
	Ship   string `json:"ship,omitempty"`
	Reason string `json:"reason,omitempty"`
}

// AddCampaignCommitment records the one open work-on commitment for a campaign, or returns the one already open.
func (l *Ledger) AddCampaignCommitment(fleet string, c Commitment, via string) (Commitment, bool, error) {
	return Commitment{}, false, errNotImplemented
}

func (l *Ledger) ChildLaunched(fleet, id, url, ship string) error { return errNotImplemented }
func (l *Ledger) ChildWaiting(fleet, id, url string) error        { return errNotImplemented }
func (l *Ledger) ChildDeclined(fleet, id, url string) error       { return errNotImplemented }
func (l *Ledger) ChildFailed(fleet, id, url, reason string) error { return errNotImplemented }
