package ships

import (
	"context"
	"os"
	"os/exec"
	"testing"
)

// markShip sets a launch-time option on saltpaw's session the way the launcher does.
func (r *liveRig) markShip(t *testing.T, name, value string) {
	t.Helper()
	args := []string{"-S", r.s.Launcher.Socket, "set-option", "-t", "=saltpaw:", "@admiral-" + name, value}
	if out, err := exec.Command(r.s.Launcher.Tmux, args...).CombinedOutput(); err != nil {
		t.Fatalf("set @admiral-%s: %v: %s", name, err, out)
	}
}

func (r *liveRig) markLocalOnly(t *testing.T) {
	t.Helper()
	r.markShip(t, "local-only", "1")
	r.markShip(t, "branch", "set-by-the-ship")
}

func TestALocalOnlyShipWhoseTurnEndsOnADoneReportWithNoPRIsFinishedWithoutStop(t *testing.T) {
	t.Parallel()
	r := newLiveRig(t, false)
	r.markLocalOnly(t)
	go r.s.Run(r.ctx)
	r.turn(t, "working", "")
	r.writeReport(t, "done")
	holdsFor(t, settleHold(), "the ship ended while its turn was still running", func() bool { return r.live(t) })

	r.turn(t, "needs_you", "stop")
	r.gone(t, "the finished ship to leave the live list")
	waitUntil(t, "the finished record", func() bool { return len(outcomeLines(t, r.home, "saltpaw", "finished")) == 1 })
	waitUntil(t, "the ended record of the finished ship", func() bool { return len(outcomeLines(t, r.home, "saltpaw", "ended")) == 1 })
	for _, no := range []string{"moored", "stopped"} {
		if contains(r.eventNames(t), no) {
			t.Errorf("records = %v, want a finished ship that was not %s", r.eventNames(t), no)
		}
	}
	if _, err := os.Stat(r.mooredFile()); err == nil {
		t.Error("a finished ship left a moored record")
	}
}

func TestAShipThatIsNotDoneOrNotLocalOnlyIsNotFinishedByItsTurnEnding(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		name      string
		localOnly bool
		report    string
		write     bool
	}{
		{"a local-only ship with a partial report", true, "partial", true},
		{"a local-only ship with no report", true, "", false},
		{"a GitHub ship with a done report and no PR", false, "done", true},
	} {
		t.Run(c.name, func(t *testing.T) {
			r := newLiveRig(t, false)
			if c.localOnly {
				r.markLocalOnly(t)
			}
			go r.s.Run(r.ctx)
			if c.write {
				r.writeReport(t, c.report)
			}
			r.turn(t, "needs_you", "stop")
			holdsFor(t, settleHold(), "the ship was ended or finished", func() bool {
				s, _, ok := r.wire(t)
				return ok && s.Status == "needs_you" && !contains(r.eventNames(t), "finished")
			})
		})
	}
}

func TestALiveShipCarriesLocalOnlyAndItsBranchOnTheWireOnlyWhenItIsLocalOnly(t *testing.T) {
	t.Parallel()
	r := newLiveRig(t, false)
	_ = r.s.Host.Sync(context.Background())
	if _, raw, ok := r.wire(t); !ok || raw["local_only"] != nil || raw["branch"] != nil {
		t.Fatalf("a GitHub ship's wire = %v (listed %v), want no local_only or branch", raw, ok)
	}
	r.markLocalOnly(t)
	waitUntil(t, "the ship to show as local-only on its branch", func() bool {
		_ = r.s.Host.Sync(context.Background())
		_, raw, ok := r.wire(t)
		return ok && string(raw["local_only"]) == "true" && string(raw["branch"]) == `"admiral/saltpaw"`
	})
}
