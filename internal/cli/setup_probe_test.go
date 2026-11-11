package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jbuffin/admiral/internal/capabilities"
	"github.com/jbuffin/admiral/internal/config"
)

func TestSetupPrintsTheAdvisorOncePerFleetAsTheGateComputesIt(t *testing.T) {
	exe := filepath.Join(t.TempDir(), "claude")
	if err := os.WriteFile(exe, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	fleet := config.Fleet{Key: "work", Name: "Work", Harnesses: []config.Harness{
		{Name: "claude", Executable: exe, Found: true},
	}}
	printed := func(store *capabilities.Store) string {
		var out bytes.Buffer
		printAdvisor(IO{Out: &out}, fleet, store)
		return out.String()
	}
	store := capabilities.Open(t.TempDir())
	if got := printed(store); !strings.Contains(got, "Advisor in fleet Work: not checked") {
		t.Errorf("before any judge result = %q, want not checked", got)
	}
	if err := store.RecordVersion("work", "claude", "2.1.300", time.Now()); err != nil {
		t.Fatal(err)
	}
	if err := store.Record("work", "claude", "2.1.300", capabilities.JudgeNoTools, capabilities.Result{Passed: true, At: time.Now(), Detail: "fake"}); err != nil {
		t.Fatal(err)
	}
	if got := printed(store); !strings.Contains(got, "Advisor in fleet Work: failed:") {
		t.Errorf("with a partial judge result = %q, want failed with the reason", got)
	}
	if err := store.Record("work", "claude", "2.1.300", capabilities.JudgeNoNetwork, capabilities.Result{Passed: true, At: time.Now(), Detail: "fake"}); err != nil {
		t.Fatal(err)
	}
	if got := printed(store); !strings.Contains(got, "passed: the fleet's judge harness is claude") {
		t.Errorf("with a proven judge harness = %q, want passed naming claude", got)
	}
}

func TestSetupProbePrintsTheAdvisorOnceAfterEveryHarnessOfTheFleet(t *testing.T) {
	dir := t.TempDir()
	cfg := `{"version":1,"fleets":{"work":{"name":"Work","claude":{"executable":"/nonexistent/claude"},"codex":{"executable":"/nonexistent/codex"}}}}`
	if err := os.WriteFile(filepath.Join(dir, config.FileName), []byte(cfg), 0o600); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err := probeFleets(dir, IO{Out: &out}); err != nil {
		t.Fatal(err)
	}
	got := out.String()
	if n := strings.Count(got, "Advisor in fleet Work: not checked"); n != 1 {
		t.Errorf("fleet advisor lines = %d in %q, want 1", n, got)
	}
	if strings.Contains(got, "  Advisor:") {
		t.Errorf("output %q holds a per-harness Advisor line", got)
	}
	last := strings.LastIndex(got, "Probing ")
	if strings.Count(got, "Probing ") != 2 || strings.Index(got, "Advisor in fleet") < last {
		t.Errorf("output %q must print the advisor after both harnesses are probed", got)
	}
}
