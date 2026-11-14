package routines_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/jbuffin/admiral/internal/routines"
)

func (r *rig) ordersFile() string {
	return filepath.Join(r.home, "routines", "work", "standing-orders.json")
}

func (r *rig) setChecks(checks ...string) {
	r.t.Helper()
	if checks == nil {
		checks = []string{}
	}
	if _, err := r.store.SetStandingOrders("work", routines.StandingOrdersPatch{Checks: &checks}); err != nil {
		r.t.Fatal(err)
	}
}

func (r *rig) writeBad() {
	r.t.Helper()
	if err := os.WriteFile(r.ordersFile()+".bad", []byte("{not json"), 0o600); err != nil {
		r.t.Fatal(err)
	}
}

func TestACorruptionThatLeavesOnlyABadFileCountsAsHavingChecks(t *testing.T) {
	r := newRig(t)
	r.setChecks()
	if err := os.WriteFile(r.ordersFile(), []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := r.store.StandingOrders("work"); err != nil { // a read sets the corrupt file aside
		t.Fatal(err)
	}
	if _, err := os.Stat(r.ordersFile()); err == nil {
		t.Fatal("the corrupt file is still in place, want it set aside as .bad")
	}
	if !r.store.HasChecks("work") {
		t.Error("HasChecks = false after a corruption left only a .bad file, want true (fail closed)")
	}
}

func TestABadFileBesideAValidCurrentFileWithoutChecksDoesNotCountAsHavingChecks(t *testing.T) {
	r := newRig(t)
	r.setChecks()
	r.writeBad()
	if r.store.HasChecks("work") {
		t.Error("HasChecks = true for a valid current file with no Checks beside an old .bad file, want false")
	}
}

func TestABadFileBesideAValidCurrentFileWithChecksCountsAsHavingChecks(t *testing.T) {
	r := newRig(t)
	r.setChecks("Always run make check before a PR.")
	r.writeBad()
	if !r.store.HasChecks("work") {
		t.Error("HasChecks = false for a valid current file that holds Checks, want true")
	}
}

func TestACorruptCurrentFileCountsAsHavingChecksBeforeAnyReadSetsItAside(t *testing.T) {
	r := newRig(t)
	r.setChecks()
	if err := os.WriteFile(r.ordersFile(), []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	if !r.store.HasChecks("work") {
		t.Error("HasChecks = false for a corrupt current file, want true (fail closed)")
	}
}
