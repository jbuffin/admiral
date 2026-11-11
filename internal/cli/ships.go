package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/jbuffin/admiral/internal/home"
	"github.com/jbuffin/admiral/internal/sessionhost"
	"github.com/jbuffin/admiral/internal/ships"
)

type shipsBody struct {
	Ships []ships.Ship `json:"ships"`
	Home  string       `json:"home"`
}

// probe checks the server's `home` field because another Admiral may hold the port.
func probe(port, dir string) (shipsBody, bool) {
	base, err := loopbackURL(port)
	if err != nil {
		return shipsBody{}, false
	}
	client := &http.Client{Timeout: time.Second}
	resp, err := client.Get(base + "/api/ships")
	if err != nil {
		return shipsBody{}, false
	}
	defer resp.Body.Close()
	var body shipsBody
	ok := resp.StatusCode == http.StatusOK && json.NewDecoder(resp.Body).Decode(&body) == nil && body.Home == home.ID(dir)
	return body, ok
}

// readShips prefers the running server so the status matches the dashboard, and
// falls back to tmux with the server down.
func readShips(port, dir string) (list []ships.Ship, running bool, err error) {
	if body, ok := probe(port, dir); ok {
		return body.Ships, true, nil
	}
	list, err = shipsFromTmux(dir)
	return list, false, err
}

func shipsFromTmux(dir string) ([]ships.Ship, error) {
	sessions, err := shipSessions(dir)
	if err != nil {
		return nil, err
	}
	now := time.Now()
	list := []ships.Ship{}
	for _, s := range sessions {
		list = append(list, ships.FromSession(s, now))
	}
	return list, nil
}

func shipSessions(dir string) ([]sessionhost.Session, error) {
	tmux, err := sessionhost.FindTmux()
	if err != nil {
		return nil, fmt.Errorf("tmux not found on PATH")
	}
	sessions, err := sessionhost.ReadOnce(tmux, filepath.Join(dir, "tmux.sock"))
	if err != nil {
		return nil, err
	}
	var out []sessionhost.Session
	for _, s := range sessions {
		if s.IsShip() {
			out = append(out, s)
		}
	}
	return out, nil
}

// The fields come from option text an agent can write, so none reaches the
// terminal with a control character in it.
func row(out io.Writer, fields ...string) {
	for i, f := range fields {
		fields[i] = printable(f)
	}
	fmt.Fprintln(out, strings.Join(fields, "\t"))
}

func printable(s string) string {
	return strings.Map(func(r rune) rune {
		if r < 0x20 || (r >= 0x7f && r <= 0x9f) {
			return ' '
		}
		return r
	}, s)
}

func statusText(s ships.Ship) string { return exitText(s.Status, s.ExitCode, s.ExitSignal) }

func exitText(status string, code *int, signal *string) string {
	switch {
	case status != ships.StatusAnchored:
		return status
	case signal != nil:
		return "anchored (" + *signal + ")"
	case code != nil:
		return fmt.Sprintf("anchored (exit %d)", *code)
	}
	return status
}

func Ships(args []string, std IO) error {
	fs := newFlags("ships")
	port := fs.String("port", defaultPort(), "the port Admiral listens on")
	names := fs.Bool("names", false, "print display names only, one per line")
	if err := parseFlags(fs, args); err != nil {
		return err
	}
	dir, err := admiralHome()
	if err != nil {
		return err
	}
	list, running, err := readShips(*port, dir)
	if err != nil {
		return err
	}
	sort.Slice(list, func(i, j int) bool { return list[i].Session < list[j].Session })
	notice := "Admiral is not running for this home; these ships are read from tmux directly."
	if *names {
		if !running {
			fmt.Fprintln(std.Err, notice)
		}
		for _, s := range list {
			fmt.Fprintln(std.Out, printable(s.Name))
		}
		return nil
	}
	if !running {
		fmt.Fprintln(std.Out, notice)
	}
	fmt.Fprintln(std.Out, "NAME\tSESSION\tFLEET\tHARNESS\tSTATUS\tREPO")
	for _, s := range list {
		row(std.Out, s.Name, s.Session, s.Fleet, s.Harness, statusText(s), s.Repo)
		// Only teammates are crew boats here; subagents have no window.
		crew := append([]ships.CrewBoat(nil), s.Crew...)
		sort.Slice(crew, func(i, j int) bool { return crew[i].Name < crew[j].Name })
		for _, c := range crew {
			repo := ""
			if c.Dir != "" {
				repo = filepath.Base(c.Dir)
			}
			row(std.Out, "  "+c.Name, "", "", c.Harness, exitText(c.Status, c.ExitCode, c.ExitSignal), repo)
		}
	}
	return nil
}

// Attach tries the whole argument list as a ship first (Saltpaw II, unquoted);
// failing that the last argument is the crew boat's name, which holds no space.
func Attach(args []string, std IO) error {
	if len(args) == 0 {
		return usagef("usage: admiral attach <ship> [<crew>]")
	}
	refuse := func(name string) error {
		for _, hidden := range []string{sessionhost.Internal, sessionhost.SetupName, sessionhost.ChatName} {
			if name == hidden {
				return fmt.Errorf("%s is Admiral's own session, not a ship", hidden)
			}
		}
		return nil
	}
	name := strings.Join(args, " ")
	if err := refuse(name); err != nil {
		return err
	}
	dir, err := admiralHome()
	if err != nil {
		return err
	}
	tmux, err := sessionhost.FindTmux()
	if err != nil {
		return fmt.Errorf("tmux not found on PATH")
	}
	sock := filepath.Join(dir, "tmux.sock")
	sessions, err := shipSessions(dir)
	if err != nil {
		return err
	}
	find := func(name string) *sessionhost.Session {
		for i, s := range sessions {
			if s.Name == name || s.Options["name"] == name {
				return &sessions[i]
			}
		}
		return nil
	}
	ship, crewName := find(name), ""
	if ship == nil && len(args) > 1 {
		shipName := strings.Join(args[:len(args)-1], " ")
		if err := refuse(shipName); err != nil {
			return err
		}
		if ship = find(shipName); ship != nil {
			crewName = args[len(args)-1]
		}
	}
	if ship == nil {
		return fmt.Errorf("no ship called %q (admiral ships lists them)", name)
	}
	// The ship's own window, never "=session": that is whichever window is current.
	where, readOnly := sessionhost.ShipTarget(ship.Name), false
	if crewName == "" {
		st := ships.FromSession(*ship, time.Now()).Status
		readOnly = st != ships.StatusUnderway && st != ships.StatusNeedsYou
	} else {
		var boat *sessionhost.Crew
		for i, c := range ship.Crew {
			if c.Name == crewName {
				boat = &ship.Crew[i]
			}
		}
		if boat == nil {
			return fmt.Errorf("no crew boat called %q on ship %q (admiral ships lists them)", crewName, ship.Options["name"])
		}
		// The crew boat's own status decides, not the ship's.
		readOnly = boat.State == sessionhost.CrewAnchored
		where = sessionhost.CrewTarget(ship.Name, boat.WindowID)
	}
	fmt.Fprintln(std.Out, "Ctrl-b d to detach")
	return sessionhost.ExecAttach(tmux, sock, where, readOnly)
}

func Completion(args []string, std IO) error {
	if len(args) != 1 {
		return usagef("usage: admiral completion zsh|bash")
	}
	switch args[0] {
	case "zsh":
		_, _ = io.WriteString(std.Out, zshCompletion)
	case "bash":
		_, _ = io.WriteString(std.Out, bashCompletion)
	default:
		return usagef("completion: want zsh or bash, got %q", args[0])
	}
	return nil
}

// The ship names may hold spaces ("Saltpaw II"), so each is quoted for the shell.
const zshCompletion = `#compdef admiral
# Put this on your fpath, or run: source <(admiral completion zsh)
_admiral() {
  local -a commands ship_names
  commands=(setup fleet config token ships attach completion)
  if (( CURRENT == 2 )); then
    compadd -a commands
  elif [[ ${words[2]} == attach ]]; then
    ship_names=("${(@f)$(admiral ships --names 2>/dev/null)}")
    compadd -Q -- ${(q)ship_names}
  fi
}
compdef _admiral admiral
`

const bashCompletion = `# Run: source <(admiral completion bash)
_admiral() {
  local cur=${COMP_WORDS[COMP_CWORD]}
  if [ "$COMP_CWORD" -eq 1 ]; then
    COMPREPLY=($(compgen -W "setup fleet config token ships attach completion" -- "$cur"))
  elif [ "${COMP_WORDS[1]}" = attach ]; then
    local IFS=$'\n' name
    COMPREPLY=()
    for name in $(admiral ships --names 2>/dev/null); do
      case "$name" in "$cur"*) COMPREPLY+=("$(printf '%q' "$name")");; esac
    done
  fi
}
complete -F _admiral admiral
`
