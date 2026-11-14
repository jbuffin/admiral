package ships

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"slices"
	"sync"
	"time"

	"github.com/jbuffin/admiral/internal/github"
	"github.com/jbuffin/admiral/internal/moor"
	"github.com/jbuffin/admiral/internal/sessionhost"
)

// openState is what the server knows of one live ship's report, kept until the report's stamp changes.
type openState struct {
	stamp  reportStamp
	result string
	prs    []string // the report's PRs that GitHub confirms open, in report order
	tried  bool     // a moor was attempted for this report

	pending  []string // the report's PRs until GitHub answers
	inflight bool
	retryAt  time.Time
	backoff  time.Duration
}

// A check GitHub did not answer is retried with a doubling wait, so a long outage costs a few calls, not one per tick.
var openRetryFirst, openRetryMax = 30 * time.Second, 4 * time.Minute

// reportStamp says a report file changed without reading it.
type reportStamp struct {
	mtime time.Time
	size  int64
}

func (s *Service) stampOf(session string) (reportStamp, bool) {
	_, path, _, ok := s.shipFiles(session)
	if !ok {
		return reportStamp{}, false
	}
	fi, err := os.Lstat(path)
	if err != nil || !fi.Mode().IsRegular() {
		return reportStamp{}, false
	}
	return reportStamp{fi.ModTime(), fi.Size()}, true
}

// openPRsOf is a live ship's open PRs as the last check left them. This is the one place that answers it,
// so live discovery without a report can feed it later.
func (s *Service) openPRsOf(session string) []string {
	s.openMu.Lock()
	defer s.openMu.Unlock()
	if st := s.open[session]; st != nil && len(st.prs) > 0 {
		return slices.Clone(st.prs)
	}
	return nil
}

// refreshOpenPRs reads each live ship's report when its file changed and asks GitHub about its PRs, off the pass loop.
func (s *Service) refreshOpenPRs(ctx context.Context) {
	live := map[string]bool{}
	for _, sess := range s.Host.Sessions() {
		if !sess.IsShip() || sess.PaneDead {
			continue
		}
		live[sess.Name] = true
		stamp, ok := s.stampOf(sess.Name)
		s.openMu.Lock()
		st := s.open[sess.Name]
		switch {
		case !ok:
			delete(s.open, sess.Name)
			s.openMu.Unlock()
			continue
		case st != nil && st.stamp == stamp:
			s.dueCheck(ctx, sess, st)
			continue
		}
		raw, _ := s.loadReport(sess.Name)
		rep := moor.ParseReport(raw)
		if s.open == nil {
			s.open = map[string]*openState{}
		}
		st = &openState{stamp: stamp, result: rep.Result, pending: rep.PRs}
		s.open[sess.Name] = st
		s.dueCheck(ctx, sess, st)
	}
	s.openMu.Lock()
	for name := range s.open {
		if !live[name] {
			delete(s.open, name)
		}
	}
	s.openMu.Unlock()
}

// dueCheck starts the GitHub check of a report's PRs when one is pending and due. It takes openMu held and releases it.
func (s *Service) dueCheck(ctx context.Context, sess sessionhost.Session, st *openState) {
	if len(st.pending) == 0 || st.inflight || s.GitHub == nil || time.Now().Before(st.retryAt) {
		s.openMu.Unlock()
		return
	}
	st.inflight = true
	listed := st.pending
	s.openMu.Unlock()
	s.settleWG.Add(1)
	go func() {
		defer s.settleWG.Done()
		prs, answered := s.countedFrom(ctx, sess, listed)
		s.openMu.Lock()
		current := s.open[sess.Name] == st
		if current {
			st.inflight = false
			if answered {
				st.prs, st.pending = prs, nil
			} else {
				st.backoff = min(max(st.backoff*2, openRetryFirst), openRetryMax)
				st.retryAt = time.Now().Add(st.backoff)
			}
		}
		s.openMu.Unlock()
		if current && answered && ctx.Err() == nil {
			s.broadcast()
		}
	}()
}

// autoMoor moors each live ship whose own turn has ended on a done report that has open PRs.
func (s *Service) autoMoor(ctx context.Context) {
	for _, sess := range s.Host.Sessions() {
		if !sess.IsShip() || sess.PaneDead || sess.Options[sessionhost.OptReturning] != "" ||
			status(sess, time.Now()) != StatusNeedsYou || reasonOf(sess) != "stop" {
			continue
		}
		s.openMu.Lock()
		st := s.open[sess.Name]
		due := st != nil && !st.tried && st.result == "done" && len(st.prs) > 0
		var prs []string
		if due {
			st.tried = true
			prs = slices.Clone(st.prs)
		}
		s.openMu.Unlock()
		if !due {
			continue
		}
		s.settleWG.Add(1)
		go func() {
			defer s.settleWG.Done()
			s.moorShip(ctx, sess, prs, "", true)
		}()
	}
}

// moorShip moors a live ship: its record first, then the end of its session, so a crash between leaves it in one place or both.
// recorded says the ship has a record; err is a session that would not end, which a retried Moor ends again. A ship whose
// record cannot be written stays live. It holds the ship's lock, so a Stop that came first leaves nothing to moor, and an
// automatic moor also yields to a Return to port that began meanwhile.
func (s *Service) moorShip(ctx context.Context, sess sessionhost.Session, prs []string, via string, auto bool) (recorded bool, err error) {
	unlock := s.lockShip(sess.Name)
	defer unlock()
	cur, ok := s.find(sess.Name)
	if !ok || cur.PaneDead || auto && cur.Options[sessionhost.OptReturning] != "" {
		return false, nil
	}
	if !s.moorVia(ctx, cur, prs, via) {
		return false, nil
	}
	s.recordKilled(cur)
	return true, s.endSession(ctx, sess.Name, true)
}

// lockShip serializes the endings of one ship (Stop, Moor and an automatic moor) and returns the release.
func (s *Service) lockShip(name string) func() {
	s.mu.Lock()
	if s.ending == nil {
		s.ending = map[string]*sync.Mutex{}
	}
	m := s.ending[name]
	if m == nil {
		m = &sync.Mutex{}
		s.ending[name] = m
	}
	s.mu.Unlock()
	m.Lock()
	return m.Unlock
}

// moorWanted reads the optional body of Stop and Return to port; ok is false once it has answered an unreadable one.
func moorWanted(w http.ResponseWriter, r *http.Request) (moor, ok bool) {
	var req struct {
		Moor bool `json:"moor"`
	}
	err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<16)).Decode(&req)
	if err != nil && !errors.Is(err, io.EOF) {
		writeError(w, http.StatusBadRequest, "request is not valid JSON: "+err.Error())
		return false, false
	}
	return req.Moor, true
}

// serveMoor answers Stop or Return to port with the Moor choice: a fresh check of the ship's PRs, then the mooring.
func (s *Service) serveMoor(w http.ResponseWriter, r *http.Request, sess sessionhost.Session) {
	// Not the request's context: a client that hangs up must not leave a ship half moored.
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	carried := s.carriedOpen(sess)
	prs, answered := s.countedFrom(ctx, sess, withCarried(carried, s.report(sess.Name).PRs))
	if !answered && s.GitHub != nil {
		prs = carried
	}
	if len(prs) == 0 {
		msg := "ship " + sess.Name + " has no open PR to moor"
		if !answered {
			msg = "ship " + sess.Name + ": GitHub did not confirm its PRs, so it is not moored"
		}
		writeError(w, http.StatusConflict, msg)
		return
	}
	recorded, err := s.moorShip(ctx, sess, prs, viaOf(r), false)
	if !recorded {
		writeError(w, http.StatusInternalServerError, fmt.Sprintf("ship %s: its moored record cannot be written, so it stays live", sess.Name))
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"moored": sess.Name})
}

func (s *Service) countedFrom(ctx context.Context, sess sessionhost.Session, prs []string) (counted []string, answered bool) {
	if len(prs) == 0 || s.GitHub == nil {
		return nil, true
	}
	fleet := sess.Options["fleet"]
	router := github.RouterFromFleets(s.Launcher.Config().Fleets)
	return moor.CountedAnswered(ctx, moor.Check{Fleet: fleet, GitHub: s.GitHub(fleet), Route: router.Route}, prs)
}

// withCarried is the carried PRs then the report's, each URL once.
func withCarried(carried, report []string) []string {
	out := slices.Clone(carried)
	for _, u := range report {
		if !slices.Contains(out, u) {
			out = append(out, u)
		}
	}
	return out
}
