package ships

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"slices"
	"strings"

	"github.com/jbuffin/admiral/internal/moor"
)

const (
	markedReady       = "marked_ready"
	alreadyForReview  = "already_ready_for_review"
	markReadyFailed   = "failed"
	markUnknownResult = "gh did not finish in time, so whether the pull request is ready for review is unknown until the next refresh"
)

type markResult struct {
	URL     string `json:"url"`
	Outcome string `json:"outcome"`
	Error   string `json:"error,omitempty"`
}

// serveReadyForReview is the engineer's Mark ready for review act: gh takes each named draft PR out of draft. It runs only on this request and merges nothing.
func (s *Service) serveReadyForReview(w http.ResponseWriter, r *http.Request) {
	session := r.PathValue("session")
	var body struct {
		PRs []string `json:"prs"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&body); err != nil && !errors.Is(err, io.EOF) {
		writeError(w, http.StatusBadRequest, "request is not valid JSON: "+err.Error())
		return
	}
	if !s.startMerge(session) {
		writeError(w, http.StatusConflict, "a merge, branch update or ready-for-review run of ship "+session+" is already under way")
		return
	}
	defer s.endMerge(session)
	if !s.startWaking(session) {
		writeError(w, http.StatusConflict, "ship "+session+" is waking")
		return
	}
	defer s.endWaking(session)
	fleet, carried, reviewing, ok := s.carriedBy(session)
	if !ok {
		writeError(w, http.StatusNotFound, "ship "+session+" not found")
		return
	}
	if len(reviewing) > 0 {
		writeError(w, http.StatusConflict, "ship "+session+" reviews a pull request, and a review ship does not mark pull requests ready for review")
		return
	}
	if len(body.PRs) == 0 {
		writeError(w, http.StatusBadRequest, "prs names no pull request")
		return
	}
	for i, u := range body.PRs {
		if !slices.Contains(carried, u) || slices.Contains(body.PRs[:i], u) {
			writeError(w, http.StatusBadRequest, "prs holds a pull request that is not one of ship "+session+"'s, or is named twice")
			return
		}
	}
	var gh moor.GitHub
	if s.GitHub != nil {
		gh = s.GitHub(fleet)
	}
	if gh == nil {
		writeError(w, http.StatusConflict, "the fleet has no GitHub source to mark pull requests ready for review with")
		return
	}
	urls := make([]string, len(body.PRs))
	for i, u := range body.PRs {
		host, repo, number, ok := moor.ParsePR(u)
		if !ok || !strings.EqualFold(host, gh.Host()) {
			writeError(w, http.StatusUnprocessableEntity, "a pull request is not on the fleet's GitHub host, so none is marked ready for review")
			return
		}
		urls[i] = "https://" + gh.Host() + "/" + repo + "/pull/" + number
	}
	drafts := make([]bool, len(body.PRs))
	for i, u := range body.PRs {
		detail, err := gh.PullRequestDetail(r.Context(), u)
		if err != nil {
			writeError(w, http.StatusBadGateway, "cannot read the pull request before marking it ready for review: "+err.Error())
			return
		}
		if detail.State != "OPEN" {
			writeError(w, http.StatusConflict, "a pull request is not open, so none is marked ready for review")
			return
		}
		drafts[i] = detail.Draft
	}

	// Like a merge, the writes outlive a client that hangs up: stopping gh midway would leave its result unknown.
	ctx := context.WithoutCancel(r.Context())
	results := make([]markResult, len(body.PRs))
	for i, u := range body.PRs {
		results[i] = markResult{URL: u, Outcome: alreadyForReview}
		if !drafts[i] {
			continue
		}
		err := gh.MarkReadyForReview(ctx, urls[i])
		switch {
		case errors.Is(err, moor.ErrMergeTimeout):
			results[i] = markResult{URL: u, Outcome: markReadyFailed, Error: markUnknownResult}
		case err != nil:
			fmt.Fprintf(s.log(), "admiral: ship %s: marking pull request %s ready for review failed: %s\n", session, urls[i], strings.Join(strings.Fields(strings.Map(noControl, err.Error())), " "))
			results[i] = markResult{URL: u, Outcome: markReadyFailed, Error: err.Error()}
		default:
			results[i].Outcome = markedReady
		}
	}
	s.RefreshMoored()
	writeJSON(w, http.StatusOK, map[string]any{"results": results})
}
