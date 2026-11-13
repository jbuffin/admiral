package ships

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"time"

	"github.com/jbuffin/admiral/internal/moor"
	"github.com/jbuffin/admiral/internal/outcomes"
	"github.com/jbuffin/admiral/internal/sessionhost"
)

// ReturnPrompt leaves the clean-up to the ship: Admiral never decides what a run left behind.
const ReturnPrompt = "Return to port: this run is over and Admiral will stop this session when your turn ends. " +
	"Clean up after yourself first: anything you started or set up for this run that should not outlive it. " +
	"Do not start new work."

// serveReturn starts Return to port: the ship gets the clean-up prompt once its turn has ended, and is stopped once the
// turn that prompt starts has ended too.
func (s *Service) serveReturn(w http.ResponseWriter, r *http.Request) {
	session := r.PathValue("session")
	sess, ok := s.find(session)
	if !ok {
		if _, moored := s.mooredRecord(session); moored {
			writeError(w, http.StatusConflict, "ship "+session+" is moored: it has no session to clean up")
			return
		}
		writeError(w, http.StatusNotFound, "ship "+session+" not found")
		return
	}
	switch st := status(sess, time.Now()); {
	case st == StatusAnchored || st == StatusSailingOff:
		writeError(w, http.StatusConflict, "ship "+session+" has exited: stop it instead")
		return
	case sess.Options[sessionhost.OptReturning] != "":
		writeJSON(w, http.StatusOK, map[string]any{"returning": true})
		return
	case carriesPRs(sess):
		writeError(w, http.StatusConflict, "ship "+session+" carries a PR: it returns to port once its PRs are merged")
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := s.Host.SetOptions(ctx, session, map[string]string{sessionhost.OptReturning: "queued"}); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.record(outcomes.Record{Ship: session, Event: "returning", Fields: withVia(nil, viaOf(r))}, nil)
	_ = s.Host.Sync(ctx)
	s.broadcast()
	s.advanceReturns()
	writeJSON(w, http.StatusOK, map[string]any{"returning": true})
}

// advanceReturns moves each returning ship whose own turn has ended one step: a queued ship gets the clean-up prompt,
// and a ship that already had it is stopped.
func (s *Service) advanceReturns() {
	for _, sess := range s.Host.Sessions() {
		step := sess.Options[sessionhost.OptReturning]
		if !sess.IsShip() || step == "" || status(sess, time.Now()) != StatusNeedsYou || reasonOf(sess) != "stop" {
			continue
		}
		if _, busy := s.returning.LoadOrStore(sess.Name, true); busy {
			continue
		}
		s.settleWG.Add(1)
		go func(name, step string) {
			defer s.settleWG.Done()
			defer s.returning.Delete(name)
			if step == "sent" {
				if sess.Options["round"] != "" {
					s.endMooring(sess) // its PRs are done, so its round ends sailed, not anchored by the stop
				}
				_ = s.stopShip(name, map[string]any{"returned": true})
				return
			}
			s.sendReturnPrompt(name)
		}(sess.Name, step)
	}
}

// sendReturnPrompt types the prompt, then marks the pane working before it marks the prompt sent, so no pass sees a
// sent ship still at the stop the prompt ended.
func (s *Service) sendReturnPrompt(name string) {
	_, typed, _ := s.TypeResume(name, ReturnPrompt)
	if !typed {
		return // the next pass tries again while the turn is still over
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = s.Host.SetWorking(ctx, name)
	_ = s.Host.SetOptions(ctx, name, map[string]string{sessionhost.OptReturning: "sent"})
	_ = s.Host.Sync(ctx)
	s.broadcast()
}

// carriesPRs is true for a ship woken from its mooring: its PRs were open when it woke.
func carriesPRs(sess sessionhost.Session) bool {
	return sess.Options["round"] != "" && sess.Options[sessionhost.OptReturning] == ""
}

// returnMerged wakes a moored ship whose PRs are all done, at least one merged, with its resume text and the clean-up
// prompt, already marked sent so it is stopped when that turn ends. False means the ship sails off without it.
func (s *Service) returnMerged(r moor.Record, reason string, prs []moor.PR) bool {
	got, lerr := s.wake(r.Session, func(rec moor.Record) string { return rec.Resume + " " + ReturnPrompt }, "return", "", "", nil)
	if lerr != nil {
		if lerr.Wait != "" {
			return true // still moored: the next poll tries again
		}
		fmt.Fprintf(os.Stderr, "admiral: ship %s: cannot wake it to return to port, so it sails off: %s\n", r.Name, lerr.Msg)
		return false
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := s.Host.SetOptions(ctx, got.Session, map[string]string{sessionhost.OptReturning: "sent"}); err != nil {
		fmt.Fprintf(os.Stderr, "admiral: ship %s: woken to return to port but not marked returning: %v\n", r.Name, err)
	}
	final := make([]map[string]string, 0, len(prs))
	for _, p := range prs {
		final = append(final, map[string]string{"url": p.URL, "state": p.State})
	}
	s.record(outcomes.Record{Ship: got.Session, Event: "unmoored", Fields: map[string]any{"reason": reason, "prs": final}}, nil)
	s.record(outcomes.Record{Ship: got.Session, Event: "returning"}, nil)
	_ = s.Host.Sync(ctx)
	s.broadcast()
	return true
}
