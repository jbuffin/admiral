package ships

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jbuffin/admiral/internal/config"
	"github.com/jbuffin/admiral/internal/launcher"
	"github.com/jbuffin/admiral/internal/moor"
	"github.com/jbuffin/admiral/internal/sessionhost"
)

// The quiet gate refuses an unproven autopilot launch the way the normal gate does, but leaves the Inbox alone.
func TestAQuietGateRefusesAnUnprovenHarnessWithoutAnInboxNotice(t *testing.T) {
	home := guardedHome(t) // proven for claude in work only
	s := &Service{
		Host:     sessionhost.New("tmux", t.TempDir()+"/tmux.sock"), // never run: a launch that got through would be a 503
		Launcher: &launcher.Launcher{Home: home, Config: func() *config.Config { return &config.Config{Fleets: fleets()} }},
	}
	notices := func() int {
		names, _ := filepath.Glob(filepath.Join(home, "inbox", "_admiral", "notice-*.json"))
		return len(names)
	}
	req := launcher.Request{Fleet: "work", Harness: "codex", Dir: t.TempDir(), Skill: launcher.Skill{Kind: "none"}, Autopilot: true, QuietGate: true}

	_, lerr := s.Launch(context.Background(), req)
	if lerr == nil || lerr.Status != http.StatusBadRequest {
		t.Fatalf("a quiet autopilot launch of an unproven harness = %v, want a 400", lerr)
	}
	if got := notices(); got != 0 {
		t.Errorf("a quiet refusal left %d Inbox notices, want 0", got)
	}

	req.QuietGate = false
	if _, lerr := s.Launch(context.Background(), req); lerr == nil || lerr.Status != http.StatusBadRequest {
		t.Fatalf("the same launch without the quiet gate = %v, want a 400", lerr)
	}
	if got := notices(); got != 1 {
		t.Errorf("the normal refusal left %d Inbox notices, want 1: the gate itself still notifies", got)
	}
}

func TestACommitmentKeyHoldsASecondLaunchAndNamesNoRoutine(t *testing.T) {
	r := newWakeRig(t, 0)
	const key = "commitment:c1"
	first := r.mustLaunch(t, func(q *launcher.Request) { q.CommitmentKey = key })

	got, ok := r.s.Holder(key)
	if !ok || got.Session != first.Session {
		t.Errorf("Holder(%q) = %v, %v, want the launched ship %q", key, got.Session, ok, first.Session)
	}
	if first.Routine != "" || got.Routine != "" {
		t.Errorf("Routine = %q, want empty: a commitment ship is not a routine ship", got.Routine)
	}
	if _, ok := r.s.Holder("routine:c1"); ok {
		t.Errorf("a routine key found the commitment's ship")
	}
	r.wantHeld(t, func(q *launcher.Request) { q.CommitmentKey = key }, first)
}

// A ship trigger pairs a ship with its `launched` outcome record by the launch stamp, so the record must carry the stamp the listed ship has.
func TestTheLaunchedOutcomeRecordCarriesTheShipsLaunchStamp(t *testing.T) {
	r := newWakeRig(t, 0)
	sh := r.mustLaunch(t, nil)
	if sh.Launched == "" {
		t.Fatal("the launched ship has no launch stamp")
	}
	want := `"launched":"` + sh.Launched + `"`
	deadline := time.Now().Add(5 * time.Second)
	for {
		files, _ := filepath.Glob(filepath.Join(r.home, "outcomes", "*.jsonl"))
		for _, f := range files {
			b, _ := os.ReadFile(f)
			for _, line := range strings.Split(string(b), "\n") {
				if strings.Contains(line, `"event":"launched"`) && strings.Contains(line, `"ship":"`+sh.Session+`"`) && strings.Contains(line, want) {
					return
				}
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("no launched record of %s carries %s", sh.Session, want)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// A clone launch at the cap waits before gh runs, so the retry finds no clone of its own making.
func TestACommitmentCloneLaunchAtTheCapWaitsWithoutCloning(t *testing.T) {
	r := newWakeRig(t, 0)
	ran := filepath.Join(r.rec, "gh-ran")
	gh := "#!/bin/sh\ntouch " + ran + "\nmkdir -p \"$4/.git\"\n"
	if err := os.WriteFile(filepath.Join(r.rec, "gh"), []byte(gh), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", r.rec+string(os.PathListSeparator)+os.Getenv("PATH"))
	req := r.keyReq(func(q *launcher.Request) {
		q.Dir, q.Clone, q.Autopilot, q.QuietGate, q.CommitmentKey = "", true, true, true, "commitment:c1"
		q.Text = "port https://github.com/acme/widgetry to go"
	})

	_, lerr := r.s.Launch(context.Background(), req)
	if lerr == nil || lerr.Wait == "" {
		t.Fatalf("Launch = %v, want a waitable refusal for the cap", lerr)
	}
	if _, err := os.Stat(ran); err == nil {
		t.Errorf("gh ran for a launch that could only wait")
	}
	r.s.Launcher.Config().MaxShips = 5
	ship, lerr := r.s.Launch(context.Background(), req)
	if lerr != nil {
		t.Fatalf("Launch with room = %v, want the ship", lerr)
	}
	if _, err := os.Stat(ran); err != nil {
		t.Errorf("gh never ran once there was room")
	}
	if filepath.Base(ship.Dir) != "widgetry" {
		t.Errorf("ship dir = %q, want the clone of widgetry", ship.Dir)
	}
}

// A cap that fills while gh clones leaves the clone behind, and the retry must launch into it, not clone again.
func TestACommitmentCloneThatMeetsTheCapAfterCloningLaunchesIntoItOnTheRetry(t *testing.T) {
	r := newWakeRig(t, 5)
	runs, gate := filepath.Join(r.rec, "gh-runs"), filepath.Join(r.rec, "gh-go")
	gh := "#!/bin/sh\necho x >> " + runs + "\nwhile [ ! -f " + gate + " ]; do sleep 0.05; done\nmkdir -p \"$4/.git\"\n"
	if err := os.WriteFile(filepath.Join(r.rec, "gh"), []byte(gh), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", r.rec+string(os.PathListSeparator)+os.Getenv("PATH"))
	req := r.keyReq(func(q *launcher.Request) {
		q.Dir, q.Clone, q.Autopilot, q.QuietGate, q.CommitmentKey = "", true, true, true, "commitment:c1"
		q.Text = "port https://github.com/acme/widgetry to go"
	})
	done := make(chan *launcher.Error, 1)
	go func() {
		_, lerr := r.s.Launch(context.Background(), req)
		done <- lerr
	}()
	for {
		if _, err := os.Stat(runs); err == nil {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	r.s.Launcher.Config().MaxShips = 0 // the cap fills while gh runs
	if err := os.WriteFile(gate, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if lerr := <-done; lerr == nil || lerr.Wait == "" {
		t.Fatalf("Launch = %v, want a waitable refusal for the cap", lerr)
	}
	r.s.Launcher.Config().MaxShips = 5
	ship, lerr := r.s.Launch(context.Background(), req)
	if lerr != nil {
		t.Fatalf("retry = %v, want the ship launched into the clone", lerr)
	}
	if b, _ := os.ReadFile(runs); strings.Count(string(b), "x") != 1 {
		t.Errorf("gh ran %d times, want once", strings.Count(string(b), "x"))
	}
	if filepath.Base(ship.Dir) != "widgetry" {
		t.Errorf("ship dir = %q, want the clone of widgetry", ship.Dir)
	}
}

// A ship trigger on a woken ship pairs by the wake's stamp, so the `woken` record must carry it.
func TestAWokenOutcomeRecordCarriesTheWokenShipsLaunchStamp(t *testing.T) {
	r := newWakeRig(t, 0)
	r.moored(t, 0)
	ship, lerr := r.s.Wake(context.Background(), "saltpaw", "")
	if lerr != nil {
		t.Fatalf("Wake = %v", lerr)
	}
	got := r.logged(t, "woken")
	if ship.Launched == "" || len(got) != 1 || got[0]["launched"] != ship.Launched {
		t.Errorf("woken events = %v, want one carrying the listed ship's launch stamp %q", got, ship.Launched)
	}
}

// A ship trigger recorded on a moored ship needs the stamp its launch record carries.
func TestAMooredShipIsListedWithItsLaunchStamp(t *testing.T) {
	f := mooredService(t, nil)
	if err := moor.Write(f.home, moor.Record{Name: "Saltpaw", Session: "saltpaw", Fleet: "work", Launched: "2026-11-12T06:00:00Z"}); err != nil {
		t.Fatal(err)
	}
	got := f.s.mooredShips(nil)
	if len(got) != 1 || got[0].Launched != "2026-11-12T06:00:00Z" {
		t.Errorf("moored ships = %+v, want one with the record's launch stamp", got)
	}
}
