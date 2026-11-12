//go:build packaging

package e2e

import (
	"bytes"
	"os"
	"testing"
)

// The seam is compiled in by `-tags e2e` (make build); a release build must hold neither the code nor the variable name.
func TestPackagingPushTestSeamIsInTheE2EBuildAndNotInTheRelease(t *testing.T) {
	const seam = "ADMIRAL_PUSH_TEST_ROOTS"
	built, err := os.ReadFile(binaryPath(t))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(built, []byte(seam)) {
		t.Fatalf("the e2e build (bin/admiral) does not contain %s: without it the fake push service is unreachable, and the check on the release below would pass for nothing", seam)
	}
	rel, err := os.ReadFile(theRelease(t).bin)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(rel, []byte(seam)) {
		t.Fatalf("the release binary contains %s: a release must not honour a variable that makes it trust other roots", seam)
	}
}
