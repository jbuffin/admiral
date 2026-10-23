package lockroute

import (
	"strings"
	"testing"
)

func TestCleanLabel(t *testing.T) {
	if got := CleanLabel("admiral lock (pid 7): a\x00\n\x1b[31mb\xff"); got != "admiral lock (pid 7): a[31mb" {
		t.Errorf("CleanLabel kept control or invalid bytes: %q", got)
	}
	if got := CleanLabel(strings.Repeat("é", 100)); len([]rune(got)) != 64 {
		t.Errorf("CleanLabel kept %d characters, want 64", len([]rune(got)))
	}
}
