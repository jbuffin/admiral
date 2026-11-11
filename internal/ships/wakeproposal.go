package ships

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"slices"
	"time"

	"github.com/jbuffin/admiral/internal/config"
	"github.com/jbuffin/admiral/internal/inbox"
	"github.com/jbuffin/admiral/internal/launcher"
	"github.com/jbuffin/admiral/internal/moor"
)

// propose writes the Inbox proposals for the stranger feedback d found, under wakeMu with the mooring re-checked, and returns the state
// to save (d.Next with their event IDs recorded) and the batches. The files come first, so a crash before the state write repeats the batch,
// and its deterministic id makes that a no-op. ok is false when nothing may go on this poll: a proposal could not be written, the state
// changed since the decision read it or the mooring ended. The round's cursor must not pass an event that was not proposed, so the caller then neither wakes nor saves.
func (s *Service) propose(r moor.Record, was moor.WakeState, d moor.Decision, now time.Time) (next moor.WakeState, batches []moor.ProposalBatch, ok bool) {
	if len(d.T3) == 0 {
		return d.Next, nil, true
	}
	batches, next = moor.ProposeT3(r.Name, d, now, config.ValidLogin)
	if len(batches) == 0 {
		return next, nil, true
	}
	s.wakeMu.Lock()
	defer s.wakeMu.Unlock()
	s.mooredMu.Lock()
	moored := s.stillMoored(r)
	s.mooredMu.Unlock()
	if !moored {
		return d.Next, nil, false
	}
	if cur, err := moor.ReadWake(s.Launcher.Home, r.Name); err != nil || !sameWake(cur, was) {
		return d.Next, nil, false
	}
	for _, b := range batches {
		p := inbox.WakeProposal{Ship: r.Name, Session: r.Session, PR: b.PR, Login: b.Login}
		for _, e := range b.Events {
			p.Events = append(p.Events, inbox.WakeProposalEvent{ID: e.ID, Kind: string(e.Kind), URL: e.URL, At: e.At, Body: e.Body})
		}
		if err := inbox.WriteWakeProposal(s.Launcher.Home, b.ID, p); err != nil {
			fmt.Fprintf(os.Stderr, "admiral: ship %s: its wake proposal cannot be written: %v\n", r.Name, err)
			return d.Next, nil, false
		}
	}
	return next, batches, true
}

// withProposed is the state the poll read plus the events of batches recorded as proposed, the cursors untouched:
// what a refused round saves, so the events are not proposed again.
func withProposed(state moor.WakeState, batches []moor.ProposalBatch) moor.WakeState {
	out := state
	out.PRs = make(map[string]moor.PRWake, len(state.PRs))
	for u, ps := range state.PRs {
		out.PRs[u] = ps
	}
	for _, b := range batches {
		ps := out.PRs[b.PR]
		ps.Proposed = append(slices.Clone(ps.Proposed), proposedOf(b.Events)...)
		out.PRs[b.PR] = ps
	}
	return out
}

func proposedOf(events []moor.Event) []moor.ProposedEvent {
	out := make([]moor.ProposedEvent, len(events))
	for i, e := range events {
		out[i] = moor.ProposedEvent{ID: e.ID, At: e.At}
	}
	return out
}

// proposalHeld is whether the moored ship p names is the one its session holds and its PR's wake state proposed every event (recorded, or passed by the cursor and pruned): an item
// is a file anyone with disk access can write, so it only chooses among what the poll proposed.
func (s *Service) proposalHeld(p inbox.WakeProposal) bool {
	if p.Login != moor.Ghost && !config.ValidLogin(p.Login) {
		return false
	}
	s.mooredMu.Lock()
	var rec moor.Record
	found := false
	for _, c := range s.moored {
		if c.Session == p.Session {
			rec, found = c, true
			break
		}
	}
	s.mooredMu.Unlock()
	if !found || rec.Name != p.Ship || !slices.Contains(rec.PRs, p.PR) {
		return false
	}
	state, err := moor.ReadWake(s.Launcher.Home, rec.Name)
	if err != nil {
		return false
	}
	for _, e := range p.Events {
		if !state.PRs[p.PR].WasProposed(e.ID, e.At) {
			return false
		}
	}
	return true
}

// LaunchProposal starts the feedback round a wake proposal asks for, handing the ship only the proposal's events.
// It is the engineer's own act, so it runs in autopilot but is never blocked by the cap. The cursor stays where it is:
// moving it would pass any older waking event still waiting out its debounce, and the recorded IDs already keep these events from coming back.
func (s *Service) LaunchProposal(ctx context.Context, p inbox.WakeProposal) (Ship, *launcher.Error) {
	if !s.proposalHeld(p) {
		return Ship{}, &launcher.Error{Status: http.StatusNotFound, Msg: "no moored ship holds this proposal"}
	}
	events := make([]moor.Event, len(p.Events))
	for i, e := range p.Events {
		login := p.Login
		if login == moor.Ghost {
			login = ""
		}
		events[i] = moor.Event{Kind: moor.EventKind(e.Kind), PR: p.PR, ID: e.ID, Author: moor.Actor{Login: login}, At: e.At, URL: e.URL, Body: e.Body}
	}
	feedback := moor.FeedbackText(p.PR, events)
	prompt := func(rec moor.Record) string {
		return rec.Resume + " feedback round " + fmt.Sprint(rec.Round+1) + " on " + p.PR
	}
	write := func(rec moor.Record, dir string) (undoFunc, error) {
		undo, _ := s.handRoundStart(rec, dir)
		if _, err := moor.WriteFeedback(dir, rec.Round+1, feedback); err != nil {
			if undo != nil {
				undo()
			}
			return nil, err
		}
		return undo, nil
	}
	return s.wake(p.Session, prompt, "proposal", p.PR, write)
}
