package ships

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/jbuffin/admiral/internal/github"
	"github.com/jbuffin/admiral/internal/moor"
	"github.com/jbuffin/admiral/internal/outcomes"
	"github.com/jbuffin/admiral/internal/sessionhost"
)

const StatusMoored = "moored"

// settle ends a ship that exited cleanly: it moors when GitHub confirms an open PR
// it carries, and sails off otherwise. The check can wait on gh, so it runs apart
// from the pass loop, one at a time per ship.
func (s *Service) settle(ctx context.Context, sess sessionhost.Session) {
	s.mu.Lock()
	if s.settling[sess.Name] {
		s.mu.Unlock()
		return
	}
	if s.settling == nil {
		s.settling = map[string]bool{}
	}
	s.settling[sess.Name] = true
	s.mu.Unlock()
	s.settleWG.Add(1)
	go func() {
		defer s.settleWG.Done()
		defer func() {
			s.mu.Lock()
			delete(s.settling, sess.Name)
			s.mu.Unlock()
		}()
		prs := s.countedPRs(ctx, sess)
		if ctx.Err() != nil { // Admiral is stopping: the next start checks again
			return
		}
		if len(prs) > 0 {
			s.moor(ctx, sess, prs)
		}
		if err := s.killShip(ctx, sess.Name, true); err == nil {
			_ = s.Host.Sync(ctx)
			s.broadcast()
		}
	}()
}

func (s *Service) report(session string) moor.Report {
	return moor.ParseReport(s.readReport(session))
}

// countedPRs is the PRs of the ship's report that GitHub confirms open in the ship's fleet.
func (s *Service) countedPRs(ctx context.Context, sess sessionhost.Session) []string {
	prs := s.report(sess.Name).PRs
	if len(prs) == 0 || s.GitHub == nil {
		return nil
	}
	fleet := sess.Options["fleet"]
	router := github.RouterFromFleets(s.Launcher.Config().Fleets)
	return moor.Counted(ctx, moor.Check{Fleet: fleet, GitHub: s.GitHub(fleet), Route: router.Route}, prs)
}

// moor writes the record before the session goes, so a crash between leaves the ship in one place or both.
func (s *Service) moor(ctx context.Context, sess sessionhost.Session, prs []string) {
	name, o := sess.Name, sess.Options
	s.mooredMu.Lock()
	s.loadMoored()
	for _, r := range s.moored {
		if r.Session == name { // a kill that failed is retried: the first record and time stand
			s.mooredMu.Unlock()
			return
		}
	}
	s.mooredMu.Unlock()
	if s.CaptureResume != nil {
		s.CaptureResume(name) // before the report goes
	}
	rec := moor.Record{Name: o["name"], Session: name, Fleet: o["fleet"], Harness: o["harness"], Model: o["model"],
		Skill: moor.Skill{Kind: o["skill-kind"], Name: o["skill"]}, Checkout: o["dir"], PRs: prs,
		Source: o["source"], Panel: o["panel"], OrderID: o["order"], URL: o["url"], Title: o["title"],
		Resume: s.report(name).Resume, MooredAt: time.Now().UTC().Truncate(time.Second)}
	if err := moor.Write(s.Launcher.Home, rec); err != nil {
		fmt.Fprintf(os.Stderr, "admiral: ship %s: its moored record cannot be written, so it sails off: %v\n", name, err)
		return
	}
	s.setMoored(rec)
	s.record(outcomes.Record{Ship: name, Event: "moored", Fields: map[string]any{"prs": prs}}, nil)
}

func (s *Service) setMoored(rec moor.Record) {
	s.mooredMu.Lock()
	defer s.mooredMu.Unlock()
	s.loadMoored()
	delete(s.mooredPolls, rec.Session) // a poll answered for an earlier ship of this session says nothing of this one
	for i, r := range s.moored {
		if r.Session == rec.Session {
			s.moored[i] = rec
			return
		}
	}
	s.moored = append(s.moored, rec)
}

// loadMoored reads the records once; the caller holds mooredMu.
func (s *Service) loadMoored() {
	if s.mooredLoaded {
		return
	}
	s.mooredLoaded = true
	recs, err := moor.List(s.Launcher.Home)
	if err != nil {
		fmt.Fprintf(os.Stderr, "admiral: the moored records cannot be read: %v\n", err)
	}
	s.moored = recs
}

// mooredShips lists the records whose ship has no live session.
func (s *Service) mooredShips(live map[string]bool) []Ship {
	s.mooredMu.Lock()
	s.loadMoored()
	recs := append([]moor.Record(nil), s.moored...)
	prs := make([][]moor.PR, len(recs))
	polls := make([]polled, len(recs))
	for i, r := range recs {
		prs[i], polls[i] = s.prsOf(r), s.mooredPolls[r.Session]
	}
	s.mooredMu.Unlock()
	var out []Ship
	cfg := s.Launcher.Config()
	for i, r := range recs {
		if live[r.Session] {
			continue
		}
		sh := Ship{Session: r.Session, Name: r.Name, Fleet: r.Fleet, Harness: r.Harness, Dir: r.Checkout,
			Repo: filepath.Base(r.Checkout), Status: StatusMoored,
			Source: r.Source, Panel: r.Panel, OrderID: r.OrderID, URL: r.URL, Title: r.Title, PRs: prs[i], MooredAt: r.MooredAt.UTC().Format(time.RFC3339), Stale: polls[i].stale,
			Crew: []CrewBoat{}, Subagents: []sessionhost.Subagent{}}
		if !polls[i].at.IsZero() {
			sh.PolledAt = polls[i].at.Format(time.RFC3339)
		}
		if f, ok := cfg.Fleet(r.Fleet); ok {
			sh.FleetName, sh.FleetColor = f.Name, f.Color
		}
		if r.Skill.Name != "" {
			name := r.Skill.Name
			sh.Skill = &name
		}
		if p, ok := s.State.Position(r.Session); ok {
			sh.Position = &p
		}
		out = append(out, sh)
	}
	return out
}
