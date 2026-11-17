// Package commodore knows which Commodore a dock has and installs or updates it as a harness plugin.
package commodore

import (
	"context"
	"time"

	"github.com/jbuffin/admiral/internal/config"
	"github.com/jbuffin/admiral/internal/skillindex"
)

// MinVersion is the oldest Commodore this Admiral needs.
const MinVersion = "3.5.0"

// State is what the dock shows about Commodore: missing, ok, outdated or unknown-version.
type State struct {
	State     string `json:"state"`
	Installed string `json:"installed,omitempty"`
	Minimum   string `json:"minimum,omitempty"`
}

// StateOf classifies the Commodore a detected lookup found; detected is false when it found none.
func StateOf(c skillindex.Commodore, detected bool) State { return State{} }

// Detect reads the dock's identity with no checkout, so a project copy never counts.
func Detect(dock config.Harness) State { return State{} }

type Action string

const (
	Install Action = "install"
	Update  Action = "update"
)

// Commands are the exact commands to run by hand, in order.
func Commands(harness string, a Action) []string { return nil }

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
	return "", nil
}
