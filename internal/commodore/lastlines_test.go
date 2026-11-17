package commodore_test

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/jbuffin/admiral/internal/commodore"
)

func TestLastLinesKeepsTheTailOfAnInstallersOutput(t *testing.T) {
	var lines []string
	for i := 1; i <= 25; i++ {
		lines = append(lines, fmt.Sprintf("line %d", i))
	}
	long := strings.Join(lines, "\n") + "\n"

	if got, want := commodore.LastLines(long, 20), strings.Join(lines[5:], "\n"); got != want {
		t.Errorf("LastLines(25 lines, 20) = %q, want lines 6 to 25 without the trailing newline", got)
	}
	if got := commodore.LastLines("a\nb\n", 20); got != "a\nb" {
		t.Errorf("LastLines(2 lines, 20) = %q, want %q", got, "a\nb")
	}
	if got := commodore.LastLines("", 20); got != "" {
		t.Errorf("LastLines of no output = %q, want an empty string", got)
	}
}

func TestInstallTimeoutIsFiveMinutes(t *testing.T) {
	if commodore.InstallTimeout != 5*time.Minute {
		t.Errorf("InstallTimeout = %s, want 5m: a Runner with no timeout fails every command at once", commodore.InstallTimeout)
	}
}
