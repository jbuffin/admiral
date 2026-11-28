package ships

import (
	"bytes"
	"errors"
	"net/http"
	"strings"
	"sync"
	"testing"
)

func TestNoControlLeavesNoControlCharacterForTheLog(t *testing.T) {
	t.Parallel()
	got := strings.Map(noControl, "a\rb\x00c\x1b[31md\te")
	if strings.ContainsAny(got, "\r\x00\x1b\t") {
		t.Errorf("mapped = %q, want no control character", got)
	}
}

// lockedLog is a Service log the test reads while the service's goroutines may still write to it.
type lockedLog struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (l *lockedLog) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.buf.Write(p)
}

func (l *lockedLog) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.buf.String()
}

func TestAFailedGhStackMergeIsLoggedOnOneLineNamingTheShipAndTheStack(t *testing.T) {
	t.Parallel()
	r := newMergeRig(t, stackedDetails(2), layerURL(7), layerURL(9))
	r.g.stackMerr = errors.New("pull request #9 cannot be merged yet\r\n#7 below it is not mergeable\x1b[0m")
	log := &lockedLog{}
	r.s.Log = log
	rec := r.merge("{}")
	logged := log.String()
	if rec.Code != http.StatusBadGateway {
		t.Fatalf("POST merge = %d, want 502", rec.Code)
	}
	lines := strings.Split(strings.TrimSpace(logged), "\n")
	if len(lines) != 1 || !strings.Contains(lines[0], "ship "+r.session+": merging stack 4 failed: pull request #9 cannot be merged yet #7 below it is not mergeable") || strings.ContainsAny(lines[0], "\r\x1b") {
		t.Errorf("log = %q, want one line naming the ship and stack 4 with gh's message", logged)
	}
}
