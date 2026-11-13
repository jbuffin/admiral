package ships

import (
	"context"
	"fmt"
	"os"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/jbuffin/admiral/internal/moor"
	"github.com/jbuffin/admiral/internal/sessionhost"
)

// running is what the poll learned about one live ship's PRs; it belongs to one launch of the session.
type running struct {
	launched string
	found    []string        // candidates the poll confirmed, in the order it found them; the session's prs option holds them too
	rejected map[string]bool // candidates the ship did not open, or that GitHub never answered
	tries    map[string]int  // failed asks per candidate
	poll     polled
}

// maxFreshPerPoll bounds how many unjudged candidates one ship puts to GitHub in one poll.
const maxFreshPerPoll = 20

// maxCandidateTries is how many failed asks reject a candidate for the launch.
const maxCandidateTries = 3

// runAsk is one live ship in a poll: the PRs it carries, and every URL its transcripts printed.
type runAsk struct {
	sess  sessionhost.Session
	urls  []string // carried
	raw   []string // printed in tool results
	cands []string // raw, filtered and not yet judged; set per fleet
}

// runState is the live ship's state, replaced when the session is a later launch; the caller holds mooredMu.
func (s *Service) runState(sess sessionhost.Session) *running {
	if s.running == nil {
		s.running = map[string]*running{}
	}
	rs := s.running[sess.Name]
	if rs == nil || rs.launched != sess.Options["launched"] {
		rs = &running{launched: sess.Options["launched"], rejected: map[string]bool{}, tries: map[string]int{}}
		s.running[sess.Name] = rs
	}
	return rs
}

// carried is the PRs the ship holds as confirmed: the session's prs option plus any found since it was read; the caller holds mooredMu.
func (s *Service) carried(sess sessionhost.Session) []string {
	var urls []string
	if v := sess.Options["prs"]; v != "" {
		urls = strings.Split(v, ",")
	}
	for _, u := range s.runState(sess).found {
		if !slices.Contains(urls, u) {
			urls = append(urls, u)
		}
	}
	return urls
}

// runPRs is a live ship's carried PRs as last polled, each OPEN until a poll answers.
func (s *Service) runPRs(sess sessionhost.Session) ([]moor.PR, polled) {
	s.mooredMu.Lock()
	defer s.mooredMu.Unlock()
	urls := s.carried(sess)
	p := s.runState(sess).poll
	var out []moor.PR
	for _, u := range urls {
		if p.status == nil {
			out = append(out, moor.PR{URL: u, State: "OPEN"})
			continue
		}
		st, found := p.status[u]
		out = append(out, moor.View(u, st, found))
	}
	return out, p
}

// carriesPRs is true while a live ship holds a PR that is still open.
func (s *Service) carriesPRs(sess sessionhost.Session) bool {
	prs, _ := s.runPRs(sess)
	return slices.ContainsFunc(prs, func(p moor.PR) bool { return p.State == "OPEN" })
}

// runningAsks lists the live ships GitHub is asked about, by fleet; it reads candidates before taking mooredMu because Candidates re-enters Get.
func (s *Service) runningAsks() map[string][]runAsk {
	var ships []sessionhost.Session
	cands := map[string][]string{}
	for _, sess := range s.Host.Sessions() {
		if !sess.IsShip() {
			continue
		}
		ships = append(ships, sess)
		if s.Candidates != nil {
			cands[sess.Name] = s.Candidates(sess.Name)
		}
	}
	if s.KeepCandidates != nil {
		names := make([]string, len(ships))
		for i, sess := range ships {
			names[i] = sess.Name
		}
		s.KeepCandidates(names)
	}
	s.mooredMu.Lock()
	defer s.mooredMu.Unlock()
	live := map[string]bool{}
	out := map[string][]runAsk{}
	for _, sess := range ships {
		live[sess.Name] = true
		s.runState(sess)
		urls := s.carried(sess)
		if len(urls)+len(cands[sess.Name]) == 0 {
			continue
		}
		f := sess.Options["fleet"]
		out[f] = append(out[f], runAsk{sess: sess, urls: urls, raw: cands[sess.Name]})
	}
	for name := range s.running {
		if !live[name] {
			delete(s.running, name)
		}
	}
	return out
}

// freshCandidates is the ship's printed URLs worth asking about, on the client's host and routed to the fleet, capped at maxFreshPerPoll.
func (s *Service) freshCandidates(run runAsk, fleet string, host string, route func(host, repo string) string) []string {
	s.mooredMu.Lock()
	defer s.mooredMu.Unlock()
	rs := s.runState(run.sess)
	var out []string
	for _, raw := range run.raw {
		h, repo, num, ok := moor.ParsePR(raw)
		if n, err := strconv.ParseInt(num, 10, 32); !ok || err != nil || num[0] == '0' || n < 1 || !strings.EqualFold(h, host) || route(h, repo) != fleet {
			continue
		}
		u := "https://" + host + "/" + repo + "/pull/" + num
		if slices.Contains(run.urls, u) || slices.Contains(out, u) || rs.rejected[u] {
			continue
		}
		if out = append(out, u); len(out) == maxFreshPerPoll {
			break
		}
	}
	return out
}

// storeRun keeps an answer for a live ship and reports whether a candidate joined its carried PRs; a candidate whose ask fails is rejected after maxCandidateTries.
func (s *Service) storeRun(run runAsk, answer, candAnswer map[string]moor.Status, candFailed map[string]bool) bool {
	s.mooredMu.Lock()
	defer s.mooredMu.Unlock()
	rs := s.runState(run.sess)
	launched, err := time.Parse(time.RFC3339, run.sess.Options["launched"])
	added := false
	for _, u := range run.cands {
		st, ok := candAnswer[u]
		switch {
		case candFailed[u]:
			if rs.tries[u]++; rs.tries[u] >= maxCandidateTries {
				rs.rejected[u] = true
			}
		case ok && err == nil && st.Viewer != "" && st.Author == st.Viewer && !st.CreatedAt.Before(launched):
			rs.found = append(rs.found, u)
			added = true
		default:
			rs.rejected[u] = true
		}
	}
	status := map[string]moor.Status{}
	for _, u := range s.carried(run.sess) {
		if v, ok := answer[u]; ok {
			status[u] = v
		} else if v, ok := candAnswer[u]; ok {
			status[u] = v
		}
	}
	rs.poll = polled{status: status, at: time.Now().UTC().Truncate(time.Second)}
	return added
}

// persistCarried writes the ship's carried PRs to its session, so a restarted Admiral reads them back.
func (s *Service) persistCarried(ctx context.Context, sess sessionhost.Session) {
	s.mooredMu.Lock()
	v := strings.Join(s.carried(sess), ",")
	s.mooredMu.Unlock()
	if err := s.Host.SetOptions(ctx, sess.Name, map[string]string{"prs": v}); err != nil && ctx.Err() == nil {
		fmt.Fprintf(os.Stderr, "admiral: ship %s: its carried PRs cannot be saved on the session: %v\n", sess.Name, err)
	}
}

// markRunStale keeps a live ship's last answer and marks it stale.
func (s *Service) markRunStale(sess sessionhost.Session) {
	s.mooredMu.Lock()
	defer s.mooredMu.Unlock()
	s.runState(sess).poll.stale = true
}

// carriedOpen is the PRs the last poll saw open on a live ship that exits; the exit asks GitHub again before they moor.
func (s *Service) carriedOpen(sess sessionhost.Session) []string {
	s.mooredMu.Lock()
	defer s.mooredMu.Unlock()
	st := s.runState(sess).poll.status
	var out []string
	for _, u := range s.carried(sess) {
		if st[u].State == "OPEN" {
			out = append(out, u)
		}
	}
	return out
}
