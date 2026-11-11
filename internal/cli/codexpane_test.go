package cli

import (
	"errors"
	"io"
	"strings"
	"testing"
)

func TestCodexPaneNeedsTheCommandAfterDashDash(t *testing.T) {
	io0 := IO{In: strings.NewReader(""), Out: io.Discard, Err: io.Discard}
	for _, args := range [][]string{
		{},
		{"--socket", "/tmp/admiral-1/a.sock"},
		{"--socket", "/tmp/admiral-1/a.sock", "--"},
		{"--", "/bin/codex"},
	} {
		err := CodexPane(args, io0)
		var u *UsageError
		if !errors.As(err, &u) {
			t.Errorf("CodexPane(%q) = %v, want a usage error", args, err)
		}
	}
}
