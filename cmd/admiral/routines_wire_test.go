package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/jbuffin/admiral/internal/config"
	"github.com/jbuffin/admiral/internal/launcher"
	"github.com/jbuffin/admiral/internal/routines"
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
