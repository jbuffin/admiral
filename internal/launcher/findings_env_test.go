package launcher

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// ADMIRAL_FINDINGS names a ship's findings file, and the prompts say how to use it.

func TestAShipsFindingsFileIsNamedByAnEnvVarBesideItsReportAndAnOldOneIsCleared(t *testing.T) {
	home := t.TempDir()
	stale := filepath.Join(home, "reports", "dory.md")
	if err := os.MkdirAll(filepath.Dir(stale), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(stale, []byte("last voyage's findings"), 0o600); err != nil {
		t.Fatal(err)
	}
	env := map[string]string{"ADMIRAL_FINDINGS": "/fleet/says/otherwise"}
	if err := (&Launcher{Home: home}).shipContract(env, "dory", "work"); err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(home, "reports", "dory.md"); env["ADMIRAL_FINDINGS"] != want {
		t.Errorf("ADMIRAL_FINDINGS = %q, want %q", env["ADMIRAL_FINDINGS"], want)
	}
	if _, err := os.Stat(stale); !os.IsNotExist(err) {
		t.Errorf("the findings file left under the name survived the launch (%v)", err)
	}
}

func TestSailingOffDeletesAShipsFindingsFileWithItsReport(t *testing.T) {
	home := t.TempDir()
	files := []string{filepath.Join(home, "reports", "dory.json"), filepath.Join(home, "reports", "dory.md")}
	for _, p := range files {
		if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	RemoveShipFiles(home, "dory")
	for _, p := range files {
		if _, err := os.Stat(p); !os.IsNotExist(err) {
			t.Errorf("%s survived RemoveShipFiles (%v)", p, err)
		}
	}
}

func TestACrewBoatDoesNotKeepItsShipsFindingsPath(t *testing.T) {
	p := newPinRig(t, nil)
	ship, err := p.l.Launch(p.justRequest())
	if err != nil {
		t.Fatalf("Launch: %v", err)
	}
	p.allowGates()
	// The session holds the ship's own variables; the boat must not inherit this one.
	if out, err := execTmux(p.l, "set-environment", "-t", "="+ship.Session+":", "ADMIRAL_FINDINGS", "/ship/findings.md"); err != nil {
		t.Fatalf("set-environment: %v: %s", err, out)
	}
	if err := p.l.LaunchCrew(CrewRequest{Session: ship.Session, Name: "boat", Harness: "claude", Dir: p.work, Prompt: "go"}); err != nil {
		t.Fatalf("LaunchCrew: %v", err)
	}
	if v, ok := p.recorded(t, "claude")["ADMIRAL_FINDINGS"]; ok {
		t.Errorf("the boat has ADMIRAL_FINDINGS=%q from its ship's session", v)
	}
}

func execTmux(l *Launcher, args ...string) ([]byte, error) {
	return exec.Command(l.Tmux, append([]string{"-S", l.Socket}, args...)...).CombinedOutput()
}

func TestAScoutIsToldToWriteItsFindingsToTheFileTheVariableNamesAndThatNoneMeansFailed(t *testing.T) {
	p := newPinRig(t, nil)
	if _, err := p.l.Launch(p.scoutRequest(p.work)); err != nil {
		t.Fatalf("Launch: %v", err)
	}
	prompt := p.lastArg(t)
	for _, want := range []string{"$admiral_findings", "markdown", "file-writing tool", "echo $admiral_findings", "failed"} {
		if !strings.Contains(strings.ToLower(prompt), want) {
			t.Errorf("a scout's prompt = %q, want it to contain %q", prompt, want)
		}
	}
	if strings.Contains(prompt, "write what you found to your report") {
		t.Errorf("a scout's prompt still sends its findings to the report: %q", prompt)
	}
}

func TestAQuickShipsClosingStepsSayItMayWriteFindings(t *testing.T) {
	p := newPinRig(t, nil)
	if _, err := p.l.Launch(p.chatLaunch()); err != nil {
		t.Fatalf("Launch: %v", err)
	}
	if prompt := p.lastArg(t); !strings.Contains(prompt, "$ADMIRAL_FINDINGS") {
		t.Errorf("a quick ship's prompt = %q, want its closing steps to name $ADMIRAL_FINDINGS", prompt)
	}
}
