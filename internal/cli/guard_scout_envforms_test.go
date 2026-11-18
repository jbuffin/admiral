package cli

import (
	"errors"
	"testing"
)

const (
	scoutCrewShip = "scout_crew_ship"
	scoutRunner   = "scout_command_runner"
)

// Each form is one way a scout's shell could change a mode variable or reach another ship's crew.
var scoutEnvFormCommands = []struct{ name, cmd, rule string }{
	{"env -P operand skipped", `env -P /usr/bin -u ADMIRAL_SCOUT admiral crew work claude x`, scoutEnvOverride},
	{"env -C operand skipped", `env -C /tmp -u ADMIRAL_SCOUT admiral crew work claude x`, scoutEnvOverride},
	{"env --chdir operand skipped", `env --chdir /tmp -u ADMIRAL_AUTOPILOT admiral crew work claude x`, scoutEnvOverride},
	{"env -S string", `env -S 'env -u ADMIRAL_SCOUT admiral crew work claude x'`, scoutEnvOverride},
	{"env unknown flag", `env --frobnicate admiral crew work claude x`, scoutEnvOverride},
	{"env unknown short flag", `env -Z admiral crew work claude x`, scoutEnvOverride},
	{"for loop variable", `for ADMIRAL_SCOUT in /tmp; do admiral crew work claude x; done`, scoutEnvOverride},
	{"select loop variable", `select TMUX in a b; do break; done`, scoutEnvOverride},
	{"c-style loop", `for ((ADMIRAL_AUTOPILOT=0; ; )); do break; done`, scoutEnvOverride},
	{"read", `read ADMIRAL_SCOUT <<< /tmp`, scoutEnvOverride},
	{"read -r", `read -r ADMIRAL_AUTOPILOT <<< 0`, scoutEnvOverride},
	{"mapfile", `mapfile TMUX_PANE < /dev/null`, scoutEnvOverride},
	{"readarray", `readarray -t ADMIRAL_SHIP < /dev/null`, scoutEnvOverride},
	{"getopts", `getopts ab ADMIRAL_SCOUT -a`, scoutEnvOverride},
	{"printf -v", `printf -v ADMIRAL_SCOUT %s /tmp`, scoutEnvOverride},
	{"printf -v joined", `printf -vTMUX %s x`, scoutEnvOverride},
	{"arithmetic command", `(( ADMIRAL_AUTOPILOT=0 ))`, scoutEnvOverride},
	{"arithmetic expansion", `echo $(( ADMIRAL_AUTOPILOT = 0 ))`, scoutEnvOverride},
	{"let", `let ADMIRAL_AUTOPILOT=0`, scoutEnvOverride},
	{"exec -c", `exec -c admiral crew work claude x`, scoutEnvOverride},
	{"exec cluster", `exec -cl admiral crew work claude x`, scoutEnvOverride},
	{"fish -c", `fish -c 'env -u ADMIRAL_SCOUT admiral crew work claude x'`, scoutEnvOverride},
	{"ADMIRAL_SHIP prefix", `ADMIRAL_SHIP=other admiral crew work claude x`, scoutEnvOverride},
	{"TMUX unset", `unset TMUX`, scoutEnvOverride},
	{"TMUX_PANE export", `export TMUX_PANE=%1`, scoutEnvOverride},
	{"env TMUX assignment", `env TMUX=/tmp/x admiral crew work claude x`, scoutEnvOverride},
	{"crew --ship", `admiral crew work claude x --ship other`, scoutCrewShip},
	{"crew --ship=", `admiral crew --ship=other list`, scoutCrewShip},
	{"crew through env", `env FOO=1 admiral crew list --ship other`, scoutCrewShip},
}

var scoutRunnerPrograms = []string{"tmux", "screen", "at", "batch", "launchctl", "crontab", "osascript", "script"}

func TestScoutModeRefusesTheOtherWaysToChangeAModeVariableOrReachAnotherShipsCrew(t *testing.T) {
	r := newScoutRig(t)
	for _, c := range scoutEnvFormCommands {
		t.Run(c.name, func(t *testing.T) {
			err, _, _ := r.run(t, "1", "Bash", bashInput(c.cmd), "")
			var denied *GuardDenied
			if !errors.As(err, &denied) {
				t.Fatalf("Guard = %v for %q, want a refusal", err, c.cmd)
			}
			if denied.Rule != c.rule {
				t.Errorf("rule = %q for %q, want %q", denied.Rule, c.cmd, c.rule)
			}
		})
	}
}

func TestOutsideScoutModeTheOtherEnvFormsAreNotRefusedByTheScoutRules(t *testing.T) {
	r := newScoutRig(t)
	for _, c := range scoutEnvFormCommands {
		t.Run(c.name, func(t *testing.T) {
			t.Setenv(scoutEnv, "")
			err, _, _ := runGuardTest(t, bashPayload(t, c.cmd, r.co), "")
			var denied *GuardDenied
			if errors.As(err, &denied) && isScoutRule(denied.Rule) {
				t.Errorf("Guard refused %q with %s with no scout mode on", c.cmd, denied.Rule)
			}
		})
	}
}

func TestScoutModeLeavesHarmlessUseOfTheSameBuiltinsAlone(t *testing.T) {
	r := newScoutRig(t)
	for _, cmd := range []string{
		`env -C /tmp ls`,
		`env -P /usr/bin ls`,
		`for f in a b; do echo $f; done`,
		`read line <<< x`,
		`printf -v out %s x`,
		`(( n = 1 + 2 ))`,
		`let n=3`,
		`exec -a name ls`,
		`admiral crew list`,
		`ls tmux`,
		`echo screen at batch`,
	} {
		err, _, _ := r.run(t, "1", "Bash", bashInput(cmd), "")
		var denied *GuardDenied
		if errors.As(err, &denied) && (denied.Rule == scoutEnvOverride || denied.Rule == scoutCrewShip || denied.Rule == scoutRunner) {
			t.Errorf("Guard = %v for %q, want it left alone", err, cmd)
		}
	}
}

func TestScoutModeRefusesEachCommandRunner(t *testing.T) {
	r := newScoutRig(t)
	for _, p := range scoutRunnerPrograms {
		for _, cmd := range []string{p + ` -x something`, `env ` + p + ` x`, `/usr/bin/` + p + ` x`, `echo hi | ` + p} {
			t.Run(cmd, func(t *testing.T) {
				err, _, _ := r.run(t, "1", "Bash", bashInput(cmd), "")
				var denied *GuardDenied
				if !errors.As(err, &denied) || denied.Rule != scoutRunner {
					t.Errorf("Guard = %v for %q, want a refusal by %s", err, cmd, scoutRunner)
				}
			})
		}
	}
}

func TestOutsideScoutModeEachCommandRunnerIsAllowedAsBefore(t *testing.T) {
	r := newScoutRig(t)
	for _, p := range scoutRunnerPrograms {
		t.Run(p, func(t *testing.T) {
			t.Setenv(scoutEnv, "")
			err, _, _ := runGuardTest(t, bashPayload(t, p+` -x something`, r.co), "")
			var denied *GuardDenied
			if errors.As(err, &denied) {
				t.Errorf("Guard = %v for %s with no scout mode on, want it allowed", err, p)
			}
		})
	}
}
