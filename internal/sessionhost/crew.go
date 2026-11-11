package sessionhost

import (
	"os/exec"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

var CrewName = regexp.MustCompile(`^[a-z0-9-]+$`)

// ActivityOption is stamped (RFC 3339) on every hook event: a crew boat's last_activity.
const ActivityOption = Prefix + "activity"

// CrewTranscriptOption is set on crew boats only, never a ship or the chat.
const CrewTranscriptOption = Prefix + "crew-transcript"

// CrewGateFormat is for `if -F`: a bare name is no gate, since tmux reads "0", a valid
// crew name, as false.
const (
	CrewNameFormat = "#{" + Prefix + "crew-name}"
	CrewGateFormat = "#{!=:" + CrewNameFormat + ",}"
)

// A crew boat is never "working": underway covers it. Idle waits for its lead: it holds no
// lead's Stop and flags no ship.
const (
	CrewAnchored = "anchored"
	CrewIdle     = "idle"
	CrewNeedsYou = "needs_you"
	CrewUnderway = "underway"
)

type Crew struct {
	Session      string
	Name         string
	Harness      string
	Dir          string
	Launched     string
	State        string    // CrewAnchored, CrewIdle, CrewNeedsYou or CrewUnderway
	Reason       string    // only while needs_you: the pane's @admiral-reason if it is a safe token, else "unknown"
	LastActivity time.Time // zero before the boat's first hook
	Transcript   string    // the pane's @admiral-crew-transcript when valid (ADR 0021), else empty
	Role         string    // the @admiral-role the launcher gave it, empty when none or not a valid role
	PaneID       string
	WindowID     string
	ExitStatus   int    // valid when State is CrewAnchored and Signal is empty
	Signal       string // the signal that killed the pane, e.g. "kill"; then there is no exit status
}

func CrewState(paneDead bool, paneState string) string {
	switch {
	case paneDead:
		return CrewAnchored
	case paneState == "needs_you":
		return CrewNeedsYou
	case paneState == CrewIdle:
		return CrewIdle
	}
	return CrewUnderway
}

// parseCrew is the one crew rule: parseRows and ReadCrew both use it.
func parseCrew(rows []string, known map[string]bool) map[string][]Crew {
	out, _ := readCrew(rows, known)
	return out
}

func crewClashes(rows []string, known map[string]bool) []string {
	_, clashes := readCrew(rows, known)
	return clashes
}

// A name two windows of one session claim is read for neither: a boat can rewrite its own
// @admiral-crew-name, and must not win a name from the boat that launched with it.
func readCrew(rows []string, known map[string]bool) (map[string][]Crew, []string) {
	out := map[string][]Crew{}
	seen := map[string]string{} // session NUL name -> the window that claimed it
	clash := map[string]bool{}
	for _, row := range rows {
		f := strings.Split(row, sep)
		if len(f) != fixedFields+len(optionNames)+tailFields || !known[f[0]] || f[0] == Internal {
			continue
		}
		tail := f[fixedFields+len(optionNames):]
		key := f[0] + "\x00" + tail[0]
		if !CrewName.MatchString(tail[0]) {
			continue
		}
		if win, dup := seen[key]; dup {
			if win != tail[2] { // another window; a second pane of the same window is the same boat
				clash[key] = true
			}
			continue
		}
		seen[key] = tail[2]
		opt := map[string]string{}
		for i, n := range optionNames {
			opt[n] = f[fixedFields+i]
		}
		c := Crew{Session: f[0], Name: tail[0], Harness: opt["harness"], Dir: opt["dir"], Launched: opt["launched"],
			State: CrewState(f[1] == "1", f[6]), PaneID: tail[1], WindowID: tail[2]}
		if CrewName.MatchString(tail[4]) { // option text is untrusted
			c.Role = tail[4]
		}
		if ValidCrewTranscriptPath(tail[5]) { // option text is untrusted
			c.Transcript = tail[5]
		}
		if c.State == CrewNeedsYou {
			c.Reason = "unknown"
			if SafeToken.MatchString(f[7]) {
				c.Reason = f[7]
			}
		}
		if c.State == CrewAnchored {
			c.Signal = f[3]
			c.ExitStatus, _ = strconv.Atoi(f[2])
		}
		if t, err := time.Parse(time.RFC3339, tail[3]); err == nil {
			c.LastActivity = t
		}
		out[f[0]] = append(out[f[0]], c)
	}
	var clashes []string
	for key := range clash {
		session, name, _ := strings.Cut(key, "\x00")
		clashes = append(clashes, session+"/"+name)
		kept := out[session][:0]
		for _, c := range out[session] {
			if c.Name != name {
				kept = append(kept, c)
			}
		}
		out[session] = kept
	}
	sort.Strings(clashes)
	for _, l := range out {
		sort.Slice(l, func(i, j int) bool { return l[i].Name < l[j].Name })
	}
	return out, clashes
}

// The server and `admiral crew list` both read crew here, so they compute the same state.
func ReadCrew(tmux, sock, session string) ([]Crew, error) {
	run := func(args ...string) ([]string, error) {
		out, err := exec.Command(tmux, append([]string{"-f", "/dev/null", "-S", sock, "-u"}, args...)...).Output()
		if err != nil {
			return nil, err
		}
		return strings.Split(strings.TrimRight(string(out), "\n"), "\n"), nil
	}
	nonce := newNonce()
	rows, err := run("list-panes", "-s", "-t", "="+session+":", "-F", rowFormat(nonce))
	if err != nil {
		return nil, err
	}
	rows = ownRows(rows, nonce)
	return parseCrew(rows, map[string]bool{session: true})[session], nil
}

// The server's bell handling only flags ships, so a Codex crew boat that rang is marked
// needs you on its own pane here.
func crewBell(tmux, sock string) string {
	t := shQuote(tmux) + " -S " + shQuote(sock)
	return `crew=$(` + t + ` show-options -wqv -t "$2" @admiral-crew-name)` + "\n" +
		`harness=$(` + t + ` show-options -wqv -t "$2" @admiral-harness)` + "\n" +
		`if [ -n "$crew" ] && [ "$harness" = codex ]; then ` + t + ` set-option -p -t "$2" @admiral-reason bell \; set-option -p -t "$2" @admiral-state needs_you; fi` + "\n"
}
