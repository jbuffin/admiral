// The launch card's seam: HandleEvent hands the acts the saved launch record once a ship has started.
package routines_test

import (
	"net/http"
	"testing"

	"github.com/jbuffin/admiral/internal/launcher"
	"github.com/jbuffin/admiral/internal/routines"
	"github.com/jbuffin/admiral/internal/standing"
)

type launchActs struct {
	recs []routines.Trigger
	ds   []routines.Decision
}

func (a *launchActs) Act(_ string, _ routines.Routine, _ routines.Event, rec routines.Trigger, d routines.Decision, _ routines.Enrichment) {
	a.recs = append(a.recs, rec)
	a.ds = append(a.ds, d)
}

func TestAnEventShipThatStartedIsHandedToTheActsAsItsSavedLaunchRecord(t *testing.T) {
	for name, c := range map[string]struct {
		ceiling standing.Ceiling
		level   standing.Level
		profile standing.Restricted
	}{
		"A3": {standing.Ceiling{Level: standing.A3, Restricted: standing.DraftFix}, standing.A3, standing.DraftFix},
		"A4": {standing.Ceiling{Level: standing.A4}, standing.A4, ""},
	} {
		t.Run(name, func(t *testing.T) {
			h, r, _ := eventRun(t, c.ceiling, nil)
			acts := &launchActs{}
			h.sched.Acts = acts
			h.sched.HandleEvent(evFrom("Eng"))
			saved := h.latest(r)
			if len(acts.recs) != 1 {
				t.Fatalf("%d acts, want one for the started ship", len(acts.recs))
			}
			got, d := acts.recs[0], acts.ds[0]
			if got.ID == "" || got.ID != saved.ID || got.Outcome != routines.Launched || got.Ship != "ship-1" || got.Restricted != c.profile {
				t.Errorf("record = %+v, want the saved launched record %s on ship-1 with profile %q", got, saved.ID, c.profile)
			}
			if d.Outcome != routines.Launched || d.Level != c.level {
				t.Errorf("decision = %+v, want launched at level %d", d, c.level)
			}
		})
	}
}

func TestALaunchThatFailedIsNeverHandedToTheActs(t *testing.T) {
	h, r, _ := eventRun(t, standing.Ceiling{Level: standing.A3, Restricted: standing.DraftFix}, nil)
	acts := &launchActs{}
	h.sched.Acts = acts
	h.ships.set(func() {
		h.ships.launch = func(int, launcher.Request) (routines.ShipRef, *launcher.Error) {
			return routines.ShipRef{}, &launcher.Error{Status: http.StatusInternalServerError, Msg: "boom"}
		}
	})
	h.sched.HandleEvent(evFrom("Eng"))
	if rec := h.latest(r); rec.Outcome != routines.Failed {
		t.Fatalf("record = %+v, want failed", rec)
	}
	if len(acts.recs) != 0 {
		t.Errorf("%d acts for a launch that failed, want none: %+v", len(acts.recs), acts.recs)
	}
}
