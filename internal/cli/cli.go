// Package cli holds Admiral's command-line commands other than the server:
// setup, fleet, config, token, ships, attach and completion (ADR 0010, 0012).
//
// Exit codes: 0 ok, 1 error, 2 usage. A command returns a *UsageError for the
// last; main maps it.
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
			return usagef("usage: admiral %s", fs.Name())
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
