package routines

import (
	"errors"
	"fmt"
	"net/http"
	"path/filepath"

	"github.com/jbuffin/admiral/internal/launcher"
	"github.com/jbuffin/admiral/internal/promote"
	"github.com/jbuffin/admiral/internal/standing"
)

// servePromote starts a normal ship from a restricted Draft fix ship's work: a T0 act of the engineer's. It stops the
// restricted ship so the work is final, never continues its session, and never reads the event or the report.
func (s *Service) servePromote(w http.ResponseWriter, r *http.Request) {
	s.undoMu.Lock()
	defer s.undoMu.Unlock()
	fleet, routine := r.PathValue("key"), r.PathValue("id")
	rec, err := s.runOf(fleet, routine, r.PathValue("trigger"))
	if err != nil {
		fail(w, err)
		return
	}
	if (rec.Outcome != Launched && rec.Outcome != Failed) || !rec.WorkerLaunched || rec.Ship == "" {
		fail(w, invalidf("only the launch of a restricted event ship can be promoted"))
		return
	}
	if rec.Restricted != standing.DraftFix {
		fail(w, invalidf("only a Draft fix ship can be promoted: an Investigate or normal ship has no work to take"))
		return
	}
	if rec.Undone {
		fail(w, invalidf("the record was undone"))
		return
	}
	if _, _, ok := launcher.ShipFiles(s.Store.Home, rec.Ship); !ok {
		fail(w, invalidf("%q is not a name a ship can have", rec.Ship))
		return
	}
	if exists, err := cloneExists(s.Store.Home, rec.Ship); err != nil {
		writeError(w, http.StatusInternalServerError, "could not read the ship's clone: "+err.Error())
		return
	} else if !exists {
		fail(w, invalidf("the ship's clone is gone"))
		return
	}
	if rec.Promoted {
		writeJSON(w, http.StatusOK, rec)
		return
	}
	list, err := s.Store.List(fleet)
	if err != nil {
		fail(w, err)
		return
	}
	routineRec := list[indexOf(list, routine)]
	if routineRec.Do.Launch == nil {
		fail(w, invalidf("the routine does not launch a ship"))
		return
	}
	home, err := filepath.Abs(s.Store.Home)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	clone, branch := filepath.Join(home, "checkouts", rec.Ship), "admiral/"+rec.Ship
	l := *routineRec.Do.Launch
	work, err := promote.Read(r.Context(), l.Dir, clone, branch)
	if err != nil {
		if errors.Is(err, promote.ErrNothing) || errors.Is(err, promote.ErrAdmiralPaths) || errors.Is(err, promote.ErrAlternates) ||
			errors.Is(err, promote.ErrHarness) || errors.Is(err, promote.ErrSymlink) {
			fail(w, invalidf("%v", err))
			return
		}
		writeError(w, http.StatusInternalServerError, "could not read the ship's work: "+err.Error())
		return
	}
	if s.Launch == nil || s.Stop == nil {
		writeError(w, http.StatusInternalServerError, "ships cannot be launched from here")
		return
	}
	if err := s.Stop(rec.Ship); err != nil && !errors.Is(err, ErrShipNotListed) {
		writeError(w, http.StatusInternalServerError, "could not stop the ship: "+err.Error())
		return
	}
	ref, lerr := s.Launch(r.Context(), launcher.Request{
		Fleet: fleet, Harness: l.Harness, Dir: l.Dir, Model: l.Model, Effort: l.Effort, Skill: launcher.Skill{Kind: "none"},
		Autopilot: true, By: "promote", Prompt: promotedOrder(rec, routineRec.Name, branch, work),
		StartFrom: &launcher.StartFrom{Clone: clone, Branch: branch, Tip: work.Tip},
	})
	if lerr != nil {
		status := lerr.Status
		if status < 400 {
			status = http.StatusInternalServerError
		}
		writeError(w, status, lerr.Msg)
		return
	}
	if err := s.Store.Promote(fleet, rec.ID, ref.Session); err != nil {
		fail(w, err)
		return
	}
	s.changed()
	rec.Promoted, rec.PromotedShip = true, ref.Session
	writeJSON(w, http.StatusOK, rec)
}

// promotedOrder is fixed text: nothing the restricted ship or the event wrote reaches the new ship's prompt.
func promotedOrder(rec Trigger, routine, branch string, work promote.Work) string {
	return fmt.Sprintf("This checkout starts from the work a restricted ship did for routine %q (trigger-log record %s), on branch %s: %s. "+
		"That ship ran on untrusted input, so review the work before building on it and before pushing or opening a PR.",
		routine, rec.ID, branch, work.Range)
}
