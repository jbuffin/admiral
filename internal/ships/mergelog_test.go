package ships

import (
	"strings"
	"testing"
)

func TestNoControlLeavesNoControlCharacterForTheLog(t *testing.T) {
	got := strings.Map(noControl, "a\rb\x00c\x1b[31md\te")
	if strings.ContainsAny(got, "\r\x00\x1b\t") {
		t.Errorf("mapped = %q, want no control character", got)
	}
}
