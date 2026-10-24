package sessionhost

import (
	"os/exec"
	"sort"
	"strings"
	"time"
)

// ActivityOption is the pane option `admiral hook` stamps with the time of every
// hook event (RFC 3339): a crew boat's last_activity.
const ActivityOption = Prefix + "activity"

// The states a crew boat shows (#72 story 9). A crew boat never sails off, and
// is never "working": underway covers both.
const (
	CrewAnchored = "anchored"
	CrewNeedsYou = "needs_you"
	CrewUnderway = "underway"
)

// Crew is a teammate Admiral launched with `admiral crew`: a window of the
// ship's session, recorded in that window's @admiral-* options (ADR 0012, 0014).
type Crew struct {
	Session      string
	Name         string
	Harness      string
	Dir          string
	Launched     string
	State        string    // CrewAnchored, CrewNeedsYou or CrewUnderway
	LastActivity time.Time // zero before the boat's first hook
	PaneID       string
	WindowID     string
}

// CrewState maps a crew pane to its state: anchored whenever the pane is dead,
// whatever the exit code; needs_you when the pane says so; otherwise underway.
func CrewState(paneDead bool, paneState string) string {
	switch {
	case paneDead:
		return CrewAnchored
	case paneState == "needs_you":
		return CrewNeedsYou
	}
	return CrewUnderway
}

// parseCrew reads the crew boats of the list-panes rows, by session and sorted
// by name. A boat with several panes is read from its first.
func parseCrew(rows []string, known map[string]bool) map[string][]Crew {
	out := map[string][]Crew{}
	seen := map[string]bool{}
	for _, row := range rows {
		f := strings.Split(row, sep)
		if len(f) != fixedFields+len(optionNames)+tailFields || !known[f[0]] || f[0] == Internal {
			continue
		}
		tail := f[fixedFields+len(optionNames):]
		if tail[0] == "" || seen[f[0]+"\x00"+tail[0]] {
			continue
		}
		seen[f[0]+"\x00"+tail[0]] = true
		opt := map[string]string{}
		for i, n := range optionNames {
			opt[n] = f[fixedFields+i]
		}
		c := Crew{Session: f[0], Name: tail[0], Harness: opt["harness"], Dir: opt["dir"], Launched: opt["launched"],
			State: CrewState(f[1] == "1", f[6]), PaneID: tail[1], WindowID: tail[2]}
		if t, err := time.Parse(time.RFC3339, tail[3]); err == nil {
			c.LastActivity = t
		}
		out[f[0]] = append(out[f[0]], c)
	}
	for _, l := range out {
		sort.Slice(l, func(i, j int) bool { return l[i].Name < l[j].Name })
	}
	return out
}

// ReadCrew lists the crew boats of one ship's session with one tmux command and
// no control connection, so it needs no server (like ReadOnce). The server and
// `admiral crew list` both read crew here, so they compute the same state.
func ReadCrew(tmux, sock, session string) ([]Crew, error) {
	run := func(args ...string) ([]string, error) {
		out, err := exec.Command(tmux, append([]string{"-f", "/dev/null", "-S", sock, "-u"}, args...)...).Output()
		if err != nil {
			return nil, err
		}
		return strings.Split(strings.TrimRight(string(out), "\n"), "\n"), nil
	}
	rows, err := run("list-panes", "-s", "-t", "="+session+":", "-F", format)
	if err != nil {
		return nil, err
	}
	return parseCrew(rows, map[string]bool{session: true})[session], nil
}

// crewBell is the end of the alert-bell script: a Codex crew boat that rang
// needs you, written on its own pane (the server's bell handling only flags
// ships, and a crew boat's pane no longer counts as the ship's).
func crewBell(tmux, sock string) string {
	t := shQuote(tmux) + " -S " + shQuote(sock)
	return `crew=$(` + t + ` show-options -wqv -t "$2" @admiral-crew-name)` + "\n" +
		`harness=$(` + t + ` show-options -wqv -t "$2" @admiral-harness)` + "\n" +
		`if [ -n "$crew" ] && [ "$harness" = codex ]; then ` + t + ` set-option -p -t "$2" @admiral-reason bell \; set-option -p -t "$2" @admiral-state needs_you; fi` + "\n"
}
