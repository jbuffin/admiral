package ships

import (
	"context"
	"net/http"
	"slices"
	"time"

	"github.com/jbuffin/admiral/internal/moor"
)

// detailTimeout bounds one detail read of GitHub.
var detailTimeout = 15 * time.Second

// servePRDetail reads one PR a running or moored ship carries, fresh from GitHub through the ship's fleet.
func (s *Service) servePRDetail(w http.ResponseWriter, r *http.Request) {
	session, url := r.PathValue("session"), r.URL.Query().Get("url")
	fleet, urls, ok := s.carriedBy(session)
	if !ok {
		writeError(w, http.StatusNotFound, "ship "+session+" not found")
		return
	}
	if !slices.Contains(urls, url) {
		writeError(w, http.StatusNotFound, "ship "+session+" carries no PR "+url)
		return
	}
	var gh moor.GitHub
	if s.GitHub != nil {
		gh = s.GitHub(fleet)
	}
	if gh == nil {
		writeError(w, http.StatusBadGateway, "fleet "+fleet+" has no GitHub access")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), detailTimeout)
	defer cancel()
	d, err := gh.PullRequestDetail(ctx, url)
	if err != nil {
		writeError(w, http.StatusBadGateway, "GitHub did not answer for "+url+": "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, d)
}

// carriedBy is the fleet and the PRs of a running or moored ship; an exited ship has none.
func (s *Service) carriedBy(session string) (string, []string, bool) {
	if sess, live := s.find(session); live {
		if !sess.IsShip() || status(sess, time.Now()) == StatusAnchored || status(sess, time.Now()) == StatusSailingOff {
			return "", nil, false
		}
		s.mooredMu.Lock()
		defer s.mooredMu.Unlock()
		return sess.Options["fleet"], s.carried(sess), true
	}
	if r, ok := s.mooredRecord(session); ok {
		return r.Fleet, r.PRs, true
	}
	return "", nil, false
}
