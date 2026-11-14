package routines

import (
	"errors"
	"fmt"
	"io/fs"
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
	// One Undo at a time: a second would otherwise pass the undone check while the first is between its removal and its mark.
	s.undoMu.Lock()
	defer s.undoMu.Unlock()
	fleet := r.PathValue("key")
	rec, err := s.runOf(fleet, r.PathValue("id"), r.PathValue("trigger"))
	if err != nil {
		fail(w, err)
		return
	}
	// A ship that ended failed folds its launch into Failed; it still has a clone to remove.
	if (rec.Outcome != Launched && rec.Outcome != Failed) || !rec.WorkerLaunched || rec.Ship == "" {
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
	if s.Stop == nil {
		writeError(w, http.StatusInternalServerError, "ships cannot be stopped from here")
		return
	}
	// The launcher holds a name only while its clone exists, so a listed ship of that name with no clone is a newer one.
	if exists, err := cloneExists(s.Store.Home, rec.Ship); err != nil {
		writeError(w, http.StatusInternalServerError, "could not read the ship's clone: "+err.Error())
		return
	} else if exists {
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

// cloneDir is the ship's clone path inside root, and whether anything is there; a checkouts that is not a real
// directory is an error, as a delete under a link would go elsewhere.
func cloneDir(root *os.Root, session string) (string, bool, error) {
	fi, err := root.Lstat("checkouts")
	if errors.Is(err, fs.ErrNotExist) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	if !fi.IsDir() {
		return "", false, fmt.Errorf("checkouts is not a directory")
	}
	dir := filepath.Join("checkouts", session)
	if _, err := root.Lstat(dir); errors.Is(err, fs.ErrNotExist) {
		return "", false, nil
	} else if err != nil {
		return "", false, err
	}
	return dir, true, nil
}

func cloneExists(home, session string) (bool, error) {
	abs, err := filepath.Abs(home)
	if err != nil {
		return false, err
	}
	root, err := os.OpenRoot(abs)
	if err != nil {
		return false, err
	}
	defer root.Close()
	_, ok, err := cloneDir(root, session)
	return ok, err
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
	dir, ok, err := cloneDir(root, session)
	if err != nil || !ok {
		return err
	}
	return root.RemoveAll(dir)
}
