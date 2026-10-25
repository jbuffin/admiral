// Package cli holds Admiral's command-line commands other than the server:
// setup, fleet, config, token, ships, attach, outcomes, inbox, orders, chat, lock and completion (ADR 0010, 0012, 0015).
//
// Exit codes: 0 ok, 1 error, 2 usage. A command returns a *UsageError for the
// last; main maps it. An *ExitError carries its own code (lock's 75 and 127,
// and the command's own status).
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

// UsageError is a mistake in how a command was called.
type UsageError struct{ Msg string }

func (e *UsageError) Error() string { return e.Msg }

// HelpError is a request for a command's usage: main prints it and exits 0.
type HelpError struct{ Command string }

func (e *HelpError) Error() string { return Usage(e.Command) }

// IsHelp says whether an argument asks for usage. It is never a ship name or a value.
func IsHelp(arg string) bool { return arg == "-h" || arg == "--help" || arg == "-help" }

var usages = map[string]string{
	"setup":      "usage: admiral setup [--answers <file> --yes] [--dry-run]",
	"fleet":      "usage: admiral fleet list | add <key> --name <name> [flags] | edit <key> [flags] | check [<key>]\nflags: --color --harness --executable <h>=<path> --arg <h>=<arg> --env <h>=<NAME>=<value> --unset-env <h>=<NAME> --search-root --remove-search-root",
	"config":     "usage: admiral config show | validate | edit",
	"token":      "usage: admiral token rotate [--port <n>]",
	"ships":      "usage: admiral ships [--port <n>] [--names]",
	"attach":     "usage: admiral attach <ship>   (a display name or a session name; Ctrl-b d detaches)",
	"outcomes":   "usage: admiral outcomes [--since <date>] [--ship <name>]   (JSON lines; a date is YYYY-MM-DD in UTC, or RFC 3339)",
	"crew":       "usage: admiral crew launch --name <name> --dir <dir> [--harness <h>] [--ship <ship>] -- <prompt>\n       admiral crew send <name> [text] [--ship <ship>]   (text from stdin without an argument)\n       admiral crew list [--json] [--ship <ship>]\n       admiral crew close <name> [--ship <ship>]",
	"lock":       "usage: admiral lock [--port <n>] [--wait] <resource> -- <command…>   (holds one unit of the resource while the command runs; exits 75 if busy)",
	"completion": "usage: admiral completion zsh|bash",
	"service":    "usage: admiral service install | uninstall | status",
	"inbox":      "usage: admiral inbox [--port <n>] [--json]",
	"orders":     "usage: admiral orders [--port <n>] [--json]\n       admiral orders add --source <key> --panel <id> [--port <n>] [-- <text>]   (text from stdin without an argument)",
	"chat":       "usage: admiral chat ask [--timeout <dur>] [--wait <id>]   (a question as JSON on stdin: {title, questions})",
}

// Usage is the usage text of a command ("fleet add" has its command's).
func Usage(command string) string {
	first, _, _ := strings.Cut(command, " ")
	if u, ok := usages[first]; ok {
		return u
	}
	return "usage: admiral [--listen host:port] | setup | fleet | config | token | ships | attach | crew | outcomes | inbox | orders | chat | lock | completion | service | --version"
}

func usagef(format string, a ...any) error { return &UsageError{fmt.Sprintf(format, a...)} }

// IO is where a command reads and writes; tests and the setup terminal use the
// process's own.
type IO struct {
	In  io.Reader
	Out io.Writer
	Err io.Writer
}

// Std is the process's standard streams.
func Std() IO { return IO{os.Stdin, os.Stdout, os.Stderr} }

// newFlags is a flag set whose errors come back as usage errors and whose
// usage text stays off the terminal.
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
	return nil
}

// list is a flag that may repeat.
type list []string

func (l *list) String() string     { return strings.Join(*l, ",") }
func (l *list) Set(s string) error { *l = append(*l, s); return nil }

func admiralHome() (string, error) { return home.Resolve() }
