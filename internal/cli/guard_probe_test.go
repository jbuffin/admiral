package cli

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jbuffin/admiral/internal/probes"
)

func probeGuard(t *testing.T, command string) error {
	t.Helper()
	payload := `{"tool_name":"Bash","tool_input":{"command":` + string(mustRaw(command)) + `},"cwd":"/"}`
	return Guard([]string{"pretooluse"}, IO{In: strings.NewReader(payload), Out: &bytes.Buffer{}, Err: &bytes.Buffer{}})
}

func probeEnv(t *testing.T, nonce string) (dir string) {
	t.Helper()
	dir = t.TempDir()
	t.Setenv("ADMIRAL_AUTOPILOT", "1")
	if nonce != "" {
		t.Setenv(probes.NonceEnv, nonce)
		t.Setenv(probes.DirEnv, dir)
	}
	return dir
}

func TestGuardDeniesTheProbeCommandAndRecordsItForThatNonce(t *testing.T) {
	dir := probeEnv(t, "n0nce1234")
	err := probeGuard(t, probes.Command(dir, "n0nce1234"))
	var denied *GuardDenied
	if !errors.As(err, &denied) {
		t.Fatalf("Guard = %v, want a denial of the probe's command", err)
	}
	if _, err := os.Stat(probes.DeniedPath(dir, "n0nce1234")); err != nil {
		t.Errorf("no denial record for the nonce: %v", err)
	}
}

func TestGuardLetsOtherProbeLookalikesThrough(t *testing.T) {
	dir := probeEnv(t, "n0nce1234")
	for name, cmd := range map[string]string{
		"another nonce":    probes.Command(dir, "other5678"),
		"another command":  "touch " + filepath.Join(dir, "other-n0nce1234"),
		"an added command": probes.Command(dir, "n0nce1234") + "; echo hi",
	} {
		if err := probeGuard(t, cmd); err != nil {
			t.Errorf("%s: Guard denied %q although only the exact probe command is guarded", name, cmd)
		}
	}
	if m, _ := filepath.Glob(filepath.Join(dir, "denied-*")); len(m) != 0 {
		t.Errorf("a denial was recorded for a command the guard allowed: %v", m)
	}
}

func TestGuardRuleIsDeadWithoutTheProbeNonce(t *testing.T) {
	dir := probeEnv(t, "")
	if err := probeGuard(t, probes.Command(dir, "n0nce1234")); err != nil {
		t.Errorf("Guard denied the probe command with no probe env: %v", err)
	}
}

func TestGuardProbeDenialLeavesNoOutcome(t *testing.T) {
	dir := probeEnv(t, "n0nce1234")
	home := t.TempDir()
	t.Setenv("ADMIRAL_SHIP", "saltpaw-ii")
	t.Setenv("ADMIRAL_HOME", home)
	err := probeGuard(t, probes.Command(dir, "n0nce1234"))
	var denied *GuardDenied
	if !errors.As(err, &denied) {
		t.Fatalf("Guard = %v, want a denial of the probe's command", err)
	}
	if m, _ := filepath.Glob(filepath.Join(home, "outcomes", "*")); len(m) != 0 {
		t.Errorf("the probe's denial wrote outcomes %v: it is not a ship's refusal", m)
	}
}
