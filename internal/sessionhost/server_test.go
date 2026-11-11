package sessionhost

import (
	"reflect"
	"strings"
	"testing"
)

func TestBatches(t *testing.T) {
	// An argument ending in ; would be read as a command separator.
	got := batches([][]string{{"set-option", "-t", "x", "o", "a;"}, {"set-option", "-t", "x", "p", "b"}})
	want := [][]string{{"set-option", "-t", "x", "o", `a\;`, ";", "set-option", "-t", "x", "p", "b"}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("batches = %q, want %q", got, want)
	}

	// Commands split between batches once one would pass maxCommand.
	big := []string{"set-environment", "k", strings.Repeat("v", maxCommand-100)}
	got = batches([][]string{big, big, big})
	if len(got) != 3 {
		t.Errorf("batches split into %d, want 3", len(got))
	}
}
