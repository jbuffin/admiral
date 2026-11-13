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

	ChildLaunched = "launched"
	ChildWaiting  = "waiting"
	ChildDeclined = "declined"
	ChildFailed   = "failed"

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
}

func (c *Commitment) foldChild(ev event) {
	c.ChildEvents++
	state := map[string]string{EventChildLaunched: ChildLaunched, EventChildWaiting: ChildWaiting, EventChildDeclined: ChildDeclined, EventChildFailed: ChildFailed}[ev.Event]
	i := slices.IndexFunc(c.Children, func(ch Child) bool { return ch.URL == ev.URL })
	if i < 0 {
		c.Children = append(c.Children, Child{URL: ev.URL})
		i = len(c.Children) - 1
	}
	c.Children[i].State, c.Children[i].Ship, c.Children[i].Reason = state, ev.Ship, ev.Reason
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
	return l.childEvent(fleet, id, event{Event: EventChildLaunched, URL: url, Ship: ship})
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
	ev.V, ev.TS, ev.ID = version, l.now().UTC().Truncate(time.Second), id
	return l.append(fleet, ev)
}
