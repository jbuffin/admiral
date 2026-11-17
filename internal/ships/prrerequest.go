package ships

import (
	"context"
	"encoding/json"
	"net/http"
	"slices"

	"github.com/jbuffin/admiral/internal/moor"
)

// canReRequest is the rule GitHub's PR page follows for Re-request review: a person whose review was submitted, who is not
// pending again and is not the PR's author. The client repeats it to decide what to show.
func canReRequest(d moor.Detail, login string) bool {
	if login == "" || login == d.Author.Login {
		return false
	}
	if slices.ContainsFunc(d.Requested, func(a moor.DetailActor) bool { return a.Login == login }) {
		return false
	}
	return slices.ContainsFunc(d.Reviews, func(r moor.DetailReview) bool { return r.Author.Login == login && r.State != "PENDING" })
}

// serveReRequest asks GitHub to request another review from a person who already reviewed a PR the ship carries.
func (s *Service) serveReRequest(w http.ResponseWriter, r *http.Request) {
	session := r.PathValue("session")
	var req struct {
		PR    string `json:"pr"`
		Login string `json:"login"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<16)).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "request is not valid JSON: "+err.Error())
		return
	}
	fleet, urls, _, ok := s.carriedBy(session)
	if !ok {
		writeError(w, http.StatusNotFound, "ship "+session+" not found")
		return
	}
	if !slices.Contains(urls, req.PR) {
		writeError(w, http.StatusNotFound, "ship "+session+" carries no PR "+req.PR)
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
	d, err := gh.PullRequestDetail(ctx, req.PR)
	if err != nil {
		writeError(w, http.StatusBadGateway, "GitHub did not answer for "+req.PR+": "+err.Error())
		return
	}
	if !canReRequest(d, req.Login) {
		writeError(w, http.StatusConflict, "GitHub offers no re-request of a review from "+req.Login+" on this PR")
		return
	}
	if err := gh.ReRequestReview(r.Context(), req.PR, req.Login); err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"requested": req.Login})
}
