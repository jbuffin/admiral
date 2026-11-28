package probes_test

import (
	"os/exec"
	"strings"
	"testing"
)

// writeExe writes an executable fake through a child process. Written in-process, the file's
// write fd is open in this process while a parallel test forks, the fork's child inherits it
// until its own exec, and executing the fake then fails on Linux with "text file busy".
func writeExe(t *testing.T, path, body string) {
	t.Helper()
	cmd := exec.Command("sh", "-c", `cat > "$1" && chmod 755 "$1"`, "sh", path)
	cmd.Stdin = strings.NewReader(body)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("write %s: %v: %s", path, err, out)
	}
}
