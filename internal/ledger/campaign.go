package ledger

import (
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/jbuffin/admiral/internal/chatlog"
)

const (
	TriggerCampaign = "campaign"
	ActionWork      = "work"

	EventChildLaunched = "child_launched"
	EventChildWaiting  = "child_waiting"
	EventChildDeclined = "child_declined"
	EventChildFailed   = "child_failed"

	EventChildProposed  = "child_proposed"
	EventChildWithdrawn = "child_withdrawn"

	ModeWithYou    = "with_you"
	ModeWithoutYou = "without_you"

	ChildLaunched = "launched"
	ChildWaiting  = "waiting"
	ChildDeclined = "declined"
	ChildFailed   = "failed"

	ChildProposed  = "proposed"
	ChildWithdrawn = "withdrawn"

	// MaxChildEvents bounds a campaign commitment's child events, so the ledger stays small.
	MaxChildEvents = 200
)

// ErrChildLimit is a child event refused because the commitment already holds MaxChildEvents.
var ErrChildLimit = errors.New("ledger: the commitment holds too many child events")

// Child is one campaign child as a commitment's events left it: one entry per URL.
type Child struct {
	URL    string `json:"url"`
	State  string `json:"state"`
	Ship   string `json:"ship,omitempty"`
	Reason string `json:"reason,omitempty"`
	// Mode is how a launched child was started; a child with no mode on record reads as Without you.
	Mode string `json:"mode,omitempty"`
	// Proposals counts the questions asked about the child, so each one gets its own id.
	Proposals int `json:"proposals,omitempty"`
}

// Proposal is the open question about one child: at most one per commitment.
type Proposal struct {
	URL        string `json:"url"`
	QuestionID string `json:"question"`
}

func (c *Commitment) foldChild(ev event) {
	c.ChildEvents++
	state := map[string]string{EventChildLaunched: ChildLaunched, EventChildWaiting: ChildWaiting, EventChildDeclined: ChildDeclined, EventChildFailed: ChildFailed, EventChildProposed: ChildProposed, EventChildWithdrawn: ChildWithdrawn}[ev.Event]
	i := slices.IndexFunc(c.Children, func(ch Child) bool { return ch.URL == ev.URL })
	if i < 0 {
		c.Children = append(c.Children, Child{URL: ev.URL})
		i = len(c.Children) - 1
	}
	ch := &c.Children[i]
	ch.State, ch.Ship, ch.Reason = state, ev.Ship, ev.Reason
	switch ev.Event {
	case EventChildLaunched:
		ch.Mode = ModeWithoutYou
		if ev.Mode == ModeWithYou {
			ch.Mode = ModeWithYou
		}
	case EventChildProposed:
		ch.Proposals++
		c.Proposal = &Proposal{URL: ev.URL, QuestionID: ev.QuestionID}
	}
	if ev.Event != EventChildProposed && c.Proposal != nil && c.Proposal.URL == ev.URL {
		c.Proposal = nil
	}
	c.Launched, c.WaitingChildren = 0, 0
	for _, ch := range c.Children {
		switch ch.State {
		case ChildLaunched:
			c.Launched++
		case ChildWaiting:
			c.WaitingChildren++
		}
	}
}

// AddCampaignCommitment records the one open work-on commitment for a campaign, or returns the one already open.
func (l *Ledger) AddCampaignCommitment(fleet string, c Commitment, via string) (Commitment, bool, error) {
	if !chatlog.Valid(fleet) {
		return Commitment{}, false, fmt.Errorf("ledger: %q is not a fleet key", fleet)
	}
	text, err := checkText(c.Text, "commitment")
	if err != nil {
		return Commitment{}, false, err
	}
	if c.Trigger.Subject == "" {
		return Commitment{}, false, &RefusedError{"a campaign commitment needs its campaign"}
	}
	id, err := newID()
	if err != nil {
		return Commitment{}, false, err
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	_, open, err := l.fold(fleet)
	if err != nil {
		return Commitment{}, false, err
	}
	for _, o := range open {
		if o.Trigger.Kind == TriggerCampaign && o.Trigger.Subject == c.Trigger.Subject {
			return o, false, nil
		}
	}
	if len(open) >= MaxCommitments {
		return Commitment{}, false, &RefusedError{fmt.Sprintf("the ledger is full: %d commitments are open. Tell the engineer they can cancel commitments on Preferences, then record this one again", MaxCommitments)}
	}
	trig := Trigger{Kind: TriggerCampaign, Subject: c.Trigger.Subject}
	ev := event{V: version, TS: l.now().UTC().Truncate(time.Second), Event: EventAdded, ID: id, Kind: KindCommitment, Text: text, Via: via, Trigger: &trig, Action: ActionWork, Harness: c.Harness}
	if err := l.append(fleet, ev); err != nil {
		return Commitment{}, false, err
	}
	return Commitment{ID: id, Text: text, Trigger: trig, Action: ActionWork, TS: ev.TS, Via: via, Harness: c.Harness}, true, nil
}

func (l *Ledger) ChildLaunched(fleet, id, url, ship string) error {
	return l.ChildLaunchedAs(fleet, id, url, ship, ModeWithoutYou)
}

// ChildLaunchedAs records a launch and whether the child's ship works with the engineer or without.
func (l *Ledger) ChildLaunchedAs(fleet, id, url, ship, mode string) error {
	return l.childEvent(fleet, id, event{Event: EventChildLaunched, URL: url, Ship: ship, Mode: mode})
}

// ChildProposed records the question asked about a With-you child; a commitment has one open at a time.
func (l *Ledger) ChildProposed(fleet, id, url, questionID string) error {
	return l.childEvent(fleet, id, event{Event: EventChildProposed, URL: url, QuestionID: questionID})
}

// ChildWithdrawn records that the open question about a child is gone, and why.
func (l *Ledger) ChildWithdrawn(fleet, id, url, reason string) error {
	return l.childEvent(fleet, id, event{Event: EventChildWithdrawn, URL: url, Reason: reason})
}
func (l *Ledger) ChildWaiting(fleet, id, url string) error {
	return l.childEvent(fleet, id, event{Event: EventChildWaiting, URL: url})
}
func (l *Ledger) ChildDeclined(fleet, id, url string) error {
	return l.childEvent(fleet, id, event{Event: EventChildDeclined, URL: url})
}
func (l *Ledger) ChildFailed(fleet, id, url, reason string) error {
	return l.childEvent(fleet, id, event{Event: EventChildFailed, URL: url, Reason: reason})
}

func (l *Ledger) childEvent(fleet, id string, ev event) error {
	if !chatlog.Valid(fleet) {
		return fmt.Errorf("ledger: %q is not a fleet key", fleet)
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	_, open, err := l.fold(fleet)
	if err != nil {
		return err
	}
	i := slices.IndexFunc(open, func(c Commitment) bool { return c.ID == id })
	if i < 0 {
		return errNoCommitment
	}
	if open[i].Action != ActionWork {
		return &RefusedError{"only a campaign commitment records child events"}
	}
	if open[i].ChildEvents >= MaxChildEvents {
		return ErrChildLimit
	}
	if ev.Event == EventChildProposed && open[i].Proposal != nil {
		return &RefusedError{"a proposal is already open"}
	}
	ev.V, ev.TS, ev.ID = version, l.now().UTC().Truncate(time.Second), id
	return l.append(fleet, ev)
}
