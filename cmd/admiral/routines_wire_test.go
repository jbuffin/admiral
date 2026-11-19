package main

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/jbuffin/admiral/internal/config"
	"github.com/jbuffin/admiral/internal/inbox"
	"github.com/jbuffin/admiral/internal/launcher"
	"github.com/jbuffin/admiral/internal/routines"
	"github.com/jbuffin/admiral/internal/server"
)

func TestWiredLauncherReadsAFleetsGuidanceFromTheRoutinesStore(t *testing.T) {
	home := t.TempDir()
	cfgJSON := `{"version":1,"fleets":{"work":{"name":"Work"}}}`
	if err := os.WriteFile(filepath.Join(home, config.FileName), []byte(cfgJSON), 0o600); err != nil {
		t.Fatal(err)
	}
	m, err := config.Open(home)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(m.Close)
	store := &routines.Store{Home: home, Fleets: func(k string) (config.Fleet, bool) { return m.Current().Fleet(k) },
		ResourceKnown: func(string) bool { return false }}
	svc := &routines.Service{Store: store}
	l := &launcher.Launcher{}
	wireStandingOrders(l, store, svc, m)

	if l.Guidance == nil || svc.Config != m {
		t.Fatalf("Guidance set: %v, service Config wired: %v; want both", l.Guidance != nil, svc.Config == m)
	}
	if got, err := l.Guidance("work"); err != nil || got != "" {
		t.Fatalf("Guidance before any is filed = %q, %v; want empty", got, err)
	}
	text := "Prefer small PRs."
	if _, err := store.SetStandingOrders("work", routines.StandingOrdersPatch{Guidance: &text}); err != nil {
		t.Fatal(err)
	}
	if got, err := l.Guidance("work"); err != nil || got != text {
		t.Errorf("Guidance = %q, %v; want %q read through the store", got, err, text)
	}
	if _, err := l.Guidance("nowhere"); err == nil {
		t.Error("Guidance for an unknown fleet returned no error")
	}
}

func TestTheRoutineProposerCarriesTheTriggersReasonOntoTheInboxProposal(t *testing.T) {
	svc := &inbox.Service{Home: t.TempDir(), Hub: server.NewHub()}
	why := "Held for quota: Claude's 5-hour window is at 93% (line 90%), resets 14:00"
	err := routineProposer{svc}.Propose("work", routines.Routine{ID: "r1", Name: "Nightly sweep"},
		routines.Trigger{ID: "t1", Action: "launch claude", Reason: why}, time.Now().Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	for _, it := range svc.List() {
		if p, ok := it.RoutineProposal(); ok {
			if p.Reason != why {
				t.Errorf("proposal reason = %q, want %q", p.Reason, why)
			}
			return
		}
	}
	t.Fatal("no routine proposal in the Inbox")
}

func TestEventShipTellsRestrictedAndOutsideEventShipsAndFailsClosedOnAnUnreadableLog(t *testing.T) {
	home := t.TempDir()
	if err := os.WriteFile(filepath.Join(home, config.FileName), []byte(`{"version":1,"fleets":{"work":{"name":"Work"}}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	m, err := config.Open(home)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(m.Close)
	store := &routines.Store{Home: home, Fleets: func(k string) (config.Fleet, bool) { return m.Current().Fleet(k) },
		ResourceKnown: func(string) bool { return false }}
	now := time.Now()
	for _, rec := range []routines.Trigger{
		{ID: "t-open", Tier: "T1", At: now, Routine: "r1", Event: "issue.opened", Ship: "outside", Outcome: routines.Outcome("launched")},
		{ID: "t-locked", Tier: "T3", At: now, Routine: "r2", Event: "issue.opened", Ship: "locked", Restricted: "no-network", Outcome: routines.Outcome("launched")},
	} {
		if _, err := store.AppendTrigger("work", rec); err != nil {
			t.Fatal(err)
		}
	}
	listed := func() []routines.ListedShip {
		return []routines.ListedShip{
			{Session: "outside", Fleet: "work", Routine: "routine:r1", Status: "working"},
			{Session: "locked", Fleet: "work", Routine: "routine:r2", Status: "working"},
			{Session: "own", Fleet: "work", Status: "working"},
		}
	}
	ask := eventShip(store, listed)
	for _, c := range []struct {
		session            string
		restricted, fromEv bool
		tier               string
	}{{"outside", false, true, "T1"}, {"locked", true, true, "T3"}, {"own", false, false, ""}} {
		restricted, fromEv, tier, err := ask("work", c.session)
		if err != nil || restricted != c.restricted || fromEv != c.fromEv || tier != c.tier {
			t.Errorf("%s: restricted=%v fromEvent=%v tier=%q err=%v; want %v %v %q", c.session, restricted, fromEv, tier, err, c.restricted, c.fromEv, c.tier)
		}
	}
	if _, fromEv, _, err := ask("work", "gone"); err == nil || fromEv {
		t.Errorf("a ship that is not listed answered fromEvent=%v err=%v; want a refusal, so no proposal reads it as an own ship", fromEv, err)
	}
	if _, _, _, err := ask("nowhere", "own"); err == nil {
		t.Error("an unreadable trigger log answered without an error; want a refusal")
	}
}
