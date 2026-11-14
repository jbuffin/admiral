package ships

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"slices"
	"strings"

	"github.com/jbuffin/admiral/internal/moor"
	"github.com/jbuffin/admiral/internal/outcomes"
)

// serveMerge is the engineer's Merge act on a moored ship. It runs only on this request: no poll, order or agent calls it.
func (s *Service) serveMerge(w http.ResponseWriter, r *http.Request) {
	session := r.PathValue("session")
	var body struct {
		UpTo string `json:"upTo"`
		PR   string `json:"pr"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&body); err != nil && !errors.Is(err, io.EOF) {
		writeError(w, http.StatusBadRequest, "request is not valid JSON: "+err.Error())
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
	// Read after both slots are held, so a wake that just finished can't leave a stale record.
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
	if body.PR != "" && !slices.Contains(rec.PRs, body.PR) {
		writeError(w, http.StatusBadRequest, "pr is not one of ship "+session+"'s pull requests")
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
	// The PRs that were checked are the PRs that get merged: only the client's own host, and the argv URL is rebuilt from the parts.
	for _, pr := range rec.PRs {
		if host, _, _, ok := moor.ParsePR(pr); !ok || !strings.EqualFold(host, gh.Host()) {
			writeError(w, http.StatusUnprocessableEntity, "the pull request is not on the fleet's GitHub host, so it is not merged")
			return
		}
	}

	// What gets merged, and as what: the one PR, one PR of independent PRs, or GitHub's stack.
	merging, stackNumber, stacked := rec.PRs, 0, false
	if len(rec.PRs) > 1 {
		shape, err := moor.ReadShape(r.Context(), gh, rec.PRs)
		if err != nil {
			writeError(w, http.StatusBadGateway, "cannot read how the pull requests relate on GitHub, so nothing is merged: "+err.Error())
			return
		}
		switch shape.Kind {
		case moor.ShapeStacked:
			if body.PR != "" {
				writeError(w, http.StatusConflict, "these pull requests are one stack on GitHub, and a stack merges whole, so name no pr")
				return
			}
			if body.UpTo != "" && body.UpTo != shape.PRs[len(shape.PRs)-1] {
				writeError(w, http.StatusBadRequest, "Merge up to is not available; the whole stack merges")
				return
			}
			stackNumber, stacked = shape.StackNumber, true
		case moor.ShapeIndependent:
			if body.PR == "" {
				writeError(w, http.StatusConflict, "these pull requests are independent, so name the one to merge with pr")
				return
			}
			merging = []string{body.PR}
		case moor.ShapeChained:
			writeError(w, http.StatusConflict, "these pull requests are chained but not a stack on GitHub; stack it on GitHub first, then merge")
			return
		default:
			writeError(w, http.StatusConflict, "these pull requests are neither one GitHub stack nor independent of each other, so they are not merged")
			return
		}
	}

	// The last poll may be minutes old: look again before any gh merge runs.
	fresh, err := gh.PullRequests(r.Context(), merging)
	if err != nil {
		writeError(w, http.StatusBadGateway, "cannot check the pull request before merging: "+err.Error())
		return
	}
	for _, url := range merging {
		if st, found := fresh[url]; !found || !st.Ready() {
			writeError(w, http.StatusConflict, "the pull request is not ready to merge: it must be open, approved, green and mergeable")
			return
		}
	}
	_, repo, number, _ := moor.ParsePR(merging[0])
	method, err := s.mergeMethod(r.Context(), gh, rec.Fleet, repo)
	if err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}

	// The merge outlives a client that hangs up: stopping gh midway would leave its result unknown.
	if stacked {
		err = gh.MergeStack(context.WithoutCancel(r.Context()), repo, stackNumber, method)
	} else {
		err = gh.MergePR(context.WithoutCancel(r.Context()), "https://"+gh.Host()+"/"+repo+"/pull/"+number, method)
	}
	switch {
	case errors.Is(err, moor.ErrMergeTimeout):
		s.RefreshMoored() // it may have merged
		writeError(w, http.StatusGatewayTimeout, err.Error())
		return
	case err != nil:
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	s.record(outcomes.Record{Ship: session, Event: "merged", Fields: map[string]any{"prs": merging, "method": method}}, nil)
	s.RefreshMoored()
	writeJSON(w, http.StatusAccepted, map[string]any{"merged": merging, "method": method})
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

func (s *Service) mergeGuarded(h http.Handler) http.Handler {
	if s.MergeGuard == nil {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			writeError(w, http.StatusForbidden, "merging is not available")
		})
	}
	return s.MergeGuard(h)
}
