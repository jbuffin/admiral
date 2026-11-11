package ships

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"slices"
	"time"

	"github.com/jbuffin/admiral/internal/delay"
	"github.com/jbuffin/admiral/internal/launcher"
	"github.com/jbuffin/admiral/internal/moor"
	"github.com/jbuffin/admiral/internal/outcomes"
)

// MoorPoll is how often every moored ship's PRs are polled.
var MoorPoll = delay.From("ADMIRAL_MOOR_POLL", 2*time.Minute)

// polled is what the last answered poll said about one moored ship's PRs.
type polled struct {
	status map[string]moor.Status // a URL left out is NOT_FOUND
	at     time.Time              // the last poll that answered
	stale  bool                   // the latest poll failed
	// waiting is why a due feedback round has not started; a new answer replaces the whole value, so it clears
	waiting string
}

func (s *Service) kickChan() chan struct{} {
	s.mooredMu.Lock()
	defer s.mooredMu.Unlock()
	if s.moorKick == nil {
		s.moorKick = make(chan struct{}, 1)
	}
	return s.moorKick
}

// RefreshMoored asks the poller for a poll now; a kick during a poll coalesces with the next.
func (s *Service) RefreshMoored() {
	select {
	case s.kickChan() <- struct{}{}:
	default:
	}
}

func (s *Service) RunMoored(ctx context.Context) {
	kick := s.kickChan()
	tick := time.NewTicker(MoorPoll)
	defer tick.Stop()
	for {
		s.PollMoored(ctx)
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		case <-kick:
		}
	}
}

// PollMoored runs one pass: one batched query per fleet's client for every moored ship's PRs.
func (s *Service) PollMoored(ctx context.Context) {
	s.pollMu.Lock()
	defer s.pollMu.Unlock()
	if s.GitHub == nil {
		return
	}
	s.mooredMu.Lock()
	s.loadMoored()
	recs := s.notLive(s.moored)
	s.mooredMu.Unlock()
	byFleet := map[string][]moor.Record{}
	var fleets []string
	for _, r := range recs {
		if _, ok := byFleet[r.Fleet]; !ok {
			fleets = append(fleets, r.Fleet)
		}
		byFleet[r.Fleet] = append(byFleet[r.Fleet], r)
	}
	changed := false
	for _, fleet := range fleets {
		gh := s.GitHub(fleet)
		if gh == nil || ctx.Err() != nil {
			continue
		}
		var urls []string
		seen := map[string]bool{}
		for _, r := range byFleet[fleet] {
			for _, u := range r.PRs {
				if !seen[u] {
					seen[u] = true
					urls = append(urls, u)
				}
			}
		}
		answer, err := gh.PullRequests(ctx, urls)
		if ctx.Err() != nil {
			return
		}
		// Some of the PRs unanswered: the ships holding one are stale, the others are answered.
		var unanswered *moor.Unanswered
		partial := errors.As(err, &unanswered)
		changed = true
		for _, r := range byFleet[fleet] {
			if err != nil && (!partial || slices.ContainsFunc(r.PRs, func(u string) bool { return slices.Contains(unanswered.URLs, u) })) {
				s.markStale(r)
				continue
			}
			st := map[string]moor.Status{}
			for _, u := range r.PRs {
				if v, ok := answer[u]; ok {
					st[u] = v
				}
			}
			prs, ok := s.storePoll(r, polled{status: st, at: time.Now().UTC().Truncate(time.Second)})
			if !ok {
				continue
			}
			if reason, ended := moor.EndReason(prs); ended {
				_, _ = s.endMoored(r.Session, reason)
				continue
			}
			s.decide(ctx, r, st)
		}
	}
	if changed {
		s.broadcast()
	}
}

// decide reads a ship's wake state and the answered PRs, and either wakes the ship for a feedback round or
// saves what the poll first saw. A refusal that clears later leaves the state alone, so the next poll decides the same way.
func (s *Service) decide(ctx context.Context, r moor.Record, answers map[string]moor.Status) {
	if ctx.Err() != nil || !moor.ValidResume(r.Resume) {
		return
	}
	var reviewers []string
	if f, ok := s.Launcher.Config().Fleet(r.Fleet); ok {
		reviewers = f.WakeReviewers
	}
	state, err := moor.ReadWake(s.Launcher.Home, r.Name)
	if err != nil {
		fmt.Fprintf(os.Stderr, "admiral: ship %s: its wake state cannot be read: %v\n", r.Name, err)
		return
	}
	now := time.Now().UTC()
	d := moor.DecideWake(moor.WakeInput{Record: r, State: state, Answers: answers, Reviewers: reviewers, Now: now})
	next, batches, ok := s.propose(r, state, d, now)
	if !ok {
		return
	}
	d.Next = next
	if !d.Wake {
		s.saveNext(r, state, d.Next)
		return
	}
	if _, lerr := s.feedbackRound(r.Session, d.PR, d.Feedback, &state, d.Next); lerr != nil {
		s.setWaiting(r, waitingText(lerr))
		// The round did not start, so its cursor stays; the proposals it made are recorded so the next poll does not make them again.
		s.saveNext(r, state, withProposed(state, batches))
	}
}

// waitingText is what a moored ship shows for a refused round: the refusal's own wait text when a later poll can clear it, else the refusal.
func waitingText(lerr *launcher.Error) string {
	if lerr.Wait != "" {
		return lerr.Wait
	}
	return "Can't wake: " + lerr.Msg
}

func (s *Service) setWaiting(r moor.Record, text string) {
	s.mooredMu.Lock()
	defer s.mooredMu.Unlock()
	if !s.stillMoored(r) {
		return
	}
	p := s.polls()[r.Session]
	p.waiting = text
	s.polls()[r.Session] = p
}

// saveNext keeps a first-seen time the decision found. It skips a ship whose wake has committed or whose record
// has gone, and one whose state changed since the decision read it.
func (s *Service) saveNext(r moor.Record, was, next moor.WakeState) {
	if sameWake(was, next) {
		return
	}
	s.wakeMu.Lock()
	defer s.wakeMu.Unlock()
	s.mooredMu.Lock()
	skip := s.committed[r.Session] || !s.stillMoored(r)
	s.mooredMu.Unlock()
	if skip {
		return
	}
	if now, err := moor.ReadWake(s.Launcher.Home, r.Name); err != nil || !sameWake(now, was) {
		return
	}
	if err := moor.WriteWake(s.Launcher.Home, r.Name, next); err != nil {
		fmt.Fprintf(os.Stderr, "admiral: ship %s: its wake state cannot be saved: %v\n", r.Name, err)
	}
}

func sameWake(a, b moor.WakeState) bool {
	x, _ := json.Marshal(a)
	y, _ := json.Marshal(b)
	return string(x) == string(y)
}

// markStale keeps the ship's last chips and marks its poll stale, unless the ship has ended or been moored again.
func (s *Service) markStale(r moor.Record) {
	s.mooredMu.Lock()
	defer s.mooredMu.Unlock()
	if !s.stillMoored(r) {
		return
	}
	p := s.polls()[r.Session]
	p.stale = true
	s.polls()[r.Session] = p
}

// storePoll keeps an answer only for the record that was asked about: a poll can answer after its ship ended.
func (s *Service) storePoll(r moor.Record, p polled) ([]moor.PR, bool) {
	s.mooredMu.Lock()
	defer s.mooredMu.Unlock()
	if !s.stillMoored(r) {
		return nil, false
	}
	s.polls()[r.Session] = p
	return s.prsOf(r), true
}

// stillMoored: the record is in the cache and is the one r read, by session and moored time; the caller holds mooredMu.
func (s *Service) stillMoored(r moor.Record) bool {
	for _, c := range s.moored {
		if c.Session == r.Session {
			return c.MooredAt.Equal(r.MooredAt)
		}
	}
	return false
}

// notLive is the records whose ship has no live session, the ones mooredShips lists.
func (s *Service) notLive(recs []moor.Record) []moor.Record {
	live := map[string]bool{}
	for _, sess := range s.Host.Sessions() {
		live[sess.Name] = true
	}
	var out []moor.Record
	for _, r := range recs {
		if !live[r.Session] {
			out = append(out, r)
		}
	}
	return out
}

// polls is the poll state by session; the caller holds mooredMu.
func (s *Service) polls() map[string]polled {
	if s.mooredPolls == nil {
		s.mooredPolls = map[string]polled{}
	}
	return s.mooredPolls
}

// prsOf is the record's PRs as last polled, each OPEN until a poll answers; the caller holds mooredMu.
func (s *Service) prsOf(r moor.Record) []moor.PR {
	p, ok := s.mooredPolls[r.Session]
	out := make([]moor.PR, 0, len(r.PRs))
	for _, u := range r.PRs {
		if !ok || p.status == nil {
			out = append(out, moor.PR{URL: u, State: "OPEN"})
			continue
		}
		st, found := p.status[u]
		out = append(out, moor.View(u, st, found))
	}
	return out
}

// mooredRecord finds a moored ship's record by session; a record whose session is still live is not moored yet.
func (s *Service) mooredRecord(session string) (moor.Record, bool) {
	s.mooredMu.Lock()
	defer s.mooredMu.Unlock()
	s.loadMoored()
	for _, r := range s.notLive(s.moored) {
		if r.Session == session {
			return r, true
		}
	}
	return moor.Record{}, false
}

// endMoored sails a moored ship off. Whoever takes the ship out of the cache writes the event and removes the
// record; an ending that lost the race reports false, and one whose record cannot be removed an error.
func (s *Service) endMoored(session, reason string) (bool, error) {
	s.mooredMu.Lock()
	s.loadMoored()
	idx := -1
	for i, r := range s.moored {
		if r.Session == session {
			idx = i
		}
	}
	if idx < 0 {
		s.mooredMu.Unlock()
		return false, nil
	}
	rec := s.moored[idx]
	if s.committed[session] { // a wake is making the session: it wins
		s.mooredMu.Unlock()
		return false, nil
	}
	if err := moor.Remove(s.Launcher.Home, rec.Name); err != nil { // kept, so the next poll ends it
		s.mooredMu.Unlock()
		fmt.Fprintf(os.Stderr, "admiral: ship %s: its moored record cannot be removed: %v\n", rec.Name, err)
		return false, err
	}
	prs := s.prsOf(rec)
	s.moored = append(s.moored[:idx:idx], s.moored[idx+1:]...)
	delete(s.mooredPolls, session)
	s.mooredMu.Unlock()
	s.removeWake(rec.Name) // after the record leaves the cache, so a saveNext that has not written yet sees it gone
	final := make([]map[string]string, 0, len(prs))
	for _, p := range prs {
		final = append(final, map[string]string{"url": p.URL, "state": p.State})
	}
	s.record(outcomes.Record{Ship: session, Event: "unmoored", Fields: map[string]any{"reason": reason, "prs": final}}, nil)
	s.broadcast()
	return true, nil
}

// endByRequest ends a moored ship for a request and answers it: 404 for an ending that lost the race, 500 for a record that stays (the cause is in the log, not the answer, since it names a path).
func (s *Service) endByRequest(w http.ResponseWriter, session, reason, key string) {
	ended, err := s.endMoored(session, reason)
	switch {
	case err != nil:
		writeError(w, http.StatusInternalServerError, "ship "+session+" cannot be ended: its moored record cannot be removed")
	case !ended:
		writeError(w, http.StatusNotFound, "ship "+session+" not found")
	default:
		writeJSON(w, http.StatusOK, map[string]any{key: session})
	}
}

func (s *Service) serveMooredRefresh(w http.ResponseWriter, r *http.Request) {
	s.RefreshMoored()
	w.WriteHeader(http.StatusAccepted)
}

func (s *Service) serveUnfollow(w http.ResponseWriter, r *http.Request) {
	session := r.PathValue("session")
	if _, ok := s.mooredRecord(session); ok {
		s.endByRequest(w, session, "unfollowed", "unfollowed")
		return
	}
	sess, ok := s.find(session)
	if !ok {
		writeError(w, http.StatusNotFound, "ship "+session+" not found")
		return
	}
	writeError(w, http.StatusConflict, "ship "+session+" is "+status(sess, time.Now())+", and only a moored ship can stop following reviews")
}
