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
	dir := probeEnv(t, "0123456789abcdef")
	err := probeGuard(t, probes.Command(dir, "0123456789abcdef"))
	var denied *GuardDenied
	if !errors.As(err, &denied) {
		t.Fatalf("Guard = %v, want a denial of the probe's command", err)
	}
	if _, err := os.Stat(probes.DeniedPath(dir, "0123456789abcdef")); err != nil {
		t.Errorf("no denial record for the nonce: %v", err)
	}
}

func TestGuardLetsOtherProbeLookalikesThrough(t *testing.T) {
	dir := probeEnv(t, "0123456789abcdef")
	for name, cmd := range map[string]string{
		"another nonce":    probes.Command(dir, "fedcba9876543210"),
		"another command":  "touch " + filepath.Join(dir, "other-0123456789abcdef"),
		"an added command": probes.Command(dir, "0123456789abcdef") + "; echo hi",
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
	if err := probeGuard(t, probes.Command(dir, "0123456789abcdef")); err != nil {
		t.Errorf("Guard denied the probe command with no probe env: %v", err)
	}
}

func TestGuardProbeDenialLeavesNoOutcome(t *testing.T) {
	dir := probeEnv(t, "0123456789abcdef")
	home := t.TempDir()
	t.Setenv("ADMIRAL_SHIP", "saltpaw-ii")
	t.Setenv("ADMIRAL_HOME", home)
	err := probeGuard(t, probes.Command(dir, "0123456789abcdef"))
	var denied *GuardDenied
	if !errors.As(err, &denied) {
		t.Fatalf("Guard = %v, want a denial of the probe's command", err)
	}
	if m, _ := filepath.Glob(filepath.Join(home, "outcomes", "*")); len(m) != 0 {
		t.Errorf("the probe's denial wrote outcomes %v: it is not a ship's refusal", m)
	}
}

func TestGuardNotesEveryBashCallWhileTheProbeIsLive(t *testing.T) {
	dir := probeEnv(t, "0123456789abcdef")
	if err := probeGuard(t, "echo hi"); err != nil {
		t.Fatalf("Guard denied an unrelated command: %v", err)
	}
	if _, err := os.Stat(probes.SeenPath(dir, "0123456789abcdef")); err != nil {
		t.Errorf("no seen record for a Bash call: %v", err)
	}
	if _, err := os.Stat(probes.DeniedPath(dir, "0123456789abcdef")); err == nil {
		t.Error("an unrelated command was recorded as the denied one")
	}
}

func TestGuardProbeRuleIgnoresAMalformedNonceOrARelativeDir(t *testing.T) {
	outside := t.TempDir()
	t.Setenv("ADMIRAL_AUTOPILOT", "1")
	for name, c := range map[string]struct{ nonce, dir string }{
		"a nonce that climbs out": {"x/../../f", filepath.Join(outside, "run")},
		"an uppercase nonce":      {"0123456789ABCDEF", outside},
		"a short nonce":           {"n0nce1234", outside},
		"a relative dir":          {"0123456789abcdef", "run"},
	} {
		t.Setenv(probes.NonceEnv, c.nonce)
		t.Setenv(probes.DirEnv, c.dir)
		if err := probeGuard(t, probes.Command(c.dir, c.nonce)); err != nil {
			t.Errorf("%s: Guard denied the command", name)
		}
	}
	if m, _ := filepath.Glob(filepath.Join(outside, "*")); len(m) != 0 {
		t.Errorf("the guard wrote files for an invalid probe env: %v", m)
	}
	if _, err := os.Stat(filepath.Join(outside, "f")); err == nil {
		t.Error("a nonce climbed out of the probe's directory")
	}
}
