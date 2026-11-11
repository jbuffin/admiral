package e2e

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// The startup line carries the plain LAN URL, a third line points at pairing, and no token is printed or written.
func TestStartupLinesHaveNoToken(t *testing.T) {
	t.Parallel()
	home, port := newHome(t), freePort(t)
	in := startAdmiral(t, home, "0.0.0.0:"+port)
	if in.LANIP == "" {
		lanUnavailable(t, "no LAN IPv4 address: second line was %q", in.LANLine)
		return
	}
	got := []string{"Admiral is running at " + in.LocalURL + "/", in.LANLine, in.PairLine}
	if !regexp.MustCompile(`^Admiral is running at http://127\.0\.0\.1:` + port + `/$`).MatchString(got[0]) {
		t.Errorf("line 1: %q", got[0])
	}
	if !regexp.MustCompile(`^On other devices: http://[0-9.]+:` + port + `/$`).MatchString(got[1]) {
		t.Errorf("line 2 must be the plain URL with no token: %q", got[1])
	}
	if !strings.Contains(got[2], "admiral pair") {
		t.Errorf("line 3 must name `admiral pair`: %q", got[2])
	}
	for _, l := range got {
		if strings.Contains(l, "token=") {
			t.Errorf("a startup line carries a token: %q", l)
		}
	}
	if _, err := os.Stat(filepath.Join(home, "token")); err == nil {
		t.Error("the shared token file was created")
	}
}
