package sessionhost

import (
	"context"
	"os/exec"
	"strings"
	"time"
)

// The hooks run inside a ship's pane and reach its tmux server through $TMUX, with no -S
// and no -f, so they need no socket path and no running Admiral.

// CurrentDeadline bounds each read on the current server, so a stuck server
// can't stall a hook (the final write has no deadline).
const CurrentDeadline = 2 * time.Second

// The pane options only the hooks write.
const (
	HarnessOption = Prefix + "harness"
	// LaunchedOption is RFC 3339.
	LaunchedOption = Prefix + "launched"
	// CrewHeldOption marks a lead pane whose Stop waits on working crew.
	CrewHeldOption = Prefix + "crew-held"
	// MonitorsOption holds the space-separated task ids of the Monitors a Claude session
	// armed that may still run.
	MonitorsOption = Prefix + "monitors"
)

// Join skips an empty call.
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

func SetPaneCall(pane, option, value string) []string {
	return []string{"set", "-p", "-t", pane, option, value}
}

func UnsetPaneCall(pane, option string) []string {
	return []string{"set", "-p", "-u", "-q", "-t", pane, option}
}

// The reason goes first so the poll never sees needs_you without it. ifUnset (-o) keeps
// permission:Bash whichever order Claude's PermissionRequest and Notification come in;
// -q keeps a set reason from making tmux skip the state write.
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

// CrewTranscriptCall sets the option only where the pane's window has a crew name, so a
// ship's pane and the chat never get it. ValidCrewTranscriptPath leaves nothing to escape.
func CrewTranscriptCall(pane, path string) []string {
	return []string{"if", "-F", "-t", pane, CrewGateFormat,
		"set -p -t " + pane + " " + CrewTranscriptOption + " '" + path + "'"}
}

func CurrentHarnessCall(pane string) []string {
	return []string{"display", "-p", "-t", pane, "#{" + HarnessOption + "}"}
}

// PaneOptionCall does not fall back to the window's and the session's option.
func PaneOptionCall(pane, option string) []string {
	return []string{"show-options", "-pqv", "-t", pane, option}
}

// SessionPanesCall rows: pane id, pane_dead, crew name, launched, activity, crew-held.
func SessionPanesCall(pane string) []string {
	return []string{"list-panes", "-s", "-t", pane, "-F", strings.Join([]string{"#{pane_id}", "#{pane_dead}", CrewNameFormat,
		"#{" + LaunchedOption + "}", "#{" + ActivityOption + "}", "#{" + CrewHeldOption + "}"}, RowSeparator)}
}

const RowSeparator = sep

// -u keeps the unit separator in a format intact.
func ReadCurrent(args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), CurrentDeadline)
	defer cancel()
	out, err := exec.CommandContext(ctx, "tmux", append([]string{"-u"}, args...)...).Output()
	return strings.TrimRight(string(out), "\n"), err
}

func WriteCurrent(args []string) error {
	return exec.Command("tmux", args...).Run()
}
