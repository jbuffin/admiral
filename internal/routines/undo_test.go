// Undo and the single-record read, over the service with a fake stop seam and a temp home.
package routines_test

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/jbuffin/admiral/internal/routines"
	"github.com/jbuffin/admiral/internal/standing"
)

type fakeStop struct {
	mu    sync.Mutex
	calls []string
	err   error
}

func (f *fakeStop) stop(session string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, session)
	return f.err
}

func (f *fakeStop) set(err error) { f.mu.Lock(); f.err = err; f.mu.Unlock() }
func (f *fakeStop) n() int        { f.mu.Lock(); defer f.mu.Unlock(); return len(f.calls) }

type undoRig struct {
	*api
	stop    *fakeStop
	routine routines.Routine
	other   routines.Routine
	mu      sync.Mutex
	hosted  []routines.ListedShip // the ships the fake host lists
}

func (u *undoRig) list() []routines.ListedShip {
	u.mu.Lock()
	defer u.mu.Unlock()
	return append([]routines.ListedShip(nil), u.hosted...)
}

// host lists rec's ship as the host would: under the record's routine key, launched when the record was made.
func (u *undoRig) host(rec routines.Trigger) {
	u.mu.Lock()
	defer u.mu.Unlock()
	u.hosted = append(u.hosted, routines.ListedShip{Session: rec.Ship, Fleet: "work", Routine: "routine:" + rec.Routine,
		Launched: rec.At.UTC().Format(time.RFC3339), Status: "underway"})
}

func (u *undoRig) unhost(session string) {
	u.mu.Lock()
	defer u.mu.Unlock()
	u.hosted = slices.DeleteFunc(u.hosted, func(sh routines.ListedShip) bool { return sh.Session == session })
}

func newUndoRig(t *testing.T) *undoRig {
	t.Helper()
	u := &undoRig{api: &api{t: t, rig: newRig(t)}, stop: &fakeStop{}}
	mux := http.NewServeMux()
	(&routines.Service{Store: u.rig.store, Stop: u.stop.stop, Listed: u.list}).Routes(mux)
	u.srv = httptest.NewServer(mux)
	t.Cleanup(u.srv.Close)
	u.routine = u.rig.create("work", launchRoutine())
	second := launchRoutine()
	second.Name = "Other sweep"
	u.other = u.rig.create("work", second)
	return u
}

func (u *undoRig) record(t routines.Trigger) routines.Trigger {
	u.t.Helper()
	got, err := u.rig.store.AppendTrigger("work", t)
	if err != nil {
		u.t.Fatal(err)
	}
	return got
}

// launch is an event launch of ship the host lists.
func (u *undoRig) launch(ship string) routines.Trigger {
	return u.launchAt(ship, u.rig.now)
}

func (u *undoRig) launchAt(ship string, at time.Time) routines.Trigger {
	rec := u.record(routines.Trigger{Routine: u.routine.ID, Outcome: routines.Launched, Ship: ship, WorkerLaunched: true, Restricted: standing.DraftFix, Tier: "T1",
		Event: "https://github.com/acme/api/pull/7", At: at})
	u.host(rec)
	return rec
}

func (u *undoRig) clone(ship string) string {
	u.t.Helper()
	dir := filepath.Join(u.rig.home, "checkouts", ship)
	if err := os.MkdirAll(filepath.Join(dir, ".git"), 0o755); err != nil {
		u.t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "work.txt"), []byte("local work"), 0o644); err != nil {
		u.t.Fatal(err)
	}
	return dir
}

func (u *undoRig) undo(routine, id string) int {
	u.t.Helper()
	code, _ := u.do("POST", "/api/fleets/work/routines/"+routine+"/runs/"+id+"/undo", "")
	return code
}

func (u *undoRig) undone(id string) bool {
	u.t.Helper()
	runs, err := u.rig.store.Recent("work", u.routine.ID, 50)
	if err != nil {
		u.t.Fatal(err)
	}
	for _, r := range runs {
		if r.ID == id {
			return r.Undone
		}
	}
	u.t.Fatalf("record %s is not in %+v", id, runs)
	return false
}

func exists(path string) bool { _, err := os.Lstat(path); return err == nil }

func TestUndoStopsAListedShipRemovesItsCloneAndMarksTheRecord(t *testing.T) {
	u := newUndoRig(t)
	rec := u.launch("calm-otter")
	dir := u.clone("calm-otter")
	if code := u.undo(u.routine.ID, rec.ID); code != http.StatusOK {
		t.Fatalf("undo = %d, want 200", code)
	}
	if u.stop.n() != 1 || u.stop.calls[0] != "calm-otter" {
		t.Errorf("stop calls = %v, want one for calm-otter", u.stop.calls)
	}
	if exists(dir) {
		t.Errorf("%s still exists, want the clone removed", dir)
	}
	if !u.undone(rec.ID) {
		t.Error("the record is not marked undone")
	}
}

func TestUndoRemovesOnlyTheShipsCloneAndNotItsNeighboursOrTheHome(t *testing.T) {
	u := newUndoRig(t)
	rec := u.launch("calm-otter")
	u.clone("calm-otter")
	neighbour := u.clone("brisk-heron")
	if code := u.undo(u.routine.ID, rec.ID); code != http.StatusOK {
		t.Fatalf("undo = %d, want 200", code)
	}
	if !exists(filepath.Join(neighbour, "work.txt")) || !exists(filepath.Join(u.rig.home, "checkouts")) {
		t.Error("a neighbouring clone or checkouts/ itself was removed")
	}
}

func TestASecondUndoStopsNothingAndLeavesANewShipThatReusedTheName(t *testing.T) {
	u := newUndoRig(t)
	rec := u.launch("calm-otter")
	u.clone("calm-otter")
	if code := u.undo(u.routine.ID, rec.ID); code != http.StatusOK {
		t.Fatalf("first undo = %d, want 200", code)
	}
	reused := u.clone("calm-otter")
	if code := u.undo(u.routine.ID, rec.ID); code != http.StatusOK {
		t.Fatalf("second undo = %d, want 200", code)
	}
	if u.stop.n() != 1 {
		t.Errorf("%d stop calls, want the first only", u.stop.n())
	}
	if !exists(filepath.Join(reused, "work.txt")) {
		t.Error("the second undo removed the clone of a new ship with the same name")
	}
}

func TestUndoOfAShipTheHostDoesNotListStillRemovesItsCloneAndMarksTheRecord(t *testing.T) {
	u := newUndoRig(t)
	u.stop.set(routines.ErrShipNotListed)
	rec := u.launch("calm-otter")
	dir := u.clone("calm-otter")
	if code := u.undo(u.routine.ID, rec.ID); code != http.StatusOK {
		t.Fatalf("undo = %d, want 200", code)
	}
	if exists(dir) || !u.undone(rec.ID) {
		t.Errorf("clone exists = %v, undone = %v, want it removed and the record marked", exists(dir), u.undone(rec.ID))
	}
}

func TestUndoWhoseStopFailsRemovesAndMarksNothing(t *testing.T) {
	u := newUndoRig(t)
	u.stop.set(errors.New("the terminal host is down"))
	rec := u.launch("calm-otter")
	dir := u.clone("calm-otter")
	code, body := u.do("POST", "/api/fleets/work/routines/"+u.routine.ID+"/runs/"+rec.ID+"/undo", "")
	wantError(t, code, body, http.StatusInternalServerError)
	if !exists(dir) || u.undone(rec.ID) {
		t.Errorf("clone exists = %v, undone = %v, want the clone kept and the record unmarked", exists(dir), u.undone(rec.ID))
	}
}

func TestUndoWhoseRemovalFailsLeavesTheRecordUnmarkedAndARetryFinishesIt(t *testing.T) {
	u := newUndoRig(t)
	rec := u.launch("calm-otter")
	dir := u.clone("calm-otter")
	if err := os.Chmod(dir, 0o555); err != nil { // a directory that cannot lose its entries
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o755) })
	code, body := u.do("POST", "/api/fleets/work/routines/"+u.routine.ID+"/runs/"+rec.ID+"/undo", "")
	wantError(t, code, body, http.StatusInternalServerError)
	if u.undone(rec.ID) {
		t.Fatal("the record is marked though the clone is still there")
	}
	if err := os.Chmod(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	u.stop.set(routines.ErrShipNotListed)
	if code := u.undo(u.routine.ID, rec.ID); code != http.StatusOK {
		t.Fatalf("retry = %d, want 200", code)
	}
	if exists(dir) || !u.undone(rec.ID) {
		t.Errorf("after the retry the clone exists = %v and undone = %v, want removed and marked", exists(dir), u.undone(rec.ID))
	}
}

func TestUndoRefusesWhatIsNotAnEventShipOfThatRoutineAndTouchesNothing(t *testing.T) {
	u := newUndoRig(t)
	dir := u.clone("calm-otter")
	cases := map[string]struct {
		routine string
		rec     routines.Trigger
	}{
		"a proposed record":       {u.routine.ID, routines.Trigger{Routine: u.routine.ID, Outcome: routines.Proposed, Ship: "calm-otter", WorkerLaunched: true}},
		"a launch from the clock": {u.routine.ID, routines.Trigger{Routine: u.routine.ID, Outcome: routines.Launched, Ship: "calm-otter"}},
		"another routine's ship":  {u.routine.ID, routines.Trigger{Routine: u.other.ID, Outcome: routines.Launched, Ship: "calm-otter", WorkerLaunched: true}},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			rec := u.record(c.rec)
			code, body := u.do("POST", "/api/fleets/work/routines/"+c.routine+"/runs/"+rec.ID+"/undo", "")
			wantError(t, code, body, http.StatusBadRequest)
			if u.stop.n() != 0 || !exists(dir) {
				t.Errorf("stop calls = %d, clone exists = %v, want no stop and the clone kept", u.stop.n(), exists(dir))
			}
		})
	}
}

func TestUndoOfAnUnknownFleetRoutineOrRecordIs404(t *testing.T) {
	u := newUndoRig(t)
	rec := u.launch("calm-otter")
	for name, path := range map[string]string{
		"fleet":   "/api/fleets/nowhere/routines/" + u.routine.ID + "/runs/" + rec.ID + "/undo",
		"routine": "/api/fleets/work/routines/no-such/runs/" + rec.ID + "/undo",
		"record":  "/api/fleets/work/routines/" + u.routine.ID + "/runs/no-such/undo",
	} {
		t.Run(name, func(t *testing.T) {
			code, body := u.do("POST", path, "")
			wantError(t, code, body, http.StatusNotFound)
		})
	}
	if u.stop.n() != 0 {
		t.Errorf("%d stop calls, want none", u.stop.n())
	}
}

func TestUndoDoesNotFollowACheckoutsLinkOutsideTheHome(t *testing.T) {
	u := newUndoRig(t)
	rec := u.launch("calm-otter")
	outside := t.TempDir()
	keep := filepath.Join(outside, "engineers-work.txt")
	if err := os.WriteFile(keep, []byte("mine"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(u.rig.home, "checkouts"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(u.rig.home, "checkouts", "calm-otter")); err != nil {
		t.Fatal(err)
	}
	u.undo(u.routine.ID, rec.ID)
	if u.stop.n() != 1 {
		t.Errorf("%d stop calls, want the ship stopped before its checkout is looked at", u.stop.n())
	}
	if !exists(keep) {
		t.Error("Undo followed the link and deleted a file outside the home")
	}
}

func TestTheSingleRecordRouteReturnsTheRecordWithItsUndoneState(t *testing.T) {
	u := newUndoRig(t)
	rec := u.launch("calm-otter")
	u.clone("calm-otter")
	path := "/api/fleets/work/routines/" + u.routine.ID + "/runs/" + rec.ID
	code, body := u.do("GET", path, "")
	if code != http.StatusOK || body["id"] != rec.ID || body["ship"] != "calm-otter" || body["undone"] == true {
		t.Fatalf("GET = %d %v, want 200 with the record, not undone", code, body)
	}
	if c := u.undo(u.routine.ID, rec.ID); c != http.StatusOK {
		t.Fatalf("undo = %d, want 200", c)
	}
	code, body = u.do("GET", path, "")
	if code != http.StatusOK || body["undone"] != true {
		t.Errorf("GET after undo = %d %v, want undone true", code, body)
	}
}

func TestTheSingleRecordRouteIs404ForAnUnknownRecordAndForAnotherRoutinesRecord(t *testing.T) {
	u := newUndoRig(t)
	mine := u.launch("calm-otter")
	theirs := u.record(routines.Trigger{Routine: u.other.ID, Outcome: routines.Launched, Ship: "brisk-heron", WorkerLaunched: true})
	for name, path := range map[string]string{
		"unknown record":    "/api/fleets/work/routines/" + u.routine.ID + "/runs/no-such",
		"another routine's": "/api/fleets/work/routines/" + u.routine.ID + "/runs/" + theirs.ID,
		"unknown routine":   "/api/fleets/work/routines/no-such/runs/" + mine.ID,
	} {
		t.Run(name, func(t *testing.T) {
			code, body := u.do("GET", path, "")
			wantError(t, code, body, http.StatusNotFound)
		})
	}
}

func TestAnUndoOfALaunchWhoseShipEndedFailedStopsItRemovesItsCloneAndMarksIt(t *testing.T) {
	u := newUndoRig(t)
	rec := u.launch("calm-otter")
	dir := u.clone("calm-otter")
	if err := u.rig.store.MarkFailed("work", rec.ID, "the ship ended failed"); err != nil {
		t.Fatal(err)
	}
	if code := u.undo(u.routine.ID, rec.ID); code != http.StatusOK {
		t.Fatalf("undo = %d, want 200", code)
	}
	if u.stop.n() != 1 || exists(dir) || !u.undone(rec.ID) {
		t.Errorf("stops = %d, clone present = %v, undone = %v, want one stop, no clone and the mark", u.stop.n(), exists(dir), u.undone(rec.ID))
	}
}

func TestAnUndoStopsTheLaunchesShipEvenWhenItDeletedItsOwnClone(t *testing.T) {
	u := newUndoRig(t)
	rec := u.launch("calm-otter")
	if code := u.undo(u.routine.ID, rec.ID); code != http.StatusOK {
		t.Fatalf("undo = %d, want 200", code)
	}
	if u.stop.n() != 1 || u.stop.calls[0] != "calm-otter" {
		t.Errorf("stop calls = %v, want one for calm-otter: the host lists the launch's ship, clone or no clone", u.stop.calls)
	}
	if !u.undone(rec.ID) {
		t.Error("the record is not marked undone")
	}
}

func TestAnUndoWhoseShipWillNotStopLeavesTheRecordUnmarkedWhenTheCloneIsGone(t *testing.T) {
	u := newUndoRig(t)
	u.stop.set(errors.New("the terminal host is down"))
	rec := u.launch("calm-otter")
	code, body := u.do("POST", "/api/fleets/work/routines/"+u.routine.ID+"/runs/"+rec.ID+"/undo", "")
	wantError(t, code, body, http.StatusInternalServerError)
	if u.undone(rec.ID) {
		t.Error("the record is marked undone though its ship still runs")
	}
}

func TestAnUndoNeverStopsANewerShipThatReusedTheName(t *testing.T) {
	u := newUndoRig(t)
	older := u.launchAt("calm-otter", u.rig.now)
	u.unhost("calm-otter")
	u.launchAt("calm-otter", u.rig.now.Add(time.Hour))
	if code := u.undo(u.routine.ID, older.ID); code != http.StatusOK {
		t.Fatalf("undo = %d, want 200", code)
	}
	if u.stop.n() != 0 {
		t.Errorf("stop calls = %v, want none: the listed calm-otter belongs to the newer launch", u.stop.calls)
	}
	if !u.undone(older.ID) {
		t.Error("the older record is not marked undone")
	}
}

func TestAnUndoNeverStopsOrDeletesTheCloneOfANewerShipThatReusedTheName(t *testing.T) {
	u := newUndoRig(t)
	older := u.launchAt("calm-otter", u.rig.now)
	u.unhost("calm-otter")
	u.launchAt("calm-otter", u.rig.now.Add(time.Hour))
	newer := u.clone("calm-otter")
	u.undo(u.routine.ID, older.ID)
	if u.stop.n() != 0 {
		t.Errorf("stop calls = %v, want none for the newer ship", u.stop.calls)
	}
	if !exists(filepath.Join(newer, "work.txt")) {
		t.Error("the undo of the older launch deleted the newer ship's clone")
	}
}

func TestAnUndoNeverStopsOrDeletesTheCloneOfAScheduledLaunchOfTheSameRoutineThatReusedTheName(t *testing.T) {
	u := newUndoRig(t)
	older := u.launchAt("calm-otter", u.rig.now)
	u.unhost("calm-otter")
	later := u.rig.now.Add(time.Hour)
	scheduled := u.record(routines.Trigger{Routine: u.routine.ID, Outcome: routines.Launched, Ship: "calm-otter", Tier: "T1", At: later})
	u.host(scheduled)
	clone := u.clone("calm-otter")
	if code := u.undo(u.routine.ID, older.ID); code != http.StatusOK {
		t.Fatalf("undo = %d, want 200", code)
	}
	if u.stop.n() != 0 {
		t.Errorf("stop calls = %v, want none for the scheduled launch's ship", u.stop.calls)
	}
	if !exists(filepath.Join(clone, "work.txt")) {
		t.Error("the undo deleted the clone of the scheduled launch's ship")
	}
}

func TestAnUndoWithNoHostListFailsAndRemovesNothing(t *testing.T) {
	u := newUndoRig(t)
	rec := u.launch("calm-otter")
	dir := u.clone("calm-otter")
	mux := http.NewServeMux()
	(&routines.Service{Store: u.rig.store, Stop: u.stop.stop}).Routes(mux)
	srv := httptest.NewServer(mux)
	defer srv.Close()
	u.srv = srv
	if code := u.undo(u.routine.ID, rec.ID); code != http.StatusInternalServerError {
		t.Fatalf("undo = %d, want 500", code)
	}
	if !exists(dir) || u.undone(rec.ID) {
		t.Error("the undo removed the clone or marked the record")
	}
}

func TestAnUndoLeavesAListedShipOfThatNameThatIsNotThisLaunchAlone(t *testing.T) {
	at := time.Date(2026, 11, 13, 10, 0, 0, 0, time.UTC)
	cases := map[string]routines.ListedShip{
		"a hand launch with no routine key": {Session: "calm-otter", Fleet: "work", Launched: at.Add(time.Hour).Format(time.RFC3339), Status: "underway"},
		"another routine's ship":            {Session: "calm-otter", Fleet: "work", Routine: "routine:other", Launched: at.Format(time.RFC3339), Status: "underway"},
		"another fleet's ship":              {Session: "calm-otter", Fleet: "play", Routine: "routine:", Launched: at.Format(time.RFC3339), Status: "underway"},
		"a ship launched before the record": {Session: "calm-otter", Fleet: "work", Routine: "routine:", Launched: at.Add(-time.Hour).Format(time.RFC3339), Status: "underway"},
	}
	for name, sh := range cases {
		t.Run(name, func(t *testing.T) {
			u := newUndoRig(t)
			rec := u.launchAt("calm-otter", at)
			u.unhost("calm-otter")
			if sh.Routine == "routine:" {
				sh.Routine += u.routine.ID
			}
			u.mu.Lock()
			u.hosted = append(u.hosted, sh)
			u.mu.Unlock()
			if code := u.undo(u.routine.ID, rec.ID); code != http.StatusOK {
				t.Fatalf("undo = %d, want 200", code)
			}
			if u.stop.n() != 0 {
				t.Errorf("stop calls = %v, want none", u.stop.calls)
			}
		})
	}
}

func TestAnUndoWithNoStopSeamFailsAndRemovesNothing(t *testing.T) {
	u := newUndoRig(t)
	mux := http.NewServeMux()
	(&routines.Service{Store: u.rig.store}).Routes(mux)
	srv := httptest.NewServer(mux)
	defer srv.Close()
	rec := u.launch("calm-otter")
	dir := u.clone("calm-otter")
	res, err := http.Post(srv.URL+"/api/fleets/work/routines/"+u.routine.ID+"/runs/"+rec.ID+"/undo", "application/json", nil)
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusInternalServerError || !exists(dir) || u.undone(rec.ID) {
		t.Errorf("status = %d, clone present = %v, undone = %v, want 500, the clone and no mark", res.StatusCode, exists(dir), u.undone(rec.ID))
	}
}
