package cli

import (
	"encoding/json"
	"errors"
	"testing"
)

func bashPayload(t *testing.T, cmd, cwd string) string {
	t.Helper()
	b, err := json.Marshal(map[string]any{"hook_event_name": "PreToolUse", "tool_name": "Bash", "tool_input": bashInput(cmd), "cwd": cwd})
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// The rule a scout's refusal to unset or override its own mode variables carries; it differs from scout_env,
// which is a checkout path the guard cannot read.
const scoutEnvOverride = "scout_env_override"

var scoutEnvOverrideCommands = []struct{ name, cmd string }{
	{"env -u scout", `env -u ADMIRAL_SCOUT admiral crew work claude "x"`},
	{"env --unset scout", `env --unset=ADMIRAL_SCOUT admiral crew work claude "x"`},
	{"env -u autopilot", `env -u ADMIRAL_AUTOPILOT admiral crew work claude "x"`},
	{"env -i", `env -i PATH=$PATH admiral crew work claude "x"`},
	{"unset scout", `unset ADMIRAL_SCOUT`},
	{"unset autopilot", `unset ADMIRAL_AUTOPILOT`},
	{"unset then crew", `unset ADMIRAL_SCOUT && admiral crew work claude "x"`},
	{"empty prefix assignment", `ADMIRAL_SCOUT= admiral crew work claude "x"`},
	{"prefix assignment to another checkout", `ADMIRAL_SCOUT=/tmp admiral crew work claude "x"`},
	{"autopilot prefix assignment", `ADMIRAL_AUTOPILOT=0 admiral crew work claude "x"`},
	{"export autopilot zero", `export ADMIRAL_AUTOPILOT=0`},
	{"export scout", `export ADMIRAL_SCOUT=/tmp`},
	{"env assignment", `env ADMIRAL_AUTOPILOT=0 admiral crew work claude "x"`},
	{"inside bash -c", `bash -c 'unset ADMIRAL_SCOUT; admiral crew work claude x'`},
	{"after a pipe", `echo x | ADMIRAL_SCOUT= admiral crew work claude x`},
}

func TestScoutModeRefusesAShellCommandThatUnsetsOrOverridesItsModeVariables(t *testing.T) {
	r := newScoutRig(t)
	for _, c := range scoutEnvOverrideCommands {
		t.Run(c.name, func(t *testing.T) {
			err, out, errb := r.run(t, "1", "Bash", bashInput(c.cmd), "")
			var denied *GuardDenied
			if !errors.As(err, &denied) {
				t.Fatalf("Guard = %v for %q, want a refusal", err, c.cmd)
			}
			if denied.Rule != scoutEnvOverride {
				t.Errorf("rule = %q for %q, want %q", denied.Rule, c.cmd, scoutEnvOverride)
			}
			if out != "" || errb != "" {
				t.Errorf("stdout %q, stderr %q: Guard prints nothing itself, main prints the reason", out, errb)
			}
		})
	}
}

func TestScoutModeLeavesOtherEnvironmentUseAlone(t *testing.T) {
	r := newScoutRig(t)
	for _, cmd := range []string{
		`env FOO=1 ls`,
		`export FOO=bar`,
		`unset FOO`,
		`ADMIRAL_HOME=/tmp/h admiral crew work claude x`,
		`echo ADMIRAL_SCOUT`,
	} {
		if err, _, _ := r.run(t, "1", "Bash", bashInput(cmd), ""); err != nil {
			t.Errorf("Guard = %v for %q, want it allowed: only the scout's mode variables are protected", err, cmd)
		}
	}
}

func TestOutsideScoutModeTheEnvOverridesAreNotRefused(t *testing.T) {
	r := newScoutRig(t)
	for _, c := range scoutEnvOverrideCommands {
		t.Run(c.name, func(t *testing.T) {
			t.Setenv(scoutEnv, "")
			payload := bashPayload(t, c.cmd, r.co)
			err, _, _ := runGuardTest(t, payload, "")
			var denied *GuardDenied
			if errors.As(err, &denied) && denied.Rule == scoutEnvOverride {
				t.Errorf("Guard refused %q with %s with no scout mode on", c.cmd, scoutEnvOverride)
			}
		})
	}
}
