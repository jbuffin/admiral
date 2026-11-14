// Undo and the single-record read, over the service with a fake stop seam and a temp home.
package routines_test

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"

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
}

func newUndoRig(t *testing.T) *undoRig {
	t.Helper()
	u := &undoRig{api: &api{t: t, rig: newRig(t)}, stop: &fakeStop{}}
	mux := http.NewServeMux()
	(&routines.Service{Store: u.rig.store, Stop: u.stop.stop}).Routes(mux)
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

func (u *undoRig) launch(ship string) routines.Trigger {
	return u.record(routines.Trigger{Routine: u.routine.ID, Outcome: routines.Launched, Ship: ship, WorkerLaunched: true, Restricted: standing.DraftFix, Tier: "T1"})
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
