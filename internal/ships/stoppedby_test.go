// A ship whose harness Admiral ended at the wall clock stays anchored, with the signal that ended it and why.
package ships

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/jbuffin/admiral/internal/sessionhost"
)

func TestAShipTheWallClockStoppedIsAnchoredWithItsSignalAndWhy(t *testing.T) {
	sess := exitedShip("late", map[string]string{sessionhost.OptStoppedBy: "wall_clock:60"})
	sess.Signal = "term"
	sh := FromSession(sess, time.Date(2026, 11, 11, 10, 6, 0, 0, time.UTC))
	if sh.Status != "anchored" || sh.ExitSignal == nil || *sh.ExitSignal != "TERM" {
		t.Errorf("status %q signal %v, want anchored with TERM", sh.Status, sh.ExitSignal)
	}
	if sh.StoppedBy != "wall_clock:60" {
		t.Errorf("StoppedBy = %q, want wall_clock:60", sh.StoppedBy)
	}
	raw, _ := json.Marshal(sh)
	var out map[string]any
	_ = json.Unmarshal(raw, &out)
	if out["stopped_by"] != "wall_clock:60" {
		t.Errorf("the ship's JSON has stopped_by = %v, want wall_clock:60", out["stopped_by"])
	}
}

func TestAShipNoOneStoppedHasNoStoppedBy(t *testing.T) {
	raw, _ := json.Marshal(FromSession(exitedShip("quiet", nil), time.Date(2026, 11, 11, 10, 6, 0, 0, time.UTC)))
	var out map[string]any
	_ = json.Unmarshal(raw, &out)
	if _, ok := out["stopped_by"]; ok {
		t.Errorf("a ship nobody stopped carries stopped_by = %v", out["stopped_by"])
	}
}
