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
	print := func(store *capabilities.Store) string {
		var out bytes.Buffer
		printAdvisor(IO{Out: &out}, fleet, store)
		return out.String()
	}
	store := capabilities.Open(t.TempDir())
	if got := print(store); !strings.Contains(got, "Advisor in fleet Work: not checked") {
		t.Errorf("before any judge result = %q, want not checked", got)
	}
	if err := store.RecordVersion("work", "claude", "2.1.300", time.Now()); err != nil {
		t.Fatal(err)
	}
	if err := store.Record("work", "claude", "2.1.300", capabilities.JudgeNoTools, capabilities.Result{Passed: true, At: time.Now(), Detail: "fake"}); err != nil {
		t.Fatal(err)
	}
	if got := print(store); !strings.Contains(got, "Advisor in fleet Work: failed:") {
		t.Errorf("with a partial judge result = %q, want failed with the reason", got)
	}
	if err := store.Record("work", "claude", "2.1.300", capabilities.JudgeNoNetwork, capabilities.Result{Passed: true, At: time.Now(), Detail: "fake"}); err != nil {
		t.Fatal(err)
	}
	if got := print(store); !strings.Contains(got, "passed: the fleet's judge harness is claude") {
		t.Errorf("with a proven judge harness = %q, want passed naming claude", got)
	}
}
