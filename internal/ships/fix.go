package ships

import (
	"context"
	"maps"
	"net/http"
	"time"

	"github.com/jbuffin/admiral/internal/launcher"
	"github.com/jbuffin/admiral/internal/moor"
)

// FixOffer is the Fix PR / Fix stack a moored ship is offered: the round's PR, the command the wake sends and each PR's merge problems.
type FixOffer struct {
	PR       string            `json:"pr"`
	Command  string            `json:"command"`
	Problems []moor.PRProblems `json:"problems"`
}

// fixRound decides the whole-ship fix round for rec from the wake state and answers given. The caller holds no lock.
func (s *Service) fixRound(rec moor.Record, state moor.WakeState, answers map[string]moor.Status) (moor.FixRound, bool) {
	return moor.DecideFixRound(moor.WakeInput{Record: rec, State: state, Answers: answers, Reviewers: s.wakeReviewers(rec), Now: time.Now().UTC()})
}

func (s *Service) setFix(r moor.Record, fix *FixOffer) {
	s.mooredMu.Lock()
	defer s.mooredMu.Unlock()
	if !s.stillMoored(r) {
		return
	}
	p := s.polls()[r.Session]
	p.fix = fix
	s.polls()[r.Session] = p
}

// fixHook is the launch hook of a fix round: it decides again from the stored state, and refuses when the round's PR is not the one the prompt names.
func (s *Service) fixHook(pr string) func(moor.Record, string) (undoFunc, error) {
	return s.roundHook(func(rec moor.Record, cur moor.WakeState) (moor.WakeState, string, *launcher.Error) {
		s.mooredMu.Lock()
		answers := maps.Clone(s.mooredPolls[rec.Session].status)
		s.mooredMu.Unlock()
		round, ok := s.fixRound(rec, cur, answers)
		if !ok || round.PR != pr {
			return moor.WakeState{}, "", &launcher.Error{Status: http.StatusConflict, Msg: "the ship's merge problems changed; try again"}
		}
		return round.Next, round.Feedback, nil
	})
}

// fixWake is the Fix PR / Fix stack wake, started by hand: one feedback round on the lowest PR with a merge problem, for the whole ship.
func (s *Service) fixWake(ctx context.Context, session string) (Ship, *launcher.Error) {
	if _, live := s.find(session); live {
		return Ship{}, &launcher.Error{Status: http.StatusConflict, Msg: "ship " + session + " is live, and only a moored ship can be fixed"}
	}
	rec, lerr := s.wakeable(session, true)
	if lerr != nil {
		return Ship{}, lerr
	}
	state, err := moor.ReadWake(s.Launcher.Home, rec.Name)
	if err != nil {
		return Ship{}, &launcher.Error{Status: http.StatusInternalServerError, Msg: "ship " + session + ": its wake state cannot be read"}
	}
	s.mooredMu.Lock()
	answers := maps.Clone(s.mooredPolls[rec.Session].status)
	s.mooredMu.Unlock()
	round, ok := s.fixRound(rec, state, answers)
	if !ok {
		return Ship{}, &launcher.Error{Status: http.StatusConflict, Msg: "ship " + session + " has no merge problem on its open PRs"}
	}
	pr := round.PR
	return s.wake(session, func(rec moor.Record) string { return answerPrompt(rec, pr) }, "hand", pr, viaOfCtx(ctx), s.fixHook(pr))
}

func (s *Service) serveFix(w http.ResponseWriter, r *http.Request) {
	got, lerr := s.fixWake(r.Context(), r.PathValue("session"))
	if lerr != nil {
		writeError(w, lerr.Status, lerr.Msg)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"ship": got})
}
