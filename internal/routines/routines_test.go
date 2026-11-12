package routines_test

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jbuffin/admiral/internal/actor"
	"github.com/jbuffin/admiral/internal/capabilities"
	"github.com/jbuffin/admiral/internal/config"
	"github.com/jbuffin/admiral/internal/routines"
)

var t0 = time.Date(2026, 11, 12, 8, 0, 0, 0, time.UTC)

type rig struct {
	t     *testing.T
	home  string
	store *routines.Store
	now   time.Time
}

// newRig has fleets work and play, each with a claude dock; claude is proven in work only.
func newRig(t *testing.T) *rig {
	t.Helper()
	home := t.TempDir()
	exe := filepath.Join(t.TempDir(), "claude")
	if err := os.WriteFile(exe, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	fleets := map[string]config.Fleet{}
	for _, k := range []string{"work", "play"} {
		fleets[k] = config.Fleet{Key: k, Name: k, Harnesses: []config.Harness{{Name: "claude", Executable: exe, Found: true}}}
	}
	r := &rig{t: t, home: home, now: t0}
	r.store = &routines.Store{
		Home:   home,
		Fleets: func(k string) (config.Fleet, bool) { f, ok := fleets[k]; return f, ok },
		Now:    func() time.Time { return r.now },
	}
	r.prove("work", true, true)
	return r
}

func (r *rig) prove(fleet string, guard, judge bool) {
	r.t.Helper()
	s := capabilities.Open(r.home)
	if err := s.RecordVersion(fleet, "claude", "2.1.300", t0); err != nil {
		r.t.Fatal(err)
	}
	rec := func(id capabilities.ID, pass bool) {
		if err := s.Record(fleet, "claude", "2.1.300", id, capabilities.Result{Passed: pass, At: t0}); err != nil {
			r.t.Fatal(err)
		}
	}
	rec(capabilities.AutopilotGuard, guard)
	rec(capabilities.JudgeNoTools, judge)
	rec(capabilities.JudgeNoNetwork, judge)
}

func launchRoutine() routines.Routine {
	return routines.Routine{
		Name: "Nightly sweep", Enabled: true, When: "30 8 * * 1-5", Before: routines.AskFirst, Notify: true,
		Do: routines.Do{Launch: &routines.Launch{Harness: "claude", Dir: "/tmp/repo", Text: "sweep the board",
			Model: "sonnet", Args: []string{"--effort", "high"}}},
	}
}

func (r *rig) create(fleet string, in routines.Routine) routines.Routine {
	r.t.Helper()
	got, err := r.store.Create(fleet, in)
	if err != nil {
		r.t.Fatalf("Create(%s): %v", fleet, err)
	}
	return got
}

func (r *rig) count(fleet string) int {
	r.t.Helper()
	list, err := r.store.List(fleet)
	if err != nil {
		r.t.Fatalf("List(%s): %v", fleet, err)
	}
	return len(list)
}

func TestARoutineSurvivesAReopenWithEveryField(t *testing.T) {
	r := newRig(t)
	saved := r.create("work", launchRoutine())
	if saved.ID == "" || !saved.CreatedAt.Equal(t0) {
		t.Fatalf("a saved routine gets an id and its creation time: %+v", saved)
	}
	reopened := &routines.Store{Home: r.home, Fleets: r.store.Fleets, Now: r.store.Now}
	list, err := reopened.List("work")
	if err != nil || len(list) != 1 {
		t.Fatalf("List after reopen = %v, %v", list, err)
	}
	got := list[0]
	if got.ID != saved.ID || got.Name != "Nightly sweep" || !got.Enabled || got.When != "30 8 * * 1-5" ||
		got.Before != routines.AskFirst || !got.Notify || got.Do.Launch == nil {
		t.Fatalf("round trip lost a field: %+v", got)
	}
	l := got.Do.Launch
	if l.Harness != "claude" || l.Dir != "/tmp/repo" || l.Text != "sweep the board" || l.Model != "sonnet" ||
		strings.Join(l.Args, " ") != "--effort high" {
		t.Fatalf("launch spec lost a field: %+v", l)
	}
	if _, err := os.Stat(filepath.Join(r.home, "routines", "work", "routines.json")); err != nil {
		t.Fatalf("routines are stored at routines/<fleet>/routines.json: %v", err)
	}
}

func TestAnAdmiralBotPromptRoutineSaves(t *testing.T) {
	r := newRig(t)
	in := routines.Routine{Name: "Standup", When: "0 9 * * *", Before: routines.ReportOnly, Do: routines.Do{Prompt: "Summarise the board"}}
	got := r.create("work", in)
	if got.Do.Prompt != "Summarise the board" || got.Do.Launch != nil {
		t.Fatalf("prompt Do changed: %+v", got.Do)
	}
}

func TestEachFleetKeepsItsOwnRoutines(t *testing.T) {
	r := newRig(t)
	r.prove("play", true, true)
	r.create("work", launchRoutine())
	if r.count("play") != 0 {
		t.Fatal("work's routine shows in play")
	}
}

func TestUpdateAndDeleteChangeTheStoredRoutine(t *testing.T) {
	r := newRig(t)
	saved := r.create("work", launchRoutine())
	edit := saved
	edit.Name = "Renamed"
	edit.When = "0 2 * * *"
	r.now = t0.Add(time.Hour)
	got, err := r.store.Update("work", saved.ID, edit)
	if err != nil {
		t.Fatal(err)
	}
	if got.Name != "Renamed" || got.When != "0 2 * * *" || !got.UpdatedAt.Equal(r.now) || !got.CreatedAt.Equal(t0) {
		t.Fatalf("update = %+v", got)
	}
	if _, err := r.store.Update("work", "no-such", edit); !errors.Is(err, routines.ErrNotFound) {
		t.Fatalf("Update of an unknown id = %v, want ErrNotFound", err)
	}
	if err := r.store.Delete("work", saved.ID); err != nil {
		t.Fatal(err)
	}
	if r.count("work") != 0 {
		t.Fatal("the routine is still listed after Delete")
	}
	if err := r.store.Delete("work", saved.ID); !errors.Is(err, routines.ErrNotFound) {
		t.Fatalf("Delete of a gone routine = %v, want ErrNotFound", err)
	}
}

func TestSetEnabledFlipsOnlyTheSwitch(t *testing.T) {
	r := newRig(t)
	saved := r.create("work", launchRoutine())
	off, err := r.store.SetEnabled("work", saved.ID, false)
	if err != nil || off.Enabled {
		t.Fatalf("SetEnabled(false) = %+v, %v", off, err)
	}
	on, err := r.store.SetEnabled("work", saved.ID, true)
	if err != nil || !on.Enabled || on.Name != saved.Name || on.When != saved.When {
		t.Fatalf("SetEnabled(true) = %+v, %v", on, err)
	}
}

func TestAWriteLeavesNoTempFileAndACorruptFileIsKeptAside(t *testing.T) {
	r := newRig(t)
	r.create("work", launchRoutine())
	dir := filepath.Join(r.home, "routines", "work")
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if e.Name() != "routines.json" {
			t.Errorf("unexpected file after an atomic write: %s", e.Name())
		}
	}
	if err := os.WriteFile(filepath.Join(dir, "routines.json"), []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	if n := r.count("work"); n != 0 {
		t.Fatalf("a corrupt file starts empty, got %d routines", n)
	}
	if _, err := os.Stat(filepath.Join(dir, "routines.json.bad")); err != nil {
		t.Fatalf("the corrupt file is kept as routines.json.bad: %v", err)
	}
}

func TestAFleetThatIsUnknownOrCouldEscapeIsRefusedAndNothingIsWritten(t *testing.T) {
	r := newRig(t)
	r.create("work", launchRoutine())
	for _, fleet := range []string{"ghost", "../escape", "work/../play", "a/b", ""} {
		_, err := r.store.Create(fleet, launchRoutine())
		if !errors.Is(err, routines.ErrUnknownFleet) {
			t.Errorf("Create(%q) = %v, want ErrUnknownFleet", fleet, err)
		}
		if _, err := r.store.List(fleet); !errors.Is(err, routines.ErrUnknownFleet) {
			t.Errorf("List(%q) = %v, want ErrUnknownFleet", fleet, err)
		}
	}
	var stray []string
	_ = filepath.WalkDir(filepath.Dir(r.home), func(p string, d os.DirEntry, err error) error {
		if err == nil && !d.IsDir() && strings.HasSuffix(p, "routines.json") && !strings.HasPrefix(p, filepath.Join(r.home, "routines", "work")) {
			stray = append(stray, p)
		}
		return nil
	})
	if len(stray) != 0 {
		t.Fatalf("a refused fleet wrote %v", stray)
	}
}

func TestSaveRefusals(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*rig, *routines.Routine) (fleet string)
		want   string // a word the refusal's message holds
	}{
		{"a six-field cron", func(_ *rig, in *routines.Routine) string { in.When = "0 30 8 * * 1-5"; return "work" }, "cron"},
		{"a cron with a minute out of range", func(_ *rig, in *routines.Routine) string { in.When = "61 8 * * *"; return "work" }, "cron"},
		{"no When", func(_ *rig, in *routines.Routine) string { in.When = ""; return "work" }, "cron"},
		{"a dock the fleet lacks", func(_ *rig, in *routines.Routine) string { in.Do.Launch.Harness = "codex"; return "work" }, "codex"},
		{"a guard that is not proven", func(r *rig, _ *routines.Routine) string { r.prove("play", false, true); return "play" }, "autopilot"},
		{"an advisor that is not proven", func(r *rig, _ *routines.Routine) string { r.prove("play", true, false); return "play" }, "advisor"},
		{"an extra arg off the allowlist", func(_ *rig, in *routines.Routine) string {
			in.Do.Launch.Args = []string{"--dangerously-skip-permissions"}
			return "work"
		}, "allowlist"},
		{"an empty AdmiralBot prompt", func(_ *rig, in *routines.Routine) string {
			in.Do = routines.Do{Prompt: "  "}
			return "work"
		}, "prompt"},
		{"a Do that is both", func(_ *rig, in *routines.Routine) string { in.Do.Prompt = "also this"; return "work" }, "do"},
		{"a Do that is neither", func(_ *rig, in *routines.Routine) string { in.Do = routines.Do{}; return "work" }, "do"},
		{"a Before acting that is not one of the three", func(_ *rig, in *routines.Routine) string { in.Before = "propose"; return "work" }, "before"},
		{"no name", func(_ *rig, in *routines.Routine) string { in.Name = ""; return "work" }, "name"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := newRig(t)
			r.prove("play", true, true) // the baseline is valid in both fleets
			r.create("work", launchRoutine())
			baseline := r.create("play", launchRoutine())
			in := launchRoutine()
			fleet := c.mutate(r, &in)
			_, err := r.store.Create(fleet, in)
			if !errors.Is(err, routines.ErrInvalid) {
				t.Fatalf("Create = %v, want ErrInvalid", err)
			}
			if !strings.Contains(strings.ToLower(err.Error()), c.want) {
				t.Errorf("the refusal %q does not say %q", err, c.want)
			}
			if _, err := r.store.Update(fleet, baseline.ID, in); fleet == "play" && !errors.Is(err, routines.ErrInvalid) {
				t.Errorf("Update with the same mutation = %v, want ErrInvalid", err)
			}
			if got := r.count(fleet); got != 1 {
				t.Errorf("the refused save changed the stored list: %d routines, want 1", got)
			}
		})
	}
}

func TestARefusedGuardPostsNoInboxNotice(t *testing.T) {
	r := newRig(t)
	r.create("work", launchRoutine())
	r.prove("play", false, true)
	if _, err := r.store.Create("play", launchRoutine()); !errors.Is(err, routines.ErrInvalid) {
		t.Fatalf("Create on an unproven guard = %v, want ErrInvalid", err)
	}
	if entries, _ := os.ReadDir(filepath.Join(r.home, "inbox", "_admiral")); len(entries) != 0 {
		t.Fatalf("validation posted %d Inbox notices; saving a form must not", len(entries))
	}
}

func TestTheTriggerLogAppendsOneRecordPerDecisionAndNeverRewrites(t *testing.T) {
	r := newRig(t)
	saved := r.create("work", launchRoutine())
	path := filepath.Join(r.home, "routines", "work", "triggers.jsonl")
	first, err := r.store.AppendTrigger("work", routines.Trigger{Routine: saved.ID, Outcome: routines.Launched, Tier: "T0", Ship: "ship-a", Action: "launch"})
	if err != nil {
		t.Fatal(err)
	}
	if first.ID == "" || !first.At.Equal(t0) {
		t.Fatalf("an appended record gets an id and the time: %+v", first)
	}
	before, err := os.ReadFile(path)
	if err != nil || strings.Count(string(before), "\n") != 1 {
		t.Fatalf("one record is one line: %q, %v", before, err)
	}
	r.now = t0.Add(time.Minute)
	if _, err := r.store.AppendTrigger("work", routines.Trigger{Routine: saved.ID, Outcome: routines.Skipped, Tier: "T0", Reason: "last ship is live"}); err != nil {
		t.Fatal(err)
	}
	if err := r.store.Undo("work", first.ID); err != nil {
		t.Fatal(err)
	}
	after, _ := os.ReadFile(path)
	if !strings.HasPrefix(string(after), string(before)) {
		t.Fatal("an append rewrote earlier lines")
	}
}

func TestRecentRunsAreNewestFirstPerRoutineWithUndoFolded(t *testing.T) {
	r := newRig(t)
	a := r.create("work", launchRoutine())
	b := r.create("work", launchRoutine())
	var ids []string
	for i, o := range []routines.Outcome{routines.Launched, routines.Skipped, routines.Missed} {
		r.now = t0.Add(time.Duration(i) * time.Hour)
		rec, err := r.store.AppendTrigger("work", routines.Trigger{Routine: a.ID, Outcome: o, Tier: "T0"})
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, rec.ID)
	}
	if _, err := r.store.AppendTrigger("work", routines.Trigger{Routine: b.ID, Outcome: routines.Failed, Tier: "T0"}); err != nil {
		t.Fatal(err)
	}
	r.now = t0.Add(5 * time.Hour)
	if err := r.store.Undo("work", ids[0]); err != nil {
		t.Fatal(err)
	}
	got, err := r.store.Recent("work", a.ID, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 {
		t.Fatalf("Recent returned %d records for the routine, want its 3 (no other routine's, no mark of its own)", len(got))
	}
	if got[0].ID != ids[2] || got[1].ID != ids[1] || got[2].ID != ids[0] {
		t.Fatalf("not newest first: %v %v %v", got[0].ID, got[1].ID, got[2].ID)
	}
	if !got[2].Undone || !got[2].UndoneAt.Equal(t0.Add(5*time.Hour)) || got[0].Undone {
		t.Fatalf("the undo folds into the launch it names only: %+v", got)
	}
	if two, _ := r.store.Recent("work", a.ID, 2); len(two) != 2 || two[0].ID != ids[2] {
		t.Fatalf("limit 2 = %+v", two)
	}
}

func TestHoldFireIsPerFleetAndGlobalAndEitherIsOn(t *testing.T) {
	r := newRig(t)
	for _, fleet := range []string{"work", "play"} {
		if on, err := r.store.HoldOn(fleet); err != nil || on {
			t.Fatalf("Hold fire starts off for %s: %v, %v", fleet, on, err)
		}
	}
	if _, err := r.store.SetHold("work", true, routines.Engineer); err != nil {
		t.Fatal(err)
	}
	if on, _ := r.store.HoldOn("work"); !on {
		t.Fatal("work is on after its switch")
	}
	if on, _ := r.store.HoldOn("play"); on {
		t.Fatal("play went on with work's switch")
	}
	if _, err := r.store.SetHold("work", false, routines.Engineer); err != nil {
		t.Fatal(err)
	}
	if _, err := r.store.SetHold("", true, routines.Engineer); err != nil {
		t.Fatal(err)
	}
	for _, fleet := range []string{"work", "play"} {
		if on, _ := r.store.HoldOn(fleet); !on {
			t.Errorf("the global switch leaves %s off", fleet)
		}
	}
	if _, err := r.store.SetHold("work", true, routines.Engineer); err != nil {
		t.Fatal(err)
	}
	if _, err := r.store.SetHold("work", false, routines.Engineer); err != nil {
		t.Fatal(err)
	}
	if on, _ := r.store.HoldOn("work"); !on {
		t.Fatal("turning a fleet off while the global switch is on leaves it on")
	}
	if _, err := os.Stat(filepath.Join(r.home, "routines", "hold.json")); err != nil {
		t.Fatalf("the global switch lives directly under routines/: %v", err)
	}
}

func TestHoldFireRecordsWhoAndWhenAcrossAReopen(t *testing.T) {
	r := newRig(t)
	r.now = t0.Add(3 * time.Hour)
	if _, err := r.store.SetHold("work", true, actor.Via); err != nil {
		t.Fatal(err)
	}
	reopened := &routines.Store{Home: r.home, Fleets: r.store.Fleets, Now: r.store.Now}
	h, err := reopened.GetHold("work")
	if err != nil || !h.On || h.By != "admiralbot" || !h.At.Equal(r.now) {
		t.Fatalf("GetHold = %+v, %v", h, err)
	}
}

func TestAdmiralBotCanTurnHoldFireOnButNotOff(t *testing.T) {
	r := newRig(t)
	if _, err := r.store.SetHold("work", true, actor.Via); err != nil {
		t.Fatalf("AdmiralBot turning Hold fire on: %v", err)
	}
	if _, err := r.store.SetHold("work", false, actor.Via); !errors.Is(err, routines.ErrNotYours) {
		t.Fatalf("AdmiralBot turning it off = %v, want ErrNotYours", err)
	}
	if on, _ := r.store.HoldOn("work"); !on {
		t.Fatal("the refused release turned Hold fire off")
	}
	if _, err := r.store.SetHold("work", false, routines.Engineer); err != nil {
		t.Fatalf("the engineer turning it off: %v", err)
	}
}

func TestUnderHoldFireActBecomesAskFirstAndNothingRaises(t *testing.T) {
	cases := []struct{ stored, held, off routines.Mode }{
		{routines.Act, routines.AskFirst, routines.Act},
		{routines.AskFirst, routines.AskFirst, routines.AskFirst},
		{routines.ReportOnly, routines.ReportOnly, routines.ReportOnly},
	}
	for _, c := range cases {
		if got := routines.Effective(c.stored, true); got != c.held {
			t.Errorf("Effective(%s, hold) = %q, want %q", c.stored, got, c.held)
		}
		if got := routines.Effective(c.stored, false); got != c.off {
			t.Errorf("Effective(%s, no hold) = %q, want %q", c.stored, got, c.off)
		}
	}
}

func TestHoldFireLeavesTheStoredSettingAlone(t *testing.T) {
	r := newRig(t)
	in := launchRoutine()
	in.Before = routines.Act
	saved := r.create("work", in)
	if _, err := r.store.SetHold("work", true, routines.Engineer); err != nil {
		t.Fatal(err)
	}
	list, _ := r.store.List("work")
	if len(list) != 1 || list[0].ID != saved.ID || list[0].Before != routines.Act {
		t.Fatalf("Hold fire changed the stored routine: %+v", list)
	}
}

func TestHoldFireForAnUnknownFleetIsRefused(t *testing.T) {
	r := newRig(t)
	for _, fleet := range []string{"ghost", "../x"} {
		if _, err := r.store.SetHold(fleet, true, routines.Engineer); !errors.Is(err, routines.ErrUnknownFleet) {
			t.Errorf("SetHold(%q) = %v, want ErrUnknownFleet", fleet, err)
		}
	}
}
