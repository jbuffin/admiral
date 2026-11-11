package launcher

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sort"

	"github.com/jbuffin/admiral/internal/config"
	"github.com/jbuffin/admiral/internal/sessionhost"
)

var ErrCrewNameTaken = errors.New("crew name taken")

// Harness is the ship's own when empty.
type CrewRequest struct {
	Session string
	Name    string
	Harness string
	Dir     string
	Prompt  string
	Args    []string // pass-through harness args (--arg), after the hook flags and before the prompt
	Role    string   // optional @admiral-role; the caller has checked its shape
	// Autopilot is a caller with ADMIRAL_AUTOPILOT=1: only a harness whose guard probe
	// passed in the fleet launches, and Args must fit the autopilot args allowlist.
	Autopilot bool
}

// A teammate writes neither the ship's report nor its inbox, so it gets none of these.
var shipEnvNames = []string{"ADMIRAL_SHIP", "ADMIRAL_REPORT", "ADMIRAL_INBOX"}

// LaunchCrew keeps the ship as the current and first window. Unlike Launch, it never touches
// the ship's report or inbox, and the directory is any existing one.
func (l *Launcher) LaunchCrew(req CrewRequest) error {
	if l.Tmux == "" {
		return errors.New("tmux not found on PATH, so crew cannot be launched")
	}
	if !sessionhost.CrewName.MatchString(req.Name) {
		return fmt.Errorf("crew name %q must be lowercase letters, digits and -", req.Name)
	}
	if req.Role != "" && !sessionhost.CrewName.MatchString(req.Role) {
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
	srv := l.server()
	shipOpt := func(name string) (string, error) {
		v, err := srv.ShipOption(req.Session, name)
		if err != nil {
			return "", fmt.Errorf("no ship session %s: %w", req.Session, err)
		}
		return v, nil
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
		if err := l.autopilotGate(fleet, req.Harness); err != nil {
			return fmt.Errorf("a caller with ADMIRAL_AUTOPILOT=1 can launch only on a harness whose guard and advisor are proven: %w", err)
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
	// crew boat of an autopilot ship stays under `admiral guard`.
	for _, k := range append([]string{"ADMIRAL_AUTOPILOT", "ADMIRAL_LOW_STAKES", "ADMIRAL_AUTO_MERGE", "ADMIRAL_PROBE_NONCE", "ADMIRAL_PROBE_DIR"}, shipEnvNames...) {
		delete(env, k)
	}
	// The ship's session holds the fleet's crew directory, which the boat keeps.
	delete(env, "ADMIRAL_CREW_DIR")
	delete(env, "PATH")
	clientPath := l.shipEnv(dock)["PATH"]
	// What the session holds that this boat must not have: the ship's own
	// variables, and the keys the ship's dock sets that this dock doesn't. env -u
	// clears them for this process without touching the ship's session.
	clear := append([]string(nil), shipEnvNames...)
	if shipDock, ok := fleet.Harness(shipHarness); ok {
		for k := range shipDock.Env {
			if _, set := dock.Env[k]; !set && k != "PATH" && k != "ADMIRAL_CREW_DIR" && !slices.Contains(clear, k) {
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
	if n := sessionhost.CommandSize(command); n > sessionhost.MaxMessage {
		return fmt.Errorf("the prompt is too long to launch: its command line is %d bytes and tmux carries at most %d", n, sessionhost.MaxMessage)
	}

	l.mu.Lock()
	defer l.mu.Unlock()
	names, err := srv.CrewNames(req.Session)
	if err != nil {
		return fmt.Errorf("tmux could not list the windows of %s: %w", req.Session, err)
	}
	for _, n := range names {
		if n == req.Name {
			return fmt.Errorf("%w: %q is already a crew boat of this ship", ErrCrewNameTaken, req.Name)
		}
	}
	return srv.StartCrew(sessionhost.CrewSpec{Session: req.Session, Name: req.Name, Harness: req.Harness, Dir: dir,
		Role: req.Role, Env: env, Command: command, ClientPath: clientPath})
}

func (l *Launcher) CloseCrew(ctx context.Context, windowID string) error {
	return l.server().KillWindow(ctx, windowID)
}

// autopilotGate refuses when no gate is set: autopilot is never allowed unchecked.
func (l *Launcher) autopilotGate(fleet config.Fleet, harness string) error {
	if l.AutopilotGate == nil {
		return errors.New("no autopilot gate is configured")
	}
	return l.AutopilotGate(fleet, harness)
}
