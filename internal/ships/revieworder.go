package ships

import (
	"context"
	"slices"
	"strconv"
	"strings"

	"github.com/jbuffin/admiral/internal/moor"
	"github.com/jbuffin/admiral/internal/sessionhost"
)

// prURLOn is a PR URL on host in its canonical form, with its owner/name.
func prURLOn(raw, host string) (url, repo string, ok bool) {
	h, repo, num, ok := moor.ParsePR(raw)
	if n, err := strconv.ParseInt(num, 10, 32); !ok || err != nil || num[0] == '0' || n < 1 || !strings.EqualFold(h, host) {
		return "", "", false
	}
	return "https://" + host + "/" + repo + "/pull/" + num, repo, true
}

// The check reruns after a restart because the reviewing option alone does not say every layer answered.
func (s *Service) orderPending(sess sessionhost.Session) bool {
	url := sess.Options["url"]
	if url == "" || s.runState(sess).order.done {
		return false
	}
	if _, _, _, ok := moor.ParsePR(url); ok {
		return true
	}
	_, _, _, _, ok := moor.ParseStack(url)
	return ok
}

func (s *Service) orderTargets(ctx context.Context, run runAsk, gh moor.GitHub, fleet string, route func(host, repo string) string) []string {
	s.mooredMu.Lock()
	rs := s.runState(run.sess)
	url := run.sess.Options["url"]
	if url == "" || rs.order.done {
		s.mooredMu.Unlock()
		return nil
	}
	if pr, repo, ok := prURLOn(url, gh.Host()); ok {
		if route(gh.Host(), repo) != fleet {
			rs.order.done = true
			pr = ""
		}
		s.mooredMu.Unlock()
		if pr == "" {
			return nil
		}
		return []string{pr}
	}
	host, owner, repo, number, ok := moor.ParseStack(url)
	if !ok || !strings.EqualFold(host, gh.Host()) || route(gh.Host(), owner+"/"+repo) != fleet {
		rs.order.done = true
		s.mooredMu.Unlock()
		return nil
	}
	if rs.order.resolved {
		layers := slices.Clone(rs.order.layers)
		s.mooredMu.Unlock()
		return layers
	}
	s.mooredMu.Unlock()
	layers, found, err := gh.StackOpenPRs(ctx, owner, repo, number)
	if ctx.Err() != nil {
		return nil
	}
	s.mooredMu.Lock()
	defer s.mooredMu.Unlock()
	switch {
	case err != nil:
		rs.order.fail()
	case !found || len(layers) == 0:
		rs.order.done = true
	default:
		rs.order.resolved, rs.order.layers = true, layers
	}
	if err != nil || !found {
		return nil
	}
	return slices.Clone(layers)
}

func (o *orderCheck) fail() {
	if o.tries++; o.tries >= maxCandidateTries {
		o.done = true
	}
}

// Only a clean answer of every target ends the check; the caller holds mooredMu.
func (s *Service) judgeOrder(rs *running, run runAsk, answer, candAnswer map[string]moor.Status, candFailed map[string]bool) bool {
	if len(run.order) == 0 || rs.order.done {
		return false
	}
	added, failed := false, false
	for _, u := range run.order {
		st, ok := answer[u]
		if !ok {
			st, ok = candAnswer[u]
		}
		switch {
		case candFailed[u]:
			failed = true
		case ok && st.State == "OPEN" && st.Viewer != "" && st.Author != "" && st.Author != st.Viewer:
			if !slices.Contains(rs.reviewing, u) {
				rs.reviewing = append(rs.reviewing, u)
				added = true
			}
			if !slices.Contains(s.carried(run.sess), u) {
				rs.found = append(rs.found, u)
			}
		}
	}
	if failed {
		rs.order.fail()
	} else {
		rs.order.done = true
	}
	return added
}
