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
