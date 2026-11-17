// Package commodore knows which Commodore a dock has and installs or updates it as a harness plugin.
package commodore

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	toml "github.com/pelletier/go-toml/v2"

	"github.com/jbuffin/admiral/internal/config"
	"github.com/jbuffin/admiral/internal/skillindex"
)

// MinVersion is the oldest Commodore this Admiral needs.
const MinVersion = "3.5.0"

const (
	marketplaceRepo = "jbuffin/skills"
	marketplaceName = "jbuffinskills"
	pluginKey       = "jasonbuffin@jbuffinskills"
)

// InstallTimeout is how long one install or update may run; a Runner with no timeout fails every command at once.
const InstallTimeout = 5 * time.Minute

// The values of State.State.
const (
	StateMissing        = "missing"
	StateOK             = "ok"
	StateOutdated       = "outdated"
	StateUnknownVersion = "unknown-version"
)

// ErrAutoUpdate wraps the failure to turn on auto-update after an install that otherwise worked.
var ErrAutoUpdate = errors.New("could not turn on auto-update")

// State is what the dock shows about Commodore: missing, ok, outdated or unknown-version.
type State struct {
	State     string `json:"state"`
	Installed string `json:"installed,omitempty"`
	Minimum   string `json:"minimum,omitempty"`
}

// StateOf classifies the Commodore a detected lookup found; detected is false when it found none.
func StateOf(c skillindex.Commodore, detected bool) State {
	switch {
	case !detected:
		return State{State: StateMissing}
	case c.Plugin == "" || c.Version == "":
		return State{State: StateUnknownVersion}
	case skillindex.VersionLess(c.Version, MinVersion):
		return State{State: StateOutdated, Installed: c.Version, Minimum: MinVersion}
	}
	return State{State: StateOK}
}

// Detect reads the dock's identity with no checkout, so a project copy never counts.
func Detect(dock config.Harness) State {
	return StateOf(skillindex.LookupCommodore(dock.Name, dock.Environ(), ""))
}

type Action string

const (
	Install Action = "install"
	Update  Action = "update"
)

// argv returns each command's arguments after the harness executable; nil for an unknown harness or action.
func argv(harness string, a Action) [][]string {
	add := []string{"plugin", "marketplace", "add", marketplaceRepo}
	install := []string{"plugin", "install", pluginKey}
	update := []string{"plugin", "update", pluginKey}
	switch harness {
	case "codex":
		install = []string{"plugin", "add", pluginKey}
		update = []string{"plugin", "marketplace", "upgrade", marketplaceName}
	case "claude", "copilot":
	default:
		return nil
	}
	switch a {
	case Install:
		return [][]string{add, install}
	case Update:
		return [][]string{update}
	}
	return nil
}

// Commands are the exact commands to run by hand, in order.
func Commands(harness string, a Action) []string {
	var out []string
	for _, args := range argv(harness, a) {
		out = append(out, harness+" "+strings.Join(args, " "))
	}
	return out
}

// LastLines keeps the last n lines of an installer's output.
func LastLines(s string, n int) string {
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return strings.Join(lines, "\n")
}

// Installer runs the install or update and returns the combined output of the commands it ran.
type Installer interface {
	Run(ctx context.Context, dock config.Harness, a Action) (output string, err error)
}

// Runner runs the dock's own CLI with the dock's environment and the ships' PATH.
type Runner struct {
	Home    string // $ADMIRAL_HOME
	Timeout time.Duration
}

func (r *Runner) Run(ctx context.Context, dock config.Harness, a Action) (string, error) {
	cmds := argv(dock.Name, a)
	if cmds == nil {
		return "", fmt.Errorf("cannot %s Commodore on %q", a, dock.Name)
	}
	if !dock.Found {
		return "", fmt.Errorf("%s is not installed: no %s executable found", dock.Name, dock.Name)
	}
	environ := dock.Environ()
	identity := Identity(dock)
	_, hadPlugin := detectedPlugin(dock)
	registered := hasMarketplace(dock.Name, identity)

	ctx, cancel := context.WithTimeout(ctx, r.Timeout)
	defer cancel()
	var out bytes.Buffer
	for i, args := range cmds {
		if a == Install && i == 0 && registered {
			continue
		}
		cmd := exec.CommandContext(ctx, dock.Executable, args...)
		cmd.Env = append(withShipPath(environ, filepath.Join(r.Home, "bin")), "GIT_TERMINAL_PROMPT=0")
		cmd.Stdout, cmd.Stderr = &out, &out
		// The CLI's own git and ssh children die with it, so a retry never races them.
		cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
		cmd.Cancel = func() error { return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL) }
		cmd.WaitDelay = 2 * time.Second
		if err := cmd.Run(); err != nil {
			if ctx.Err() != nil {
				err = fmt.Errorf("%s timed out after %s", Commands(dock.Name, a)[i], r.Timeout)
			}
			return out.String(), err
		}
	}
	if a == Install && !registered && !hadPlugin && dock.Name != "codex" {
		if err := setAutoUpdate(identity); err != nil {
			return out.String(), fmt.Errorf("%w: %v", ErrAutoUpdate, err)
		}
	}
	return out.String(), nil
}

// Identity is the dock's configuration directory, where its plugins and settings live.
func Identity(dock config.Harness) string {
	return config.IdentityDir(dock.Name, envMap(dock.Environ()))
}

func detectedPlugin(dock config.Harness) (skillindex.Commodore, bool) {
	c, ok := skillindex.LookupCommodore(dock.Name, dock.Environ(), "")
	return c, ok && c.Plugin != ""
}

func envMap(environ []string) map[string]string {
	m := map[string]string{}
	for _, kv := range environ {
		if k, v, ok := strings.Cut(kv, "="); ok {
			m[k] = v
		}
	}
	return m
}

// withShipPath puts $ADMIRAL_HOME/bin first on PATH, as a ship's pane has it.
func withShipPath(environ []string, bin string) []string {
	path := bin
	out := make([]string, 0, len(environ)+1)
	for _, kv := range environ {
		if p, ok := strings.CutPrefix(kv, "PATH="); ok {
			if p != "" {
				path += string(os.PathListSeparator) + p
			}
			continue
		}
		out = append(out, kv)
	}
	return append(out, "PATH="+path)
}

// hasMarketplace says whether the identity already registers the jbuffinskills marketplace.
func hasMarketplace(harness, identity string) bool {
	if harness == "codex" {
		data, err := os.ReadFile(filepath.Join(identity, "config.toml"))
		if err != nil {
			return false
		}
		var cfg struct {
			Marketplaces map[string]any `toml:"marketplaces"`
		}
		if toml.Unmarshal(data, &cfg) != nil {
			return false
		}
		_, ok := cfg.Marketplaces[marketplaceName]
		return ok
	}
	settings, _, err := readSettings(identity)
	if err != nil {
		return false
	}
	ms, _ := settings["extraKnownMarketplaces"].(map[string]any)
	_, ok := ms[marketplaceName]
	return ok
}

// readSettings returns settings.json and the leading // lines Copilot may put before its JSON.
func readSettings(identity string) (settings map[string]any, comments []byte, err error) {
	data, err := os.ReadFile(filepath.Join(identity, "settings.json"))
	if errors.Is(err, os.ErrNotExist) {
		return map[string]any{}, nil, nil
	}
	if err != nil {
		return nil, nil, err
	}
	body := skillindex.DropLeadingComments(data)
	comments = data[:len(data)-len(body)]
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.UseNumber()
	if err := dec.Decode(&settings); err != nil {
		return nil, nil, err
	}
	if settings == nil {
		settings = map[string]any{}
	}
	return settings, comments, nil
}

// setAutoUpdate edits one key of settings.json and leaves the rest as it was. A symlinked
// file keeps its link and its target gets the change.
func setAutoUpdate(identity string) error {
	settings, comments, err := readSettings(identity)
	if err != nil {
		return err
	}
	ms, _ := settings["extraKnownMarketplaces"].(map[string]any)
	if ms == nil {
		ms = map[string]any{}
		settings["extraKnownMarketplaces"] = ms
	}
	mk, _ := ms[marketplaceName].(map[string]any)
	if mk == nil {
		mk = map[string]any{}
		ms[marketplaceName] = mk
	}
	mk["autoUpdate"] = true
	var buf bytes.Buffer
	buf.Write(comments)
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(settings); err != nil {
		return err
	}
	path := filepath.Join(identity, "settings.json")
	mode := os.FileMode(0o600)
	if _, err := os.Lstat(path); err == nil {
		if path, err = filepath.EvalSymlinks(path); err != nil {
			return err
		}
	}
	if st, err := os.Stat(path); err == nil {
		mode = st.Mode().Perm()
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".settings-*.tmp")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(buf.Bytes()); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Chmod(mode); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}
