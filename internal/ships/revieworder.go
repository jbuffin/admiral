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

// The check reruns after a restart because the reviewing option alone does not say every layer answered, or any launch PR.
func (s *Service) orderPending(sess sessionhost.Session) bool {
	url := sess.Options["url"]
	if s.runState(sess).order.done {
		return false
	}
	if sess.Options["launch-prs"] != "" {
		return true
	}
	if url == "" {
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
	var launch []string
	if v := run.sess.Options["launch-prs"]; v != "" {
		launch = strings.Split(v, ",")
	}
	if (url == "" && len(launch) == 0) || rs.order.done {
		s.mooredMu.Unlock()
		return nil
	}
	var prs []string
	for _, raw := range append([]string{url}, launch...) {
		if pr, repo, ok := prURLOn(raw, gh.Host()); ok && route(gh.Host(), repo) == fleet && !slices.Contains(prs, pr) {
			prs = append(prs, pr)
		}
	}
	host, owner, repo, number, isStack := moor.ParseStack(url)
	if !isStack {
		if len(prs) == 0 {
			rs.order.done = true
		}
		s.mooredMu.Unlock()
		return prs
	}
	if !strings.EqualFold(host, gh.Host()) || route(gh.Host(), owner+"/"+repo) != fleet {
		rs.order.done = true
		s.mooredMu.Unlock()
		return prs
	}
	if rs.order.resolved {
		layers := slices.Clone(rs.order.layers)
		s.mooredMu.Unlock()
		return withoutDuplicates(append(layers, prs...))
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
		return prs
	}
	return withoutDuplicates(append(slices.Clone(layers), prs...))
}

func withoutDuplicates(urls []string) []string {
	var out []string
	for _, u := range urls {
		if !slices.Contains(out, u) {
			out = append(out, u)
		}
	}
	return out
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
		case ok && st.State == "OPEN" && st.Viewer != "" && st.Author != "":
			reviews := st.Author != st.Viewer
			if reviews && !slices.Contains(rs.reviewing, u) {
				rs.reviewing = append(rs.reviewing, u)
				added = true
			}
			// the viewer's own stack layers stay uncarried: they are the viewer's work, not a review
			if !slices.Contains(s.carried(run.sess), u) && (reviews || !slices.Contains(rs.order.layers, u)) {
				rs.found = append(rs.found, u)
				added = true
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
