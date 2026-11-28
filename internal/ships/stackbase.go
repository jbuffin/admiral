package ships

import (
	"context"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/jbuffin/admiral/internal/moor"
)

// stackBaseTimeout bounds the shape read and the check of one ship.
const stackBaseTimeout = 2 * time.Minute

// stackShape is a ship's stack shape and base, valid while the PRs' head SHAs are the ones it was read for.
type stackShape struct {
	key   string
	shape moor.Shape
	base  string
}

// stackBaseState is the checks' memory: a shape per ship, and the last failure logged per ship so a lasting one logs once.
type stackBaseState struct {
	mu     sync.Mutex
	shapes map[string]stackShape
	logged map[string]string
}

// checkStackBase asks whether a stacked or chained ship's top open PR merges into the stack's base, which GitHub does not
// answer per layer, and marks the top PR's status when it does not. It only adds a problem: when it cannot run, the answers stay as GitHub gave them.
// Merged and closed layers are left out; with fewer than two open layers GitHub's own answer for the lone PR against its base stands.
func (s *Service) checkStackBase(ctx context.Context, fleet string, gh moor.GitHub, r moor.Record, answers map[string]moor.Status) {
	if s.StackChecker == nil || len(r.Reviewing) > 0 {
		return
	}
	var open []string
	var key strings.Builder
	for _, u := range r.PRs {
		st, ok := answers[u]
		if !ok {
			return
		}
		if st.State != "OPEN" {
			continue
		}
		if st.Mergeable != "MERGEABLE" {
			return
		}
		open = append(open, u)
		key.WriteString(u + "@" + st.Head + "\n")
	}
	if len(open) < 2 {
		return
	}
	ctx, cancel := context.WithTimeout(ctx, stackBaseTimeout)
	defer cancel()
	conflicts, top, base, err := s.stackConflicts(ctx, fleet, gh, r.Session, r.PRs, open, key.String(), answers)
	s.stackBaseLog(r.Session, err)
	if err != nil || !conflicts {
		return
	}
	st := answers[top]
	st.ConflictsWithBase = base
	answers[top] = st
}

func (s *Service) stackConflicts(ctx context.Context, fleet string, gh moor.GitHub, session string, all, open []string, key string, answers map[string]moor.Status) (conflicts bool, top, base string, err error) {
	sh, ok := s.cachedStackShape(session, key)
	if !ok {
		// The shape is of all the ship's PRs, since GitHub's stack lists its merged layers too; the check is of the open ones.
		shape, err := moor.ReadShape(ctx, gh, all)
		if err != nil {
			return false, "", "", err
		}
		var live []string
		for _, u := range shape.PRs {
			if slices.Contains(open, u) {
				live = append(live, u)
			}
		}
		switch {
		case (shape.Kind != moor.ShapeStacked && shape.Kind != moor.ShapeChained) || len(live) < 2:
			base = "" // kept, so a ship with nothing to check is not read again until a head moves
		case shape.Kind == moor.ShapeChained && live[0] == shape.PRs[0]:
			base = shape.Base
		default:
			d, err := gh.PullRequestDetail(ctx, live[0])
			if err != nil {
				return false, "", "", err
			}
			base = d.Base
		}
		shape.PRs = live
		sh = stackShape{key: key, shape: shape, base: base}
		s.stackBase.mu.Lock()
		if s.stackBase.shapes == nil {
			s.stackBase.shapes = map[string]stackShape{}
		}
		s.stackBase.shapes[session] = sh
		s.stackBase.mu.Unlock()
	}
	if sh.base == "" || len(sh.shape.PRs) == 0 {
		return false, "", "", nil
	}
	top = sh.shape.PRs[len(sh.shape.PRs)-1]
	host, repo, number, ok := moor.ParsePR(top)
	n, nerr := strconv.Atoi(number)
	if !ok || nerr != nil {
		return false, "", "", fmt.Errorf("%s is not a pull request URL", top)
	}
	if h := gh.Host(); h != "" {
		host = h
	}
	conflicts, err = s.StackChecker.Conflicts(ctx, moor.StackCheck{Fleet: fleet, Host: host, Repo: repo, Base: sh.base, PR: n, Head: answers[top].Head})
	return conflicts, top, sh.base, err
}

func (s *Service) cachedStackShape(session, key string) (stackShape, bool) {
	s.stackBase.mu.Lock()
	defer s.stackBase.mu.Unlock()
	sh, ok := s.stackBase.shapes[session]
	return sh, ok && sh.key == key
}

func (s *Service) stackBaseLog(session string, err error) {
	s.stackBase.mu.Lock()
	defer s.stackBase.mu.Unlock()
	if s.stackBase.logged == nil {
		s.stackBase.logged = map[string]string{}
	}
	if err == nil {
		delete(s.stackBase.logged, session)
		return
	}
	if s.stackBase.logged[session] == err.Error() {
		return
	}
	s.stackBase.logged[session] = err.Error()
	fmt.Fprintf(s.log(), "admiral: ship %s: the stack's base check cannot run: %v\n", session, err)
}
