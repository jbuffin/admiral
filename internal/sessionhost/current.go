package sessionhost

import (
	"context"
	"os/exec"
	"strings"
	"time"
)

// The hooks (`admiral hook`) run inside a ship's pane and act on the tmux server
// that pane lives on, through $TMUX: a bare `tmux` found on the PATH the launcher
// gave the ship, with no -S and no -f (ADR 0012). They need no socket path, no
// Host and no running Admiral. The builders below return the argument list of
// one tmux command; Join strings them into one call, and WriteCurrent runs it.

// CurrentDeadline bounds each read on the current server, so a stuck server
// can't stall a hook (the final write has no deadline).
const CurrentDeadline = 2 * time.Second

// The pane options only the hooks write.
const (
	// HarnessOption is the ship's harness, set on its session at launch.
	HarnessOption = Prefix + "harness"
	// LaunchedOption is the window's launch time, RFC 3339.
	LaunchedOption = Prefix + "launched"
	// CrewHeldOption marks a lead pane whose Stop waits on working crew (#189).
	CrewHeldOption = Prefix + "crew-held"
	// MonitorsOption holds the task ids of the Monitors a Claude session armed and
	// that may still run, space-separated (#110).
	MonitorsOption = Prefix + "monitors"
)

// Join is the calls as one tmux invocation: tmux reads ";" between them as a
// separator. An empty call is skipped.
func Join(calls ...[]string) []string {
	var out []string
	for _, c := range calls {
		if len(c) == 0 {
			continue
		}
		if len(out) > 0 {
			out = append(out, ";")
		}
		out = append(out, c...)
	}
	return out
}

// SetPaneCall sets a pane option.
func SetPaneCall(pane, option, value string) []string {
	return []string{"set", "-p", "-t", pane, option, value}
}

// UnsetPaneCall unsets a pane option, whether it is set or not.
func UnsetPaneCall(pane, option string) []string {
	return []string{"set", "-p", "-u", "-q", "-t", pane, option}
}

// StateCall writes a pane's reason and then its state, reason first (for working
// too, as Host.setPane does) so the poll never sees needs_you without it. An empty
// reason unsets it. With ifUnset the reason is set only if none is (-o), so
// permission:Bash survives Claude's PermissionRequest and Notification in either
// order; -q keeps a set reason from making tmux skip the state write.
func StateCall(pane, reason string, ifUnset bool, state string) []string {
	reasonCall := []string{"set", "-p"}
	switch {
	case reason == "":
		reasonCall = append(reasonCall, "-u", "-t", pane, ReasonOption)
	case ifUnset:
		reasonCall = append(reasonCall, "-o", "-q", "-t", pane, ReasonOption, reason)
	default:
		reasonCall = append(reasonCall, "-t", pane, ReasonOption, reason)
	}
	return Join(reasonCall, SetPaneCall(pane, StateOption, state))
}

// CrewTranscriptCall records a crew boat's transcript path in its pane option,
// but only where the pane's window has a crew name (`if -F`), so a ship's pane and
// the chat never get it. The caller has checked pane and path: the path needs no
// escaping, since ValidCrewTranscriptPath refuses every character that would.
func CrewTranscriptCall(pane, path string) []string {
	return []string{"if", "-F", "-t", pane, CrewGateFormat,
		"set -p -t " + pane + " " + CrewTranscriptOption + " '" + path + "'"}
}

// CurrentHarnessCall reads the harness of the pane's ship.
func CurrentHarnessCall(pane string) []string {
	return []string{"display", "-p", "-t", pane, "#{" + HarnessOption + "}"}
}

// PaneOptionCall reads one pane option on its own (not the format's fall back to
// the window's and the session's).
func PaneOptionCall(pane, option string) []string {
	return []string{"show-options", "-pqv", "-t", pane, option}
}

// SessionPanesCall lists every pane of the pane's session, a row each:
// pane id, pane_dead, crew name, launched, activity and crew-held, joined by
// RowSeparator.
func SessionPanesCall(pane string) []string {
	return []string{"list-panes", "-s", "-t", pane, "-F", strings.Join([]string{"#{pane_id}", "#{pane_dead}", CrewNameFormat,
		"#{" + LaunchedOption + "}", "#{" + ActivityOption + "}", "#{" + CrewHeldOption + "}"}, RowSeparator)}
}

// RowSeparator separates the fields of a row SessionPanesCall reads.
const RowSeparator = sep

// ReadCurrent runs a read on the current server and returns its stdout without
// the trailing newline. -u keeps the unit separator in a format intact. It gives
// up after CurrentDeadline.
func ReadCurrent(args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), CurrentDeadline)
	defer cancel()
	out, err := exec.CommandContext(ctx, "tmux", append([]string{"-u"}, args...)...).Output()
	return strings.TrimRight(string(out), "\n"), err
}

// WriteCurrent runs a write on the current server. It has no deadline and
// reports nothing a hook could act on beyond the error.
func WriteCurrent(args []string) error {
	return exec.Command("tmux", args...).Run()
}
