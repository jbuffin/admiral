package launcher

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
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
	Args    []string // pass-through harness args (--arg), after the hook flags and before the prompt
	Role    string   // optional @admiral-role; the caller has checked its shape
	// Autopilot is a caller with ADMIRAL_AUTOPILOT=1: only claude launches, and
	// Args must fit the autopilot args allowlist.
	Autopilot bool
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
	if req.Role != "" && !CrewName.MatchString(req.Role) {
		return fmt.Errorf("crew role %q must be lowercase letters, digits and -", req.Role)
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
	shipHarness, err := shipOpt("harness")
	if err != nil {
		return err
	}
	if req.Harness == "" {
		req.Harness = shipHarness
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
	if err := RefuseHookArgs(req.Harness, req.Args); err != nil {
		return err
	}
	if req.Autopilot {
		if !slices.Contains(AutopilotHarnesses, req.Harness) {
			return fmt.Errorf("a caller with ADMIRAL_AUTOPILOT=1 can launch only %s crew: `admiral guard` doesn't cover %s", strings.Join(AutopilotHarnesses, ", "), req.Harness)
		}
		if err := AutopilotArgs(req.Harness, dock.Args, req.Args); err != nil {
			return err
		}
	}
	exe, found := dock.Resolve()
	if !found {
		return fmt.Errorf("%s executable %s not found for fleet %s", req.Harness, dock.Executable, fleet.Name)
	}
	argv := append(append([]string{exe}, dock.Args...), flags...)
	argv = append(argv, req.Args...)
	if req.Harness == "copilot" {
		if req.Prompt != "" {
			argv = append(argv, "--interactive="+req.Prompt)
		}
	} else {
		argv = appendPrompt(argv, req.Prompt, true)
	}

	// The window inherits the ship's session environment, so -e carries only what
	// this dock sets itself; the caller's own environment (the ship harness's tool
	// shell, or another fleet's credentials under --ship) never goes in. PATH
	// is the spawning client's, built as a ship's is.
	env := map[string]string{}
	for k, v := range dock.Env {
		env[k] = v
	}
	// The ship's own variables and Admiral's, which only the session sets, so a
	// crew boat of an autopilot ship stays under its guard.
	for _, k := range append([]string{"ADMIRAL_AUTOPILOT", "ADMIRAL_LOW_STAKES", "ADMIRAL_AUTO_MERGE"}, shipEnvNames...) {
		delete(env, k)
	}
	delete(env, "PATH")
	clientPath := l.shipEnv(dock)["PATH"]
	// What the session holds that this boat must not have: the ship's own
	// variables, and the keys the ship's dock sets that this dock doesn't. env -u
	// clears them for this process without touching the ship's session.
	clear := append([]string(nil), shipEnvNames...)
	if shipDock, ok := fleet.Harness(shipHarness); ok {
		for k := range shipDock.Env {
			if _, set := dock.Env[k]; !set && k != "PATH" && !slices.Contains(clear, k) {
				clear = append(clear, k)
			}
		}
	}
	sort.Strings(clear)
	command := []string{"env"}
	for _, k := range clear {
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
	opts := [][2]string{{"crew-name", req.Name}, {"harness", req.Harness}, {"dir", dir},
		{"launched", time.Now().UTC().Format(time.RFC3339)}}
	if req.Role != "" {
		opts = append(opts, [2]string{"role", req.Role})
	}
	for _, o := range opts {
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
		if out, err := l.runWith(clientPath, batch...); err != nil {
			_, _ = l.run("kill-window", "-t", w)
			return fmt.Errorf("tmux could not start the crew boat: %s", strings.TrimSpace(out))
		}
	}
	return nil
}

// ErrCrewGone is CloseCrew's answer for a window that is already gone; the
// error's text is tmux's own.
var ErrCrewGone = errors.New("crew window is gone")

type windowGone struct{ msg string }

func (e windowGone) Error() string        { return e.msg }
func (e windowGone) Is(target error) bool { return target == ErrCrewGone }

// CloseCrew kills one crew boat's window by its tmux window id, and with it the
// teammate's process; ctx bounds the kill. tmux never reuses a window id within
// one server, so an id from a stale read can't close another boat's window.
// `admiral crew close` and the server's close route both end here.
func (l *Launcher) CloseCrew(ctx context.Context, windowID string) error {
	out, err := l.runCtx(ctx, "kill-window", "-t", windowID)
	if err == nil {
		return nil
	}
	out = strings.TrimSpace(out)
	if out == "" {
		out = err.Error()
	}
	if strings.Contains(out, "can't find window") {
		return windowGone{out}
	}
	return errors.New(out)
}
