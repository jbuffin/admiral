package sources

import (
	"context"
	"errors"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/jbuffin/admiral/internal/moor"
)

// readTimeout keeps a slow GitHub from holding the stack request open.
const readTimeout = 15 * time.Second

// mergeGuarded looks the guard up per request, since main wires it after the routes are registered.
func (s *Service) mergeGuarded(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if s.MergeGuard == nil {
			writeError(w, http.StatusForbidden, "merging is not available")
			return
		}
		s.MergeGuard(h).ServeHTTP(w, r)
	})
}

func (s *Service) startLink(key string) bool {
	s.linkMu.Lock()
	defer s.linkMu.Unlock()
	if s.linking[key] {
		return false
	}
	if s.linking == nil {
		s.linking = map[string]bool{}
	}
	s.linking[key] = true
	return true
}

func (s *Service) endLink(key string) {
	s.linkMu.Lock()
	delete(s.linking, key)
	s.linkMu.Unlock()
}

// stackAnswer is the PR shape after the link, the body the ship's stack route answers with.
type stackAnswer struct {
	Shape       string   `json:"shape"`
	StackNumber int      `json:"stack_number,omitempty"`
	PRs         []string `json:"prs"`
}

// serveStack is the engineer's Stack on GitHub act on a chain of their own pull requests shown in a source's views.
// It reads the pull requests fresh and links only a chain every one of whose PRs is the source's viewer's.
func (s *Service) serveStack(w http.ResponseWriter, r *http.Request) {
	key := r.PathValue("key")
	var req struct {
		PRs []string `json:"prs"`
	}
	if !decode(w, r, &req) {
		return
	}
	_, p, fail := s.mutableFor(key, "refresh")
	if fail != nil {
		writeError(w, fail.Status, fail.Msg)
		return
	}
	g, ok := p.(*githubConn)
	if !ok {
		writeError(w, http.StatusConflict, "source "+key+" is not the GitHub source, so it stacks nothing")
		return
	}
	if !s.startLink(key) {
		writeError(w, http.StatusConflict, "a link for source "+key+" is already under way")
		return
	}
	defer s.endLink(key)
	g.mu.Lock()
	gh := g.client
	g.mu.Unlock()
	viewer := g.Viewer()
	if gh == nil || viewer == "" {
		writeError(w, http.StatusConflict, "the source has not signed in to GitHub yet")
		return
	}

	var repo string
	for _, pr := range req.PRs {
		host, prRepo, _, ok := moor.ParsePR(pr)
		if !ok || !strings.EqualFold(host, gh.Host()) {
			writeError(w, http.StatusUnprocessableEntity, "the pull request is not on the source's GitHub host, so it is not stacked")
			return
		}
		if repo == "" {
			repo = prRepo
		}
		if !strings.EqualFold(repo, prRepo) {
			writeError(w, http.StatusConflict, "these pull requests are in more than one repository, so they are not stacked")
			return
		}
	}
	if len(req.PRs) < 2 {
		writeError(w, http.StatusConflict, "a stack needs at least two pull requests")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), readTimeout)
	defer cancel()
	details := make([]moor.Detail, len(req.PRs))
	for i, u := range req.PRs {
		d, err := gh.PullRequestDetail(ctx, u)
		if err != nil {
			writeError(w, http.StatusBadGateway, "cannot read how the pull requests relate on GitHub, so nothing is stacked: "+err.Error())
			return
		}
		details[i] = d
	}
	shape := moor.ShapeOf(req.PRs, details)
	if shape.Kind != moor.ShapeChained {
		writeError(w, http.StatusConflict, "these pull requests are not a chain that GitHub has not stacked, so they are not stacked")
		return
	}
	for _, d := range details {
		if !strings.EqualFold(d.Author.Login, viewer) {
			writeError(w, http.StatusConflict, "a pull request in this chain is not yours, so it is not stacked")
			return
		}
	}
	numbers := make([]int, 0, len(shape.PRs))
	for _, pr := range shape.PRs {
		_, _, n, _ := moor.ParsePR(pr)
		num, err := strconv.Atoi(n)
		if err != nil {
			writeError(w, http.StatusUnprocessableEntity, "the pull request has no number, so it is not stacked")
			return
		}
		numbers = append(numbers, num)
	}
	// The link outlives a client that hangs up: stopping gh midway would leave its result unknown.
	err := gh.LinkStack(context.WithoutCancel(r.Context()), repo, shape.Base, numbers)
	switch {
	case errors.Is(err, moor.ErrMergeTimeout):
		g.refreshNow()
		writeError(w, http.StatusGatewayTimeout, err.Error())
		return
	case err != nil:
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	g.refreshNow()
	after, err := moor.ReadShape(ctx, gh, shape.PRs)
	if err != nil {
		writeError(w, http.StatusBadGateway, "the pull requests were linked, but GitHub did not answer after: "+err.Error())
		return
	}
	if after.PRs == nil {
		after.PRs = slices.Clone(shape.PRs)
	}
	writeJSON(w, http.StatusOK, stackAnswer{Shape: string(after.Kind), StackNumber: after.StackNumber, PRs: after.PRs})
}
