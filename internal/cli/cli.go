// Package cli holds Admiral's command-line commands other than the server.
// Exit codes: 0 ok, 1 error, 2 usage (*UsageError); an *ExitError carries its own.
package cli

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/jbuffin/admiral/internal/home"
)

type UsageError struct{ Msg string }

func (e *UsageError) Error() string { return e.Msg }

// HelpError makes main print the command's usage and exit 0.
type HelpError struct{ Command string }

func (e *HelpError) Error() string { return Usage(e.Command) }

// IsHelp's arguments are never a ship name or a value.
func IsHelp(arg string) bool { return arg == "-h" || arg == "--help" || arg == "-help" }

var usages = map[string]string{
	"setup":      "usage: admiral setup [--answers <file> --yes] [--dry-run]",
	"fleet":      "usage: admiral fleet list | add <key> --name <name> [flags] | edit <key> [flags] | check [<key>]\nflags: --color --harness --executable <h>=<path> --arg <h>=<arg> --env <h>=<NAME>=<value> --unset-env <h>=<NAME> --search-root --remove-search-root",
	"config":     "usage: admiral config show | validate | edit",
	"ships":      "usage: admiral ships [--port <n>] [--names]",
	"attach":     "usage: admiral attach <ship>   (a display name or a session name; Ctrl-b d detaches)",
	"outcomes":   "usage: admiral outcomes [--since <date>] [--ship <name>]   (JSON lines; a date is YYYY-MM-DD in UTC, or RFC 3339)",
	"crew":       "usage: admiral crew launch --dir <dir> [--name <name>] [--member <member>] [--harness <h>] [--ship <ship>] [--role <role>] [--arg <value>]... -- <prompt>\n       admiral crew send <name> [text] [--ship <ship>]   (text from stdin without an argument)\n       admiral crew list [--json] [--ship <ship>]\n       admiral crew close <name> [--ship <ship>]",
	"lock":       "usage: admiral lock [--port <n>] [--wait] <resource> -- <command…>   (holds one unit of the resource while the command runs; exits 75 if busy)",
	"completion": "usage: admiral completion zsh|bash",
	"service":    "usage: admiral service install | uninstall | status",
	"hold":       "usage: admiral hold on|off --fleet <key> | --all [--port <n>]\n       admiral hold status [--fleet <key>] [--port <n>]   (Hold fire lowers every routine's Act to Ask first; status with no fleet reads the all-fleets switch)",
	"inbox":      "usage: admiral inbox [--port <n>] [--json]",
	"orders":     "usage: admiral orders [--port <n>] [--json]\n       admiral orders add --source <key> --panel <id> [--port <n>] [-- <text>]   (text from stdin without an argument)",
	"advise":     "usage: admiral advise   (a decision as JSON on stdin: {question, options, recommendation}; prints {stakes, reason, cost})",
	"pair":       "usage: admiral pair [--port <n>] [--json]   (mints a single-use pairing code for a new device)",
	"devices":    "usage: admiral devices [--port <n>] [--json]\n       admiral devices rename <name-or-id> <new-name> [--port <n>]\n       admiral devices revoke <name-or-id> | --all [--port <n>]",
	"codex-pane": "usage: admiral codex-pane --socket <path> -- <codex> [args…]   (run by a Codex ship's pane: a per-ship app-server, then the TUI on it)",
	"mcp":        "usage: admiral mcp   (AdmiralBot's MCP server over stdio; needs ADMIRAL_ACTOR_TOKEN or ADMIRAL_ACTOR_TOKEN_FILE, and takes ADMIRAL_PORT, ADMIRAL_HOME and ADMIRAL_CHAT_FLEET)",
	"mcp-probe":  "usage: admiral mcp-probe   (the fence probe's MCP server over stdio; run by the probe, not by people)",
	"chat":       "usage: admiral chat ask [--timeout <dur>] [--wait <id>]   (a question as JSON on stdin: {title, questions})",
}

// Usage of a subcommand ("fleet add") is its command's.
func Usage(command string) string {
	first, _, _ := strings.Cut(command, " ")
	if u, ok := usages[first]; ok {
		return u
	}
	return "usage: admiral [--listen host:port] | setup | fleet | config | ships | attach | crew | outcomes | inbox | orders | hold | pair | devices | chat | advise | lock | completion | service | --version"
}

func usagef(format string, a ...any) error { return &UsageError{fmt.Sprintf(format, a...)} }

type IO struct {
	In  io.Reader
	Out io.Writer
	Err io.Writer
}

func Std() IO { return IO{os.Stdin, os.Stdout, os.Stderr} }

// newFlags keeps flag's own usage text off the terminal.
func newFlags(name string) *flag.FlagSet {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	return fs
}

func parseFlags(fs *flag.FlagSet, args []string) error {
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return &HelpError{fs.Name()}
		}
		return &UsageError{err.Error()}
	}
	if f := fs.Lookup("port"); f != nil {
		if _, err := parsePort(f.Value.String()); err != nil {
			return &UsageError{"--port: " + err.Error()}
		}
	}
	return nil
}

type list []string

func (l *list) String() string     { return strings.Join(*l, ",") }
func (l *list) Set(s string) error { *l = append(*l, s); return nil }

func admiralHome() (string, error) { return home.Resolve() }
