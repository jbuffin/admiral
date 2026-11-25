package crewapi

import (
	"errors"
	"testing"
	"time"
)

// FailSidecarWrites makes every sidecar write fail until the test ends.
func FailSidecarWrites(t *testing.T) {
	t.Helper()
	was := writeSidecar
	writeSidecar = func(string, string, Sidecar) error { return errors.New("the disk is full") }
	t.Cleanup(func() { writeSidecar = was })
}

// HoldWrites takes the writers' lock, so a request that reaches its write waits there until release.
func HoldWrites(t *testing.T) (release func()) {
	t.Helper()
	writeMu.Lock()
	var done bool
	release = func() {
		if !done {
			done = true
			writeMu.Unlock()
		}
	}
	t.Cleanup(release)
	return release
}

// StoreKeepsASetStateOverAnOlderCheck reports each member's state after a check that began before a set of "a" finished with both members available.
func StoreKeepsASetStateOverAnOlderCheck() map[string]string {
	var u updateStore
	began := u.begin()
	u.set("work", "a", updateNone)
	return u.put("work", time.Time{}, map[string]string{"a": updateAvailable, "b": updateAvailable}, began)
}

// StoreTakesALaterCheckOverAnEarlierSet is the same, with the check beginning after the set.
func StoreTakesALaterCheckOverAnEarlierSet() map[string]string {
	var u updateStore
	u.set("work", "a", updateNone)
	began := u.begin()
	return u.put("work", time.Time{}, map[string]string{"a": updateAvailable, "b": updateAvailable}, began)
}
