package moor

import (
	"crypto/sha256"
	"encoding/hex"
	"slices"
	"sort"
	"strings"
	"time"
)

// Ghost is the login GitHub shows for a deleted account.
const Ghost = "ghost"

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
// A batch waits until its author's newest event is older than WakeDebounce, unless d wakes the ship on that PR: the round's cursor passes every event there.
func ProposeT3(ship string, d Decision, now time.Time, validLogin func(string) bool) ([]ProposalBatch, WakeState) {
	next := cloneState(d.Next)
	type key struct{ pr, login string }
	groups := map[key][]Event{}
	var order []key
	for _, e := range d.T3 {
		if recorded(d.Next.PRs[e.PR].Proposed, e.ID) {
			continue
		}
		login := e.Author.Login
		if login == "" || !validLogin(login) {
			login = Ghost
		}
		k := key{e.PR, login}
		if _, ok := groups[k]; !ok {
			order = append(order, k)
		}
		groups[k] = append(groups[k], e)
	}

	var batches []ProposalBatch
	for _, k := range order {
		events := groups[k]
		sort.SliceStable(events, func(i, j int) bool { return events[i].At.Before(events[j].At) })
		newest := events[len(events)-1].At
		if !(d.Wake && d.PR == k.pr) && now.Sub(newest) < WakeDebounce {
			continue
		}
		ids := make([]string, len(events))
		ps := next.PRs[k.pr]
		if next.PRs == nil {
			next.PRs = map[string]PRWake{}
		}
		for i, e := range events {
			ids[i] = e.ID
			ps.Proposed = append(ps.Proposed, ProposedEvent{ID: e.ID, At: e.At})
		}
		next.PRs[k.pr] = ps
		batches = append(batches, ProposalBatch{PR: k.pr, Login: k.login, ID: ProposalID(ship, k.pr, k.login, ids), Events: events})
	}
	// A batch's events may sit behind a cursor this decision already advanced; they go with the pruning, having been proposed.
	for pr, ps := range next.PRs {
		var keep []ProposedEvent
		for _, p := range ps.Proposed {
			if newerThan(ps.Cursor, p.At, p.ID) {
				keep = append(keep, p)
			}
		}
		ps.Proposed = keep
		next.PRs[pr] = ps
	}
	return batches, next
}

func recorded(ps []ProposedEvent, id string) bool {
	return slices.ContainsFunc(ps, func(p ProposedEvent) bool { return p.ID == id })
}

// ProposalID is "wake-" and the first 20 hex characters of a SHA-256 over the ship, PR, login and sorted event IDs.
func ProposalID(ship, pr, login string, eventIDs []string) string {
	ids := slices.Clone(eventIDs)
	sort.Strings(ids)
	sum := sha256.Sum256([]byte(strings.Join(append([]string{ship, pr, login}, ids...), "\x00")))
	return "wake-" + hex.EncodeToString(sum[:])[:20]
}

// Proposed is whether the poll proposed the event: its ID is recorded, or the cursor has passed it and the record went with the pruning.
func (p PRWake) WasProposed(id string, at time.Time) bool {
	return recorded(p.Proposed, id) || !newerThan(p.Cursor, at, id)
}
