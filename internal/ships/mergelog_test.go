package ships

import (
	"errors"
	"io"
	"net/http"
	"os"
	"strings"
	"testing"
)

func TestNoControlLeavesNoControlCharacterForTheLog(t *testing.T) {
	got := strings.Map(noControl, "a\rb\x00c\x1b[31md\te")
	if strings.ContainsAny(got, "\r\x00\x1b\t") {
		t.Errorf("mapped = %q, want no control character", got)
	}
}

// Only the process's stderr shows the log line, so the test swaps it for a pipe while the merge runs.
func TestAFailedGhStackMergeIsLoggedOnOneLineNamingTheShipAndTheStack(t *testing.T) {
	r := newMergeRig(t, stackedDetails(2), layerURL(7), layerURL(9))
	r.g.stackMerr = errors.New("pull request #9 cannot be merged yet\r\n#7 below it is not mergeable\x1b[0m")
	pr, pw, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	saved := os.Stderr
	os.Stderr = pw
	rec := r.merge("{}")
	os.Stderr = saved
	pw.Close()
	logged, _ := io.ReadAll(pr)
	if rec.Code != http.StatusBadGateway {
		t.Fatalf("POST merge = %d, want 502", rec.Code)
	}
	lines := strings.Split(strings.TrimSpace(string(logged)), "\n")
	if len(lines) != 1 || !strings.Contains(lines[0], "ship "+r.session+": merging stack 4 failed: pull request #9 cannot be merged yet #7 below it is not mergeable") || strings.ContainsAny(lines[0], "\r\x1b") {
		t.Errorf("log = %q, want one line naming the ship and stack 4 with gh's message", logged)
	}
}
