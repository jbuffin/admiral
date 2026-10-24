package launcher

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

// ErrCrewNameTaken is a crew name another crew boat of the ship already holds.
var ErrCrewNameTaken = errors.New("crew name taken")

// CrewName is what a crew boat's name may be (#72 story 3).
var CrewName = regexp.MustCompile(`^[a-z0-9-]+$`)

// CrewRequest is a teammate to launch in a ship's session. Harness is the
// ship's own when empty.
type CrewRequest struct {
	Session string
	Name    string
	Harness string
	Dir     string
	Prompt  string
}

// shipEnvNames are the ship's own variables; a teammate gets none of them (a
// teammate writes neither the ship's report nor its inbox, and crew of crew is
// out of scope).
var shipEnvNames = []string{"ADMIRAL_SHIP", "ADMIRAL_REPORT", "ADMIRAL_INBOX"}

// LaunchCrew starts a teammate in a new window of the ship's session (ADR 0014).
// The ship stays the current and first window. The window is made on a
// placeholder, given its options, then respawned as the harness, so the harness's
// first hook finds the options there. It is not Launch: it never touches the
// ship's report or inbox, and the directory is any existing one.
func (l *Launcher) LaunchCrew(req CrewRequest) error {
	if l.Tmux == "" {
		return errors.New("tmux not found on PATH, so crew cannot be launched")
	}
	if !CrewName.MatchString(req.Name) {
		return fmt.Errorf("crew name %q must be lowercase letters, digits and -", req.Name)
	}
	st, err := os.Stat(req.Dir)
	switch {
	case err != nil:
		return fmt.Errorf("directory %s not found", req.Dir)
	case !st.IsDir():
		return fmt.Errorf("%s is not a directory", req.Dir)
	case hasControl(req.Dir):
		return errors.New("directory contains a control character")
	}
	dir, err := filepath.Abs(req.Dir)
	if err != nil {
		return err
	}
	target := "=" + req.Session + ":"
	shipOpt := func(name string) (string, error) {
		out, err := l.run("show-options", "-qv", "-t", target, "@admiral-"+name)
		if err != nil {
			return "", fmt.Errorf("no ship session %s: %s", req.Session, strings.TrimSpace(out))
		}
		return strings.TrimSpace(out), nil
	}
	fleetKey, err := shipOpt("fleet")
	if err != nil {
		return err
	}
	if req.Harness == "" {
		if req.Harness, err = shipOpt("harness"); err != nil {
			return err
		}
	}
	fleet, ok := l.Config().Fleet(fleetKey)
	if !ok {
		return fmt.Errorf("fleet %q of the ship not found", fleetKey)
	}
	dock, ok := fleet.Harness(req.Harness)
	if !ok {
		return fmt.Errorf("fleet %s has no %s dock", fleet.Name, req.Harness)
	}
	flags, known := l.hookFlags(req.Harness)
	if !known {
		return fmt.Errorf("unknown harness %q", req.Harness)
	}
	exe, found := dock.Resolve()
	if !found {
		return fmt.Errorf("%s executable %s not found for fleet %s", req.Harness, dock.Executable, fleet.Name)
	}
	argv := append(append([]string{exe}, dock.Args...), flags...)
	if req.Harness == "copilot" {
		if req.Prompt != "" {
			argv = append(argv, "--interactive="+req.Prompt)
		}
	} else {
		argv = appendPrompt(argv, req.Prompt, true)
	}

	env := l.shipEnv(dock)
	for _, k := range shipEnvNames {
		delete(env, k)
	}
	// Admiral owns these, and only the ship's own session environment (which the
	// window inherits) may set them, so a crew boat of an autopilot ship stays
	// under its guard and Admiral's own environment never leaks one in.
	for _, k := range []string{"ADMIRAL_AUTOPILOT", "ADMIRAL_LOW_STAKES", "ADMIRAL_AUTO_MERGE"} {
		delete(env, k)
	}
	// The window inherits the ship's session environment, so a variable the
	// ship's harness has and this one does not must be cleared: env -u does that
	// for the three that are the ship's own.
	command := []string{"env"}
	for _, k := range shipEnvNames {
		command = append(command, "-u", k)
	}
	command = append(command, argv...)
	if n := commandSize(command); n > maxMessage {
		return fmt.Errorf("the prompt is too long to launch: its command line is %d bytes and tmux carries at most %d", n, maxMessage)
	}

	l.mu.Lock()
	defer l.mu.Unlock()
	out, err := l.run("list-windows", "-t", target, "-F", "#{@admiral-crew-name}")
	if err != nil {
		return fmt.Errorf("tmux could not list the windows of %s: %s", req.Session, strings.TrimSpace(out))
	}
	for _, n := range strings.Split(out, "\n") {
		if n == req.Name {
			return fmt.Errorf("%w: %q is already a crew boat of this ship", ErrCrewNameTaken, req.Name)
		}
	}

	cdir := strings.ReplaceAll(dir, "#", "##") // tmux format-expands -c; ## is a literal #
	win, err := l.run("new-window", "-d", "-P", "-F", "#{window_id}", "-t", target, "-n", req.Name, "-c", cdir, "--", "tail", "-f", "/dev/null")
	if err != nil {
		return fmt.Errorf("tmux could not create the crew window: %s", strings.TrimSpace(win))
	}
	w := strings.TrimSpace(win)
	cmds := [][]string{
		{"set-option", "-w", "-t", w, "remain-on-exit", "on"},
		{"set-option", "-w", "-t", w, "monitor-bell", "on"},
		{"set-option", "-w", "-t", w, "automatic-rename", "off"},
	}
	for _, o := range [][2]string{{"crew-name", req.Name}, {"harness", req.Harness}, {"dir", dir},
		{"launched", time.Now().UTC().Format(time.RFC3339)}} {
		cmds = append(cmds, []string{"set-option", "-w", "-t", w, "@admiral-" + o[0], o[1]})
	}
	respawn := []string{"respawn-pane", "-k", "-t", w, "-c", cdir}
	keys := make([]string, 0, len(env))
	for k := range env {
		if k != "PATH" { // the window's PATH is the spawning client's
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)
	for _, k := range keys {
		kv := k + "=" + env[k]
		if len(kv) > maxMessage {
			_, _ = l.run("kill-window", "-t", w)
			return fmt.Errorf("environment variable %s is too long to launch", k)
		}
		respawn = append(respawn, "-e", kv)
	}
	cmds = append(cmds, append(append(respawn, "--"), command...))
	for _, batch := range batches(cmds) {
		if out, err := l.runWith(env["PATH"], batch...); err != nil {
			_, _ = l.run("kill-window", "-t", w)
			return fmt.Errorf("tmux could not start the crew boat: %s", strings.TrimSpace(out))
		}
	}
	return nil
}
