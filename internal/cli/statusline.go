package cli

import "time"

// Stub so the status-line tests compile and fail on behavior; the implementation replaces it.
type statuslineEnv struct {
	Pane        string
	ConfigDir   string // $CLAUDE_CONFIG_DIR of the wrapper's own env
	Home        string
	ManagedPath string // the managed settings file
	Timeout     time.Duration
	Now         func() time.Time
	Write       func(call []string) error
}

func Statusline(args []string, std IO) error { return nil }

func runStatusline(args []string, std IO, env statuslineEnv) error { return nil }
