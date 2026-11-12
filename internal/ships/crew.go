package ships

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/jbuffin/admiral/internal/sessionhost"
)

// ReasonCrew: the ship needs you only because one of its crew boats does.
const ReasonCrew = "crew"

// CrewBoat's Status is what `admiral crew list --json` calls state. Reason is set
// only while needs_you, LastActivity only after the boat's first hook.
type CrewBoat struct {
	Name         string `json:"name"`
	Harness      string `json:"harness"`
	Dir          string `json:"dir"`
	Status       string `json:"status"`
	Reason       string `json:"reason,omitempty"`
	Role         string `json:"role,omitempty"`
	Launched     string `json:"launched"`
	LastActivity string `json:"last_activity,omitempty"`
	// Conversation: a Claude Code, Codex or Copilot boat with a recorded transcript. The path itself stays off the API.
	Conversation bool `json:"conversation"`
	// ExitCode and ExitSignal are set only on an anchored boat (a signal wins).
	ExitCode   *int    `json:"exit_code,omitempty"`
	ExitSignal *string `json:"exit_signal,omitempty"`
}

// HasConversation is whether Admiral reads a harness's own record.
func HasConversation(harness string) bool {
	return harness == "claude" || harness == "copilot" || harness == "codex"
}

// crewBoats never returns nil.
func crewBoats(l []sessionhost.Crew) []CrewBoat {
	out := make([]CrewBoat, 0, len(l))
	for _, c := range l {
		b := CrewBoat{Name: c.Name, Harness: c.Harness, Dir: c.Dir, Status: c.State, Reason: c.Reason, Role: c.Role, Launched: c.Launched,
			Conversation: HasConversation(c.Harness) && c.Transcript != ""}
		b.ExitCode, b.ExitSignal = CrewExit(c)
		if !c.LastActivity.IsZero() {
			b.LastActivity = c.LastActivity.UTC().Format(time.RFC3339)
		}
		out = append(out, b)
	}
	return out
}

// CrewExit reports 0 too: a crew boat anchors whatever its exit code.
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

func crewLive(crew []CrewBoat) int {
	n := 0
	for _, b := range crew {
		if b.Status != sessionhost.CrewAnchored {
			n++
		}
	}
	return n
}

// wireStatus makes an underway ship with a needing crew boat needs_you, since the
// inbox is built from the ship's status. since never uses the clock, which would
// restart the inbox entry's age on every push.
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

// crewSignature leaves last_activity out, since every hook restamps it; it rides
// along on the next push.
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

func (s *Service) Crew(session string) ([]sessionhost.Crew, bool) {
	sess, ok := s.find(session)
	if !ok {
		return nil, false
	}
	return sess.Crew, true
}

// CrewInputSeen ignores the ship's own status: an anchored ship keeps its live crew.
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

// serveCrewClose closes live or anchored boats; the UI decides when to offer it.
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
	var boat sessionhost.Crew
	for _, c := range sess.Crew {
		if c.Name == name {
			boat = c
		}
	}
	if boat.WindowID == "" {
		writeError(w, http.StatusNotFound, "crew boat "+name+" not found")
		return
	}
	switch err := s.Launcher.CloseCrew(ctx, boat); {
	case errors.Is(err, sessionhost.ErrWindowGone):
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
