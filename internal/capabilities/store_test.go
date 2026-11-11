package capabilities

import (
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

var t0 = time.Date(2026, 11, 11, 9, 0, 0, 0, time.UTC)

func pass(detail string) Result { return Result{Passed: true, At: t0, Detail: detail} }
func fail(detail string) Result { return Result{Passed: false, At: t0, Detail: detail} }

func mustStatus(t *testing.T, s *Store, fleet, harness string, id ID) Status {
	t.Helper()
	st, err := s.Status(fleet, harness, id)
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	return st
}

func seed(t *testing.T, s *Store, fleet, harness, version string, id ID, r Result) {
	t.Helper()
	if err := s.RecordVersion(fleet, harness, version, t0); err != nil {
		t.Fatalf("RecordVersion: %v", err)
	}
	if err := s.Record(fleet, harness, version, id, r); err != nil {
		t.Fatalf("Record: %v", err)
	}
}

func TestNoResultReadsNotChecked(t *testing.T) {
	s := Open(t.TempDir())
	if err := s.RecordVersion("work", "claude", "2.1.300", t0); err != nil {
		t.Fatal(err)
	}
	if got := mustStatus(t, s, "work", "claude", AutopilotGuard); got != NotChecked {
		t.Errorf("status = %q, want not_checked", got)
	}
	if s.Proven("work", "claude", AutopilotGuard) {
		t.Error("an unchecked capability is proven")
	}
}

func TestPassingResultForCurrentVersionIsProven(t *testing.T) {
	s := Open(t.TempDir())
	seed(t, s, "work", "claude", "2.1.300", AutopilotGuard, pass("blocked a review"))
	if got := mustStatus(t, s, "work", "claude", AutopilotGuard); got != Passed {
		t.Errorf("status = %q, want passed", got)
	}
	if !s.Proven("work", "claude", AutopilotGuard) {
		t.Error("Proven = false for a passed capability")
	}
	r, ok, err := s.Result("work", "claude", AutopilotGuard)
	if err != nil || !ok || !r.Passed || r.Detail != "blocked a review" || !r.At.Equal(t0) {
		t.Errorf("Result = %+v, %v, %v", r, ok, err)
	}
}

func TestFailingResultReadsFailedAndNotProven(t *testing.T) {
	s := Open(t.TempDir())
	seed(t, s, "work", "claude", "2.1.300", AutopilotGuard, fail("review went through"))
	if got := mustStatus(t, s, "work", "claude", AutopilotGuard); got != Failed {
		t.Errorf("status = %q, want failed", got)
	}
	if s.Proven("work", "claude", AutopilotGuard) {
		t.Error("a failed capability is proven")
	}
}

func TestLaterResultForSameVersionReplacesEarlier(t *testing.T) {
	s := Open(t.TempDir())
	seed(t, s, "work", "claude", "2.1.300", AutopilotGuard, fail("flaked"))
	if err := s.Record("work", "claude", "2.1.300", AutopilotGuard, pass("ok")); err != nil {
		t.Fatal(err)
	}
	if got := mustStatus(t, s, "work", "claude", AutopilotGuard); got != Passed {
		t.Errorf("status = %q, want passed", got)
	}
}

func TestResultForOlderVersionReadsNotChecked(t *testing.T) {
	s := Open(t.TempDir())
	seed(t, s, "work", "claude", "2.1.300", AutopilotGuard, pass("ok"))
	if err := s.RecordVersion("work", "claude", "2.1.301", t0.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if got := mustStatus(t, s, "work", "claude", AutopilotGuard); got != NotChecked {
		t.Errorf("status = %q, want not_checked after a version change", got)
	}
	if s.Proven("work", "claude", AutopilotGuard) {
		t.Error("a result for version A proves version B")
	}
	// Going back to the probed version finds the result again.
	if err := s.RecordVersion("work", "claude", "2.1.300", t0.Add(2*time.Hour)); err != nil {
		t.Fatal(err)
	}
	if got := mustStatus(t, s, "work", "claude", AutopilotGuard); got != Passed {
		t.Errorf("status = %q, want passed on the probed version", got)
	}
}

func TestResultRecordedForNonCurrentVersionDoesNotCount(t *testing.T) {
	s := Open(t.TempDir())
	if err := s.RecordVersion("work", "claude", "2.1.301", t0); err != nil {
		t.Fatal(err)
	}
	if err := s.Record("work", "claude", "2.1.300", AutopilotGuard, pass("late")); err != nil {
		t.Fatal(err)
	}
	if got := mustStatus(t, s, "work", "claude", AutopilotGuard); got != NotChecked {
		t.Errorf("status = %q, want not_checked", got)
	}
}

func TestResultsAreKeyedByFleetAndHarness(t *testing.T) {
	s := Open(t.TempDir())
	seed(t, s, "work", "claude", "2.1.300", AutopilotGuard, pass("ok"))
	if err := s.RecordVersion("home", "claude", "2.1.300", t0); err != nil {
		t.Fatal(err)
	}
	if err := s.RecordVersion("work", "codex", "2.1.300", t0); err != nil {
		t.Fatal(err)
	}
	if got := mustStatus(t, s, "home", "claude", AutopilotGuard); got != NotChecked {
		t.Errorf("fleet home status = %q, want not_checked", got)
	}
	if got := mustStatus(t, s, "work", "codex", AutopilotGuard); got != NotChecked {
		t.Errorf("work/codex status = %q, want not_checked", got)
	}
	if got := mustStatus(t, s, "work", "claude", AutopilotGuard); got != Passed {
		t.Errorf("work/claude status = %q, want passed", got)
	}
}

func TestNoDetectedVersionMeansNotChecked(t *testing.T) {
	s := Open(t.TempDir())
	if err := s.Record("work", "claude", "2.1.300", AutopilotGuard, pass("ok")); err != nil {
		t.Fatal(err)
	}
	if got := mustStatus(t, s, "work", "claude", AutopilotGuard); got != NotChecked {
		t.Errorf("status = %q, want not_checked with no detected version", got)
	}
	if s.Proven("work", "claude", AutopilotGuard) {
		t.Error("proven with no detected version")
	}
}

func TestDetectedVersionIsReadableByAnotherStore(t *testing.T) {
	home := t.TempDir()
	if err := Open(home).RecordVersion("work", "claude", "2.1.300", t0); err != nil {
		t.Fatal(err)
	}
	v, at, err := Open(home).Version("work", "claude")
	if err != nil || v != "2.1.300" || !at.Equal(t0) {
		t.Errorf("Version = %q, %v, %v", v, at, err)
	}
	v, _, err = Open(home).Version("home", "claude")
	if err != nil || v != "" {
		t.Errorf("Version for an unrecorded fleet = %q, %v", v, err)
	}
}

func TestResultsSurviveReload(t *testing.T) {
	home := t.TempDir()
	seed(t, Open(home), "work", "claude", "2.1.300", AutopilotGuard, pass("ok"))
	seed(t, Open(home), "work", "claude", "2.1.300", AutopilotAdvisor, fail("no advisor"))
	s := Open(home)
	if got := mustStatus(t, s, "work", "claude", AutopilotGuard); got != Passed {
		t.Errorf("guard = %q, want passed", got)
	}
	if got := mustStatus(t, s, "work", "claude", AutopilotAdvisor); got != Failed {
		t.Errorf("advisor = %q, want failed", got)
	}
}

func TestMissingFileIsAnEmptyStore(t *testing.T) {
	s := Open(t.TempDir())
	st, err := s.Status("work", "claude", AutopilotGuard)
	if err != nil || st != NotChecked {
		t.Errorf("Status = %q, %v; want not_checked, nil", st, err)
	}
	if v, _, err := s.Version("work", "claude"); err != nil || v != "" {
		t.Errorf("Version = %q, %v", v, err)
	}
}

func TestCorruptFileReadsNotCheckedWithTheError(t *testing.T) {
	home := t.TempDir()
	path := filepath.Join(home, FileName)
	if err := os.WriteFile(path, []byte(`{"fleets": {"work": {"claude": `), 0o600); err != nil {
		t.Fatal(err)
	}
	s := Open(home)
	st, err := s.Status("work", "claude", AutopilotGuard)
	if err == nil {
		t.Error("a corrupt file reported no error")
	}
	if st != NotChecked {
		t.Errorf("status = %q, want not_checked", st)
	}
	if s.Proven("work", "claude", AutopilotGuard) {
		t.Error("a corrupt file proved a capability")
	}
	if _, _, err := s.Version("work", "claude"); err == nil {
		t.Error("Version on a corrupt file reported no error")
	}
	if raw, _ := os.ReadFile(path); string(raw) != `{"fleets": {"work": {"claude": ` {
		t.Error("a read changed the corrupt file")
	}
}

func TestWriteMovesACorruptFileAsideThenStoresTheResult(t *testing.T) {
	home := t.TempDir()
	path := filepath.Join(home, FileName)
	if err := os.WriteFile(path, []byte("not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	s := Open(home)
	seed(t, s, "work", "claude", "2.1.300", AutopilotGuard, pass("ok"))
	bad, err := os.ReadFile(path + ".bad")
	if err != nil || string(bad) != "not json" {
		t.Errorf(".bad = %q, %v; want the corrupt content", bad, err)
	}
	if got := mustStatus(t, Open(home), "work", "claude", AutopilotGuard); got != Passed {
		t.Errorf("status = %q, want passed after the write", got)
	}
}

func TestFileIsPrivateAndLeavesNoTempFiles(t *testing.T) {
	home := t.TempDir()
	seed(t, Open(home), "work", "claude", "2.1.300", AutopilotGuard, pass("ok"))
	info, err := os.Stat(filepath.Join(home, FileName))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Errorf("mode = %v, want 0600", info.Mode().Perm())
	}
	entries, _ := os.ReadDir(home)
	for _, e := range entries {
		if e.Name() != FileName && e.Name() != FileName+".lock" {
			t.Errorf("stray file %s", e.Name())
		}
	}
}

func TestTwoStoresEachKeepTheOthersResults(t *testing.T) {
	home := t.TempDir()
	a, b := Open(home), Open(home)
	for _, s := range []*Store{a, b} {
		if err := s.RecordVersion("work", "claude", "2.1.300", t0); err != nil {
			t.Fatal(err)
		}
	}
	if err := a.Record("work", "claude", "2.1.300", AutopilotGuard, pass("a")); err != nil {
		t.Fatal(err)
	}
	if err := b.Record("work", "claude", "2.1.300", AutopilotAdvisor, fail("b")); err != nil {
		t.Fatal(err)
	}
	fresh := Open(home)
	if got := mustStatus(t, fresh, "work", "claude", AutopilotGuard); got != Passed {
		t.Errorf("guard = %q, want passed (store b overwrote store a)", got)
	}
	if got := mustStatus(t, fresh, "work", "claude", AutopilotAdvisor); got != Failed {
		t.Errorf("advisor = %q, want failed", got)
	}
}

func TestAStoreSeesAnotherStoresWriteWithoutReopening(t *testing.T) {
	home := t.TempDir()
	reader, writer := Open(home), Open(home)
	if got := mustStatus(t, reader, "work", "claude", AutopilotGuard); got != NotChecked {
		t.Fatalf("before = %q", got)
	}
	seed(t, writer, "work", "claude", "2.1.300", AutopilotGuard, pass("ok"))
	if !reader.Proven("work", "claude", AutopilotGuard) {
		t.Error("the reader kept a stale copy")
	}
}

func TestConcurrentWritersLoseNothing(t *testing.T) {
	home := t.TempDir()
	if err := Open(home).RecordVersion("work", "claude", "2.1.300", t0); err != nil {
		t.Fatal(err)
	}
	ids := []ID{JudgeNoTools, JudgeNoNetwork, JudgeJSONSchema, JudgeCallCap, JudgeUsage,
		BotMCPFence, BotConfigHome, RestrictedSandbox, RestrictedAllow, AutopilotGuard, AutopilotArgs, AutopilotAdvisor}
	var wg sync.WaitGroup
	for _, id := range ids {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := Open(home).Record("work", "claude", "2.1.300", id, pass("ok")); err != nil {
				t.Errorf("Record %s: %v", id, err)
			}
		}()
	}
	wg.Wait()
	fresh := Open(home)
	for _, id := range ids {
		if !fresh.Proven("work", "claude", id) {
			t.Errorf("%s lost", id)
		}
	}
}

func TestUnreadableFileReadsNotCheckedWithTheError(t *testing.T) {
	home := t.TempDir()
	if err := os.Mkdir(filepath.Join(home, FileName), 0o700); err != nil {
		t.Fatal(err)
	}
	s := Open(home)
	st, err := s.Status("work", "claude", AutopilotGuard)
	if err == nil || st != NotChecked {
		t.Errorf("Status = %q, %v; want not_checked and an error", st, err)
	}
	if s.Proven("work", "claude", AutopilotGuard) {
		t.Error("an unreadable file proved a capability")
	}
}
