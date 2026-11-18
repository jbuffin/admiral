package ships

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/jbuffin/admiral/internal/launcher"
	"github.com/jbuffin/admiral/internal/moor"
)

// What a ship's findings do when it sails off: kept, served by the route, and the scout's result.

type keptFindings struct {
	Ship          string `json:"ship"`
	FirstLaunched string `json:"first_launched"`
	Fleet         string `json:"fleet"`
	Findings      string `json:"findings"`
}

func (r *liveRig) findingsPath() string { return filepath.Join(r.home, "reports", "saltpaw.md") }

func (r *liveRig) writeFindings(t *testing.T, body string) {
	t.Helper()
	if err := os.WriteFile(r.findingsPath(), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

func (r *liveRig) firstLaunched(t *testing.T) string {
	t.Helper()
	_, raw, ok := r.wire(t)
	var first string
	if !ok || json.Unmarshal(raw["first_launched"], &first) != nil || first == "" {
		t.Fatalf("the ship carries no first_launched (listed %v)", ok)
	}
	return first
}

// sailClean lets a quick ship end the way its closing steps ask: a report, then a clean exit.
func (r *liveRig) sailClean(t *testing.T) {
	t.Helper()
	r.writeReport(t, "done")
	if err := os.WriteFile(filepath.Join(r.rec, "exit"), []byte("0"), 0o600); err != nil {
		t.Fatal(err)
	}
}

// ended waits for the ship's ended record, after which its findings are kept.
func (r *liveRig) ended(t *testing.T) map[string]any {
	t.Helper()
	waitUntil(t, "the ended record", func() bool { return len(outcomeLines(t, r.home, "saltpaw", "ended")) > 0 })
	got := outcomeLines(t, r.home, "saltpaw", "ended")
	return got[len(got)-1]
}

// sailedOff waits for the ship to leave tmux and its report file to go: its files go after its findings are kept.
func (r *liveRig) sailedOff(t *testing.T) {
	t.Helper()
	r.gone(t, "the ship to leave tmux")
	waitUntil(t, "the ship's report file to go", func() bool {
		_, err := os.Stat(filepath.Join(r.home, "reports", "saltpaw.json"))
		return os.IsNotExist(err)
	})
}

func (r *liveRig) getFindings(ship, first string) *httptest.ResponseRecorder {
	target := "/api/ships/" + ship + "/findings"
	if first != "" {
		target += "?first_launched=" + url.QueryEscape(first)
	}
	rec := httptest.NewRecorder()
	r.mux.ServeHTTP(rec, httptest.NewRequest("GET", target, nil))
	return rec
}

func (r *liveRig) wantFindings(t *testing.T, first string, want keptFindings) {
	t.Helper()
	rec := r.getFindings("saltpaw", first)
	var got keptFindings
	if rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &got) != nil || got != want {
		t.Errorf("GET findings (first_launched=%q) = %d %s, want 200 with %+v", first, rec.Code, rec.Body, want)
	}
}

func (r *liveRig) wantNoFindings(t *testing.T, first string) {
	t.Helper()
	wantError(t, r.getFindings("saltpaw", first), http.StatusNotFound, "a ship with no kept findings")
}

func TestAQuickShipsFindingsAreServedAfterItSailsOffAndItsFilesAreGone(t *testing.T) {
	r := scoutRig(t, false)
	first := r.firstLaunched(t)
	r.writeFindings(t, "# Found\n\nthe race is in the cache\n")
	r.sailClean(t)
	if e := r.ended(t); e["findings"] != true {
		t.Errorf("ended = %v, want findings true", e)
	}
	r.gone(t, "the ship to sail off")
	waitUntil(t, "the ship's files to go", func() bool {
		_, e1 := os.Stat(r.findingsPath())
		_, e2 := os.Stat(filepath.Join(r.home, "reports", "saltpaw.json"))
		return os.IsNotExist(e1) && os.IsNotExist(e2)
	})
	want := keptFindings{Ship: "saltpaw", FirstLaunched: first, Fleet: "work", Findings: "# Found\n\nthe race is in the cache\n"}
	r.wantFindings(t, first, want)
	r.wantFindings(t, "", want)
}

func TestAShipThatWroteNoFindingsHasNoneToServeAndIsNotMarkedFailed(t *testing.T) {
	r := scoutRig(t, false)
	first := r.firstLaunched(t)
	r.sailClean(t)
	e := r.ended(t)
	if _, has := e["result"]; has || e["findings"] != false {
		t.Errorf("ended = %v, want findings false and no result field for a ship that is not a scout", e)
	}
	r.gone(t, "the ship to sail off")
	r.wantNoFindings(t, "")
	r.wantNoFindings(t, first)
}

func TestAScoutsFindingsAreKeptAndItsEndedRecordSaysFindingsAndLeavesTheReportAlone(t *testing.T) {
	r := scoutRig(t, true)
	first := r.firstLaunched(t)
	r.turn(t, "working", "")
	r.writeFindings(t, "# Flaky test\n\nit sleeps\n")
	r.writeReport(t, "partial")
	r.turn(t, "needs_you", "stop")
	e := r.ended(t)
	if _, has := e["result"]; has || e["findings"] != true {
		t.Errorf("ended = %v, want findings true and no result field", e)
	}
	if rep, _ := e["report"].(map[string]any); rep == nil || rep["result"] != "partial" || rep["version"] != float64(2) {
		t.Errorf("ended report = %v, want the report as the scout left it", e["report"])
	}
	r.sailedOff(t)
	r.wantFindings(t, first, keptFindings{Ship: "saltpaw", FirstLaunched: first, Fleet: "work", Findings: "# Flaky test\n\nit sleeps\n"})
}

func TestAScoutThatLeavesNoFindingsOrAnEmptyFileEndsFailedWithItsReportUntouched(t *testing.T) {
	for _, c := range []struct {
		name  string
		write bool
	}{{"no findings file", false}, {"an empty findings file", true}} {
		t.Run(c.name, func(t *testing.T) {
			r := scoutRig(t, true)
			r.turn(t, "working", "")
			if c.write {
				r.writeFindings(t, "")
			}
			r.turn(t, "needs_you", "stop")
			e := r.ended(t)
			if e["result"] != "failed" || e["findings"] != false {
				t.Errorf("ended = %v, want result failed and findings false", e)
			}
			if rep, has := e["report"]; !has || rep != nil {
				t.Errorf("ended report = %v, want null: a scout's report is never rewritten", rep)
			}
			r.gone(t, "the scout to be stopped")
			r.wantNoFindings(t, "")
		})
	}
}

func TestAFindingsFileThatIsAPipeDoesNotHangAdmiralAndTheShipStillSailsOff(t *testing.T) {
	r := scoutRig(t, false)
	if err := syscall.Mkfifo(r.findingsPath(), 0o600); err != nil {
		t.Fatal(err)
	}
	r.sailClean(t)
	if e := r.ended(t); e["findings"] != false {
		t.Errorf("ended = %v, want findings false", e)
	}
	r.gone(t, "the ship to sail off")
	r.wantNoFindings(t, "")
}

func TestALaterRoundThatWritesNoFindingsLeavesTheEarlierRoundsKeptFindingsServed(t *testing.T) {
	r := scoutRig(t, false)
	first := r.firstLaunched(t)
	r.writeFindings(t, "round one found this\n")
	r.sailClean(t)
	r.ended(t)
	r.sailedOff(t)
	// Round two is a wake: same name, same first launch, a harness that exits cleanly at once and writes nothing.
	if _, lerr := r.s.LaunchOrder(launcher.Request{Fleet: "work", Harness: "claude", Dir: r.checkout, Model: "opus", Name: "Saltpaw",
		Session: "saltpaw", Round: 1, FirstLaunched: first, Prompt: "go on", Skill: launcher.Skill{Kind: "none"}, Autopilot: true}); lerr != nil {
		t.Fatalf("LaunchOrder = %v", lerr)
	}
	r.started(t, 2)
	waitUntil(t, "round two's ended record", func() bool { return len(outcomeLines(t, r.home, "saltpaw", "ended")) == 2 })
	r.gone(t, "round two to sail off")
	r.wantFindings(t, first, keptFindings{Ship: "saltpaw", FirstLaunched: first, Fleet: "work", Findings: "round one found this\n"})
}

// voyageRig is scoutRig's quick ship with a stamped first launch, so a later voyage under the name cannot share its second.
func voyageRig(t *testing.T, first string) *liveRig {
	t.Helper()
	r := newWakeRig(t, 0)
	req := launcher.Request{Fleet: "work", Harness: "claude", Dir: r.checkout, Model: "opus", Name: "Saltpaw", Session: "saltpaw",
		FirstLaunched: first, Prompt: "go", Skill: launcher.Skill{Kind: "none"}, Autopilot: true}
	if _, lerr := r.s.LaunchOrder(req); lerr != nil {
		t.Fatalf("LaunchOrder = %v", lerr)
	}
	r.started(t, 1)
	mux := http.NewServeMux()
	r.s.Routes(mux)
	l := &liveRig{nextRig: &nextRig{wakeRig: r, mux: mux}, gh: &openGH{open: map[string]bool{}}, stamp: time.Now()}
	r.s.GitHub = func(string) moor.GitHub { return l.gh }
	r.s.baselineAll()
	go r.s.Run(r.ctx)
	return l
}

func TestAReusedShipNameKeepsTheOldVoyagesFindingsUnderItsOwnFirstLaunchAndTheNewVoyageHasNone(t *testing.T) {
	const old = "2026-01-02T03:04:05Z"
	r := voyageRig(t, old)
	r.writeFindings(t, "old voyage\n")
	r.sailClean(t)
	r.ended(t)
	r.sailedOff(t)
	// A fresh launch under the same name: no FirstLaunched, so it is a new voyage.
	if _, lerr := r.s.LaunchOrder(launcher.Request{Fleet: "work", Harness: "claude", Dir: r.checkout, Model: "opus", Name: "Saltpaw",
		Session: "saltpaw", Prompt: "go", Skill: launcher.Skill{Kind: "none"}, Autopilot: true}); lerr != nil {
		t.Fatalf("LaunchOrder = %v", lerr)
	}
	r.started(t, 2)
	if _, err := os.Stat(r.findingsPath()); !os.IsNotExist(err) {
		t.Errorf("the new voyage starts with a findings file (%v)", err)
	}
	fresh := r.firstLaunched(t)
	r.wantFindings(t, old, keptFindings{Ship: "saltpaw", FirstLaunched: old, Fleet: "work", Findings: "old voyage\n"})
	r.wantNoFindings(t, fresh)
}
