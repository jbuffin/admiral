package ships

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/jbuffin/admiral/internal/launcher"
	"github.com/jbuffin/admiral/internal/moor"
)

const oldStamp = "2026-11-12T06:00:00Z"

func TestAFreshShipsFirstLaunchStampIsItsLaunchStamp(t *testing.T) {
	r := newWakeRig(t, 0)
	got := r.mustLaunch(t, func(q *launcher.Request) { q.Name, q.Session = "Saltpaw", "saltpaw" })
	o := r.options(t, "saltpaw")
	if o["launched"] == "" || o["first-launched"] != o["launched"] {
		t.Errorf("options launched = %q, first-launched = %q, want the same stamp", o["launched"], o["first-launched"])
	}
	if j := shipJSON(t, got); j["first_launched"] != o["launched"] {
		t.Errorf("first_launched = %v, want %q", j["first_launched"], o["launched"])
	}
}

func TestAMooredShipIsListedWithAndWakesWithItsFirstLaunchStamp(t *testing.T) {
	r := newWakeRig(t, 0)
	first := shipJSON(t, r.moorKeyed(t))["first_launched"]
	if first == nil || first == "" {
		t.Fatal("the launched ship has no first_launched")
	}
	recs, err := moor.List(r.home)
	if err != nil || len(recs) != 1 {
		t.Fatalf("moored records = %+v, %v", recs, err)
	}
	if got := r.s.mooredShips(nil); len(got) != 1 || shipJSON(t, got[0])["first_launched"] != first {
		t.Errorf("moored ships = %+v, want one listed with first_launched %v", got, first)
	}
	// A record whose launch stamp differs from its first launch must not lend the wake its stamp.
	recs[0].Resume, recs[0].Launched = "/commodore resume run-7", oldStamp
	if err := moor.Write(r.home, recs[0]); err != nil {
		t.Fatal(err)
	}
	r.s.mooredMu.Lock()
	r.s.mooredLoaded = false
	r.s.mooredMu.Unlock()
	if err := os.Remove(filepath.Join(r.rec, "exit")); err != nil {
		t.Fatal(err)
	}
	woken, lerr := r.s.Wake(context.Background(), "saltpaw", "")
	if lerr != nil {
		t.Fatalf("Wake = %v", lerr)
	}
	if got := r.options(t, "saltpaw")["first-launched"]; got != first {
		t.Errorf("first-launched after the wake = %q, want the first launch's %v", got, first)
	}
	if got := shipJSON(t, woken)["first_launched"]; got != first {
		t.Errorf("woken ship's first_launched = %v, want %v", got, first)
	}
}

// A record written before the field has no first launch to carry, so its launch stamp stands in.
func TestAWakeOfARecordFromBeforeTheFieldFallsBackToItsLaunchStamp(t *testing.T) {
	r := newWakeRig(t, 0)
	if err := moor.Write(r.home, moor.Record{Name: "Saltpaw", Session: "saltpaw", Fleet: "work", Harness: "claude", Model: "opus",
		Skill: moor.Skill{Kind: "skill", Name: "review"}, Checkout: r.checkout, Source: "s", OrderID: "o1", Panel: "p",
		URL: "https://github.com/acme/widgets/issues/3", Title: "Fix it", PRs: []string{wakePR}, Resources: r.resources,
		Resume: "/commodore resume run-7", Launched: oldStamp, MooredAt: time.Now().Add(-time.Hour).UTC().Truncate(time.Second)}); err != nil {
		t.Fatal(err)
	}
	r.s.mooredMu.Lock()
	r.s.mooredLoaded = false
	r.s.mooredMu.Unlock()
	if _, lerr := r.s.Wake(context.Background(), "saltpaw", ""); lerr != nil {
		t.Fatalf("Wake = %v", lerr)
	}
	o := r.options(t, "saltpaw")
	if o["first-launched"] != oldStamp || o["launched"] == oldStamp {
		t.Errorf("first-launched = %q, launched = %q, want the record's %q and a new launch stamp", o["first-launched"], o["launched"], oldStamp)
	}
}

func TestAHandRelaunchKeepsTheShipsFirstLaunchStamp(t *testing.T) {
	r := newHandRig(t)
	r.setPaneOption(t, "@admiral-first-launched", oldStamp)
	ref := r.queue(t, `{"kind":"issue"}`)
	writeRef(t, ref, "https://github.com/Acme/Widgets/issues/9\n")
	r.endTurn(t)

	r.started(t, 2)
	waitUntil(t, "the ship relaunched as Commodore", func() bool { return r.options(t, "saltpaw")["skill-kind"] == "commodore" })
	if got := r.options(t, "saltpaw")["first-launched"]; got != oldStamp {
		t.Errorf("first-launched after the hand = %q, want %q", got, oldStamp)
	}
	if got := r.saltpaw(t)["first_launched"]; got != oldStamp {
		t.Errorf("first_launched in the ship list = %v, want %q", got, oldStamp)
	}
}
