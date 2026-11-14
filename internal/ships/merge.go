package ships

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

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
	stack := len(rec.PRs) > 1
	if stack && body.UpTo != "" && body.UpTo != rec.PRs[len(rec.PRs)-1] {
		writeError(w, http.StatusBadRequest, "Merge up to is not available; the whole stack merges")
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
	var repo, mergeURL string
	for _, pr := range rec.PRs {
		host, prRepo, number, ok := moor.ParsePR(pr)
		if !ok || !strings.EqualFold(host, gh.Host()) {
			writeError(w, http.StatusUnprocessableEntity, "the pull request is not on the fleet's GitHub host, so it is not merged")
			return
		}
		if repo == "" {
			repo, mergeURL = prRepo, "https://"+gh.Host()+"/"+prRepo+"/pull/"+number
		}
	}
	if stack {
		if rec.Checkout == "" {
			writeError(w, http.StatusConflict, "the ship has no checkout to merge its stack from")
			return
		}
		if fi, err := os.Stat(rec.Checkout); err != nil || !fi.IsDir() {
			writeError(w, http.StatusConflict, "the ship's checkout is gone, so its stack is not merged")
			return
		}
		if s.checkoutShared(session, rec.Checkout) {
			writeError(w, http.StatusConflict, "another ship is working in this checkout, so its stack is not merged")
			return
		}
	}

	// The last poll may be minutes old: look again before any gh merge runs.
	fresh, err := gh.PullRequests(r.Context(), rec.PRs)
	if err != nil {
		writeError(w, http.StatusBadGateway, "cannot check the pull request before merging: "+err.Error())
		return
	}
	for _, url := range rec.PRs {
		if st, found := fresh[url]; !found || !st.Ready() {
			writeError(w, http.StatusConflict, "the pull request is not ready to merge: it must be open, approved, green and mergeable")
			return
		}
	}
	// Before the view, so that only starting gh sits between reading the stack and merging it.
	method, err := s.mergeMethod(r.Context(), gh, rec.Fleet, repo)
	if err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	if stack {
		layers, err := gh.StackPRs(r.Context(), rec.Checkout, repo)
		if err != nil {
			writeError(w, http.StatusConflict, "cannot read the stack in the ship's checkout, so it is not merged: "+err.Error())
			return
		}
		if !stackIsShips(layers, rec.PRs, fresh) {
			writeError(w, http.StatusConflict, "the stack in the ship's checkout is not the ship's pull requests as GitHub has them, so it is not merged")
			return
		}
	}

	// The merge outlives a client that hangs up: stopping gh midway would leave its result unknown.
	if stack {
		err = gh.MergeStack(context.WithoutCancel(r.Context()), repo, 0, method)
	} else {
		err = gh.MergePR(context.WithoutCancel(r.Context()), mergeURL, method)
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
	s.record(outcomes.Record{Ship: session, Event: "merged", Fields: map[string]any{"prs": rec.PRs, "method": method}}, nil)
	s.RefreshMoored()
	writeJSON(w, http.StatusAccepted, map[string]any{"merged": rec.PRs, "method": method})
}

// stackIsShips is whether the checkout's own record of its stack names exactly the ship's pull requests, in order, with
// the numbers and branch heads GitHub has for them. The record is the ship's to write, so each layer is bound to GitHub.
func stackIsShips(layers []moor.StackLayer, prs []string, fresh map[string]moor.Status) bool {
	if len(layers) != len(prs) {
		return false
	}
	for i, l := range layers {
		_, _, number, ok := moor.ParsePR(prs[i])
		if !ok || l.URL != prs[i] || strconv.Itoa(l.Number) != number || l.Head == "" || l.Head != fresh[prs[i]].Head {
			return false
		}
	}
	return true
}

// checkoutShared is whether another live ship works in the same directory, where it could switch the branch gh-stack merges.
func (s *Service) checkoutShared(session, checkout string) bool {
	want := realDir(checkout)
	for _, sess := range s.Host.Sessions() {
		if sess.Name == session || !sess.IsShip() {
			continue
		}
		if dir := s.ship(sess).Dir; dir != "" && realDir(dir) == want {
			return true
		}
	}
	return false
}

func realDir(p string) string {
	if r, err := filepath.EvalSymlinks(p); err == nil {
		p = r
	}
	return filepath.Clean(p)
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
