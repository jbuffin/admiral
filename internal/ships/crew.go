package ships

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/jbuffin/admiral/internal/launcher"
	"github.com/jbuffin/admiral/internal/sessionhost"
)

// ReasonCrew is the reason of a ship that needs you only because one of its
// crew boats does (the wire status, see wireStatus).
const ReasonCrew = "crew"

// CrewBoat is one crew boat (a teammate) of a ship as sent to the UI. Status is
// the value `admiral crew list --json` calls state. Reason is set only while
// needs_you, LastActivity only after the boat's first hook.
type CrewBoat struct {
	Name         string `json:"name"`
	Harness      string `json:"harness"`
	Dir          string `json:"dir"`
	Status       string `json:"status"`
	Reason       string `json:"reason,omitempty"`
	Launched     string `json:"launched"`
	LastActivity string `json:"last_activity,omitempty"`
	// ExitCode and ExitSignal are set only on an anchored boat (a signal wins).
	ExitCode   *int    `json:"exit_code,omitempty"`
	ExitSignal *string `json:"exit_signal,omitempty"`
}

// crewBoats is the wire form of a session's crew, never nil.
func crewBoats(l []sessionhost.Crew) []CrewBoat {
	out := make([]CrewBoat, 0, len(l))
	for _, c := range l {
		b := CrewBoat{Name: c.Name, Harness: c.Harness, Dir: c.Dir, Status: c.State, Reason: c.Reason, Launched: c.Launched}
		b.ExitCode, b.ExitSignal = CrewExit(c)
		if !c.LastActivity.IsZero() {
			b.LastActivity = c.LastActivity.UTC().Format(time.RFC3339)
		}
		out = append(out, b)
	}
	return out
}

// CrewExit is how an anchored crew boat ended: a signal, else the exit code. A
// crew boat anchors whatever its exit code, so 0 is reported too. Both are nil
// while it lives.
func CrewExit(c sessionhost.Crew) (code *int, sig *string) {
	if c.State != sessionhost.CrewAnchored {
		return nil, nil
	}
	if c.Signal != "" {
		n := strings.TrimPrefix(strings.ToUpper(c.Signal), "SIG")
		return nil, &n
	}
	n := c.ExitStatus
	return &n, nil
}

// crewLive counts the crew boats with a live pane: underway or needs you.
func crewLive(crew []CrewBoat) int {
	n := 0
	for _, b := range crew {
		if b.Status != sessionhost.CrewAnchored {
			n++
		}
	}
	return n
}

// wireStatus is the status, reason and stop time the UI gets for a ship: its own
// status, except that an underway ship with a crew boat that needs you is
// needs_you with reason "crew", since the inbox is built from the ship's status.
// since is the earliest, over the needing boats, of the boat's last_activity or
// else its launch time, as those strings stand; never the clock, which would
// restart the inbox entry's age on every push. ok says the crew changed the
// status; otherwise the caller keeps the ship's own.
func wireStatus(own string, crew []CrewBoat) (status, reason, since string, ok bool) {
	if own != StatusUnderway {
		return own, "", "", false
	}
	var first time.Time
	for _, b := range crew {
		if b.Status != sessionhost.CrewNeedsYou {
			continue
		}
		ok = true
		stamp := b.LastActivity
		if stamp == "" {
			stamp = b.Launched
		}
		if t, err := time.Parse(time.RFC3339, stamp); err == nil && (first.IsZero() || t.Before(first)) {
			first, since = t, stamp
		}
	}
	if !ok {
		return own, "", "", false
	}
	return StatusNeedsYou, ReasonCrew, since, true
}

// crewSignature is what a push must follow: each crew boat, its status and
// reason, and the subagent ids. last_activity stays out, since every hook
// restamps it; it rides along on the next push.
func crewSignature(sh Ship) string {
	var b strings.Builder
	for _, c := range sh.Crew {
		b.WriteString("|" + c.Name + "=" + c.Status + ":" + c.Reason)
	}
	for _, a := range sh.Subagents {
		b.WriteString("|" + a.ID)
	}
	b.WriteString("|live=" + strconv.Itoa(sh.CrewLive))
	return b.String()
}

// Crew is the crew boats of a ship as tmux has them, with their pane and window
// ids, for the bridge. ok is false when the session is not a ship.
func (s *Service) Crew(session string) ([]sessionhost.Crew, bool) {
	sess, ok := s.find(session)
	if !ok {
		return nil, false
	}
	return sess.Crew, true
}

// CrewInputSeen records that a browser sent input to the crew boat in a window:
// it clears that boat's needs you, by pane id, and returns the boat as it is
// now. ErrNotFound when the boat is gone, ErrExited when it is anchored. The
// ship's own status doesn't matter: an anchored ship keeps its live crew.
func (s *Service) CrewInputSeen(session, windowID string) (sessionhost.Crew, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	find := func() (sessionhost.Crew, bool) {
		l, _ := s.Crew(session)
		for _, c := range l {
			if c.WindowID == windowID {
				return c, true
			}
		}
		return sessionhost.Crew{}, false
	}
	c, ok := find()
	if !ok {
		return sessionhost.Crew{}, ErrNotFound
	}
	switch c.State {
	case sessionhost.CrewAnchored:
		return c, ErrExited
	case sessionhost.CrewNeedsYou:
		if err := s.Host.SetPaneWorking(ctx, c.PaneID); err != nil {
			return sessionhost.Crew{}, err
		}
		_ = s.Host.Sync(ctx)
		s.broadcast()
		if c, ok = find(); !ok {
			return sessionhost.Crew{}, ErrNotFound
		}
	}
	return c, nil
}

// serveCrewClose kills one crew boat's window, live or anchored, as
// `admiral crew close` does; the UI decides when to offer it.
func (s *Service) serveCrewClose(w http.ResponseWriter, r *http.Request) {
	session, name := r.PathValue("session"), r.PathValue("name")
	// Not the request's context: a client that hangs up must not abandon the write.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	sess, ok := s.find(session)
	if !ok {
		writeError(w, http.StatusNotFound, "ship "+session+" not found")
		return
	}
	windowID := ""
	for _, c := range sess.Crew {
		if c.Name == name {
			windowID = c.WindowID
		}
	}
	if windowID == "" {
		writeError(w, http.StatusNotFound, "crew boat "+name+" not found")
		return
	}
	switch err := s.Launcher.CloseCrew(ctx, windowID); {
	case errors.Is(err, launcher.ErrCrewGone):
		writeError(w, http.StatusNotFound, "crew boat "+name+" not found")
		return
	case err != nil:
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	_ = s.Host.Sync(ctx)
	s.broadcast()
	writeJSON(w, http.StatusOK, map[string]any{"closed": name})
}
