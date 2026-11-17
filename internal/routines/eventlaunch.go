package routines

import (
	"context"
	"errors"
	"net/url"
	"strings"
	"time"

	"github.com/jbuffin/admiral/internal/campaigns"
	"github.com/jbuffin/admiral/internal/github"
	"github.com/jbuffin/admiral/internal/launcher"
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
	if l == nil || (r.On != nil && r.On.Kind == RetiredViewKind) {
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
	link := s.eventURL(fleet, prop.Event)
	spec.Text = launcher.EventAsk(link, l.Text)
	rec := Trigger{Routine: r.ID, Tier: prop.Tier, At: now, Via: ViaProposal, ProposalOf: prop.ID, Event: prop.Event,
		Sender: prop.Sender, DedupeKey: prop.DedupeKey, Action: describe(r)}
	if err := s.launch(ctx, fleet, r, spec, rec, now, true, link); err != nil {
		return Trigger{}, err
	}
	recent, err := s.Store.Recent(fleet, r.ID, 1)
	if err != nil || len(recent) == 0 {
		return Trigger{}, err
	}
	return recent[0], nil
}

// eventURL is ref when it is a pull request or issue URL on a host the fleet uses, else empty: any other ref is untrusted text.
func (s *Scheduler) eventURL(fleet, ref string) string {
	hosts := []string{github.DefaultHost}
	if s.GitHubHosts != nil {
		hosts = s.GitHubHosts(fleet)
	}
	for _, pull := range []bool{true, false} {
		if _, err := campaigns.ParseGitHubURL(ref, hosts, pull); err == nil {
			return strings.TrimSpace(ref)
		}
	}
	return ""
}

// EventLink is ref when it is one plain http(s) URL, else empty: a plugin's ref is untrusted text.
func EventLink(ref string) string {
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

// LiveShip is a listed ship a routine launched on an event, with the dedupe_key of its trigger record. Ended is a ship that is
// no longer underway or asking for you: it still holds its routine and dedupe_key, but not a worker slot.
type LiveShip struct {
	Session, DedupeKey string
	Ended              bool
	// Proposal is a ship an accepted event proposal launched: it holds its dedupe_key, but is no worker ship.
	Proposal bool
	// Trigger is the id of the launch record, and Launched when the ship started.
	Trigger  string
	Launched time.Time
}

// ListedShip is what the lookup reads of a listed ship: Routine is its routine:<id> key, Launched its RFC 3339 launch time, when it has one, and Status its ship status.
type ListedShip struct{ Session, Fleet, Routine, Launched, Status string }

// EventShips picks the listed ships of fleet that an event launch started on its own, not from an accepted
// proposal: the worker view of the cap, the wall clock and Stop all.
func EventShips(fleet string, recs []Trigger, listed []ListedShip) []LiveShip {
	var out []LiveShip
	for _, sh := range EventLaunchedShips(fleet, recs, listed) {
		if !sh.Proposal {
			out = append(out, sh)
		}
	}
	return out
}

// EventLaunchedShips is EventShips with the ships of accepted proposals, flagged Proposal: the dedupe view. recs is newest
// first, so the first record per session is the current launch; session names are reused, so the ship must still hold that
// record's routine key and not predate the record.
func EventLaunchedShips(fleet string, recs []Trigger, listed []ListedShip) []LiveShip {
	newest := map[string]Trigger{}
	for _, r := range recs {
		if _, ok := newest[r.Ship]; !ok && r.Ship != "" {
			newest[r.Ship] = r
		}
	}
	var out []LiveShip
	for _, sh := range listed {
		rec, ok := newest[sh.Session]
		// a newer launch of any kind that reused the name is not an event ship
		if !ok || rec.Event == "" || sh.Fleet != fleet || sh.Routine != routineKey(rec.Routine) {
			continue
		}
		// the wall clock starts from Admiral's own record, not from a time the ship's tmux session holds
		if at, err := time.Parse(time.RFC3339, sh.Launched); err == nil && at.Before(rec.At.Truncate(time.Second)) {
			continue
		}
		out = append(out, LiveShip{Session: sh.Session, DedupeKey: rec.DedupeKey, Ended: !isLiveStatus(sh.Status), Trigger: rec.ID, Launched: rec.At, Proposal: rec.Via == ViaProposal})
	}
	return out
}

// EventLive is the EventLoad over the listed event-launched ships, proposal ships included: they hold dedupe_keys but never count as worker ships. When Listed is set, Load reads them against records the
// caller already holds, so one event reads the trigger log once; Ships answers the other callers.
type EventLive struct {
	Ships  func(fleet string) []LiveShip
	Listed func() []ListedShip
}

// Load is the worker ship count and the held dedupe_keys, read against recs when Listed is set.
func (l EventLive) Load(fleet string, recs []Trigger) (int, map[string]string) {
	ships := l.live(fleet, recs)
	return countWorking(ships), dedupeKeysOf(ships)
}

func (l EventLive) live(fleet string, recs []Trigger) []LiveShip {
	if l.Listed != nil {
		return EventLaunchedShips(fleet, recs, l.Listed())
	}
	return l.Ships(fleet)
}

func (l EventLive) WorkerShips(fleet string) int { return countWorking(l.Ships(fleet)) }

func isLiveStatus(status string) bool { return status == "underway" || status == "needs_you" }

func countWorking(ships []LiveShip) int {
	n := 0
	for _, sh := range ships {
		if !sh.Ended && !sh.Proposal {
			n++
		}
	}
	return n
}

func (l EventLive) DedupeKeys(fleet string) map[string]string { return dedupeKeysOf(l.Ships(fleet)) }

func dedupeKeysOf(ships []LiveShip) map[string]string {
	keys := map[string]string{}
	for _, sh := range ships {
		if sh.DedupeKey != "" {
			keys[sh.DedupeKey] = sh.Session
		}
	}
	return keys
}
