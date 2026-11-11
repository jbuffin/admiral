package probes_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jbuffin/admiral/internal/capabilities"
	"github.com/jbuffin/admiral/internal/config"
	"github.com/jbuffin/admiral/internal/probes"
)

// versionScript is a harness whose --version prints $FAKE_VERSION and fails when it is "FAIL".
func versionScript(t *testing.T) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "claude")
	body := "#!/bin/sh\n" +
		`if [ "$1" = "--version" ]; then [ "$FAKE_VERSION" = FAIL ] && { echo boom >&2; exit 1; }; echo "$FAKE_VERSION"; exit 0; fi` + "\n" +
		"exit 0\n"
	if err := os.WriteFile(p, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	return p
}

func fleetOf(exe, version string) config.Fleet {
	return config.Fleet{Key: "work", Harnesses: []config.Harness{
		{Name: "claude", Executable: exe, Found: true, Env: map[string]string{"FAKE_VERSION": version}},
	}}
}

func home(t *testing.T) string { return filepath.Join(t.TempDir(), "h") }

func pass(detail string) probes.Probe {
	return probes.Probe{Harness: "claude", Capability: capabilities.JudgeNoTools,
		Run: func(context.Context, probes.Target) probes.Outcome {
			return probes.Outcome{Passed: true, Detail: detail}
		}}
}

func line(t *testing.T, r probes.Report, id capabilities.ID) probes.Line {
	t.Helper()
	for _, l := range r.Lines {
		if l.ID == id {
			return l
		}
	}
	t.Fatalf("report has no line for %s: %+v", id, r.Lines)
	return probes.Line{}
}

func TestARegisteredProbeIsRunAndItsResultStoredUnderTheDetectedVersion(t *testing.T) {
	h := home(t)
	r := probes.New(h, pass("saw it work"))
	rep, err := r.Run(context.Background(), fleetOf(versionScript(t), "9.1.0"), "claude")
	if err != nil {
		t.Fatal(err)
	}
	if rep.Version != "9.1.0" {
		t.Errorf("report version = %q, want the harness's own 9.1.0", rep.Version)
	}
	l := line(t, rep, capabilities.JudgeNoTools)
	if l.Status != capabilities.Passed || l.Detail != "saw it work" || l.Label == "" {
		t.Errorf("line = %+v, want passed with its detail and a label", l)
	}
	snap, err := capabilities.Open(h).Snapshot("work", "claude")
	if err != nil {
		t.Fatal(err)
	}
	if snap.Version != "9.1.0" || snap.States[capabilities.JudgeNoTools].Status != capabilities.Passed {
		t.Errorf("store: version %q, status %v; want 9.1.0 and passed", snap.Version, snap.States[capabilities.JudgeNoTools].Status)
	}
}

func TestACapabilityWithNoProbeStaysNotChecked(t *testing.T) {
	h := home(t)
	r := probes.New(h, pass("x"))
	rep, err := r.Run(context.Background(), fleetOf(versionScript(t), "9.1.0"), "claude")
	if err != nil {
		t.Fatal(err)
	}
	if l := line(t, rep, capabilities.AutopilotAdvisor); l.Status != capabilities.NotChecked {
		t.Errorf("advisor has no probe and reads %v, want not_checked", l.Status)
	}
	n := 0
	for _, f := range capabilities.Table() {
		n += len(f.Capabilities)
	}
	if len(rep.Lines) != n {
		t.Errorf("report has %d lines, want one per table capability (%d)", len(rep.Lines), n)
	}
}

func TestAProbeRegisteredForAnotherHarnessDoesNotRun(t *testing.T) {
	var calls atomic.Int32
	p := pass("x")
	p.Harness = "codex"
	run := p.Run
	p.Run = func(ctx context.Context, tg probes.Target) probes.Outcome { calls.Add(1); return run(ctx, tg) }
	fleet := fleetOf(versionScript(t), "9.1.0")
	r := probes.New(home(t), p)
	rep, err := r.Run(context.Background(), fleet, "claude")
	if err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 0 || line(t, rep, capabilities.JudgeNoTools).Status != capabilities.NotChecked {
		t.Errorf("a codex probe ran for claude: %d calls, status %v", calls.Load(), line(t, rep, capabilities.JudgeNoTools).Status)
	}
}

func TestWhenTheVersionCannotBeDetectedRunFailsAndStoresNothing(t *testing.T) {
	h := home(t)
	r := probes.New(h, pass("x"))
	if _, err := r.Run(context.Background(), fleetOf(versionScript(t), "FAIL"), "claude"); err == nil {
		t.Fatal("Run succeeded although --version failed")
	}
	if capabilities.Open(h).Proven("work", "claude", capabilities.JudgeNoTools) {
		t.Error("a capability is proven although no version was detected")
	}
}

func TestTwoRunsForOneHarnessShareOneProbeCall(t *testing.T) {
	var calls atomic.Int32
	entered, release := make(chan struct{}, 4), make(chan struct{})
	p := probes.Probe{Harness: "claude", Capability: capabilities.JudgeNoTools,
		Run: func(context.Context, probes.Target) probes.Outcome {
			calls.Add(1)
			entered <- struct{}{}
			<-release
			return probes.Outcome{Passed: true, Detail: "one call"}
		}}
	r := probes.New(home(t), p)
	fleet := fleetOf(versionScript(t), "9.1.0")

	var wg sync.WaitGroup
	reports := make([]probes.Report, 2)
	errs := make([]error, 2)
	start := func(i int) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			reports[i], errs[i] = r.Run(context.Background(), fleet, "claude")
		}()
	}
	start(0)
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		close(release)
		wg.Wait()
		t.Fatalf("the probe never ran (run error: %v)", errs[0])
	}
	start(1)
	time.Sleep(300 * time.Millisecond) // the second run has had time to join
	close(release)
	wg.Wait()

	for i := range reports {
		if errs[i] != nil {
			t.Fatalf("run %d: %v", i, errs[i])
		}
		if l := line(t, reports[i], capabilities.JudgeNoTools); l.Status != capabilities.Passed || l.Detail != "one call" {
			t.Errorf("run %d line = %+v, want the shared passed result", i, l)
		}
	}
	if n := calls.Load(); n != 1 {
		t.Errorf("the probe ran %d times for two concurrent runs, want 1 (each run would spend a model call)", n)
	}
}

func TestRefreshRecordsTheVersionOfAHarnessNeverProbedAndRunsNothing(t *testing.T) {
	h := home(t)
	var calls atomic.Int32
	p := pass("x")
	p.Run = func(context.Context, probes.Target) probes.Outcome { calls.Add(1); return probes.Outcome{Passed: true} }
	r := probes.New(h, p)
	r.Refresh(context.Background(), []config.Fleet{fleetOf(versionScript(t), "9.1.0")})
	r.Wait()
	v, _, err := capabilities.Open(h).Version("work", "claude")
	if err != nil || v != "9.1.0" {
		t.Errorf("recorded version = %q, %v; want 9.1.0", v, err)
	}
	if calls.Load() != 0 {
		t.Errorf("a harness that was never probed was probed %d times at refresh", calls.Load())
	}
}

func TestRefreshWithTheSameVersionRunsNothing(t *testing.T) {
	h := home(t)
	var calls atomic.Int32
	p := pass("x")
	inner := p.Run
	p.Run = func(ctx context.Context, tg probes.Target) probes.Outcome { calls.Add(1); return inner(ctx, tg) }
	r := probes.New(h, p)
	fleet := fleetOf(versionScript(t), "9.1.0")
	if _, err := r.Run(context.Background(), fleet, "claude"); err != nil {
		t.Fatal(err)
	}
	r.Refresh(context.Background(), []config.Fleet{fleet})
	r.Wait()
	if calls.Load() != 1 {
		t.Errorf("probe calls = %d, want the one from Run and none from Refresh", calls.Load())
	}
}

func TestRefreshReprobesInTheBackgroundWhenTheVersionChanged(t *testing.T) {
	h := home(t)
	exe := versionScript(t)
	entered, release := make(chan struct{}, 4), make(chan struct{})
	var calls atomic.Int32
	p := probes.Probe{Harness: "claude", Capability: capabilities.JudgeNoTools,
		Run: func(context.Context, probes.Target) probes.Outcome {
			if calls.Add(1) > 1 {
				entered <- struct{}{}
				<-release
			}
			return probes.Outcome{Passed: true, Detail: "ok"}
		}}
	r := probes.New(h, p)
	if _, err := r.Run(context.Background(), fleetOf(exe, "9.1.0"), "claude"); err != nil {
		t.Fatal(err)
	}

	done := make(chan struct{})
	go func() {
		r.Refresh(context.Background(), []config.Fleet{fleetOf(exe, "9.2.0")})
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Refresh waited for the probe: it must not delay startup")
	}
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("the probe did not re-run in the background after the version changed")
	}
	if v, _, _ := capabilities.Open(h).Version("work", "claude"); v != "9.2.0" {
		t.Errorf("detected version = %q, want 9.2.0 recorded before the probe finishes", v)
	}
	close(release)
	r.Wait()
	if v, _, ok, _ := capabilities.Open(h).LastProbed("work", "claude"); !ok || v != "9.2.0" {
		t.Errorf("last probed under %q, want 9.2.0", v)
	}
	if !capabilities.Open(h).Proven("work", "claude", capabilities.JudgeNoTools) {
		t.Error("the new version's result is not proven")
	}
}

func TestRefreshClearsTheRecordedVersionWhenDetectionFails(t *testing.T) {
	h := home(t)
	st := capabilities.Open(h)
	if err := st.RecordVersion("work", "claude", "9.1.0", time.Now()); err != nil {
		t.Fatal(err)
	}
	if err := st.Record("work", "claude", "9.1.0", capabilities.JudgeNoTools, capabilities.Result{Passed: true, At: time.Now()}); err != nil {
		t.Fatal(err)
	}
	if !st.Proven("work", "claude", capabilities.JudgeNoTools) {
		t.Fatal("setup: the seeded result is not proven")
	}
	r := probes.New(h, pass("x"))
	r.Refresh(context.Background(), []config.Fleet{fleetOf(versionScript(t), "FAIL")})
	r.Wait()
	if v, _, _ := st.Version("work", "claude"); v != "" {
		t.Errorf("version = %q after a failed --version, want it cleared", v)
	}
	if st.Proven("work", "claude", capabilities.JudgeNoTools) {
		t.Error("a capability stays proven although the harness's version is unknown")
	}
}

func TestAFailedVersionCheckKeepsItsReasonInTheStore(t *testing.T) {
	h := home(t)
	r := probes.New(h, pass("x"))
	if _, err := r.Run(context.Background(), fleetOf(versionScript(t), "FAIL"), "claude"); err == nil {
		t.Fatal("Run succeeded although --version failed")
	}
	snap, _ := capabilities.Open(h).Snapshot("work", "claude")
	if snap.Version != "" || !strings.Contains(snap.VersionError, "boom") {
		t.Errorf("snapshot = %+v, want no version and a reason that holds the harness's stderr", snap)
	}
	r2 := probes.New(h, pass("x"))
	r2.Refresh(context.Background(), []config.Fleet{fleetOf(versionScript(t), "FAIL")})
	r2.Wait()
	if snap, _ = capabilities.Open(h).Snapshot("work", "claude"); !strings.Contains(snap.VersionError, "boom") {
		t.Errorf("Refresh kept no reason: %+v", snap)
	}
}

// blocking is a probe that signals it started, then waits for its context or for release.
func blocking(started chan<- struct{}, release <-chan struct{}) probes.Probe {
	return probes.Probe{Harness: "claude", Capability: capabilities.JudgeNoTools,
		Run: func(ctx context.Context, _ probes.Target) probes.Outcome {
			started <- struct{}{}
			select {
			case <-release:
				return probes.Outcome{Passed: true, Detail: "finished"}
			case <-ctx.Done():
				return probes.Outcome{Detail: "stopped"}
			}
		}}
}

func TestACallerGoingAwayCancelsNothingForTheRun(t *testing.T) {
	h := home(t)
	started, release := make(chan struct{}, 1), make(chan struct{})
	r := probes.New(h, blocking(started, release))
	fleet := fleetOf(versionScript(t), "9.1.0")
	first, cancel := context.WithCancel(context.Background())
	firstErr := make(chan error, 1)
	go func() { _, err := r.Run(first, fleet, "claude"); firstErr <- err }()
	<-started
	joined := make(chan probes.Report, 1)
	go func() { rep, _ := r.Run(context.Background(), fleet, "claude"); joined <- rep }()
	time.Sleep(200 * time.Millisecond)
	cancel() // the first caller (a closed tab) leaves; the joiner still wants the result
	time.Sleep(200 * time.Millisecond)
	close(release)
	select {
	case rep := <-joined:
		if l := line(t, rep, capabilities.JudgeNoTools); l.Status != capabilities.Passed {
			t.Errorf("joiner's line = %+v, want the run to finish and pass", l)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the joiner never got a result")
	}
	<-firstErr
	if !capabilities.Open(h).Proven("work", "claude", capabilities.JudgeNoTools) {
		t.Error("the result of the run the first caller left was not stored")
	}
}

func TestCloseStopsARunQuicklyAndStoresNothingAndKeepsTheVersion(t *testing.T) {
	h := home(t)
	st := capabilities.Open(h)
	if err := st.RecordVersion("work", "claude", "9.0.0", time.Now()); err != nil {
		t.Fatal(err)
	}
	started, release := make(chan struct{}, 1), make(chan struct{})
	defer close(release)
	r := probes.New(h, blocking(started, release))
	errc := make(chan error, 1)
	go func() {
		_, err := r.Run(context.Background(), fleetOf(versionScript(t), "9.1.0"), "claude")
		errc <- err
	}()
	<-started
	r.Close()
	select {
	case err := <-errc:
		if err == nil {
			t.Error("a stopped run returned no error")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Close did not stop the run")
	}
	if st.Proven("work", "claude", capabilities.JudgeNoTools) {
		t.Error("a stopped run stored a result")
	}
	if _, _, ok, _ := st.LastProbed("work", "claude"); ok {
		t.Error("a stopped run stored a result for the new version")
	}
}

func TestCloseDuringVersionDetectionLeavesTheRecordedVersionAlone(t *testing.T) {
	h := home(t)
	st := capabilities.Open(h)
	if err := st.RecordVersion("work", "claude", "9.0.0", time.Now()); err != nil {
		t.Fatal(err)
	}
	exe := filepath.Join(t.TempDir(), "claude")
	if err := os.WriteFile(exe, []byte("#!/bin/sh\nsleep 30\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	r := probes.New(h, pass("x"))
	r.Refresh(context.Background(), []config.Fleet{fleetOf(exe, "")})
	time.Sleep(300 * time.Millisecond)
	r.Close()
	done := make(chan struct{})
	go func() { r.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Close did not stop the version detection")
	}
	if v, _, _ := st.Version("work", "claude"); v != "9.0.0" {
		t.Errorf("version = %q after a shutdown, want the recorded 9.0.0 untouched", v)
	}
}

func TestARunsOwnTimeLimitStillCountsAsAFailure(t *testing.T) {
	h := home(t)
	started, release := make(chan struct{}, 1), make(chan struct{})
	defer close(release)
	r := probes.New(h, blocking(started, release))
	r.Timeout = time.Second
	rep, err := r.Run(context.Background(), fleetOf(versionScript(t), "9.1.0"), "claude")
	if err != nil {
		t.Fatal(err)
	}
	if l := line(t, rep, capabilities.JudgeNoTools); l.Status != capabilities.Failed {
		t.Errorf("line = %+v, want failed when the run's own time limit ends it", l)
	}
}

func TestAPanickingProbeDoesNotHangTheNextRun(t *testing.T) {
	boom := probes.Probe{Harness: "claude", Capability: capabilities.JudgeNoTools,
		Run: func(context.Context, probes.Target) probes.Outcome { panic("boom") }}
	r := probes.New(home(t), boom)
	fleet := fleetOf(versionScript(t), "9.1.0")
	func() {
		defer func() { _ = recover() }()
		_, _ = r.Run(context.Background(), fleet, "claude")
	}()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	r2done := make(chan struct{})
	go func() {
		defer func() { _ = recover(); close(r2done) }()
		_, _ = r.Run(ctx, fleet, "claude")
	}()
	select {
	case <-r2done:
	case <-time.After(2 * time.Second):
		t.Fatal("a later run hangs on the flight the panic left behind")
	}
}
