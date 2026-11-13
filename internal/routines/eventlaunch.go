package routines

import (
	"context"
	"errors"
	"net/url"
	"strings"
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

// LaunchProposed launches the routine's stored launch spec for an event proposal this scheduler issued, and never waits.
// The prompt is the routine's template and the event's link, never the event's text.
func (s *Scheduler) LaunchProposed(ctx context.Context, fleet, routineID, triggerID string) (Trigger, error) {
	s.init()
	s.mu.Lock()
	defer s.mu.Unlock()
	list, err := s.Store.List(fleet)
	if err != nil {
		return Trigger{}, err
	}
	i := indexOf(list, routineID)
	if i < 0 {
		return Trigger{}, ErrNotFound
	}
	r := list[i]
	if !r.Enabled {
		return Trigger{}, ErrOff
	}
	l := r.Do.Launch
	if l == nil {
		return Trigger{}, ErrNotIssued
	}
	recs, err := s.Store.Records(fleet)
	if err != nil {
		return Trigger{}, err
	}
	var prop *Trigger
	for j := range recs {
		switch {
		case recs[j].ProposalOf == triggerID && recs[j].Outcome == Launched:
			return Trigger{}, ErrNotIssued
		case recs[j].ID == triggerID:
			prop = &recs[j]
		}
	}
	if triggerID == "" || prop == nil || prop.Routine != r.ID || prop.Outcome != Proposed || prop.Via != "" {
		return Trigger{}, ErrNotIssued
	}
	now := s.Clock.Now()
	if !now.Before(prop.At.Add(EventProposalExpiry)) {
		return Trigger{}, ErrExpired
	}
	if _, held := s.Ships.Holder(routineKey(r.ID)); held {
		return Trigger{}, ErrBusy
	}
	spec := *l
	if link := eventLink(prop.Event); link != "" {
		spec.Text = strings.TrimRight(spec.Text, "\n") + "\n\n" + link
	}
	rec := Trigger{Routine: r.ID, Tier: prop.Tier, At: now, Via: ViaProposal, ProposalOf: prop.ID, Event: prop.Event,
		Sender: prop.Sender, DedupeKey: prop.DedupeKey, Action: describe(r)}
	if err := s.launch(ctx, fleet, r, spec, rec, now, true); err != nil {
		return Trigger{}, err
	}
	recent, err := s.Store.Recent(fleet, r.ID, 1)
	if err != nil || len(recent) == 0 {
		return Trigger{}, err
	}
	return recent[0], nil
}

// eventLink is ref when it is one plain http(s) URL, else empty: a plugin's ref is untrusted text.
func eventLink(ref string) string {
	u, err := url.Parse(ref)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || len(ref) > 2048 {
		return ""
	}
	for _, r := range ref {
		if r <= ' ' || r == 0x7f || r > 0x7e {
			return ""
		}
	}
	return ref
}

// LiveShip is a listed ship a routine launched on an event, with the dedupe_key of its trigger record.
type LiveShip struct{ Session, DedupeKey string }

// ListedShip is what the lookup reads of a listed ship: Routine is its routine:<id> key and Launched its RFC 3339 launch time, when it has one.
type ListedShip struct{ Session, Fleet, Routine, Launched string }

// EventShips picks the listed ships of fleet that an event launch started. recs is newest first, so the first record per session is the
// current launch; session names are reused, so the ship must still hold that record's routine key and not predate the record.
func EventShips(fleet string, recs []Trigger, listed []ListedShip) []LiveShip {
	newest := map[string]Trigger{}
	for _, r := range recs {
		if _, ok := newest[r.Ship]; !ok && r.Ship != "" && r.Event != "" {
			newest[r.Ship] = r
		}
	}
	var out []LiveShip
	for _, sh := range listed {
		rec, ok := newest[sh.Session]
		if !ok || sh.Fleet != fleet || sh.Routine != routineKey(rec.Routine) {
			continue
		}
		if at, err := time.Parse(time.RFC3339, sh.Launched); err == nil && at.Before(rec.At.Truncate(time.Second)) {
			continue
		}
		out = append(out, LiveShip{Session: sh.Session, DedupeKey: rec.DedupeKey})
	}
	return out
}

// EventLive is the EventLoad over the listed event-launched ships. When Listed is set, Load reads them against records the
// caller already holds, so one event reads the trigger log once; Ships answers the other callers.
type EventLive struct {
	Ships  func(fleet string) []LiveShip
	Listed func() []ListedShip
}

// Load is the worker ship count and the held dedupe_keys, read against recs when Listed is set.
func (l EventLive) Load(fleet string, recs []Trigger) (int, []string) {
	ships := l.live(fleet, recs)
	return len(ships), dedupeKeysOf(ships)
}

func (l EventLive) live(fleet string, recs []Trigger) []LiveShip {
	if l.Listed != nil {
		return EventShips(fleet, recs, l.Listed())
	}
	return l.Ships(fleet)
}

func (l EventLive) WorkerShips(fleet string) int { return len(l.Ships(fleet)) }

func (l EventLive) DedupeKeys(fleet string) []string { return dedupeKeysOf(l.Ships(fleet)) }

func dedupeKeysOf(ships []LiveShip) []string {
	var keys []string
	for _, sh := range ships {
		if sh.DedupeKey != "" {
			keys = append(keys, sh.DedupeKey)
		}
	}
	return keys
}
