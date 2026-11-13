package standing_test

import (
	"testing"

	"github.com/jbuffin/admiral/internal/standing"
)

func TestEffectiveAction(t *testing.T) {
	work := standing.PresetLimits(standing.Work)
	home := standing.PresetLimits(standing.Home)
	handEditedT3 := standing.PresetLimits(standing.Home)
	handEditedT3.Ceilings.T3 = standing.Ceiling{Level: standing.A5}
	homeFleetA4 := standing.PresetLimits(standing.Home)
	homeFleetA4.Ceilings.Fleet = standing.Ceiling{Level: standing.A4}
	fleetA3 := standing.PresetLimits(standing.Home)
	fleetA3.Ceilings.Fleet = standing.Ceiling{Level: standing.A3, Restricted: standing.Investigate}

	a3 := func(r standing.Restricted) standing.Action { return standing.Action{Level: standing.A3, Restricted: r} }
	lvl := func(l standing.Level) standing.Action { return standing.Action{Level: l} }
	capable := func(e standing.Event) standing.Event { e.RestrictedHarness = true; return e }

	for _, c := range []struct {
		name string
		l    standing.Limits
		e    standing.Event
		want standing.Action
	}{
		{"the tier ceiling lowers an Act routine", work, capable(standing.Event{Tier: standing.T2, Routine: standing.A5}), lvl(standing.A1)},
		{"the routine lowers below the tier ceiling", work, capable(standing.Event{Tier: standing.T4, Routine: standing.A1}), lvl(standing.A1)},
		{"report only stays a card", home, capable(standing.Event{Tier: standing.T1, Routine: standing.A0}), lvl(standing.A0)},
		{"the fleet ceiling lowers T1", homeFleetA4, capable(standing.Event{Tier: standing.T1, Routine: standing.A5}), lvl(standing.A4)},
		{"a restricted fleet ceiling gives its level to the result", fleetA3, capable(standing.Event{Tier: standing.T1, Routine: standing.A5}), a3(standing.Investigate)},
		{"Home T2 runs a restricted ship that drafts a fix", home, capable(standing.Event{Tier: standing.T2, Routine: standing.A5}), a3(standing.DraftFix)},
		{"the lower restricted level wins between two A3 ceilings", fleetA3, capable(standing.Event{Tier: standing.T2, Routine: standing.A5}), a3(standing.Investigate)},
		{"the reviewer allows without lowering", home, capable(standing.Event{Tier: standing.T1, Routine: standing.A5, Reviewer: standing.Allow}), lvl(standing.A5)},
		{"the reviewer asks: A1", home, capable(standing.Event{Tier: standing.T1, Routine: standing.A5, Reviewer: standing.Ask}), lvl(standing.A1)},
		{"the reviewer blocks: A0", home, capable(standing.Event{Tier: standing.T1, Routine: standing.A5, Reviewer: standing.Block}), lvl(standing.A0)},
		{"T3 is clamped to A1 even when the file says A5", handEditedT3, capable(standing.Event{Tier: standing.T3, Routine: standing.A5}), lvl(standing.A1)},
		{"Work T3 stays a card", work, capable(standing.Event{Tier: standing.T3, Routine: standing.A5}), lvl(standing.A0)},
		{"Work T1 asks first: an A5 request becomes a proposal", work, capable(standing.Event{Tier: standing.T1, Routine: standing.A5}), lvl(standing.A1)},
		{"Work T1 runs an A4 request", work, capable(standing.Event{Tier: standing.T1, Routine: standing.A4}), lvl(standing.A4)},
		{"Home T4 below the threshold investigates", home, capable(standing.Event{Tier: standing.T4, Routine: standing.A5, SameKeyEvents: 9}), a3(standing.Investigate)},
		{"Home T4 at the threshold drafts a fix", home, capable(standing.Event{Tier: standing.T4, Routine: standing.A5, SameKeyEvents: 10}), a3(standing.DraftFix)},
		{"Work T4 has no threshold", work, capable(standing.Event{Tier: standing.T4, Routine: standing.A5, SameKeyEvents: 50}), a3(standing.Investigate)},
		{"no restricted-capable harness: an A3 result drops to A1", home, standing.Event{Tier: standing.T4, Routine: standing.A5, SameKeyEvents: 10}, lvl(standing.A1)},
		{"no restricted-capable harness leaves A5 alone", home, standing.Event{Tier: standing.T1, Routine: standing.A5}, lvl(standing.A5)},
		{"no restricted-capable harness leaves A4 alone", work, standing.Event{Tier: standing.T1, Routine: standing.A4}, lvl(standing.A4)},
	} {
		t.Run(c.name, func(t *testing.T) {
			if got := c.l.Effective(c.e); got != c.want {
				t.Errorf("Effective(%+v) = %+v, want %+v", c.e, got, c.want)
			}
		})
	}
}

func TestEffectiveFailsClosedAndNeverLetsALowerCeilingRaiseTheResult(t *testing.T) {
	work := standing.PresetLimits(standing.Work)
	work.Ceilings.Fleet = standing.Ceiling{Level: standing.A4}
	home := standing.PresetLimits(standing.Home)
	lvl := func(l standing.Level) standing.Action { return standing.Action{Level: l} }
	for _, c := range []struct {
		name string
		l    standing.Limits
		e    standing.Event
		want standing.Action
	}{
		{"Work T1 asks first under a fleet ceiling of A4", work, standing.Event{Tier: standing.T1, Routine: standing.A5}, lvl(standing.A1)},
		{"an unknown verdict counts as Block", home, standing.Event{Tier: standing.T1, Routine: standing.A5, Reviewer: standing.Verdict(7)}, lvl(standing.A0)},
		{"an off-ladder routine of 2 clamps to A1", home, standing.Event{Tier: standing.T1, Routine: 2}, lvl(standing.A1)},
		{"a routine below 0 clamps to A0", home, standing.Event{Tier: standing.T1, Routine: -3}, lvl(standing.A0)},
		{"a routine above 5 clamps to A5", home, standing.Event{Tier: standing.T1, Routine: 9}, lvl(standing.A5)},
		{"a routine at A3 under a higher ceiling investigates", home, standing.Event{Tier: standing.T1, Routine: standing.A3, RestrictedHarness: true}, standing.Action{Level: standing.A3, Restricted: standing.Investigate}},
		{"a routine at A3 keeps a tier ceiling's draft fix", home, standing.Event{Tier: standing.T2, Routine: standing.A3, RestrictedHarness: true}, standing.Action{Level: standing.A3, Restricted: standing.DraftFix}},
	} {
		t.Run(c.name, func(t *testing.T) {
			if got := c.l.Effective(c.e); got != c.want {
				t.Errorf("Effective(%+v) = %+v, want %+v", c.e, got, c.want)
			}
		})
	}
}
