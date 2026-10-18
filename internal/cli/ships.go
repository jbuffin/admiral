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

// readShips is the ships as the running server sees them (so the status
// matches the dashboard), or, with the server down, as read from tmux
// directly. running says which.
func readShips(port, dir string) (list []ships.Ship, running bool, err error) {
	client := &http.Client{Timeout: 3 * time.Second}
	if resp, err := client.Get("http://" + net.JoinHostPort("127.0.0.1", port) + "/api/ships"); err == nil {
		defer resp.Body.Close()
		var body struct {
			Ships []ships.Ship `json:"ships"`
			Home  string       `json:"home"`
		}
		// Only this home's server counts: another Admiral may hold the port.
		if resp.StatusCode == http.StatusOK && json.NewDecoder(resp.Body).Decode(&body) == nil && body.Home == home.ID(dir) {
			return body.Ships, true, nil
		}
	}
	list, err = shipsFromTmux(dir)
	return list, false, err
}

func shipsFromTmux(dir string) ([]ships.Ship, error) {
	tmux, err := exec.LookPath("tmux")
	if err != nil {
		return nil, fmt.Errorf("tmux not found on PATH")
	}
	sessions, err := sessionhost.ReadOnce(tmux, filepath.Join(dir, "tmux.sock"))
	if err != nil {
		return nil, err
	}
	now := time.Now()
	list := []ships.Ship{}
	for _, s := range sessions {
		if s.IsShip() {
			list = append(list, ships.FromSession(s, now))
		}
	}
	return list, nil
}

func statusText(s ships.Ship) string {
	switch {
	case s.Status != ships.StatusAnchored:
		return s.Status
	case s.ExitSignal != nil:
		return "anchored (" + *s.ExitSignal + ")"
	case s.ExitCode != nil:
		return fmt.Sprintf("anchored (exit %d)", *s.ExitCode)
	}
	return s.Status
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
			fmt.Fprintln(std.Out, s.Name)
		}
		return nil
	}
	if !running {
		fmt.Fprintln(std.Out, notice)
	}
	fmt.Fprintln(std.Out, "NAME\tSESSION\tFLEET\tHARNESS\tSTATUS\tREPO")
	for _, s := range list {
		fmt.Fprintf(std.Out, "%s\t%s\t%s\t%s\t%s\t%s\n", s.Name, s.Session, s.Fleet, s.Harness, statusText(s), s.Repo)
	}
	return nil
}

// Attach is `admiral attach <ship>` (story 125, ADR 0012): the ship's tmux
// session in this terminal. The ship is found from tmux, so it works without the
// server. The pane keeps the size the bridge gave it (ignore-size) and an
// anchored ship is read-only.
func Attach(args []string, std IO) error {
	if len(args) == 0 {
		return usagef("usage: admiral attach <ship>")
	}
	name := strings.Join(args, " ") // Saltpaw II, with or without quotes
	for _, hidden := range []string{sessionhost.Internal, sessionhost.SetupName} {
		if name == hidden {
			return fmt.Errorf("%s is Admiral's own session, not a ship", hidden)
		}
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
	list, err := shipsFromTmux(dir)
	if err != nil {
		return err
	}
	var found *ships.Ship
	for i, s := range list {
		if s.Session == name || s.Name == name {
			found = &list[i]
			break
		}
	}
	if found == nil {
		return fmt.Errorf("no ship called %q (admiral ships lists them)", name)
	}
	argv := []string{tmux, "-S", sock, "attach", "-f", "ignore-size"}
	if found.Status != ships.StatusUnderway && found.Status != ships.StatusNeedsYou {
		argv = append(argv, "-r")
	}
	argv = append(argv, "-t", "="+found.Session)
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
