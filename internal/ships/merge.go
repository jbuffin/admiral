package ships

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"slices"

	"github.com/jbuffin/admiral/internal/moor"
	"github.com/jbuffin/admiral/internal/outcomes"
)

// serveMerge is the engineer's Merge act on a moored ship. It runs only on this request: no poll, order or agent calls it.
func (s *Service) serveMerge(w http.ResponseWriter, r *http.Request) {
	session := r.PathValue("session")
	var body struct {
		UpTo string `json:"upTo"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&body); err != nil && !errors.Is(err, io.EOF) {
		writeError(w, http.StatusBadRequest, "request is not valid JSON: "+err.Error())
		return
	}
	rec, ok := s.mooredRecord(session)
	if !ok {
		if _, live := s.find(session); live {
			writeError(w, http.StatusConflict, "ship "+session+" is not moored, and only a moored ship can be merged")
			return
		}
		writeError(w, http.StatusNotFound, "ship "+session+" not found")
		return
	}
	if body.UpTo != "" && !slices.Contains(rec.PRs, body.UpTo) {
		writeError(w, http.StatusBadRequest, "upTo is not one of ship "+session+"'s pull requests")
		return
	}
	if len(rec.PRs) > 1 {
		// gh stack merge reads a bare number as a stack number before a PR number, so it can merge another stack.
		writeError(w, http.StatusUnprocessableEntity, "merging a stack is not available yet; merge its pull requests on GitHub")
		return
	}
	var gh moor.GitHub
	if s.GitHub != nil {
		gh = s.GitHub(rec.Fleet)
	}
	if gh == nil {
		writeError(w, http.StatusConflict, "the fleet has no GitHub source to merge with")
		return
	}
	if !s.startMerge(session) {
		writeError(w, http.StatusConflict, "a merge of ship "+session+" is already under way")
		return
	}
	defer s.endMerge(session)
	if !s.startWaking(session) {
		writeError(w, http.StatusConflict, "ship "+session+" is waking")
		return
	}
	defer s.endWaking(session) // a wake waits out the merge

	// The last poll may be minutes old: look again before any gh merge runs.
	fresh, err := gh.PullRequests(r.Context(), rec.PRs)
	if err != nil {
		writeError(w, http.StatusBadGateway, "cannot check the pull request before merging: "+err.Error())
		return
	}
	url := rec.PRs[0]
	st, found := fresh[url]
	if !found || !st.Ready() {
		writeError(w, http.StatusConflict, "the pull request is not ready to merge: it must be open, approved, green and mergeable")
		return
	}
	repo, _ := moor.RepoOf(url)
	method, err := s.mergeMethod(r.Context(), gh, rec.Fleet, repo)
	if err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}

	// The merge outlives a client that hangs up: stopping gh midway would leave its result unknown.
	err = gh.MergePR(context.WithoutCancel(r.Context()), url, method)
	switch {
	case errors.Is(err, moor.ErrMergeTimeout):
		s.RefreshMoored() // it may have merged
		writeError(w, http.StatusGatewayTimeout, err.Error())
		return
	case err != nil:
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	s.record(outcomes.Record{Ship: session, Event: "merged", Fields: map[string]any{"prs": []string{url}, "method": method}}, nil)
	s.RefreshMoored()
	writeJSON(w, http.StatusAccepted, map[string]any{"merged": []string{url}, "method": method})
}

// mergeMethod is the repository's default merge method, from the cache or else asked of GitHub.
func (s *Service) mergeMethod(ctx context.Context, gh moor.GitHub, fleet, repo string) (string, error) {
	s.mooredMu.Lock()
	m, ok := s.mergeMethods[methodKey(fleet, repo)]
	s.mooredMu.Unlock()
	if ok {
		return m, nil
	}
	m, err := gh.DefaultMergeMethod(ctx, repo)
	if err != nil {
		return "", err
	}
	s.setMergeMethod(fleet, repo, m)
	return m, nil
}

func (s *Service) startMerge(session string) bool {
	s.mooredMu.Lock()
	defer s.mooredMu.Unlock()
	if s.merging[session] {
		return false
	}
	if s.merging == nil {
		s.merging = map[string]bool{}
	}
	s.merging[session] = true
	return true
}

func (s *Service) endMerge(session string) {
	s.mooredMu.Lock()
	delete(s.merging, session)
	s.mooredMu.Unlock()
}
