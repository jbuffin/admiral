package update

import (
	"os"
	"path/filepath"
	"regexp"
	"testing"
)

func commandLines(in Install) []string {
	var out []string
	for _, c := range in.Commands {
		out = append(out, c.Command)
	}
	return out
}

const restart = "launchctl kickstart -k gui/$UID/dev.jasonbuffin.admiral"

func requireRestart(t *testing.T, in Install) {
	t.Helper()
	if len(in.Commands) == 0 {
		t.Fatal("no commands")
	}
	last := in.Commands[len(in.Commands)-1]
	if last.Command != restart {
		t.Errorf("last command %q, want the service restart %q", last.Command, restart)
	}
	if !regexp.MustCompile(`(?i)service`).MatchString(last.Label) {
		t.Errorf("the restart's label %q doesn't say it is for a service install", last.Label)
	}
	if in.RestartNote == "" {
		t.Error("no note on restarting Admiral when it didn't start as a service")
	}
}

func TestAHomebrewInstallUpgradesWithBrew(t *testing.T) {
	in := InstallFor("0.9.1", "/opt/homebrew/Cellar/admiral/0.9.1/bin/admiral")
	if in.Method != "homebrew" {
		t.Fatalf("method %q, want homebrew", in.Method)
	}
	cmds := commandLines(in)
	if len(cmds) != 2 || !regexp.MustCompile(`^brew upgrade (jbuffin/admiral/)?admiral$`).MatchString(cmds[0]) {
		t.Errorf("commands %q, want a brew upgrade of admiral then the restart", cmds)
	}
	requireRestart(t, in)
}

func TestAScriptInstallRerunsTheInstallScriptWithTheSamePrefix(t *testing.T) {
	in := InstallFor("0.9.1", "/Users/sam/.local/bin/admiral")
	if in.Method != "script" {
		t.Fatalf("method %q, want script", in.Method)
	}
	cmds := commandLines(in)
	if len(cmds) != 2 || !regexp.MustCompile(`^curl .*-fsSL https://github\.com/jbuffin/admiral/releases/latest/download/install\.sh \| PREFIX=/Users/sam/\.local sh$`).MatchString(cmds[0]) {
		t.Errorf("commands %q, want the install script piped to sh with PREFIX=/Users/sam/.local, then the restart", cmds)
	}
	requireRestart(t, in)
}

func TestADevBuildInACheckoutPullsAndRebuilds(t *testing.T) {
	in := InstallFor("dev", "/Users/sam/src/admiral/bin/admiral")
	if in.Method != "source" {
		t.Fatalf("method %q, want source", in.Method)
	}
	want := []string{"git -C /Users/sam/src/admiral pull", "make -C /Users/sam/src/admiral release", restart}
	if got := commandLines(in); !equal(got, want) {
		t.Errorf("commands %q, want %q", got, want)
	}
	requireRestart(t, in)
}

func TestAGitDescribeVersionIsASourceInstall(t *testing.T) {
	if in := InstallFor("0.9.1-12-gabc123-dirty", "/Users/sam/src/admiral/bin/admiral"); in.Method != "source" {
		t.Errorf("method %q, want source", in.Method)
	}
}

func TestAReleaseVersionInsideAGitCheckoutIsASourceInstall(t *testing.T) {
	dir := t.TempDir()
	// A worktree's .git is a file, which counts.
	if err := os.WriteFile(filepath.Join(dir, ".git"), []byte("gitdir: elsewhere\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	in := InstallFor("0.9.1", filepath.Join(dir, "bin", "admiral"))
	if in.Method != "source" {
		t.Fatalf("method %q, want source", in.Method)
	}
	if got := commandLines(in); len(got) == 0 || got[0] != "git -C "+dir+" pull" {
		t.Errorf("commands %q, want a pull of %s first", got, dir)
	}
}

func TestACellarPathIsHomebrewEvenForADevVersion(t *testing.T) {
	if in := InstallFor("dev", "/opt/homebrew/Cellar/admiral/0.9.1/bin/admiral"); in.Method != "homebrew" {
		t.Errorf("method %q, want homebrew", in.Method)
	}
}

func equal(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
