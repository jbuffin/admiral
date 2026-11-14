package routines

import (
	"context"
	"strings"
	"time"

	"github.com/jbuffin/admiral/internal/launcher"
	"github.com/jbuffin/admiral/internal/standing"
)

const noEventText = "(the event carried no text)"

// eventText is what the launched ship reads: the Lookout's text, else the title and one line about the event.
func eventText(ev Event, en Enrichment, sender string) string {
	if t := strings.TrimSpace(en.Text); t != "" {
		return en.Text
	}
	title := strings.TrimSpace(ev.Title)
	if title == "" && ev.Ref == "" {
		return noEventText
	}
	meta := []string{"kind: " + ev.Kind}
	if repo := RepoOfRef(ev.Ref); repo != "" {
		meta = append(meta, "repo: "+repo)
	}
	meta = append(meta, "sender: "+sender)
	if ev.Ref != "" {
		meta = append(meta, "link: "+ev.Ref)
	}
	return title + "\n\n" + strings.Join(meta, ", ")
}

// proposeInstead turns a launch that can't happen now into the A1 proposal of the decision, with reason.
func proposeInstead(d Decision, reason string) Decision {
	d.Outcome, d.Level, d.Restricted = Proposed, standing.A1, ""
	d.Reasons = append(d.Reasons, reason)
	return d
}

func refusalReason(e *launcher.Error) (string, bool) {
	switch e.Cause {
	case launcher.CauseCap:
		return ReasonGlobalCap, true
	case launcher.CauseQuota:
		return ReasonQuotaHold, true
	case launcher.CauseResources:
		return ReasonResources, true
	case launcher.CauseHost:
		return ReasonHostUnk, true
	case launcher.CauseRoutineHeld:
		return ReasonBusy, true
	}
	return "", false
}

// launchEvent starts the ship of a Launched decision. It returns done when the launch or its failure is recorded; a
// refusal that a later try could clear returns the decision as a proposal for the caller to record and act on.
// A launch never waits and never falls back to a less confined one.
func (s *Scheduler) launchEvent(fleet string, r Routine, ev Event, d Decision, en Enrichment, sender string, rec Trigger) (Decision, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	l := *r.Do.Launch
	rec.Action = describe(r)
	fail := func(reason string) (Decision, bool) {
		rec.Outcome, rec.Reason = Failed, reason
		s.recordFor(fleet, r, rec)
		return d, true
	}
	if _, held := s.Ships.Holder(routineKey(r.ID)); held {
		return proposeInstead(d, ReasonBusy), false
	}
	skill, err := s.gates(fleet, l)
	if err != nil {
		return fail(err.Error())
	}
	req := launcher.Request{
		Fleet: fleet, Harness: l.Harness, Dir: l.Dir, Text: l.Text, Model: l.Model, Effort: l.Effort,
		Skill: skill, Resources: l.Resources, ExtraArgs: l.Args, Autopilot: true, Unattended: true,
		RoutineKey: routineKey(r.ID), TriggerID: newID(), WorkerLaunched: true,
		Template: l.Text, EventText: eventText(ev, en, sender),
	}
	if d.Level == standing.A3 {
		req.Restricted = d.Restricted
		rec.Restricted = d.Restricted
	}
	ref, lerr := s.Ships.Launch(context.Background(), req)
	if lerr != nil {
		if reason, ok := refusalReason(lerr); ok {
			return proposeInstead(d, reason), false
		}
		return fail(lerr.Msg)
	}
	rec.ID, rec.Outcome, rec.Ship, rec.WorkerLaunched = req.TriggerID, Launched, ref.Session, true
	if saved, ok := s.recordFor(fleet, r, rec); ok && ref.Session != "" {
		s.watched[saved.ID] = watch{fleet: fleet, routine: r.ID, session: ref.Session, at: rec.At}
	}
	return d, true
}

// tripEvent is the breaker for an event routine. No tick walks an event routine's ships, so before the count it
// turns the records of its recent ships that ended failed into failed ones.
func (s *Scheduler) tripEvent(fleet string, r Routine, now time.Time) Routine {
	if r.Before != Act {
		return r
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	recs, err := s.Store.Recent(fleet, r.ID, 0)
	if err != nil {
		return r
	}
	named := map[string]bool{}
	held, holding := s.Ships.Holder(routineKey(r.ID))
	for _, rec := range recs {
		if rec.Outcome != Launched || rec.Undone || rec.Ship == "" || now.Sub(rec.At) > watchFor {
			continue
		}
		// session names are reused: only the newest launch of a name is the ship EndedFailed speaks of, and a ship that
		// now holds the key under another name says nothing about this one
		if named[rec.Ship] || holding && held.Session != rec.Ship {
			continue
		}
		named[rec.Ship] = true
		if s.Ships.EndedFailed(rec.Ship) {
			s.Store.MarkFailed(fleet, rec.ID, "the ship ended failed")
		}
	}
	return s.trip(fleet, r, now)
}
