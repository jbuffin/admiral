package crewapi

import (
	"errors"
	"testing"
)

// FailSidecarWrites makes every sidecar write fail until the test ends.
func FailSidecarWrites(t *testing.T) {
	t.Helper()
	was := writeSidecar
	writeSidecar = func(string, string, Sidecar) error { return errors.New("the disk is full") }
	t.Cleanup(func() { writeSidecar = was })
}
