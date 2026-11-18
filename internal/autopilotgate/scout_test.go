package autopilotgate_test

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/jbuffin/admiral/internal/autopilotgate"
	"github.com/jbuffin/admiral/internal/capabilities"
)

const scoutCap = capabilities.ID("scout.guard_hook")

// seedScout records the scout guard result of a harness version; seed has already made that version the detected one.
func seedScout(t *testing.T, home, fleet, harness, version string, passed bool) {
	t.Helper()
	if err := capabilities.Open(home).Record(fleet, harness, version, scoutCap, capabilities.Result{Passed: passed, At: time.Now()}); err != nil {
		t.Fatal(err)
	}
}

func TestScoutProvenNeedsBothGuardProbesOnClaudeAtTheDetectedVersion(t *testing.T) {
	home := t.TempDir()
	seed(t, home, "both", "claude", "2.1.300", yes())
	seedScout(t, home, "both", "claude", "2.1.300", true)
	seed(t, home, "autopilot-only", "claude", "2.1.300", yes())
	seed(t, home, "scout-only", "claude", "2.1.300", nil)
	seedScout(t, home, "scout-only", "claude", "2.1.300", true)
	seed(t, home, "scout-failed", "claude", "2.1.300", yes())
	seedScout(t, home, "scout-failed", "claude", "2.1.300", false)
	seed(t, home, "upgraded", "claude", "2.1.300", yes())
	seedScout(t, home, "upgraded", "claude", "2.1.300", true)
	if err := capabilities.Open(home).RecordVersion("upgraded", "claude", "2.1.400", time.Now()); err != nil {
		t.Fatal(err)
	}
	// Codex has both passes stored, but no guard hook wiring: a stored pass proves nothing.
	seed(t, home, "codex-fleet", "codex", "0.1.0", yes())
	seedScout(t, home, "codex-fleet", "codex", "0.1.0", true)

	for _, tc := range []struct {
		fleet, harness string
		want           bool
		why            string
	}{
		{"both", "claude", true, "both guards passed"},
		{"autopilot-only", "claude", false, "the scout probe never passed"},
		{"scout-only", "claude", false, "the autopilot guard never passed"},
		{"scout-failed", "claude", false, "the scout probe failed"},
		{"upgraded", "claude", false, "the new version has no probe result"},
		{"nowhere", "claude", false, "the fleet has no record"},
		{"codex-fleet", "codex", false, "codex has no scout guard"},
	} {
		if got := autopilotgate.ScoutProven(home, tc.fleet, tc.harness); got != tc.want {
			t.Errorf("ScoutProven(%s, %s) = %v, want %v: %s", tc.fleet, tc.harness, got, tc.want, tc.why)
		}
	}
}

func TestCheckScoutRefusesWhatNoScoutGuardCoversAndNamesTheHarnessAndFleet(t *testing.T) {
	home := t.TempDir()
	for _, h := range []string{"codex", "copilot"} {
		seed(t, home, "work", h, "1.0.0", yes())
		seedScout(t, home, "work", h, "1.0.0", true)
		err := autopilotgate.CheckScout(home, flt("work", "Work"), h)
		var ref *autopilotgate.ScoutRefusal
		if !errors.As(err, &ref) {
			t.Fatalf("CheckScout(%s) = %v, want a *ScoutRefusal", h, err)
		}
		if !strings.Contains(err.Error(), h) || !strings.Contains(err.Error(), "no scout guard") {
			t.Errorf("CheckScout(%s) = %q, want it to say there is no scout guard for %s", h, err, h)
		}
	}
	if n := notices(t, home); len(n) != 0 {
		t.Errorf("the refusal posted %d Inbox notices, want none: the launch's caller shows it", len(n))
	}
}

func TestCheckScoutRefusesAClaudeWhoseScoutGuardProbeHasNotPassed(t *testing.T) {
	home := t.TempDir()
	fleet := claudeFleet(t, "work", "Work")
	proveJudge(t, home, "work")
	seed(t, home, "work", "claude", "2.1.300", yes())

	err := autopilotgate.CheckScout(home, fleet, "claude")
	var ref *autopilotgate.ScoutRefusal
	if !errors.As(err, &ref) {
		t.Fatalf("CheckScout with no scout pass = %v, want a *ScoutRefusal", err)
	}
	for _, want := range []string{"scout guard probe has not passed", "claude", "Work"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q lacks %q", err, want)
		}
	}
	if ref.Harness != "claude" || ref.Fleet != "work" && ref.Fleet != "Work" {
		t.Errorf("refusal = %+v, want its fleet and harness", ref)
	}

	seedScout(t, home, "work", "claude", "2.1.300", false)
	if err := autopilotgate.CheckScout(home, fleet, "claude"); err == nil {
		t.Error("CheckScout passed after the scout probe failed")
	}
	if n := notices(t, home); len(n) != 0 {
		t.Errorf("the refusal posted %d Inbox notices, want none", len(n))
	}
}

func TestCheckScoutRefusesAClaudeWithTheScoutPassButNoAutopilotGuardPass(t *testing.T) {
	home := t.TempDir()
	fleet := claudeFleet(t, "work", "Work")
	proveJudge(t, home, "work")
	seed(t, home, "work", "claude", "2.1.300", nil)
	seedScout(t, home, "work", "claude", "2.1.300", true)

	err := autopilotgate.CheckScout(home, fleet, "claude")
	if err == nil {
		t.Fatal("CheckScout passed with no autopilot guard pass: a scout runs in autopilot")
	}
	if !strings.Contains(err.Error(), "Work") {
		t.Errorf("error %q does not name the fleet", err)
	}
	if n := notices(t, home); len(n) != 0 {
		t.Errorf("the refusal posted %d Inbox notices, want none", len(n))
	}
}

func TestCheckScoutAllowsAClaudeWithBothGuardPasses(t *testing.T) {
	home := t.TempDir()
	fleet := claudeFleet(t, "work", "Work")
	proveJudge(t, home, "work")
	seed(t, home, "work", "claude", "2.1.300", yes())
	seedScout(t, home, "work", "claude", "2.1.300", true)

	if err := autopilotgate.CheckScout(home, fleet, "claude"); err != nil {
		t.Errorf("CheckScout = %v, want nil", err)
	}
	if n := notices(t, home); len(n) != 0 {
		t.Errorf("the gate posted %d Inbox notices, want none", len(n))
	}
}
