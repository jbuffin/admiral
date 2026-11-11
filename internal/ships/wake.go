package ships

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/jbuffin/admiral/internal/launcher"
	"github.com/jbuffin/admiral/internal/moor"
	"github.com/jbuffin/admiral/internal/outcomes"
)

// Wake relaunches a moored ship by hand, with the engineer's optional text after its resume text.
// Like a hand launch it counts toward the cap and is never blocked by it.
func (s *Service) Wake(ctx context.Context, session, text string) (Ship, *launcher.Error) {
	prompt := func(rec moor.Record) string {
		if t := strings.TrimSpace(text); t != "" {
			return rec.Resume + " " + t
		}
		return rec.Resume
	}
	return s.wake(session, prompt, "hand", "", nil)
}

// FeedbackRound relaunches a moored ship, unattended, for the feedback on one of its PRs. The text reaches
// the ship only as a file in its checkout; at the cap or on a harness with no guard it is refused.
func (s *Service) FeedbackRound(ctx context.Context, session, pr, feedback string) (Ship, *launcher.Error) {
	prompt := func(rec moor.Record) string {
		return rec.Resume + " feedback round " + strconv.Itoa(rec.Round+1) + " on " + pr
	}
	write := func(rec moor.Record) error {
		_, err := moor.WriteFeedback(rec.Checkout, rec.Round+1, feedback)
		return err
	}
	return s.wake(session, prompt, "feedback", pr, write)
}

// wake is the one relaunch: a moored ship's session is recreated under its name with its record's fleet,
// harness, model, skill, checkout and resources, and the record then hands over to tmux.
func (s *Service) wake(session string, prompt func(moor.Record) string, by, pr string, beforeLaunch func(moor.Record) error) (Ship, *launcher.Error) {
	rec, lerr := s.wakeable(session)
	if lerr != nil {
		return Ship{}, lerr
	}
	if by == "feedback" && !slices.Contains(rec.PRs, pr) {
		return Ship{}, &launcher.Error{Status: http.StatusBadRequest, Msg: "ship " + session + " carries no PR " + pr}
	}
	if !s.startWaking(session) {
		return Ship{}, &launcher.Error{Status: http.StatusConflict, Msg: "a wake of ship " + session + " is already in progress"}
	}
	defer s.endWaking(session)
	if !s.Host.Known() {
		return Ship{}, &launcher.Error{Status: http.StatusServiceUnavailable, Msg: NotKnownMsg}
	}
	round := rec.Round + 1
	req := launcher.Request{Fleet: rec.Fleet, Harness: rec.Harness, Dir: rec.Checkout, Model: rec.Model,
		Skill: launcher.Skill{Kind: rec.Skill.Kind, Name: rec.Skill.Name}, Source: rec.Source, OrderID: rec.OrderID,
		Title: rec.Title, URL: rec.URL, Issues: rec.Issues, Panel: rec.Panel, Resources: rec.Resources,
		Name: rec.Name, Session: rec.Session, Round: round, Prompt: prompt(rec), Autopilot: by == "feedback"}
	if req.Autopilot {
		s.admitMu.Lock()
		defer s.admitMu.Unlock()
	}
	release, lerr := s.admit(req)
	if lerr != nil {
		return Ship{}, lerr
	}
	if beforeLaunch != nil {
		if err := beforeLaunch(rec); err != nil {
			release()
			return Ship{}, &launcher.Error{Status: http.StatusInternalServerError, Msg: "the feedback cannot be written into the ship's checkout"}
		}
	}
	got, lerr := s.LaunchOrder(req)
	if lerr != nil {
		release()
		return Ship{}, lerr
	}
	s.releaseWhenListed(got.Session, release)
	fields := map[string]any{"round": round, "by": by}
	if pr != "" {
		fields["pr"] = pr
	}
	s.record(outcomes.Record{Ship: got.Session, Event: "woken", Fields: fields}, nil)
	s.dropMoored(session)
	s.broadcast()
	return got, nil
}

// wakeable is the record of a ship that can be woken: 404 when it has none and is not live, 409 for a live
// ship or a record with no resume text to prompt with.
func (s *Service) wakeable(session string) (moor.Record, *launcher.Error) {
	s.mooredMu.Lock()
	s.loadMoored()
	var rec moor.Record
	found := false
	for _, r := range s.moored {
		if r.Session == session {
			rec, found = r, true
		}
	}
	s.mooredMu.Unlock()
	sess, live := s.find(session)
	switch {
	case live && found:
		return rec, &launcher.Error{Status: http.StatusConflict, Msg: "ship " + session + " is live, and only a moored ship can be woken"}
	case live:
		return rec, &launcher.Error{Status: http.StatusConflict, Msg: "ship " + session + " is " + status(sess, time.Now()) + ", and only a moored ship can be woken"}
	case !found:
		return rec, &launcher.Error{Status: http.StatusNotFound, Msg: "ship " + session + " not found"}
	case strings.TrimSpace(rec.Resume) == "":
		return rec, &launcher.Error{Status: http.StatusConflict, Msg: "ship " + session + " has no resume text in its report, so it cannot be woken"}
	}
	return rec, nil
}

func (s *Service) startWaking(session string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.waking[session] {
		return false
	}
	if s.waking == nil {
		s.waking = map[string]bool{}
	}
	s.waking[session] = true
	return true
}

func (s *Service) endWaking(session string) {
	s.mu.Lock()
	delete(s.waking, session)
	s.mu.Unlock()
}

// dropMoored removes the record of a ship that is live again: tmux is its record now.
func (s *Service) dropMoored(session string) {
	s.mooredMu.Lock()
	defer s.mooredMu.Unlock()
	s.loadMoored()
	for i, r := range s.moored {
		if r.Session != session {
			continue
		}
		if err := moor.Remove(s.Launcher.Home, r.Name); err != nil {
			return // kept: the live session hides it from the list, and the next moor replaces it
		}
		s.moored = slices.Delete(s.moored, i, i+1)
		delete(s.mooredPolls, session)
		return
	}
}

func (s *Service) serveWake(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Text string `json:"text"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&body); err != nil && !errors.Is(err, io.EOF) {
		writeError(w, http.StatusBadRequest, "request is not valid JSON: "+err.Error())
		return
	}
	got, lerr := s.Wake(r.Context(), r.PathValue("session"), body.Text)
	if lerr != nil {
		writeError(w, lerr.Status, lerr.Msg)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"ship": got})
}
