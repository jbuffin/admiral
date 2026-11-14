package ships

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"net/http"
	"os"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/jbuffin/admiral/internal/inbox"
	"github.com/jbuffin/admiral/internal/launcher"
	"github.com/jbuffin/admiral/internal/moor"
)

// Wait texts are what a moored ship shows while a refusal that a later poll can clear holds its round back.
const (
	WaitCap      = "Waiting for room under the cap"
	WaitHost     = "Waiting for Admiral's terminal host"
	WaitInFlight = "Waiting for the current wake to finish"
)

// undoFunc puts back what a launch hook wrote, when the launch fails after the hook ran.
type undoFunc func()

// Wake relaunches a moored ship by hand, with the engineer's optional text after its resume text.
// Like a hand launch it counts toward the cap and is never blocked by it.
func (s *Service) Wake(ctx context.Context, session, text string) (Ship, *launcher.Error) {
	prompt := func(rec moor.Record) string {
		if t := strings.TrimSpace(text); t != "" {
			return rec.Resume + " " + t
		}
		return rec.Resume
	}
	return s.wake(session, prompt, "hand", "", viaOfCtx(ctx), s.handRoundStart)
}

// FeedbackRound relaunches a moored ship, unattended, for the feedback on one of its PRs. The text reaches
// the ship only as a file in its checkout; at the cap or on a harness with no guard it is refused. next is the
// wake state the round starts from: it is saved once the launch is certain to be tried, and put back if the launch fails.
func (s *Service) FeedbackRound(ctx context.Context, session, pr, feedback string, next moor.WakeState) (Ship, *launcher.Error) {
	return s.feedbackRound(session, pr, feedback, nil, next)
}

// feedbackRound is FeedbackRound for a decision made from the wake state was: if the stored state has changed
// by the time the launch is certain, the round is refused and the next poll decides again from the new one.
func (s *Service) feedbackRound(session, pr, feedback string, was *moor.WakeState, next moor.WakeState) (Ship, *launcher.Error) {
	prompt := func(rec moor.Record) string {
		return answerPrompt(rec, pr)
	}
	write := func(rec moor.Record, dir string) (undoFunc, error) {
		undo, err := s.saveWake(rec.Name, func(cur moor.WakeState) (moor.WakeState, *launcher.Error) {
			if was != nil && !sameWake(cur, *was) {
				return moor.WakeState{}, &launcher.Error{Status: http.StatusConflict, Msg: "the ship's wake state changed since the round was decided", Wait: WaitInFlight}
			}
			return next, nil
		})
		if err != nil {
			return nil, err
		}
		if _, err := moor.WriteFeedback(dir, rec.Round+1, feedback); err != nil {
			undo()
			return nil, err
		}
		return undo, nil
	}
	return s.wake(session, prompt, "feedback", pr, "", write)
}

// handRoundStart is the hand wake's hook: it opens the round's window, and gives a PR with no cursor the old
// mooring as its boundary, so feedback that queues during the round survives the next mooring. A wake state
// that cannot be read or saved costs the round its window and never blocks the wake.
func (s *Service) handRoundStart(rec moor.Record, _ string) (undoFunc, error) {
	now := time.Now().UTC()
	undo, err := s.saveWake(rec.Name, func(cur moor.WakeState) (moor.WakeState, *launcher.Error) {
		out := moor.WakeState{PRs: map[string]moor.PRWake{}}
		for k, v := range cur.PRs {
			out.PRs[k] = v
		}
		for _, pr := range rec.PRs {
			ps := out.PRs[pr]
			if ps.Cursor.At.IsZero() {
				ps.Cursor = moor.Cursor{At: rec.MooredAt}
			}
			ps.RoundStart = now
			out.PRs[pr] = ps
		}
		return out, nil
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "admiral: ship %s: waking by hand without a round window: %v\n", rec.Name, err)
		return nil, nil
	}
	return undo, nil
}

// saveWake replaces a ship's wake state with change of the stored one, under wakeMu, and returns what puts the old one back.
// change refuses by returning an error, and then nothing is written.
func (s *Service) saveWake(name string, change func(moor.WakeState) (moor.WakeState, *launcher.Error)) (undoFunc, error) {
	s.wakeMu.Lock()
	defer s.wakeMu.Unlock()
	prev, err := moor.ReadWake(s.Launcher.Home, name)
	if err != nil {
		return nil, &launcher.Error{Status: http.StatusInternalServerError, Msg: "the ship's wake state cannot be read"}
	}
	next, lerr := change(prev)
	if lerr != nil {
		return nil, lerr
	}
	if err := moor.WriteWake(s.Launcher.Home, name, next); err != nil {
		return nil, &launcher.Error{Status: http.StatusInternalServerError, Msg: "the ship's wake state cannot be saved"}
	}
	return func() {
		s.wakeMu.Lock()
		defer s.wakeMu.Unlock()
		var err error
		if len(prev.PRs) == 0 {
			err = moor.RemoveWake(s.Launcher.Home, name)
		} else {
			err = moor.WriteWake(s.Launcher.Home, name, prev)
		}
		if err != nil {
			fmt.Fprintf(os.Stderr, "admiral: ship %s: its wake state cannot be put back after the failed wake: %v\n", name, err)
		}
	}, nil
}

// removeWake deletes a ship's wake state under wakeMu, so it cannot interleave with a save.
func (s *Service) removeWake(name string) {
	s.wakeMu.Lock()
	defer s.wakeMu.Unlock()
	if err := moor.RemoveWake(s.Launcher.Home, name); err != nil {
		fmt.Fprintf(os.Stderr, "admiral: ship %s: its wake state cannot be removed: %v\n", name, err)
	}
	if err := moor.RemoveRounds(s.Launcher.Home, name); err != nil {
		fmt.Fprintf(os.Stderr, "admiral: ship %s: its round history cannot be removed: %v\n", name, err)
	}
	if err := inbox.RemoveWakeProposals(s.Launcher.Home, name); err != nil {
		fmt.Fprintf(os.Stderr, "admiral: ship %s: its wake proposals cannot be removed: %v\n", name, err)
	}
}

// wake is the one relaunch: a moored ship's session is recreated under its name with its record's fleet,
// harness, model, skill, checkout and resources, and the record then hands over to tmux.
func (s *Service) wake(session string, prompt func(moor.Record) string, by, pr, wokenVia string, beforeLaunch func(rec moor.Record, dir string) (undoFunc, error)) (Ship, *launcher.Error) {
	if !s.startWaking(session) {
		return Ship{}, &launcher.Error{Status: http.StatusConflict, Msg: "a wake of ship " + session + " is already in progress", Wait: WaitInFlight}
	}
	defer s.endWaking(session)
	rec, lerr := s.wakeable(session)
	if lerr != nil {
		return Ship{}, lerr
	}
	if (by == "feedback" || by == "proposal") && !slices.Contains(rec.PRs, pr) {
		return Ship{}, &launcher.Error{Status: http.StatusBadRequest, Msg: "ship " + session + " carries no PR " + pr}
	}
	if !s.Host.Known() {
		return Ship{}, &launcher.Error{Status: http.StatusServiceUnavailable, Msg: NotKnownMsg, Wait: WaitHost}
	}
	round := rec.Round + 1
	req := launcher.Request{Fleet: rec.Fleet, Harness: rec.Harness, Dir: rec.Checkout, Model: rec.Model,
		Skill: launcher.Skill{Kind: rec.Skill.Kind, Name: rec.Skill.Name}, Source: rec.Source, OrderID: rec.OrderID,
		Title: rec.Title, URL: rec.URL, Link: rec.Link, Issues: rec.Issues, Panel: rec.Panel, Resources: rec.Resources, PRs: rec.PRs,
		Name: rec.Name, Session: rec.Session, Round: round, Prompt: prompt(rec), Autopilot: by == "feedback" || by == "proposal", Unattended: by == "feedback", By: by, PR: pr, WokenVia: wokenVia, Via: rec.Via, ChatKey: rec.Chat, RoutineKey: rec.Routine, CommitmentKey: rec.Commitment}
	var undo undoFunc
	// The launcher runs this under its lock, just before it makes the session: a Dismiss or a merged PR that
	// ended the mooring while this wake waited wins, and from here on an ending loses to the wake.
	req.Before = func(dir string) error {
		if !s.commitWake(rec) {
			return &launcher.Error{Status: http.StatusConflict, Msg: "ship " + session + " was ended while it waited to be woken"}
		}
		if beforeLaunch != nil {
			u, err := beforeLaunch(rec, dir)
			if err != nil {
				s.uncommitWake(session)
				var le *launcher.Error
				if errors.As(err, &le) {
					return le
				}
				return &launcher.Error{Status: http.StatusInternalServerError, Msg: "the feedback cannot be written into the ship's checkout"}
			}
			undo = u
		}
		return nil
	}
	defer s.uncommitWake(session)
	if req.Autopilot {
		s.admitMu.Lock()
		defer s.admitMu.Unlock()
	}
	release, lerr := s.admit(req)
	if lerr != nil {
		return Ship{}, lerr
	}
	got, lerr := s.LaunchOrder(req)
	if lerr != nil {
		release()
		if undo != nil {
			undo()
		}
		return Ship{}, lerr
	}
	s.releaseWhenListed(got.Session, release)
	s.dropMoored(session, got.Launched)
	s.broadcast()
	return got, nil
}

// commitWake says the record is still the one the wake read, and from then on endMoored leaves it to the wake.
func (s *Service) commitWake(rec moor.Record) bool {
	s.mooredMu.Lock()
	defer s.mooredMu.Unlock()
	s.loadMoored()
	if !s.stillMoored(rec) {
		return false
	}
	if s.committed == nil {
		s.committed = map[string]bool{}
	}
	s.committed[rec.Session] = true
	return true
}

func (s *Service) uncommitWake(session string) {
	s.mooredMu.Lock()
	delete(s.committed, session)
	s.mooredMu.Unlock()
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
	case !moor.ValidResume(rec.Resume):
		return rec, &launcher.Error{Status: http.StatusConflict, Msg: "ship " + session + " has no valid resume text in its report, so it cannot be woken"}
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

// dropMoored removes the record of a ship that is live again: tmux is its record now. The record's last poll answer
// goes to the new launch, which shows it until its first poll.
func (s *Service) dropMoored(session, launched string) {
	s.mooredMu.Lock()
	defer s.mooredMu.Unlock()
	s.loadMoored()
	for i, r := range s.moored {
		if r.Session != session {
			continue
		}
		if err := moor.Remove(s.Launcher.Home, r.Name); err != nil {
			// kept: the live session hides it from the list, and moor replaces a record of an earlier round
			fmt.Fprintf(os.Stderr, "admiral: ship %s: its moored record cannot be removed after the wake: %v\n", r.Name, err)
			return
		}
		s.moored = slices.Delete(s.moored, i, i+1)
		if s.running == nil {
			s.running = map[string]*running{}
		}
		s.running[session] = &running{launched: launched, rejected: map[string]bool{}, tries: map[string]int{}, poll: s.mooredPolls[session]}
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

// answerPrompt is what Answer review sends for a PR of the record.
func answerPrompt(rec moor.Record, pr string) string {
	return rec.Resume + " feedback round " + strconv.Itoa(rec.Round+1) + " on " + pr
}

// answerReview is the feedback round on one PR, started by hand: the automatic round's work for pr, launched attended.
// The state it saves comes from the stored poll answers and the wake state read under wakeMu, so a stale page cannot hand it old events.
func (s *Service) answerReview(ctx context.Context, session, pr string) (Ship, *launcher.Error) {
	prompt := func(rec moor.Record) string { return answerPrompt(rec, pr) }
	var feedback string
	write := func(rec moor.Record, dir string) (undoFunc, error) {
		undo, err := s.saveWake(rec.Name, func(cur moor.WakeState) (moor.WakeState, *launcher.Error) {
			s.mooredMu.Lock()
			answers := maps.Clone(s.mooredPolls[rec.Session].status)
			s.mooredMu.Unlock()
			in := moor.WakeInput{Record: rec, State: cur, Answers: answers, Reviewers: s.wakeReviewers(rec), Now: time.Now().UTC()}
			round, ok := moor.DecideHandRound(in, pr)
			if !ok {
				return moor.WakeState{}, &launcher.Error{Status: http.StatusConflict, Msg: "PR " + pr + " has no new feedback to answer"}
			}
			feedback = round.Feedback
			return round.Next, nil
		})
		if err != nil {
			return nil, err
		}
		if _, err := moor.WriteFeedback(dir, rec.Round+1, feedback); err != nil {
			undo()
			return nil, err
		}
		return undo, nil
	}
	return s.wake(session, prompt, "hand", pr, viaOfCtx(ctx), write)
}

func (s *Service) serveAnswerReview(w http.ResponseWriter, r *http.Request) {
	session := r.PathValue("session")
	var body struct {
		PR string `json:"pr"`
	}
	decodeErr := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&body)
	rec, lerr := s.wakeable(session)
	switch {
	case lerr != nil:
		writeError(w, lerr.Status, lerr.Msg)
		return
	case decodeErr != nil:
		writeError(w, http.StatusBadRequest, "request is not valid JSON: "+decodeErr.Error())
		return
	case !slices.Contains(rec.PRs, body.PR):
		writeError(w, http.StatusBadRequest, "ship "+session+" carries no PR "+body.PR)
		return
	}
	got, lerr := s.answerReview(r.Context(), session, body.PR)
	if lerr != nil {
		writeError(w, lerr.Status, lerr.Msg)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"ship": got})
}
