package ships

import (
	"context"
	"errors"
	"net/http"
	"slices"
	"strconv"
	"strings"

	"github.com/jbuffin/admiral/internal/moor"
)

// stackAnswer is the PR shape for the stack bar; shape is a moor.ShapeKind or "none" for a ship with no PR.
type stackAnswer struct {
	Shape       string   `json:"shape"`
	StackNumber int      `json:"stack_number,omitempty"`
	PRs         []string `json:"prs"`
}

func readStack(ctx context.Context, gh moor.GitHub, prs []string) (stackAnswer, error) {
	if len(prs) == 0 {
		return stackAnswer{Shape: "none", PRs: []string{}}, nil
	}
	shape, err := moor.ReadShape(ctx, gh, prs)
	if err != nil {
		return stackAnswer{}, err
	}
	if shape.PRs == nil { // mixed has no order to give: the ship's own
		shape.PRs = slices.Clone(prs)
	}
	return stackAnswer{Shape: string(shape.Kind), StackNumber: shape.StackNumber, PRs: shape.PRs}, nil
}

func (s *Service) serveStackShape(w http.ResponseWriter, r *http.Request) {
	session := r.PathValue("session")
	fleet, urls, ok := s.carriedBy(session)
	if !ok {
		writeError(w, http.StatusNotFound, "ship "+session+" not found")
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
	ans, err := readStack(ctx, gh, urls)
	if err != nil {
		writeError(w, http.StatusBadGateway, "GitHub did not answer for the pull requests: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, ans)
}

// serveStackLink is the engineer's Stack on GitHub act on a moored ship's chained PRs. It runs only on this request.
func (s *Service) serveStackLink(w http.ResponseWriter, r *http.Request) {
	session := r.PathValue("session")
	if !s.startMerge(session) {
		writeError(w, http.StatusConflict, "a merge or link of ship "+session+" is already under way")
		return
	}
	defer s.endMerge(session)
	if !s.startWaking(session) {
		writeError(w, http.StatusConflict, "ship "+session+" is waking")
		return
	}
	defer s.endWaking(session)
	rec, ok := s.mooredRecord(session)
	if !ok {
		if _, live := s.find(session); live {
			writeError(w, http.StatusConflict, "ship "+session+" is not moored: stack it on GitHub after the ship moors")
			return
		}
		writeError(w, http.StatusNotFound, "ship "+session+" not found")
		return
	}
	var gh moor.GitHub
	if s.GitHub != nil {
		gh = s.GitHub(rec.Fleet)
	}
	if gh == nil {
		writeError(w, http.StatusConflict, "the fleet has no GitHub source to stack with")
		return
	}
	var repo string
	for _, pr := range rec.PRs {
		host, prRepo, _, ok := moor.ParsePR(pr)
		if !ok || !strings.EqualFold(host, gh.Host()) {
			writeError(w, http.StatusUnprocessableEntity, "the pull request is not on the fleet's GitHub host, so it is not stacked")
			return
		}
		if repo == "" {
			repo = prRepo
		}
	}
	if len(rec.PRs) < 2 {
		writeError(w, http.StatusConflict, "a stack needs at least two pull requests")
		return
	}
	shape, err := moor.ReadShape(r.Context(), gh, rec.PRs)
	if err != nil {
		writeError(w, http.StatusBadGateway, "cannot read how the pull requests relate on GitHub, so nothing is stacked: "+err.Error())
		return
	}
	switch shape.Kind {
	case moor.ShapeChained:
	case moor.ShapeStacked:
		writeError(w, http.StatusConflict, "these pull requests are already a stack on GitHub")
		return
	case moor.ShapeIndependent:
		writeError(w, http.StatusConflict, "these pull requests are independent of each other, so there is nothing to stack")
		return
	default:
		writeError(w, http.StatusConflict, "these pull requests are neither a chain nor independent, so they are not stacked")
		return
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
	err = gh.LinkStack(context.WithoutCancel(r.Context()), repo, numbers)
	switch {
	case errors.Is(err, moor.ErrMergeTimeout):
		s.RefreshMoored()
		writeError(w, http.StatusGatewayTimeout, err.Error())
		return
	case err != nil:
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	s.RefreshMoored()
	ans, err := readStack(r.Context(), gh, rec.PRs)
	if err != nil {
		writeError(w, http.StatusBadGateway, "the pull requests were linked, but GitHub did not answer after: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, ans)
}
