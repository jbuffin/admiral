package autopilotgate_test

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jbuffin/admiral/internal/autopilotgate"
	"github.com/jbuffin/admiral/internal/capabilities"
)

// seed records the guard result of a harness version and makes that version the detected one.
func seed(t *testing.T, home, fleet, harness, version string, guard *bool) {
	t.Helper()
	s := capabilities.Open(home)
	now := time.Now()
	if err := s.RecordVersion(fleet, harness, version, now); err != nil {
		t.Fatal(err)
	}
	if guard != nil {
		if err := s.Record(fleet, harness, version, capabilities.AutopilotGuard, capabilities.Result{Passed: *guard, At: now}); err != nil {
			t.Fatal(err)
		}
	}
}

func yes() *bool { b := true; return &b }
func no() *bool  { b := false; return &b }

type notice struct{ File, Title, Body string }

// notices lists the Inbox notices in home.
func notices(t *testing.T, home string) []notice {
	t.Helper()
	dir := filepath.Join(home, "inbox", "_admiral")
	names, err := os.ReadDir(dir)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		t.Fatal(err)
	}
	var out []notice
	for _, n := range names {
		if !strings.HasSuffix(n.Name(), ".json") || strings.HasSuffix(n.Name(), ".answer.json") {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(dir, n.Name()))
		if err != nil {
			t.Fatal(err)
		}
		var it struct {
			Title  string `json:"title"`
			Notice struct {
				Body string `json:"body"`
			} `json:"notice"`
		}
		if err := json.Unmarshal(raw, &it); err != nil {
			t.Fatal(err)
		}
		out = append(out, notice{filepath.Join(dir, n.Name()), it.Title, it.Notice.Body})
	}
	return out
}

func TestProvenOnlyWhereTheGuardPassedForTheDetectedVersion(t *testing.T) {
	home := t.TempDir()
	seed(t, home, "work", "claude", "2.1.300", yes())
	seed(t, home, "home", "claude", "2.1.300", no())
	seed(t, home, "unprobed", "claude", "2.1.300", nil)
	// The harness moved on to a version nobody probed.
	seed(t, home, "upgraded", "claude", "2.1.300", yes())
	if err := capabilities.Open(home).RecordVersion("upgraded", "claude", "2.1.400", time.Now()); err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		fleet, harness string
		want           bool
		why            string
	}{
		{"work", "claude", true, "the guard passed in work"},
		{"home", "claude", false, "the guard failed in home"},
		{"unprobed", "claude", false, "the guard was never probed"},
		{"upgraded", "claude", false, "the new version has no probe result"},
		{"nowhere", "claude", false, "the fleet has no record"},
		{"work", "codex", false, "codex has no probe result in work"},
	}
	for _, tc := range cases {
		if got := autopilotgate.Proven(home, tc.fleet, tc.harness); got != tc.want {
			t.Errorf("Proven(%s, %s) = %v, want %v: %s", tc.fleet, tc.harness, got, tc.want, tc.why)
		}
	}
}

func TestProvenIsFalseWhenNothingWasEverRecorded(t *testing.T) {
	if autopilotgate.Proven(t.TempDir(), "work", "claude") {
		t.Error("a fresh home proves the guard, want not proven")
	}
}

func TestProvenIsFalseWhenAFailedVersionCheckClearedTheVersion(t *testing.T) {
	home := t.TempDir()
	seed(t, home, "work", "claude", "2.1.300", yes())
	if err := capabilities.Open(home).RecordVersion("work", "claude", "", time.Now()); err != nil {
		t.Fatal(err)
	}
	if autopilotgate.Proven(home, "work", "claude") {
		t.Error("an old pass still counts after the version could not be detected")
	}
}

func TestCheckLetsAProvenHarnessThroughWithoutANotice(t *testing.T) {
	home := t.TempDir()
	seed(t, home, "work", "claude", "2.1.300", yes())
	if err := autopilotgate.Check(home, "work", "Work", "claude"); err != nil {
		t.Fatalf("Check on a proven harness = %v, want nil", err)
	}
	if n := notices(t, home); len(n) != 0 {
		t.Errorf("a proven harness posted %d notices", len(n))
	}
}

func TestCheckRefusesAnUnprovenHarnessWithOneNoticeNamingHarnessAndGuard(t *testing.T) {
	home := t.TempDir()
	seed(t, home, "work", "codex", "0.9.0", nil)
	err := autopilotgate.Check(home, "work", "Work", "codex")
	var refusal *autopilotgate.Refusal
	if !errors.As(err, &refusal) {
		t.Fatalf("Check = %v, want a *Refusal", err)
	}
	if refusal.Fleet != "work" || refusal.Harness != "codex" {
		t.Errorf("refusal = %+v, want fleet work and harness codex", refusal)
	}
	if msg := strings.ToLower(err.Error()); !strings.Contains(msg, "codex") || !strings.Contains(msg, "guard") {
		t.Errorf("error %q must name the harness and the missing guard", err)
	}
	n := notices(t, home)
	if len(n) != 1 {
		t.Fatalf("got %d notices, want 1: %+v", len(n), n)
	}
	if n[0].Title != "Couldn't launch unattended on Codex: guard not proven" {
		t.Errorf("notice title = %q", n[0].Title)
	}
	if !strings.Contains(n[0].Body, "Work") {
		t.Errorf("notice body %q must name the fleet", n[0].Body)
	}
}

func TestCheckDoesNotStackNoticesWhileOneIsOpen(t *testing.T) {
	home := t.TempDir()
	for range 3 {
		if autopilotgate.Check(home, "work", "Work", "codex") == nil {
			t.Fatal("an unproven harness was let through")
		}
	}
	n := notices(t, home)
	if len(n) != 1 {
		t.Fatalf("three refusals left %d notices, want 1", len(n))
	}

	// Dismissed, the next refusal tells the engineer again.
	if err := os.Remove(n[0].File); err != nil {
		t.Fatal(err)
	}
	autopilotgate.Check(home, "work", "Work", "codex")
	if got := len(notices(t, home)); got != 1 {
		t.Errorf("after a dismissal the next refusal left %d notices, want 1", got)
	}
}

func TestCheckKeepsOneNoticePerFleetAndHarness(t *testing.T) {
	home := t.TempDir()
	autopilotgate.Check(home, "work", "Work", "codex")
	autopilotgate.Check(home, "home", "Home", "codex")
	autopilotgate.Check(home, "work", "Work", "copilot")
	n := notices(t, home)
	if len(n) != 3 {
		t.Fatalf("three different fleet and harness pairs left %d notices, want 3: %+v", len(n), n)
	}
	var codexBodies []string
	for _, x := range n {
		if strings.Contains(x.Title, "Codex") {
			codexBodies = append(codexBodies, x.Body)
		}
	}
	if len(codexBodies) != 2 || codexBodies[0] == codexBodies[1] {
		t.Errorf("the two Codex notices must tell the fleets apart, bodies = %q", codexBodies)
	}
}

// The store may only narrow what the code allows: a stored pass for a harness with no
// guard probe in code still refuses.
func TestAPassedGuardRecordedForAHarnessWithNoProbeInCodeStillRefuses(t *testing.T) {
	home := t.TempDir()
	for _, h := range []string{"codex", "copilot"} {
		seed(t, home, "work", h, "1.0.0", yes())
		if autopilotgate.Proven(home, "work", h) {
			t.Errorf("a stored pass proved %s, which has no registered guard probe", h)
		}
		if autopilotgate.Check(home, "work", "Work", h) == nil {
			t.Errorf("Check let %s through on a stored pass", h)
		}
	}
}

// Two fleets can share a display name: each gets its own notice, and a repeat dedupes.
func TestCheckKeepsFleetsThatShareANameApart(t *testing.T) {
	home := t.TempDir()
	autopilotgate.Check(home, "work", "Work", "codex")
	autopilotgate.Check(home, "work-2", "Work", "codex")
	autopilotgate.Check(home, "work-2", "Work", "codex")
	n := notices(t, home)
	if len(n) != 2 {
		t.Fatalf("got %d notices, want one per fleet key: %+v", len(n), n)
	}
	if !strings.Contains(n[0].Body+n[1].Body, "work-2") {
		t.Errorf("bodies %q and %q must name the fleet by key", n[0].Body, n[1].Body)
	}
}

func TestCheckHandlesAMultibyteHarnessName(t *testing.T) {
	home := t.TempDir()
	autopilotgate.Check(home, "work", "Work", "émacs")
	if n := notices(t, home); len(n) != 1 || !strings.Contains(n[0].Title, "Émacs") {
		t.Errorf("notices = %+v, want one titled with Émacs", n)
	}
}
