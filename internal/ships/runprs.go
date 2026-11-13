package ships

import (
	"slices"
	"strings"
	"time"

	"github.com/jbuffin/admiral/internal/moor"
	"github.com/jbuffin/admiral/internal/sessionhost"
)

// running is what the poll learned about one live ship's PRs; it belongs to one launch of the session.
type running struct {
	launched string
	found    []string        // candidates the poll confirmed, in the order it found them
	rejected map[string]bool // candidates GitHub answered that the ship did not open
	poll     polled
}

// runAsk is one live ship in a poll: every URL to ask for, and the candidates not yet judged.
type runAsk struct {
	sess  sessionhost.Session
	urls  []string
	cands []string
}

// runState is the live ship's state, replaced when the session is a later launch; the caller holds mooredMu.
func (s *Service) runState(sess sessionhost.Session) *running {
	if s.running == nil {
		s.running = map[string]*running{}
	}
	rs := s.running[sess.Name]
	if rs == nil || rs.launched != sess.Options["launched"] {
		rs = &running{launched: sess.Options["launched"], rejected: map[string]bool{}}
		s.running[sess.Name] = rs
	}
	return rs
}

// carried is the PRs the ship holds as confirmed: the ones its mooring handed over, then the ones the poll found; the caller holds mooredMu.
func (s *Service) carried(sess sessionhost.Session) []string {
	var urls []string
	if v := sess.Options["prs"]; v != "" {
		urls = strings.Split(v, ",")
	}
	return append(urls, s.runState(sess).found...)
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

// runningAsks lists the live ships GitHub is asked about, by fleet. The candidates are read before mooredMu is taken,
// since reading a transcript can be slow and every list of the ships waits on that lock.
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
	s.mooredMu.Lock()
	defer s.mooredMu.Unlock()
	live := map[string]bool{}
	out := map[string][]runAsk{}
	for _, sess := range ships {
		live[sess.Name] = true
		rs := s.runState(sess)
		urls := s.carried(sess)
		var fresh []string
		for _, u := range cands[sess.Name] {
			if !slices.Contains(urls, u) && !slices.Contains(fresh, u) && !rs.rejected[u] {
				fresh = append(fresh, u)
			}
		}
		if len(urls)+len(fresh) == 0 {
			continue
		}
		f := sess.Options["fleet"]
		out[f] = append(out[f], runAsk{sess: sess, urls: append(slices.Clone(urls), fresh...), cands: fresh})
	}
	for name := range s.running {
		if !live[name] {
			delete(s.running, name)
		}
	}
	return out
}

// storeRun keeps an answer for a live ship: a candidate the fleet's user opened since the launch joins the carried PRs.
func (s *Service) storeRun(run runAsk, answer map[string]moor.Status) {
	s.mooredMu.Lock()
	defer s.mooredMu.Unlock()
	rs := s.runState(run.sess)
	launched, err := time.Parse(time.RFC3339, run.sess.Options["launched"])
	for _, u := range run.cands {
		st, ok := answer[u]
		switch {
		case !ok:
		case err == nil && st.Viewer != "" && st.Author == st.Viewer && !st.CreatedAt.Before(launched):
			rs.found = append(rs.found, u)
		default:
			rs.rejected[u] = true
		}
	}
	status := map[string]moor.Status{}
	for _, u := range s.carried(run.sess) {
		if v, ok := answer[u]; ok {
			status[u] = v
		}
	}
	rs.poll = polled{status: status, at: time.Now().UTC().Truncate(time.Second)}
}

// markRunStale keeps a live ship's last answer and marks it stale.
func (s *Service) markRunStale(sess sessionhost.Session) {
	s.mooredMu.Lock()
	defer s.mooredMu.Unlock()
	s.runState(sess).poll.stale = true
}

// carriedOpen is the PRs the last poll saw open on a live ship that exits, which moor with it beside the report's.
// A PR no poll has answered for is not counted, so a woken ship that never polled sails off as it did.
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
