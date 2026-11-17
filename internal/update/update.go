// Package update checks whether a newer public Admiral release exists and reports how to upgrade.
package update

import (
	"context"
	"net/http"
	"time"
)

// STUB: the u1 test writer's placeholder so the tests compile red. The implementer replaces everything below.

// Clock is the checker's source of time; tests drive it by hand.
type Clock interface {
	Now() time.Time
	After(d time.Duration) <-chan time.Time
}

// Command is one upgrade step to show the user.
type Command struct {
	Label   string `json:"label"`
	Command string `json:"command"`
}

// Install says how this Admiral was installed and the commands that upgrade it.
type Install struct {
	Method      string // homebrew, script or source
	Commands    []Command
	RestartNote string
}

type Options struct {
	BaseURL string       // the releases API's origin; default https://api.github.com
	Client  *http.Client // default has a request timeout
	Clock   Clock        // default is the real one
	Version string       // the running version
	Exe     string       // the resolved executable path
	// Enabled is read at every tick; Changed (may be nil) says the answer may have changed.
	Enabled func() bool
	Changed <-chan struct{}
}

type Checker struct{}

func New(Options) *Checker { return &Checker{} }

// Run blocks until ctx ends.
func (c *Checker) Run(ctx context.Context) { <-ctx.Done() }

// Routes registers GET /api/update.
func (c *Checker) Routes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/update", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotImplemented)
		_, _ = w.Write([]byte(`{"error":"not implemented"}` + "\n"))
	})
}

// Available is whether latest (a bare x.y.z) is newer than the running version's base; unknown running versions give false.
func Available(running, latest string) bool { return false }

// InstallFor derives the install method and upgrade commands from the running version and the resolved executable path.
func InstallFor(version, exe string) Install { return Install{} }
