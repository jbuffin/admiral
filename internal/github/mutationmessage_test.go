package github

import (
	"strings"
	"testing"
)

func TestAMutationMessageRedactsATokenAnEscapeSequenceSplits(t *testing.T) {
	got := mutationMessage("bad credentials fake-tok\x1b[0men-1 sent", "", "fake-token-1")
	if strings.Contains(got, "fake-tok") || strings.Contains(got, "\x1b") {
		t.Errorf("message = %q, want the split token gone and no escape left", got)
	}
}
