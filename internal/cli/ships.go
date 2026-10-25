package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"time"

	"github.com/jbuffin/admiral/internal/home"
	"github.com/jbuffin/admiral/internal/sessionhost"
	"github.com/jbuffin/admiral/internal/ships"
)

type shipsBody struct {
	Ships []ships.Ship `json:"ships"`
	Home  string       `json:"home"`
}

// probe asks the server on port for its ships, and returns them only when it
// serves this home (its `home` field is home.ID of dir): another Admiral may hold
// the port.
func probe(port, dir string) (shipsBody, bool) {
	client := &http.Client{Timeout: time.Second}
	resp, err := client.Get("http://" + net.JoinHostPort("127.0.0.1", port) + "/api/ships")
	if err != nil {
		return shipsBody{}, false
	}
	defer resp.Body.Close()
	var body shipsBody
	ok := resp.StatusCode == http.StatusOK && json.NewDecoder(resp.Body).Decode(&body) == nil && body.Home == home.ID(dir)
	return body, ok
}

// readShips is the ships as the running server sees them (so the status
// matches the dashboard), or, with the server down, as read from tmux
// directly. running says which.
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

// shipSessions are the ships' sessions as tmux has them, with their crew boats.
func shipSessions(dir string) ([]sessionhost.Session, error) {
	tmux, err := exec.LookPath("tmux")
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

// row prints one line of the table. The fields come from option text an agent
// can write, so none reaches the terminal with a control character in it.
func row(out io.Writer, fields ...string) {
	for i, f := range fields {
		fields[i] = printable(f)
	}
	fmt.Fprintln(out, strings.Join(fields, "\t"))
}

// printable turns the control characters (below 0x20, and 0x7f to 0x9f) into
// spaces.
func printable(s string) string {
	return strings.Map(func(r rune) rune {
		if r < 0x20 || (r >= 0x7f && r <= 0x9f) {
			return ' '
		}
		return r
	}, s)
}

func statusText(s ships.Ship) string { return exitText(s.Status, s.ExitCode, s.ExitSignal) }

// exitText is a status, with how it ended when it is anchored.
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

// Ships is `admiral ships [--port <n>] [--names]` (story 125): a tab-separated
// table, or just the display names for shell completion.
func Ships(args []string, std IO) error {
	fs := newFlags("ships")
	port := fs.String("port", DefaultPort, "the port Admiral listens on")
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
		// One line per crew boat (teammates; subagents have no window), under the
		// ship's, in the same six columns with SESSION and FLEET empty.
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

// Attach is `admiral attach <ship> [<crew>]` (story 125, ADR 0012, #72 story 18):
// the ship's tmux session in this terminal, on its own window, or on a crew
// boat's window. The ship is found from tmux, so it works without the server.
// The pane keeps the size the bridge gave it (ignore-size) and an anchored ship
// or crew boat is read-only.
//
// The whole argument list is tried as a ship first (Saltpaw II, with or without
// quotes); failing that the last argument is the crew boat's name, which holds
// no space, and the rest is the ship's.
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
	tmux, err := exec.LookPath("tmux")
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
	argv := []string{tmux, "-u", "-S", sock, "attach", "-f", "ignore-size"}
	// The ship's own window, never "=session": that is whichever window is current.
	where := "=" + ship.Name + ":^"
	if crewName == "" {
		if st := ships.FromSession(*ship, time.Now()).Status; st != ships.StatusUnderway && st != ships.StatusNeedsYou {
			argv = append(argv, "-r")
		}
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
		if boat.State == sessionhost.CrewAnchored {
			argv = append(argv, "-r")
		}
		where = "=" + ship.Name + ":" + boat.WindowID
	}
	argv = append(argv, "-t", where)
	fmt.Fprintln(std.Out, "Ctrl-b d to detach")
	return syscall.Exec(tmux, argv, os.Environ())
}

// Completion is `admiral completion zsh|bash`: scripts that complete ship names.
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
