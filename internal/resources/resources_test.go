package resources_test

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/jbuffin/admiral/internal/config"
	"github.com/jbuffin/admiral/internal/resources"
	"github.com/jbuffin/admiral/internal/ships"
)

// The interface these tests assume (#118, locked resources). The Set answers
// which units of each resource are held and by whom, whether a set of resources
// is free, and each resource's check state; it runs the checks.
//
//	resources.New(resources.Options) *resources.Set
//	Options{Config func() *config.Config; Ships func() []ships.Ship;
//	        CheckTimeout, CheckEvery time.Duration  // 10s and 15s when zero
//	        OnChange func()}                          // a check state changed, a lock was
//	                                                 // released or a reservation dropped
//	(*Set).Run(ctx context.Context)    // checks now, on config change and every CheckEvery
//	(*Set).CheckNow()                  // runs every check and returns when all are done
//	(*Set).State() []resources.State   // sorted by name; State has the API's JSON shape:
//	  State{Name; Capacity, InUse int; Holders []Holder; Check string} with Check
//	  "ok"|"missing"|"pending"|"none"; Holder{Kind "ship"|"lock"; Session, Label string}.
//	  InUse counts reservations too; a reservation has no Holder. Holders list ships
//	  by session, then locks in the order they were taken.
//	(*Set).Reserve(names []string) (release func(), waits []resources.Wait)
//	  all-or-nothing: with every name free and its check "ok" or "none" it reserves a
//	  unit of each and returns release (idempotent); otherwise it reserves nothing and
//	  returns, for each name that is not ready, Wait{Name, Why "busy"|"missing"|"pending"}.
//	(*Set).Lock(name, label string) (release func(), err error)
//	  non-blocking; err is resources.ErrBusy when no unit is free and
//	  resources.ErrUnknown when the name isn't defined; the check isn't consulted.
//	  release is idempotent.
//	(*Set).Changed() <-chan struct{}   // closed when a lock is released, a reservation
//	                                   // dropped or a check state changed; fetch a fresh one each time

type rig struct {
	cfg     *config.Config
	ships   []ships.Ship
	changes atomic.Int32
	set     *resources.Set
}

func newRig(t *testing.T, defs map[string]config.Resource) *rig {
	t.Helper()
	r := &rig{cfg: &config.Config{Resources: defs}}
	r.set = resources.New(resources.Options{
		Config:       func() *config.Config { return r.cfg },
		Ships:        func() []ships.Ship { return append([]ships.Ship(nil), r.ships...) },
		CheckTimeout: 300 * time.Millisecond,
		CheckEvery:   time.Hour,
		OnChange:     func() { r.changes.Add(1) },
	})
	return r
}

func hold(session, status string, res ...string) ships.Ship {
	return ships.Ship{Session: session, Status: status, Resources: res}
}

func (r *rig) state(t *testing.T, name string) resources.State {
	t.Helper()
	for _, s := range r.set.State() {
		if s.Name == name {
			return s
		}
	}
	t.Fatalf("no state for %s in %+v", name, r.set.State())
	return resources.State{}
}

// Units are held by ships that are underway or need you and recorded the
// resource, by locks, and by reservations; nothing else.
func TestUnitsHeld(t *testing.T) {
	phone := map[string]config.Resource{"phone": {Capacity: 1}}
	tests := []struct {
		name   string
		defs   map[string]config.Resource
		ships  []ships.Ship
		locks  int
		reserv int
		inUse  int
		holder []resources.Holder
		free   bool // a Reserve of ["phone"] succeeds
	}{
		{"nothing", phone, nil, 0, 0, 0, []resources.Holder{}, true},
		{"an underway ship", phone, []ships.Ship{hold("a", ships.StatusUnderway, "phone")}, 0, 0, 1,
			[]resources.Holder{{Kind: "ship", Session: "a"}}, false},
		{"a needs-you ship", phone, []ships.Ship{hold("a", ships.StatusNeedsYou, "phone")}, 0, 0, 1,
			[]resources.Holder{{Kind: "ship", Session: "a"}}, false},
		{"anchored and sailing-off ships hold nothing", phone,
			[]ships.Ship{hold("a", ships.StatusAnchored, "phone"), hold("b", ships.StatusSailingOff, "phone")}, 0, 0, 0,
			[]resources.Holder{}, true},
		{"a ship that recorded no resource holds nothing", phone, []ships.Ship{hold("a", ships.StatusUnderway)}, 0, 0, 0,
			[]resources.Holder{}, true},
		{"a ship holding another resource", phone, []ships.Ship{hold("a", ships.StatusUnderway, "tablet")}, 0, 0, 0,
			[]resources.Holder{}, true},
		{"a lock", phone, nil, 1, 0, 1, []resources.Holder{{Kind: "lock", Label: "lock-0"}}, false},
		{"a reservation counts but has no holder", phone, nil, 0, 1, 1, []resources.Holder{}, false},
		{"capacity 2 with one ship leaves a unit", map[string]config.Resource{"phone": {Capacity: 2}},
			[]ships.Ship{hold("a", ships.StatusUnderway, "phone")}, 0, 0, 1,
			[]resources.Holder{{Kind: "ship", Session: "a"}}, true},
		{"capacity 2 with a ship and a lock is full", map[string]config.Resource{"phone": {Capacity: 2}},
			[]ships.Ship{hold("a", ships.StatusUnderway, "phone")}, 1, 0, 2,
			[]resources.Holder{{Kind: "ship", Session: "a"}, {Kind: "lock", Label: "lock-0"}}, false},
		{"capacity 2 with a ship and a reservation is full",
			map[string]config.Resource{"phone": {Capacity: 2}},
			[]ships.Ship{hold("a", ships.StatusUnderway, "phone")}, 0, 1, 2,
			[]resources.Holder{{Kind: "ship", Session: "a"}}, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			r := newRig(t, tc.defs)
			r.ships = tc.ships
			for i := 0; i < tc.locks; i++ {
				if _, err := r.set.Lock("phone", "lock-"+strconv.Itoa(i)); err != nil {
					t.Fatalf("lock %d refused", i)
				}
			}
			for i := 0; i < tc.reserv; i++ {
				if rel, waits := r.set.Reserve([]string{"phone"}); waits != nil || rel == nil {
					t.Fatalf("reservation %d refused: %v", i, waits)
				}
			}
			st := r.state(t, "phone")
			if st.InUse != tc.inUse || st.Capacity != tc.defs["phone"].Capacity {
				t.Errorf("in_use/capacity = %d/%d, want %d/%d", st.InUse, st.Capacity, tc.inUse, tc.defs["phone"].Capacity)
			}
			if !reflect.DeepEqual(st.Holders, tc.holder) {
				t.Errorf("holders = %+v, want %+v", st.Holders, tc.holder)
			}
			if st.Check != "none" {
				t.Errorf("check = %q, want none for a resource without a check", st.Check)
			}
			rel, waits := r.set.Reserve([]string{"phone"})
			if got := waits == nil; got != tc.free {
				t.Errorf("Reserve free = %v (waits %v), want %v", got, waits, tc.free)
			}
			if rel != nil {
				rel()
			}
		})
	}
}

// A ship that recorded a resource the config doesn't define holds nothing:
// there is nothing to count it against. The record survives, so if the config
// defines the name again the ship holds it again.
func TestAShipHoldingAResourceGoneFromTheConfigCountsAgainstNothing(t *testing.T) {
	r := newRig(t, map[string]config.Resource{"phone": {Capacity: 1}})
	r.ships = []ships.Ship{hold("a", ships.StatusUnderway, "tablet", "phone")}
	if got := r.set.State(); len(got) != 1 || got[0].Name != "phone" || got[0].InUse != 1 {
		t.Errorf("State = %+v, want only phone, in use by a", got)
	}
	r.cfg = &config.Config{Resources: map[string]config.Resource{"tablet": {Capacity: 1}}}
	if got := r.state(t, "tablet"); got.InUse != 1 {
		t.Errorf("tablet in use = %d, want 1: the ship recorded it", got.InUse)
	}
	r.cfg = &config.Config{}
	if got := r.set.State(); len(got) != 0 {
		t.Errorf("State with no resources = %+v, want none", got)
	}
}

func TestStateIsSortedByName(t *testing.T) {
	r := newRig(t, map[string]config.Resource{"b": {Capacity: 1}, "c": {Capacity: 1}, "a": {Capacity: 1}})
	var names []string
	for _, s := range r.set.State() {
		names = append(names, s.Name)
	}
	if !reflect.DeepEqual(names, []string{"a", "b", "c"}) {
		t.Errorf("names = %v, want [a b c]", names)
	}
}

// A set of resources is reserved whole or not at all, and says why each isn't ready.
func TestReserveTakesEveryNameOrNone(t *testing.T) {
	flag := filepath.Join(t.TempDir(), "present")
	r := newRig(t, map[string]config.Resource{
		"phone": {Capacity: 1}, "ipad": {Capacity: 1},
		"gone": {Capacity: 1, Check: []string{"test", "-f", flag}}})
	r.ships = []ships.Ship{hold("a", ships.StatusUnderway, "phone")}

	rel, waits := r.set.Reserve([]string{"ipad", "phone"})
	if rel != nil || !reflect.DeepEqual(waits, []resources.Wait{{Name: "phone", Why: "busy"}}) {
		t.Fatalf("Reserve = %v, %v, want only phone busy", rel != nil, waits)
	}
	if got := r.state(t, "ipad").InUse; got != 0 {
		t.Errorf("ipad in use = %d after a refused Reserve, want 0", got)
	}

	// Until the first check finishes the state is pending; a missing one waits too.
	_, waits = r.set.Reserve([]string{"gone"})
	if !reflect.DeepEqual(waits, []resources.Wait{{Name: "gone", Why: "pending"}}) {
		t.Errorf("before the first check: %v, want gone pending", waits)
	}
	r.set.CheckNow()
	_, waits = r.set.Reserve([]string{"gone", "phone"})
	if !reflect.DeepEqual(waits, []resources.Wait{{Name: "gone", Why: "missing"}, {Name: "phone", Why: "busy"}}) {
		t.Errorf("with the check failing: %v, want gone missing and phone busy", waits)
	}
	if err := os.WriteFile(flag, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	r.set.CheckNow()
	rel, waits = r.set.Reserve([]string{"gone", "ipad"})
	if rel == nil || waits != nil {
		t.Fatalf("with the check passing: %v, %v, want a reservation", rel != nil, waits)
	}
	if r.state(t, "gone").InUse != 1 || r.state(t, "ipad").InUse != 1 {
		t.Errorf("reserved units not counted: %+v", r.set.State())
	}
	rel()
	rel() // idempotent
	if r.state(t, "gone").InUse != 0 || r.state(t, "ipad").InUse != 0 {
		t.Errorf("units still held after release: %+v", r.set.State())
	}
}

// Locks (u2 calls these): non-blocking, don't need the check to pass, release
// makes the unit free again and announces the change.
func TestLocks(t *testing.T) {
	r := newRig(t, map[string]config.Resource{"phone": {Capacity: 1, Check: []string{"false"}}})
	r.set.CheckNow() // the check fails; a lock doesn't care
	rel, err := r.set.Lock("phone", "install")
	if err != nil {
		t.Fatalf("Lock refused although a unit is free and only the check fails: %v", err)
	}
	if _, err := r.set.Lock("phone", "second"); !errors.Is(err, resources.ErrBusy) {
		t.Errorf("a second Lock = %v, want ErrBusy", err)
	}
	if _, err := r.set.Lock("nope", "x"); !errors.Is(err, resources.ErrUnknown) {
		t.Errorf("Lock of an undefined resource = %v, want ErrUnknown", err)
	}
	if _, waits := r.set.Reserve([]string{"phone"}); len(waits) == 0 {
		t.Error("Reserve passed while a lock holds the unit")
	}

	changed := r.set.Changed()
	before := r.changes.Load()
	rel()
	select {
	case <-changed:
	case <-time.After(time.Second):
		t.Fatal("Changed was not closed by the release")
	}
	if r.changes.Load() == before {
		t.Error("OnChange did not run on release")
	}
	rel() // idempotent: the unit isn't freed twice
	if _, err := r.set.Lock("phone", "third"); err != nil {
		t.Errorf("Lock after the release: %v", err)
	}
	if _, err := r.set.Lock("phone", "fourth"); !errors.Is(err, resources.ErrBusy) {
		t.Errorf("after a double release, Lock = %v, want ErrBusy: the second release freed a unit", err)
	}
}

func TestDroppingAReservationAnnouncesTheChange(t *testing.T) {
	r := newRig(t, map[string]config.Resource{"phone": {Capacity: 1}})
	rel, _ := r.set.Reserve([]string{"phone"})
	changed := r.set.Changed()
	rel()
	select {
	case <-changed:
	case <-time.After(time.Second):
		t.Fatal("Changed was not closed when the reservation dropped")
	}
}

// AC 2, the package half: a check's exit code is the state, and a change of
// state announces itself once.
func TestCheckStates(t *testing.T) {
	flag := filepath.Join(t.TempDir(), "present")
	r := newRig(t, map[string]config.Resource{"phone": {Capacity: 1, Check: []string{"test", "-f", flag}}})
	if got := r.state(t, "phone").Check; got != "pending" {
		t.Errorf("before the first check = %q, want pending", got)
	}
	r.set.CheckNow()
	if got := r.state(t, "phone").Check; got != "missing" {
		t.Errorf("exit 1 = %q, want missing", got)
	}
	n := r.changes.Load()
	r.set.CheckNow()
	if r.changes.Load() != n {
		t.Error("OnChange ran although the check state did not change")
	}
	if err := os.WriteFile(flag, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	r.set.CheckNow()
	if got := r.state(t, "phone").Check; got != "ok" {
		t.Errorf("exit 0 = %q, want ok", got)
	}
	if r.changes.Load() != n+1 {
		t.Errorf("OnChange ran %d times for the change to ok, want 1", r.changes.Load()-n)
	}
}

// The check is an argv with no shell: a metacharacter is just an argument.
// A check that can't start is missing.
func TestCheckRunsWithoutAShell(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "ran")
	r := newRig(t, map[string]config.Resource{
		"a": {Capacity: 1, Check: []string{"true", "; touch " + marker}},
		"b": {Capacity: 1, Check: []string{"/no/such/program"}}})
	r.set.CheckNow()
	if got := r.state(t, "a").Check; got != "ok" {
		t.Errorf("a = %q, want ok", got)
	}
	if _, err := os.Stat(marker); err == nil {
		t.Error("the argument was run by a shell")
	}
	if got := r.state(t, "b").Check; got != "missing" {
		t.Errorf("a check that can't start = %q, want missing", got)
	}
}

// A check that runs past the timeout is missing, within the timeout, and its
// whole process group is gone: the children of sh -c included.
func TestCheckTimeoutKillsTheProcessGroup(t *testing.T) {
	dir := t.TempDir()
	pids := filepath.Join(dir, "pids")
	script := `echo $$ >> ` + pids + `; sleep 60 & echo $! >> ` + pids + `; wait`
	r := newRig(t, map[string]config.Resource{"phone": {Capacity: 1, Check: []string{"sh", "-c", script}}})

	start := time.Now()
	r.set.CheckNow()
	if took := time.Since(start); took > 3*time.Second {
		t.Errorf("CheckNow took %v with a 300ms timeout", took)
	}
	if got := r.state(t, "phone").Check; got != "missing" {
		t.Errorf("a check past the timeout = %q, want missing", got)
	}
	b, err := os.ReadFile(pids)
	if err != nil {
		t.Fatal(err)
	}
	fields := strings.Fields(string(b))
	if len(fields) != 2 {
		t.Fatalf("pids file = %q, want the shell's pid and its child's", b)
	}
	for _, f := range fields {
		pid, _ := strconv.Atoi(f)
		deadline := time.Now().Add(2 * time.Second)
		for syscall.Kill(pid, 0) == nil {
			if time.Now().After(deadline) {
				t.Fatalf("process %d is still running after the timeout", pid)
			}
			time.Sleep(20 * time.Millisecond)
		}
	}
}

// A ship that stops holding a unit wakes a Changed waiter once the caller says
// the ships changed; this is how a waiting lock learns of a freed unit.
func TestWakeAnnouncesAShipThatStoppedHoldingAUnit(t *testing.T) {
	r := newRig(t, map[string]config.Resource{"phone": {Capacity: 1}})
	r.ships = []ships.Ship{hold("a", ships.StatusUnderway, "phone")}
	if _, err := r.set.Lock("phone", "install"); !errors.Is(err, resources.ErrBusy) {
		t.Fatalf("Lock = %v, want ErrBusy while the ship holds the unit", err)
	}
	changed := r.set.Changed()
	r.ships[0].Status = ships.StatusAnchored
	r.set.Wake()
	select {
	case <-changed:
	case <-time.After(time.Second):
		t.Fatal("Changed was not closed by Wake")
	}
	if _, err := r.set.Lock("phone", "install"); err != nil {
		t.Errorf("Lock after the ship anchored: %v", err)
	}
}
