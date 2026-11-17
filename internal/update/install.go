package update

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// Command is one upgrade step to show the user.
type Command struct {
	Label   string `json:"label"`
	Command string `json:"command"`
}

// Install says how this Admiral was installed and the commands that upgrade it.
type Install struct {
	Method      string // homebrew, script or source
	Commands    []Command
	RestartNote string
}

const (
	restartCommand = "launchctl kickstart -k gui/$UID/dev.jasonbuffin.admiral"
	restartNote    = "If Admiral did not start as a service, restart it the way you started it."
	installScript  = "https://github.com/jbuffin/admiral/releases/latest/download/install.sh"
)

var plainPath = regexp.MustCompile(`^[A-Za-z0-9_./@:+-]+$`)

func quote(s string) string {
	if plainPath.MatchString(s) {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// InstallFor derives the install method and upgrade commands from the running version and the resolved executable path.
func InstallFor(version, exe string) Install {
	root := filepath.Dir(filepath.Dir(exe))
	var steps []Command
	method := "script"
	switch {
	case strings.Contains(exe, "/Cellar/"):
		method = "homebrew"
		steps = []Command{{"Upgrade with Homebrew", "brew upgrade admiral"}}
	case version == "dev" || isDescribe(version) || inCheckout(exe):
		method = "source"
		steps = []Command{{"Pull the latest source", "git -C " + quote(root) + " pull"}, {"Build Admiral", "make -C " + quote(root) + " release"}}
	default:
		steps = []Command{{"Upgrade with the install script", "curl --proto '=https' --tlsv1.2 -fsSL " + installScript + " | PREFIX=" + quote(root) + " sh"}}
	}
	steps = append(steps, Command{"Restart the service", restartCommand})
	return Install{Method: method, Commands: steps, RestartNote: restartNote}
}

// inCheckout is whether exe is <dir>/bin/admiral with a git checkout at <dir>; a worktree's .git is a file.
func inCheckout(exe string) bool {
	if filepath.Base(filepath.Dir(exe)) != "bin" {
		return false
	}
	_, err := os.Stat(filepath.Join(filepath.Dir(filepath.Dir(exe)), ".git"))
	return err == nil
}
