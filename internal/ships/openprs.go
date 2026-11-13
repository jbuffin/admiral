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
}

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
			s.openMu.Unlock()
			continue
		}
		raw, _ := s.loadReport(sess.Name)
		rep := moor.ParseReport(raw)
		if s.open == nil {
			s.open = map[string]*openState{}
		}
		st = &openState{stamp: stamp, result: rep.Result}
		s.open[sess.Name] = st
		s.openMu.Unlock()
		if len(rep.PRs) > 0 && s.GitHub != nil {
			s.checkOpenPRs(ctx, sess, st, rep.PRs)
		}
	}
	s.openMu.Lock()
	for name := range s.open {
		if !live[name] {
			delete(s.open, name)
		}
	}
	s.openMu.Unlock()
}

func (s *Service) checkOpenPRs(ctx context.Context, sess sessionhost.Session, st *openState, listed []string) {
	s.settleWG.Add(1)
	go func() {
		defer s.settleWG.Done()
		prs := s.countedFrom(ctx, sess, listed)
		s.openMu.Lock()
		current := s.open[sess.Name] == st
		if current {
			st.prs = prs
		}
		s.openMu.Unlock()
		if current && ctx.Err() == nil {
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
			s.moorShip(ctx, sess, prs, "")
		}()
	}
}

// moorShip moors a live ship: its record first, then the end of its session, so a crash between leaves it in one place or both.
// It reports whether the ship has a record; a ship whose record cannot be written stays live.
func (s *Service) moorShip(ctx context.Context, sess sessionhost.Session, prs []string, via string) bool {
	if !s.moorVia(ctx, sess, prs, via) {
		return false
	}
	s.recordKilled(sess)
	_ = s.endSession(ctx, sess.Name, true)
	return true
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
	prs := s.countedPRs(ctx, sess)
	if len(prs) == 0 {
		writeError(w, http.StatusConflict, "ship "+sess.Name+" has no open PR to moor")
		return
	}
	if !s.moorShip(ctx, sess, prs, viaOf(r)) {
		writeError(w, http.StatusInternalServerError, fmt.Sprintf("ship %s: its moored record cannot be written, so it stays live", sess.Name))
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"moored": sess.Name})
}

func (s *Service) countedFrom(ctx context.Context, sess sessionhost.Session, prs []string) []string {
	if len(prs) == 0 || s.GitHub == nil {
		return nil
	}
	fleet := sess.Options["fleet"]
	router := github.RouterFromFleets(s.Launcher.Config().Fleets)
	return moor.Counted(ctx, moor.Check{Fleet: fleet, GitHub: s.GitHub(fleet), Route: router.Route}, prs)
}
