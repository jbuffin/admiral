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

// Findings is the store of the findings ships kept at sail-off.
func (s *Service) Findings() *findings.Store {
	s.findingsOnce.Do(func() {
		home, err := filepath.Abs(s.Launcher.Home)
		if err != nil {
			home = s.Launcher.Home
		}
		s.findingsStore = findings.Open(home)
	})
	return s.findingsStore
}

// keepFindings copies the ship's findings file into the store before its files go. A scout that leaves none, in this
// round or an earlier one, has failed.
func (s *Service) keepFindings(sess sessionhost.Session) (kept, failed bool) {
	home, err := filepath.Abs(s.Launcher.Home)
	if err != nil {
		return false, sess.Options["scout"] != ""
	}
	path, ok := launcher.FindingsFile(home, sess.Name)
	first := firstLaunched(sess.Options)
	if ok && first != "" {
		var kerr error
		kept, kerr = s.Findings().Keep(sess.Name, first, sess.Options["fleet"], path)
		if kerr != nil {
			fmt.Fprintf(os.Stderr, "admiral: ship %s: its findings are not kept: %v\n", sess.Name, kerr)
		}
	}
	if kept || sess.Options["scout"] == "" {
		return kept, false
	}
	_, earlier := s.Findings().Voyage(sess.Name, first)
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
		v, ok = s.Findings().Voyage(session, first)
	} else {
		v, ok = s.Findings().Latest(session)
	}
	if !ok {
		writeError(w, http.StatusNotFound, "no findings are kept for this ship")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{
		"ship": v.Ship, "first_launched": v.FirstLaunched, "fleet": v.Fleet, "findings": v.Markdown,
	})
}
