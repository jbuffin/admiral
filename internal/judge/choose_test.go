package judge_test

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jbuffin/admiral/internal/capabilities"
	"github.com/jbuffin/admiral/internal/config"
	"github.com/jbuffin/admiral/internal/judge"
)

// fleetOf lists harnesses in the dock order claude, copilot, codex, as the config does.
func fleetOf(t *testing.T, names ...string) config.Fleet {
	t.Helper()
	f := config.Fleet{Key: "work"}
	for _, n := range names {
		f.Harnesses = append(f.Harnesses, config.Harness{Name: n, Executable: filepath.Join(t.TempDir(), n), Found: true})
	}
	return f
}

func fleetWithExes(t *testing.T, names ...string) config.Fleet {
	t.Helper()
	f := config.Fleet{Key: "work"}
	for _, n := range names {
		r := newRig(t, n)
		f.Harnesses = append(f.Harnesses, r.fleet.Harnesses[0])
	}
	return f
}

func noJudge(t *testing.T, err error) *judge.NoJudgeHarness {
	t.Helper()
	var nj *judge.NoJudgeHarness
	if !errors.As(err, &nj) {
		t.Fatalf("err = %v, want *judge.NoJudgeHarness", err)
	}
	if nj.Reason == "" {
		t.Error("the no judge harness outcome has no reason")
	}
	return nj
}

func TestChoosePicksClaudeThenCodexThenCopilotWhateverTheDockOrder(t *testing.T) {
	store := capabilities.Open(t.TempDir())
	fleet := fleetWithExes(t, "claude", "copilot", "codex")

	prove(t, store, "work", "copilot", version, basic...)
	prove(t, store, "work", "codex", version, basic...)
	c, err := judge.Choose(fleet, store)
	if err != nil || c.Name != "codex" {
		t.Fatalf("Choose = %q, %v, want codex over copilot", c.Name, err)
	}

	prove(t, store, "work", "claude", version, basic...)
	c, err = judge.Choose(fleet, store)
	if err != nil || c.Name != "claude" {
		t.Fatalf("Choose = %q, %v, want claude first", c.Name, err)
	}
}

func TestChooseSkipsAHarnessThatLacksNoToolsOrNoNetwork(t *testing.T) {
	store := capabilities.Open(t.TempDir())
	fleet := fleetWithExes(t, "claude", "codex", "copilot")
	prove(t, store, "work", "claude", version, capabilities.JudgeNoTools)
	prove(t, store, "work", "codex", version, capabilities.JudgeNoNetwork)
	prove(t, store, "work", "copilot", version, basic...)
	c, err := judge.Choose(fleet, store)
	if err != nil || c.Name != "copilot" {
		t.Fatalf("Choose = %q, %v, want copilot, the only one with both proven", c.Name, err)
	}
}

func TestChooseSkipsAHarnessWhoseVersionChanged(t *testing.T) {
	store := capabilities.Open(t.TempDir())
	fleet := fleetWithExes(t, "claude")
	prove(t, store, "work", "claude", version, basic...)
	if err := store.RecordVersion("work", "claude", "2.0.0", fleetTime()); err != nil {
		t.Fatal(err)
	}
	_, err := judge.Choose(fleet, store)
	noJudge(t, err)
}

func TestNoJudgeHarnessReasonNamesEachHarnessAndWhatItLacks(t *testing.T) {
	store := capabilities.Open(t.TempDir())
	fleet := fleetWithExes(t, "claude", "codex")
	prove(t, store, "work", "claude", version, capabilities.JudgeNoTools)
	prove(t, store, "work", "codex", version, capabilities.JudgeNoNetwork)
	_, err := judge.Choose(fleet, store)
	reason := noJudge(t, err).Reason
	claude, codex := strings.Index(reason, "claude:"), strings.Index(reason, "codex:")
	if claude < 0 || codex < claude {
		t.Fatalf("reason = %q, want a claude: part then a codex: part", reason)
	}
	if !strings.Contains(reason[claude:codex], "network") {
		t.Errorf("reason = %q, want claude's part to say the network is not proven", reason)
	}
	if !strings.Contains(reason[codex:], "tools") {
		t.Errorf("reason = %q, want codex's part to say tools are not proven", reason)
	}
}

func TestNoJudgeHarnessReasonSaysNotFoundForAHarnessThatDoesNotResolve(t *testing.T) {
	store := capabilities.Open(t.TempDir())
	fleet := fleetOf(t, "claude")
	prove(t, store, "work", "claude", version, basic...)
	_, err := judge.Choose(fleet, store)
	if reason := noJudge(t, err).Reason; !strings.Contains(reason, "claude: not found") {
		t.Errorf("reason = %q, want it to say claude: not found", reason)
	}
}

func TestNoJudgeHarnessReasonForAFleetWithNoneOfTheThree(t *testing.T) {
	_, err := judge.Choose(config.Fleet{Key: "work"}, capabilities.Open(t.TempDir()))
	if reason := noJudge(t, err).Reason; !strings.Contains(reason, "the fleet has no claude, codex or copilot harness") {
		t.Errorf("reason = %q", reason)
	}
}

func TestChooseReportsWhetherSchemaAndCapAreProven(t *testing.T) {
	r := newRig(t, "claude", basic...)
	if c := r.choose(); c.JSONSchema || c.CallCap {
		t.Errorf("choice = %+v, want neither proven", c)
	}
	prove(t, r.store, "work", "claude", version, full...)
	if c := r.choose(); !c.JSONSchema || !c.CallCap {
		t.Errorf("choice = %+v, want both proven", c)
	}
	if err := r.store.Record("work", "claude", version, capabilities.JudgeCallCap, capabilities.Result{Passed: false, At: fleetTime(), Detail: "fake"}); err != nil {
		t.Fatal(err)
	}
	if c := r.choose(); !c.JSONSchema || c.CallCap {
		t.Errorf("choice = %+v, want schema proven and the cap not, after a failed cap probe", c)
	}
}
