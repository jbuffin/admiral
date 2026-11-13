package ships

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/jbuffin/admiral/internal/github"
	"github.com/jbuffin/admiral/internal/moor"
	"github.com/jbuffin/admiral/internal/outcomes"
	"github.com/jbuffin/admiral/internal/sessionhost"
	"github.com/jbuffin/admiral/internal/transcriptfs"
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
		if len(prs) == 0 || !s.moor(ctx, sess, prs) {
			s.endMooring(sess)
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

// countedPRs is the PRs GitHub confirms open in the ship's fleet out of its report's and the ones it carries. When GitHub
// cannot be asked, the carried PRs the last poll saw open stand.
func (s *Service) countedPRs(ctx context.Context, sess sessionhost.Session) []string {
	carried := s.carriedOpen(sess)
	prs := slices.Clone(carried)
	for _, u := range s.report(sess.Name).PRs {
		if !slices.Contains(prs, u) {
			prs = append(prs, u)
		}
	}
	if len(prs) == 0 {
		return nil
	}
	var counted []string
	ok := false
	if s.GitHub != nil {
		fleet := sess.Options["fleet"]
		router := github.RouterFromFleets(s.Launcher.Config().Fleets)
		counted, ok = moor.CountedOK(ctx, moor.Check{Fleet: fleet, GitHub: s.GitHub(fleet), Route: router.Route}, prs)
	}
	if !ok {
		return carried
	}
	return counted
}

// moor writes the record before the session goes, so a crash between leaves the ship in one place or both.
// It reports whether the ship has a record.
func (s *Service) moor(ctx context.Context, sess sessionhost.Session, prs []string) bool {
	name, o := sess.Name, sess.Options
	s.mooredMu.Lock()
	s.loadMoored()
	round, _ := strconv.Atoi(o["round"])
	for _, r := range s.moored {
		if r.Session == name && r.Round >= round { // a kill that failed is retried: the first record and time stand
			s.mooredMu.Unlock()
			return true
		}
	}
	s.mooredMu.Unlock()
	if s.CaptureResume != nil {
		s.CaptureResume(name) // before the report goes
	}
	rec := moor.Record{Name: o["name"], Session: name, Fleet: o["fleet"], Harness: o["harness"], Model: o["model"],
		Skill: moor.Skill{Kind: o["skill-kind"], Name: o["skill"]}, Checkout: o["dir"], PRs: prs,
		Source: o["source"], Panel: o["panel"], OrderID: o["order"], URL: o["url"], Issues: splitIssues(o["issues"]), Title: o["title"], Link: o["link"],
		Resume: s.report(name).Resume, Round: round, Via: o["via"], Chat: o["chat"], Routine: o["routine"], Commitment: o["commitment"], Launched: o["launched"], Resources: splitResources(o["resources"]), MooredAt: time.Now().UTC().Truncate(time.Second)}
	if err := s.recordRound(ctx, sess, rec, time.Now().UTC()); err != nil {
		fmt.Fprintf(os.Stderr, "admiral: ship %s: its round history cannot be written, so it keeps no dividers: %v\n", name, err)
	}
	if err := moor.Write(s.Launcher.Home, rec); err != nil {
		fmt.Fprintf(os.Stderr, "admiral: ship %s: its moored record cannot be written, so it sails off: %v\n", name, err)
		return false
	}
	s.setMoored(rec)
	s.record(outcomes.Record{Ship: name, Event: "moored", Fields: map[string]any{"prs": prs}}, nil)
	s.endRound(sess, "moored")
	return true
}

// recordRound appends the round the pane just finished to the ship's history, before the record goes, under wakeMu so an end cannot leave a file behind.
// The transcript is kept only when the pane's option binds to its start command; any other round keeps its place with none.
func (s *Service) recordRound(ctx context.Context, sess sessionhost.Session, rec moor.Record, ended time.Time) error {
	o := sess.Options
	r := moor.Round{Round: rec.Round, Fleet: rec.Fleet, Ended: ended}
	r.Started, _ = time.Parse(time.RFC3339, o["launched"])
	if sess.PaneID != "" && o["harness"] == "claude" {
		c, cancel := context.WithTimeout(ctx, 3*time.Second)
		defer cancel()
		host := sessionhost.NewServer(s.Launcher.Tmux, s.Launcher.Socket)
		raw, rerr := host.ReadPaneOption(c, sess.PaneID, "transcript")
		cmd, cerr := host.ReadPaneStartCommand(c, sess.PaneID)
		if rerr == nil && cerr == nil {
			if path, id, ok := transcriptfs.Bind(raw, cmd); ok {
				r.Transcript, r.SessionID = path, id
			}
		}
	}
	s.wakeMu.Lock()
	defer s.wakeMu.Unlock()
	return moor.AppendRound(s.Launcher.Home, rec.Name, r)
}

func splitResources(v string) []string {
	if v == "" {
		return nil
	}
	return strings.Split(v, ",")
}

// endMooring ends a woken ship's last round for good: its mooring will not come back, so its wake state goes.
func (s *Service) endMooring(sess sessionhost.Session) {
	s.endRound(sess, "sailed")
	name := sess.Options["name"]
	if name == "" {
		return
	}
	s.removeWake(name)
}

// endRound logs how a woken ship's round ended, once per round; a ship that was never woken has no round.
func (s *Service) endRound(sess sessionhost.Session, end string) {
	round, _ := strconv.Atoi(sess.Options["round"])
	if round == 0 {
		return
	}
	// The launch time and pane tell this ship from a later one under the same name, so the name's one entry is replaced.
	id := fmt.Sprintf("%s/%s/%d", sess.Options["launched"], sess.PaneID, round)
	s.mu.Lock()
	logged := s.roundLogged[sess.Name] == id
	if s.roundLogged == nil {
		s.roundLogged = map[string]string{}
	}
	s.roundLogged[sess.Name] = id
	s.mu.Unlock()
	if !logged {
		s.record(outcomes.Record{Ship: sess.Name, Event: "round", Fields: map[string]any{"round": round, "end": end}}, nil)
	}
}

func (s *Service) setMoored(rec moor.Record) {
	s.mooredMu.Lock()
	defer s.mooredMu.Unlock()
	s.loadMoored()
	delete(s.running, rec.Session)
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

// Checkout is the directory a live ship runs in, or a moored ship's recorded checkout, which is still on disk.
// Only read-only views of the work (its changes) use it; routes that talk to a session still need a live one.
func (s *Service) Checkout(session string) (string, bool) {
	if sh, ok := s.Get(session); ok {
		return sh.Dir, true
	}
	s.mooredMu.Lock()
	defer s.mooredMu.Unlock()
	s.loadMoored()
	for _, r := range s.moored {
		if r.Session == session {
			return r.Checkout, true
		}
	}
	return "", false
}

// mooredShips lists the records whose ship has no live session.
func (s *Service) mooredShips(live map[string]bool) []Ship {
	s.mooredMu.Lock()
	s.loadMoored()
	recs := append([]moor.Record(nil), s.moored...)
	prs := make([][]moor.PR, len(recs))
	polls := make([]polled, len(recs))
	methods := make([]string, len(recs))
	for i, r := range recs {
		prs[i], polls[i] = s.prsOf(r), s.mooredPolls[r.Session]
		if repo, ok := repoOfPRs(r.PRs); ok {
			methods[i] = s.mergeMethods[methodKey(r.Fleet, repo)]
		}
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
			Source: r.Source, Panel: r.Panel, OrderID: r.OrderID, URL: r.URL, Issues: r.Issues, Title: r.Title, Link: r.Link, Titled: true, Via: r.Via, Chat: r.Chat, Routine: r.Routine, Commitment: r.Commitment, Launched: r.Launched, PRs: prs[i], MooredAt: r.MooredAt.UTC().Format(time.RFC3339), Stale: polls[i].stale, Waiting: polls[i].waiting, NoResume: !moor.ValidResume(r.Resume),
			Crew: []CrewBoat{}, Subagents: []sessionhost.Subagent{}}
		sh.StackReady, sh.ReadyUpTo = moor.StackReady(prs[i])
		sh.MergeMethod = methods[i]
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

// Moored is the moored ship with this session when it has no live one; only its identity is filled.
func (s *Service) Moored(session string) (Ship, bool) {
	if _, live := s.Get(session); live {
		return Ship{}, false
	}
	s.mooredMu.Lock()
	defer s.mooredMu.Unlock()
	s.loadMoored()
	for _, r := range s.moored {
		if r.Session == session {
			return Ship{Session: r.Session, Name: r.Name, Fleet: r.Fleet, Harness: r.Harness, Status: StatusMoored}, true
		}
	}
	return Ship{}, false
}

// Round is the feedback round a live ship is in; 0 is its first.
func (s *Service) Round(session string) int {
	for _, sess := range s.Host.Sessions() {
		if sess.Name == session {
			n, _ := strconv.Atoi(sess.Options["round"])
			return n
		}
	}
	return 0
}

// Rounds is the history of a ship's earlier rounds, oldest first.
func (s *Service) Rounds(name string) ([]moor.Round, error) {
	return moor.ReadRounds(s.Launcher.Home, name)
}
