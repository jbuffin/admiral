package ships

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/jbuffin/admiral/internal/config"
	"github.com/jbuffin/admiral/internal/inbox"
	"github.com/jbuffin/admiral/internal/launcher"
	"github.com/jbuffin/admiral/internal/moor"
)

// propose writes the Inbox proposals for the stranger feedback d found, and returns the state to save: d.Next with their event IDs
// recorded. The files come first, so a crash before the state write repeats the batch, and its deterministic id makes that a no-op.
func (s *Service) propose(r moor.Record, d moor.Decision, now time.Time) moor.WakeState {
	if len(d.T3) == 0 {
		return d.Next
	}
	batches, next := moor.ProposeT3(r.Name, d, now, config.ValidLogin)
	if len(batches) == 0 {
		return next
	}
	s.mooredMu.Lock()
	moored := s.stillMoored(r)
	s.mooredMu.Unlock()
	if !moored {
		return d.Next
	}
	for _, b := range batches {
		p := inbox.WakeProposal{Ship: r.Name, Session: r.Session, PR: b.PR, Login: b.Login}
		for _, e := range b.Events {
			p.Events = append(p.Events, inbox.WakeProposalEvent{ID: e.ID, Kind: string(e.Kind), URL: e.URL, At: e.At, Body: e.Body})
		}
		if err := inbox.WriteWakeProposal(s.Launcher.Home, b.ID, p); err != nil {
			fmt.Fprintf(os.Stderr, "admiral: ship %s: its wake proposal cannot be written: %v\n", r.Name, err)
			return d.Next
		}
	}
	return next
}

// LaunchProposal starts the feedback round a wake proposal asks for, handing the ship only the proposal's events.
// It is the engineer's own act, so it runs in autopilot but is never blocked by the cap. The cursor stays where it is:
// moving it would pass any older waking event still waiting out its debounce, and the recorded IDs already keep these events from coming back.
func (s *Service) LaunchProposal(ctx context.Context, p inbox.WakeProposal) (Ship, *launcher.Error) {
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
