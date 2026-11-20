package harnessmodels_test

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/jbuffin/admiral/internal/config"
	"github.com/jbuffin/admiral/internal/harnessmodels"
)

// The store keeps each fleet's list per harness under $ADMIRAL_HOME; the service lists docks into it.

func homeDir(t *testing.T) string { return filepath.Join(t.TempDir(), "h") }

var at = time.Date(2026, 11, 19, 10, 0, 0, 0, time.UTC)

func TestStoreRoundTripsAListAndSurvivesAReopen(t *testing.T) {
	home := homeDir(t)
	s := harnessmodels.Open(home)
	if _, ok, err := s.Get("work", "claude"); ok || err != nil {
		t.Fatalf("Get on an empty store = ok %v err %v, want not found and no error", ok, err)
	}
	if err := s.Record("work", "claude", []string{"opus[1m]", "fable"}, at); err != nil {
		t.Fatal(err)
	}
	e, ok, err := harnessmodels.Open(home).Get("work", "claude")
	if err != nil || !ok {
		t.Fatalf("Get after a reopen = ok %v err %v", ok, err)
	}
	if !slices.Equal(e.Models, []string{"opus[1m]", "fable"}) || !e.ListedAt.Equal(at) || e.Error != "" {
		t.Errorf("entry = %+v, want the list, its time and no error", e)
	}
	if _, ok, _ := s.Get("work", "codex"); ok {
		t.Error("a list for claude answers for codex")
	}
	if _, ok, _ := s.Get("play", "claude"); ok {
		t.Error("a list for fleet work answers for fleet play")
	}
}

func TestStoreKeepsTheLastGoodListWhenAListingFailsAndClearsTheErrorOnTheNextGoodOne(t *testing.T) {
	s := harnessmodels.Open(homeDir(t))
	if err := s.Record("work", "codex", []string{"gpt-6.1-sol"}, at); err != nil {
		t.Fatal(err)
	}
	if err := s.RecordFailure("work", "codex", "timed out", at.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	e, ok, _ := s.Get("work", "codex")
	if !ok || !slices.Equal(e.Models, []string{"gpt-6.1-sol"}) || !e.ListedAt.Equal(at) || e.Error != "timed out" {
		t.Errorf("entry after a failure = %+v, want the old list and time with the error", e)
	}
	if err := s.Record("work", "codex", []string{"gpt-6.2-sol"}, at.Add(2*time.Hour)); err != nil {
		t.Fatal(err)
	}
	if e, _, _ := s.Get("work", "codex"); e.Error != "" || !slices.Equal(e.Models, []string{"gpt-6.2-sol"}) {
		t.Errorf("entry after a good listing = %+v, want the new list and no error", e)
	}
}

func TestStoreRecordsAFailureForADockThatNeverListed(t *testing.T) {
	s := harnessmodels.Open(homeDir(t))
	if err := s.RecordFailure("work", "copilot", "not signed in", at); err != nil {
		t.Fatal(err)
	}
	e, ok, _ := s.Get("work", "copilot")
	if !ok || e.Error != "not signed in" || len(e.Models) != 0 {
		t.Errorf("entry = %+v ok %v, want only the error", e, ok)
	}
}

func fleetWith(key, harness string, h config.Harness) config.Fleet {
	h.Name = harness
	return config.Fleet{Key: key, Harnesses: []config.Harness{h}}
}

type changes struct{ n int }

func (c *changes) fn() { c.n++ }

func TestServiceListsADockAndServesAndStoresItsModels(t *testing.T) {
	home := homeDir(t)
	h, _ := dock(t, "claude", map[string]string{"FAKE_MODELS": "sonnet,opus,haiku,fable"})
	svc := harnessmodels.New(home, 20*time.Second, func() {})
	if _, listed := svc.Models("work", "claude"); listed {
		t.Fatal("a dock never listed reports a list")
	}
	if err := svc.List(context.Background(), fleetWith("work", "claude", h), "claude"); err != nil {
		t.Fatal(err)
	}
	got, listed := svc.Models("work", "claude")
	if !listed || !slices.Equal(got, []string{"sonnet", "opus", "haiku", "fable"}) {
		t.Errorf("Models = %v listed %v, want the harness's four", got, listed)
	}
	if e, ok, _ := harnessmodels.Open(home).Get("work", "claude"); !ok || len(e.Models) != 4 {
		t.Errorf("the store holds %+v ok %v, want the list written", e, ok)
	}
}

func TestStoreDoesNotOverwriteAFileItCannotRead(t *testing.T) {
	home := homeDir(t)
	if err := os.MkdirAll(filepath.Join(home, harnessmodels.FileName), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := harnessmodels.Open(home).Record("work", "claude", []string{"sonnet"}, at); err == nil {
		t.Error("Record over an unreadable store succeeded, so it replaced what it could not read")
	}
}

func TestServiceServesOnlyValidNamesFromTheStoredList(t *testing.T) {
	home := homeDir(t)
	if err := harnessmodels.Open(home).Record("work", "claude", []string{"sonnet", "-x", "a b", "sonnet"}, at); err != nil {
		t.Fatal(err)
	}
	got, listed := harnessmodels.New(home, time.Second, nil).Models("work", "claude")
	if !listed || !slices.Equal(got, []string{"sonnet"}) {
		t.Errorf("models = %v listed %v, want just sonnet", got, listed)
	}
}

func TestServiceServesTheStoredListAfterARestartBeforeAnythingIsListed(t *testing.T) {
	home := homeDir(t)
	if err := harnessmodels.Open(home).Record("work", "copilot", []string{"claude-opus-5.5", "gpt-6.2-sol"}, at); err != nil {
		t.Fatal(err)
	}
	svc := harnessmodels.New(home, time.Second, func() {})
	got, listed := svc.Models("work", "copilot")
	if !listed || !slices.Equal(got, []string{"claude-opus-5.5", "gpt-6.2-sol"}) {
		t.Errorf("Models = %v listed %v, want the stored list", got, listed)
	}
}

func TestServiceKeepsEachFleetsOwnListForTheSameHarness(t *testing.T) {
	svc := harnessmodels.New(homeDir(t), 20*time.Second, func() {})
	personal, _ := dock(t, "copilot", map[string]string{"FAKE_MODELS": "gpt-6.2-sol,gpt-6-luna"})
	workDock, _ := dock(t, "copilot", map[string]string{"FAKE_MODELS": "claude-opus-5.5,grok-4.7,kimi-k3"})
	for key, h := range map[string]config.Harness{"personal": personal, "work": workDock} {
		if err := svc.List(context.Background(), fleetWith(key, "copilot", h), "copilot"); err != nil {
			t.Fatal(err)
		}
	}
	p, _ := svc.Models("personal", "copilot")
	w, _ := svc.Models("work", "copilot")
	if !slices.Equal(p, []string{"auto", "gpt-6.2-sol", "gpt-6-luna"}) || !slices.Equal(w, []string{"auto", "claude-opus-5.5", "grok-4.7", "kimi-k3"}) {
		t.Errorf("personal = %v, work = %v, want each fleet's own list", p, w)
	}
}

func TestServiceKeepsTheLastGoodListAndRecordsTheErrorWhenAListingFails(t *testing.T) {
	home := homeDir(t)
	svc := harnessmodels.New(home, 20*time.Second, func() {})
	good, _ := dock(t, "codex", map[string]string{"FAKE_MODELS": "gpt-6.2-sol,gpt-6-luna"})
	if err := svc.List(context.Background(), fleetWith("work", "codex", good), "codex"); err != nil {
		t.Fatal(err)
	}
	bad, _ := dock(t, "codex", map[string]string{"FAKE_MODELS": "x", "FAKE_MODE": "fail"})
	if err := svc.List(context.Background(), fleetWith("work", "codex", bad), "codex"); err == nil {
		t.Error("a failing listing returned no error")
	}
	got, listed := svc.Models("work", "codex")
	if !listed || !slices.Equal(got, []string{"gpt-6.2-sol", "gpt-6-luna"}) {
		t.Errorf("Models after a failure = %v listed %v, want the last good list", got, listed)
	}
	if e, _, _ := harnessmodels.Open(home).Get("work", "codex"); e.Error == "" {
		t.Errorf("entry = %+v, want the failure recorded", e)
	}
}

func TestServiceFirstListingThatFailsLeavesTheDockUnlistedWithItsErrorRecorded(t *testing.T) {
	for _, mode := range []string{"fail", "garbage", "hang"} {
		t.Run(mode, func(t *testing.T) {
			home := homeDir(t)
			svc := harnessmodels.New(home, 300*time.Millisecond, func() {})
			h, _ := dock(t, "claude", map[string]string{"FAKE_MODELS": "a", "FAKE_MODE": mode})
			start := time.Now()
			if err := svc.List(context.Background(), fleetWith("work", "claude", h), "claude"); err == nil {
				t.Error("List returned no error")
			}
			if took := time.Since(start); took > 10*time.Second {
				t.Errorf("List took %s with a 300ms timeout", took)
			}
			if _, listed := svc.Models("work", "claude"); listed {
				t.Error("a dock whose listing failed reports a list, so the built-in one would not be served")
			}
			if e, ok, _ := harnessmodels.Open(home).Get("work", "claude"); !ok || e.Error == "" {
				t.Errorf("entry = %+v ok %v, want the error recorded", e, ok)
			}
		})
	}
}

func TestServiceTellsOnChangeOnlyWhenTheListedModelsDiffer(t *testing.T) {
	var c changes
	svc := harnessmodels.New(homeDir(t), 20*time.Second, c.fn)
	list := func(models string) {
		t.Helper()
		h, _ := dock(t, "claude", map[string]string{"FAKE_MODELS": models})
		if err := svc.List(context.Background(), fleetWith("work", "claude", h), "claude"); err != nil {
			t.Fatal(err)
		}
	}
	list("sonnet,fable")
	if c.n != 1 {
		t.Fatalf("onChange ran %d times after the first listing, want 1", c.n)
	}
	list("sonnet,fable")
	if c.n != 1 {
		t.Errorf("onChange ran %d times after an unchanged listing, want still 1", c.n)
	}
	list("sonnet,fable,opus[1m]")
	if c.n != 2 {
		t.Errorf("onChange ran %d times after a changed listing, want 2", c.n)
	}
	bad, _ := dock(t, "claude", map[string]string{"FAKE_MODE": "fail"})
	_ = svc.List(context.Background(), fleetWith("work", "claude", bad), "claude")
	if c.n != 2 {
		t.Errorf("onChange ran %d times after a failed listing, want still 2 (the served list did not change)", c.n)
	}
}

func TestServiceAnswersListingForADockTheFleetLacks(t *testing.T) {
	svc := harnessmodels.New(homeDir(t), time.Second, func() {})
	if err := svc.List(context.Background(), config.Fleet{Key: "work"}, "claude"); err == nil {
		t.Error("List for a harness the fleet has no dock for returned no error")
	}
}
