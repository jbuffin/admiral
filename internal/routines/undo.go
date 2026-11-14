package routines

import (
	"errors"
	"net/http"
	"os"
	"path/filepath"

	"github.com/jbuffin/admiral/internal/launcher"
)

// ErrShipNotListed is what Stop returns for a session the host does not list.
var ErrShipNotListed = errors.New("the ship is not listed")

// runOf is the record id of routine, or an error that fail maps to its status.
func (s *Service) runOf(fleet, routine, id string) (Trigger, error) {
	list, err := s.Store.List(fleet)
	if err != nil {
		return Trigger{}, err
	}
	if indexOf(list, routine) < 0 {
		return Trigger{}, ErrNotFound
	}
	recs, err := s.Store.Records(fleet)
	if err != nil {
		return Trigger{}, err
	}
	for _, r := range recs {
		if r.ID == id {
			if r.Routine != routine {
				return Trigger{}, invalidf("run %s belongs to another routine", id)
			}
			return r, nil
		}
	}
	return Trigger{}, ErrNotFound
}

func (s *Service) serveRun(w http.ResponseWriter, r *http.Request) {
	rec, err := s.runOf(r.PathValue("key"), r.PathValue("id"), r.PathValue("trigger"))
	if errors.Is(err, ErrInvalid) {
		err = ErrNotFound
	}
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, rec)
}

// serveUndo stops the launch's ship, deletes its clone and marks the record. A record already undone is left alone, as
// its session name can belong to a newer ship.
func (s *Service) serveUndo(w http.ResponseWriter, r *http.Request) {
	fleet := r.PathValue("key")
	rec, err := s.runOf(fleet, r.PathValue("id"), r.PathValue("trigger"))
	if err != nil {
		fail(w, err)
		return
	}
	if rec.Outcome != Launched || !rec.WorkerLaunched || rec.Ship == "" {
		fail(w, invalidf("only a launch of an event ship can be undone"))
		return
	}
	if rec.Undone {
		writeJSON(w, http.StatusOK, rec)
		return
	}
	if _, _, ok := launcher.ShipFiles(s.Store.Home, rec.Ship); !ok {
		fail(w, invalidf("%q is not a name a ship can have", rec.Ship))
		return
	}
	if s.Stop != nil {
		if err := s.Stop(rec.Ship); err != nil && !errors.Is(err, ErrShipNotListed) {
			writeError(w, http.StatusInternalServerError, "could not stop the ship: "+err.Error())
			return
		}
	}
	if err := removeClone(s.Store.Home, rec.Ship); err != nil {
		writeError(w, http.StatusInternalServerError, "could not remove the ship's clone: "+err.Error())
		return
	}
	if err := s.Store.Undo(fleet, rec.ID); err != nil {
		fail(w, err)
		return
	}
	s.changed()
	rec.Undone = true
	writeJSON(w, http.StatusOK, rec)
}

// removeClone deletes checkouts/<session> under home through a root on home, so nothing outside it is touched; a link
// there is removed and never followed.
func removeClone(home, session string) error {
	abs, err := filepath.Abs(home)
	if err != nil {
		return err
	}
	root, err := os.OpenRoot(abs)
	if err != nil {
		return err
	}
	defer root.Close()
	dir := filepath.Join("checkouts", session)
	if fi, err := root.Lstat("checkouts"); err != nil || !fi.IsDir() {
		return nil
	}
	if _, err := root.Lstat(dir); err != nil {
		return nil
	}
	return root.RemoveAll(dir)
}
