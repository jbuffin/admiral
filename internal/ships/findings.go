package ships

import (
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/jbuffin/admiral/internal/findings"
	"github.com/jbuffin/admiral/internal/launcher"
	"github.com/jbuffin/admiral/internal/sessionhost"
)

func (s *Service) findingsKept() *findings.Store {
	s.findingsOnce.Do(func() {
		s.findingsHome = s.Launcher.Home
		if home, err := filepath.Abs(s.Launcher.Home); err == nil {
			s.findingsHome = home
		}
		s.findingsStore = findings.Open(s.findingsHome)
	})
	return s.findingsStore
}

// Findings reads what ships kept at sail-off.
func (s *Service) Findings() findings.Reader { return s.findingsKept() }

// keepFindings copies the ship's findings file into the store before its files go. A scout that leaves none, in this
// round or an earlier one, has failed; a store error is not the scout's fault, so it fails nothing.
func (s *Service) keepFindings(sess sessionhost.Session) (kept, failed bool) {
	store := s.findingsKept()
	path, ok := launcher.FindingsFile(s.findingsHome, sess.Name)
	first := firstLaunched(sess.Options)
	if ok && first != "" {
		var kerr error
		kept, kerr = store.Keep(sess.Name, first, sess.Options["fleet"], path)
		if kerr != nil {
			fmt.Fprintf(os.Stderr, "admiral: ship %s: its findings are not kept: %v\n", sess.Name, kerr)
			return false, false
		}
	}
	if kept || sess.Options["scout"] == "" {
		return kept, false
	}
	_, earlier := store.Voyage(sess.Name, first)
	return false, !earlier
}

func (s *Service) serveFindings(w http.ResponseWriter, r *http.Request) {
	session := r.PathValue("session")
	var v findings.Voyage
	var ok bool
	if first := r.URL.Query().Get("first_launched"); first != "" {
		if _, err := time.Parse(time.RFC3339, first); err != nil {
			writeError(w, http.StatusBadRequest, "first_launched must be an RFC 3339 time")
			return
		}
		v, ok = s.findingsKept().Voyage(session, first)
	} else {
		v, ok = s.findingsKept().Latest(session)
	}
	if !ok {
		writeError(w, http.StatusNotFound, "no findings are kept for this ship")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{
		"ship": v.Ship, "first_launched": v.FirstLaunched, "fleet": v.Fleet, "findings": v.Markdown,
	})
}
