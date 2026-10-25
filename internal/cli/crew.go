package cli

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/jbuffin/admiral/internal/config"
	"github.com/jbuffin/admiral/internal/home"
	"github.com/jbuffin/admiral/internal/launcher"
	"github.com/jbuffin/admiral/internal/sessionhost"
)

// Exit codes of `admiral crew` besides 0, 1 and 2 (#72 stories 4 and 8).
const (
	crewExitTaken    = 5 // the crew name is in use in this ship
	crewExitAnchored = 4 // send to a crew boat whose process ended
)

// codexEnterPause is how long send waits between the paste and Enter on Codex,
// which drops an Enter that arrives with the paste (research: crew-boats).
const codexEnterPause = 300 * time.Millisecond

// Crew is `admiral crew launch|send|list|close` (#152, ADR 0014): teammates in
// their own windows of the ship's tmux session. It talks to tmux on
// $ADMIRAL_HOME/tmux.sock directly and needs no server.
func Crew(args []string, std IO) error {
	if len(args) == 0 {
		return usagef("%s", Usage("crew"))
	}
	switch args[0] {
	case "launch":
		return crewLaunch(args[1:], std)
	case "send":
		return crewSend(args[1:], std)
	case "list":
		return crewList(args[1:], std)
	case "close":
		return crewClose(args[1:], std)
	}
	return usagef("%s", Usage("crew"))
}

// crewShip is what a crew command works on: the ship's session and the tmux it
// runs on.
type crewShip struct {
	session string
	tmux    string
	sock    string
	l       *launcher.Launcher
}

// crewTarget finds the ship: --ship (a session or display name), or the
// ADMIRAL_SHIP of the ship the command runs in. The home is ADMIRAL_HOME, or
// the one ADMIRAL_REPORT lies in, as the guard finds it.
func crewTarget(ship string) (*crewShip, error) {
	env := os.Getenv("ADMIRAL_SHIP")
	if ship == "" && env == "" {
		return nil, usagef("not inside an Admiral ship (ADMIRAL_SHIP is not set); pass --ship <name> to name one")
	}
	if os.Getenv("ADMIRAL_HOME") == "" {
		if report := os.Getenv("ADMIRAL_REPORT"); report != "" {
			_ = os.Setenv("ADMIRAL_HOME", filepath.Dir(filepath.Dir(report)))
		}
	}
	dir, err := home.Resolve()
	if err != nil {
		return nil, err
	}
	tmux, err := exec.LookPath("tmux")
	if err != nil {
		return nil, errors.New("tmux not found on PATH")
	}
	sock := filepath.Join(dir, "tmux.sock")
	session := env
	if ship != "" {
		session = ""
		sessions, err := sessionhost.ReadOnce(tmux, sock)
		if err != nil {
			return nil, err
		}
		for _, s := range sessions {
			if s.IsShip() && (s.Name == ship || s.Options["name"] == ship) {
				session = s.Name
				break
			}
		}
		if session == "" {
			return nil, fmt.Errorf("no ship called %q (admiral ships lists them)", ship)
		}
		// An autopilot ship must not reach the crew of another ship, whose guard
		// rules and credentials differ. With no ADMIRAL_SHIP (a crew boat's) it
		// reaches none by --ship: fail closed.
		if os.Getenv("ADMIRAL_AUTOPILOT") == "1" && session != env {
			return nil, fmt.Errorf("an autopilot ship cannot reach crew of another ship (--ship %s is not this ship)", ship)
		}
	}
	l := &launcher.Launcher{Tmux: tmux, Socket: sock, Home: dir}
	if l.Config, err = crewConfig(); err != nil {
		return nil, err
	}
	return &crewShip{session: session, tmux: tmux, sock: sock, l: l}, nil
}

func crewConfig() (func() *config.Config, error) {
	cfg, err := loadConfig()
	if err != nil {
		return nil, err
	}
	return func() *config.Config { return cfg }, nil
}

func (c *crewShip) find(name string) (sessionhost.Crew, error) {
	boats, err := sessionhost.ReadCrew(c.tmux, c.sock, c.session)
	if err != nil {
		return sessionhost.Crew{}, fmt.Errorf("could not read the crew of %s: %w", c.session, err)
	}
	for _, b := range boats {
		if b.Name == name {
			return b, nil
		}
	}
	return sessionhost.Crew{}, fmt.Errorf("no crew boat called %q in %s (admiral crew list shows them)", name, c.session)
}

func crewLaunch(args []string, std IO) error {
	// Everything after the first -- is the prompt, untouched.
	var prompt []string
	var hasPrompt bool
	for i, a := range args {
		if a == "--" {
			args, prompt, hasPrompt = args[:i], args[i+1:], true
			break
		}
	}
	fs := newFlags("crew launch")
	name := fs.String("name", "", "the crew boat's name")
	dir := fs.String("dir", "", "the directory it works in")
	harness := fs.String("harness", "", "the harness (default: the ship's)")
	ship := fs.String("ship", "", "the ship (default: the one this runs in)")
	role := fs.String("role", "", "the crew boat's role, lowercase letters, digits and -")
	var extra list
	fs.Var(&extra, "arg", "an argument for the harness, repeatable")
	if err := parseFlags(fs, args); err != nil {
		return err
	}
	if *role != "" && !launcher.CrewName.MatchString(*role) {
		return usagef("crew launch: --role %q must be lowercase letters, digits and -", *role)
	}
	if fs.NArg() > 0 || *name == "" || *dir == "" || !hasPrompt || len(prompt) == 0 {
		return usagef("%s", Usage("crew"))
	}
	c, err := crewTarget(*ship)
	if err != nil {
		return err
	}
	err = c.l.LaunchCrew(launcher.CrewRequest{Session: c.session, Name: *name, Harness: *harness, Dir: *dir, Prompt: strings.Join(prompt, " "), Args: extra, Role: *role})
	if errors.Is(err, launcher.ErrCrewNameTaken) {
		return &ExitError{Code: crewExitTaken, Msg: err.Error()}
	}
	return err
}

// parseInterleaved parses flags that may come after the positional arguments
// (`crew send <name> --ship x`), and returns the positionals.
func parseInterleaved(fs *flag.FlagSet, args []string) ([]string, error) {
	var rest []string
	for len(args) > 0 {
		if err := parseFlags(fs, args); err != nil {
			return nil, err
		}
		if fs.NArg() == 0 {
			break
		}
		rest, args = append(rest, fs.Arg(0)), fs.Args()[1:]
	}
	return rest, nil
}

func crewSend(args []string, std IO) error {
	fs := newFlags("crew send")
	ship := fs.String("ship", "", "the ship (default: the one this runs in)")
	rest, err := parseInterleaved(fs, args)
	if err != nil {
		return err
	}
	if len(rest) == 0 || len(rest) > 2 {
		return usagef("%s", Usage("crew"))
	}
	c, err := crewTarget(*ship)
	if err != nil {
		return err
	}
	boat, err := c.find(rest[0])
	if err != nil {
		return err
	}
	if boat.State == sessionhost.CrewAnchored {
		return &ExitError{Code: crewExitAnchored, Msg: fmt.Sprintf("crew boat %q is anchored, so it cannot take text", boat.Name)}
	}
	var text string
	if len(rest) == 2 {
		text = rest[1]
	} else {
		b, err := io.ReadAll(std.In)
		if err != nil {
			return err
		}
		text = strings.TrimRight(string(b), "\r\n")
	}
	if text == "" {
		return usagef("crew send: no text")
	}
	if i := strings.IndexFunc(text, func(r rune) bool { return (r < 0x20 && r != '\n' && r != '\t') || r == 0x7f }); i >= 0 {
		return fmt.Errorf("the text holds a control character (byte %d); only newline and tab are sent", i)
	}
	return c.submit(boat, text)
}

// submit is the harness's submit: a bracketed paste of the text, then Enter,
// with a pause before it for Codex.
func (c *crewShip) submit(boat sessionhost.Crew, text string) error {
	buf := fmt.Sprintf("admiral-crew-%d", os.Getpid())
	cmd := exec.Command(c.tmux, "-f", "/dev/null", "-S", c.sock, "load-buffer", "-b", buf, "-")
	cmd.Env = withoutTmuxEnv()
	cmd.Stdin = strings.NewReader(text)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("tmux could not load the text: %s", strings.TrimSpace(string(out)))
	}
	if out, err := c.l.RunTmux("paste-buffer", "-p", "-d", "-b", buf, "-t", boat.PaneID); err != nil {
		_, _ = c.l.RunTmux("delete-buffer", "-b", buf)
		return fmt.Errorf("tmux could not paste into %s: %s", boat.Name, strings.TrimSpace(out))
	}
	if boat.Harness == "codex" {
		time.Sleep(codexEnterPause)
	}
	if out, err := c.l.RunTmux("send-keys", "-t", boat.PaneID, "Enter"); err != nil {
		return fmt.Errorf("tmux could not press Enter in %s: %s", boat.Name, strings.TrimSpace(out))
	}
	return nil
}

func withoutTmuxEnv() []string {
	var env []string
	for _, kv := range os.Environ() {
		if !strings.HasPrefix(kv, "TMUX=") && !strings.HasPrefix(kv, "TMUX_PANE=") {
			env = append(env, kv)
		}
	}
	return env
}

type crewRow struct {
	Name         string  `json:"name"`
	Harness      string  `json:"harness"`
	Dir          string  `json:"dir"`
	State        string  `json:"state"`
	Launched     string  `json:"launched"`
	LastActivity *string `json:"last_activity"`
	Role         *string `json:"role"`
}

func crewList(args []string, std IO) error {
	fs := newFlags("crew list")
	asJSON := fs.Bool("json", false, "print JSON")
	ship := fs.String("ship", "", "the ship (default: the one this runs in)")
	if err := parseFlags(fs, args); err != nil {
		return err
	}
	if fs.NArg() > 0 {
		return usagef("%s", Usage("crew"))
	}
	c, err := crewTarget(*ship)
	if err != nil {
		return err
	}
	boats, err := sessionhost.ReadCrew(c.tmux, c.sock, c.session)
	if err != nil {
		return fmt.Errorf("could not read the crew of %s: %w", c.session, err)
	}
	rows := make([]crewRow, 0, len(boats))
	for _, b := range boats {
		r := crewRow{b.Name, b.Harness, b.Dir, b.State, b.Launched, nil, nil}
		if b.Role != "" {
			role := b.Role
			r.Role = &role
		}
		if !b.LastActivity.IsZero() {
			t := b.LastActivity.UTC().Format(time.RFC3339)
			r.LastActivity = &t
		}
		rows = append(rows, r)
	}
	if *asJSON {
		return json.NewEncoder(std.Out).Encode(rows)
	}
	fmt.Fprintln(std.Out, "NAME\tHARNESS\tSTATE\tDIR")
	for _, r := range rows {
		fmt.Fprintf(std.Out, "%s\t%s\t%s\t%s\n", r.Name, r.Harness, r.State, r.Dir)
	}
	return nil
}

func crewClose(args []string, std IO) error {
	fs := newFlags("crew close")
	ship := fs.String("ship", "", "the ship (default: the one this runs in)")
	rest, err := parseInterleaved(fs, args)
	if err != nil {
		return err
	}
	if len(rest) != 1 {
		return usagef("%s", Usage("crew"))
	}
	c, err := crewTarget(*ship)
	if err != nil {
		return err
	}
	boat, err := c.find(rest[0])
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := c.l.CloseCrew(ctx, boat.WindowID); err != nil {
		return fmt.Errorf("tmux could not close %s: %s", boat.Name, err)
	}
	return nil
}
