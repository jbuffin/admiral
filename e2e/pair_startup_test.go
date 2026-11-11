package e2e

import (
	"bufio"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"syscall"
	"testing"
	"time"
)

// The startup line carries the plain LAN URL, a third line points at pairing, and no token is printed or written.
func TestStartupLinesHaveNoToken(t *testing.T) {
	t.Parallel()
	home, port := newHome(t), freePort(t)
	cmd := exec.Command(binaryPath(t), "--listen", "0.0.0.0:"+port)
	cmd.Env = append(hermeticEnv(t), "ADMIRAL_HOME="+home)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() { _ = cmd.Wait(); close(done) }()
	t.Cleanup(func() { killTmux(filepath.Join(home, "tmux.sock")) })
	t.Cleanup(func() {
		_ = cmd.Process.Signal(syscall.SIGTERM)
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			_ = cmd.Process.Kill()
			<-done
		}
	})

	lines := make(chan string, 8)
	go func() {
		sc := bufio.NewScanner(stdout)
		for sc.Scan() {
			lines <- sc.Text()
		}
		close(lines)
	}()
	var got []string
	timeout := time.After(10 * time.Second)
	for len(got) < 3 {
		select {
		case l, ok := <-lines:
			if !ok {
				t.Fatalf("admiral exited before printing three lines (got %q)", got)
			}
			got = append(got, l)
		case <-timeout:
			if len(got) == 2 && !strings.HasPrefix(got[1], "On other devices: http://") {
				t.Skipf("no LAN address, so no pairing line: %q", got)
			}
			t.Fatalf("admiral printed %d of 3 lines within 10s: %q", len(got), got)
		}
	}
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
