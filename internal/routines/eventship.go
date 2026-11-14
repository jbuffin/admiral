package routines

import (
	"context"
	"strings"

	"github.com/jbuffin/admiral/internal/launcher"
	"github.com/jbuffin/admiral/internal/standing"
)

const noEventText = "(the event carried no text)"

// eventText is what the launched ship reads: the Lookout's text, else the title and one line about the event.
// The line is kept here, not shared with lookout.Metadata: lookout imports routines, so routines can't import it.
func eventText(ev Event, en Enrichment, sender string) string {
	if t := strings.TrimSpace(en.Text); t != "" {
		return en.Text
	}
	repo := RepoOfRef(ev.Ref)
	if repo == "" {
		repo = en.Repo
	}
	key := GitHubKey(ev.Ref)
	if key == "" {
		key = en.DedupeKey
	}
	var meta []string
	add := func(name, v string) {
		if v = strings.TrimSpace(v); v != "" {
			meta = append(meta, name+": "+v)
		}
	}
	add("kind", ev.Kind)
	add("repo", repo)
	if _, n, ok := strings.Cut(key, "#"); ok {
		add("number", n)
	}
	add("sender", sender)
	add("link", ev.Ref)
	line := strings.Join(meta, ", ")
	title := strings.TrimSpace(ev.Title)
	switch {
	case title != "" && line != "":
		return title + "\n\n" + line
	case title != "":
		return title
	case line != "":
		return line
	}
	return noEventText
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

// launchEvent starts the ship of a Launched decision, with s.mu held. It returns done when the launch or its failure is
// recorded, and started when a ship was launched, with the saved launch record; a refusal that a later try could clear returns the decision as a
// proposal for the caller to record and act on. A launch never waits and never falls back to a less confined one.
func (s *Scheduler) launchEvent(fleet string, r Routine, ev Event, d Decision, en Enrichment, sender string, rec Trigger) (Decision, Trigger, bool, bool) {
	l := *r.Do.Launch
	rec.Action = describe(r)
	fail := func(reason string) (Decision, Trigger, bool, bool) {
		rec.Outcome, rec.Reason = Failed, reason
		s.recordFor(fleet, r, rec)
		return d, Trigger{}, false, true
	}
	if _, held := s.Ships.Holder(routineKey(r.ID)); held {
		return proposeInstead(d, ReasonBusy), Trigger{}, false, false
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
			return proposeInstead(d, reason), Trigger{}, false, false
		}
		return fail(lerr.Msg)
	}
	rec.ID, rec.Outcome, rec.Ship, rec.WorkerLaunched = req.TriggerID, Launched, ref.Session, true
	saved, ok := s.recordFor(fleet, r, rec)
	if ok && ref.Session != "" {
		s.watched[saved.ID] = watch{fleet: fleet, routine: r.ID, session: ref.Session, at: rec.At}
	}
	if !ok {
		saved = rec
	}
	return d, saved, true, true
}
