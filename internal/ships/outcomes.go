package ships

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"maps"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/jbuffin/admiral/internal/inbox"
	"github.com/jbuffin/admiral/internal/launcher"
	"github.com/jbuffin/admiral/internal/outcomes"
	"github.com/jbuffin/admiral/internal/sessionhost"
	"github.com/jbuffin/admiral/internal/state"
)

var tallyOptions = []string{sessionhost.OptNeedsCount, sessionhost.OptNeedsSecs, sessionhost.OptStopSince}

// The ship writes the report, so a larger one counts as invalid.
const maxReport = 1 << 20

// Any process in a pane can set @admiral-reason, so it must match this to reach the log.
var reasonPattern = sessionhost.SafeToken

// tally's Count, Seconds and Since live in tmux session options, so a restart of
// Admiral keeps them.
type tally struct {
	Status  string
	Count   int       // needs_you records written
	Seconds float64   // how long the finished stops lasted
	Since   time.Time // when the open stop began; zero when none
}

// An empty option value is unset.
func (t tally) options() map[string]string {
	o := map[string]string{}
	for _, n := range tallyOptions {
		o[n] = ""
	}
	if t.Count > 0 {
		o[sessionhost.OptNeedsCount] = strconv.Itoa(t.Count)
	}
	if t.Seconds > 0 {
		o[sessionhost.OptNeedsSecs] = strconv.FormatFloat(t.Seconds, 'f', 3, 64)
	}
	if !t.Since.IsZero() {
		o[sessionhost.OptStopSince] = t.Since.UTC().Format(time.RFC3339Nano)
	}
	return o
}

// baseline starts from the current status, so an open stop gets no second
// needs_you; a stop that ended while Admiral was down is closed by the first advance.
func baseline(sess sessionhost.Session, now time.Time) tally {
	t := tally{Status: status(sess, now)}
	t.Count, _ = strconv.Atoi(sess.Options[sessionhost.OptNeedsCount])
	t.Seconds, _ = strconv.ParseFloat(sess.Options[sessionhost.OptNeedsSecs], 64)
	// ParseFloat takes NaN and Inf, and seconds() overflows on a huge value; JSON can't hold either.
	if math.IsNaN(t.Seconds) || math.IsInf(t.Seconds*10, 0) {
		t.Seconds = 0
	}
	if since, err := time.Parse(time.RFC3339Nano, sess.Options[sessionhost.OptStopSince]); err == nil {
		t.Since = since
		if t.Status != StatusNeedsYou {
			t.Status = StatusNeedsYou // the stop was open when Admiral stopped
		}
	} else if t.Status == StatusNeedsYou {
		t.Since, t.Count = now, max(t.Count, 1)
	}
	return t
}

func reasonOf(sess sessionhost.Session) string {
	switch {
	case sess.State == "":
		return "idle"
	case reasonPattern.MatchString(sess.Reason):
		return sess.Reason
	}
	return "unknown"
}

// advance: a stop is one stop whatever its reason does, and one the pane's death
// ends gets no underway.
func advance(t tally, st, reason string, at time.Time) (tally, []outcomes.Record) {
	var recs []outcomes.Record
	was, is := t.Status == StatusNeedsYou, st == StatusNeedsYou
	switch {
	case is && !was:
		t.Count++
		t.Since = at
		recs = append(recs, outcomes.Record{Event: "needs_you", Fields: map[string]any{"reason": reason}})
	case was && !is:
		var waited time.Duration
		if !t.Since.IsZero() {
			waited = max(at.Sub(t.Since), 0)
		}
		t.Seconds += waited.Seconds()
		t.Since = time.Time{}
		if st == StatusUnderway {
			recs = append(recs, outcomes.Record{Event: "underway", Fields: map[string]any{"waited_s": seconds(waited.Seconds())}})
		}
	}
	t.Status = st
	return t, recs
}

func seconds(s float64) float64 { return math.Round(s*10) / 10 }

type track struct {
	tally
	saved map[string]string // the options last written to tmux
	end   *endState         // set once `ended` is queued
	seen  bool              // the host has listed the session; a launch is tracked before it does
}

type endState struct{ written, failed, clean bool }

type ending struct {
	sess  sessionhost.Session
	tally tally
	end   *endState
	owner *track
}

func (s *Service) baselineAll() {
	now := time.Now()
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.tracks == nil {
		s.tracks = map[string]*track{}
	}
	for _, sess := range s.Host.Sessions() {
		if _, ok := s.tracks[sess.Name]; ok || !sess.IsShip() {
			continue
		}
		saved := map[string]string{}
		for _, n := range tallyOptions {
			saved[n] = sess.Options[n]
		}
		s.tracks[sess.Name] = &track{tally: baseline(sess, now), saved: saved, seen: true}
	}
}

func (s *Service) record(r outcomes.Record, onDone func(err error)) {
	if s.Outcomes != nil {
		s.Outcomes.Enqueue(r, onDone)
	}
}

// observe keeps gone ships while the host has lost tmux. One pass at a time, so
// Dismiss's `ended` is queued before its `dismissed`.
func (s *Service) observe(ctx context.Context) {
	s.pass.Lock()
	defer s.pass.Unlock()
	now := time.Now()
	type write struct {
		session string
		opts    map[string]string
	}
	var writes []write
	var endings []ending
	live := map[string]bool{}
	sessions, lost := s.Host.Snapshot()
	s.mu.Lock()
	for _, sess := range sessions {
		live[sess.Name] = true
		tr, ok := s.tracks[sess.Name]
		if !ok || !sess.IsShip() {
			continue
		}
		tr.seen = true
		at := now
		if sess.PaneDead && !sess.DeadTime.IsZero() {
			at = sess.DeadTime
		}
		next, recs := advance(tr.tally, status(sess, now), reasonOf(sess), at)
		for _, r := range recs {
			r.Ship = sess.Name
			s.record(r, nil)
		}
		tr.tally = next
		if sess.PaneDead && tr.end == nil && sess.Options[sessionhost.OptEnded] == "" {
			tr.end = &endState{}
			endings = append(endings, ending{sess, next, tr.end, tr})
		}
		if o := next.options(); !maps.Equal(o, tr.saved) {
			writes = append(writes, write{sess.Name, o})
		}
	}
	for _, name := range s.Host.Unreadable() {
		live[name] = true
	}
	for name, tr := range s.tracks {
		if !lost && !live[name] && tr.seen {
			delete(s.tracks, name)
		}
	}
	s.mu.Unlock()
	for _, e := range endings { // the report is read outside the lock
		s.queueEnded(e)
	}
	for _, w := range writes {
		if s.Host.SetOptions(ctx, w.session, w.opts) == nil {
			s.mu.Lock()
			if tr, ok := s.tracks[w.session]; ok {
				tr.saved = w.opts
			}
			s.mu.Unlock()
		}
	}
}

// queueEnded marks the ship in tmux once `ended` is written, so no later start
// writes it again. A failed write sets end.failed, which lets Dismiss queue it again.
func (s *Service) queueEnded(e ending) {
	sess, t, end, owner := e.sess, e.tally, e.end, e.owner
	name := sess.Name
	var code, sig any
	if sess.Signal != "" {
		sig = signalName(sess.Signal)
	} else {
		code = sess.ExitStatus
	}
	var duration any
	if l, err := time.Parse(time.RFC3339, sess.Options["launched"]); err == nil && !sess.DeadTime.IsZero() {
		duration = max(seconds(sess.DeadTime.Sub(l).Seconds()), 0)
	}
	s.record(outcomes.Record{Ship: name, Event: "ended", Fields: map[string]any{
		"exit_code": code, "signal": sig, "duration_s": duration,
		"needs_you_count": t.Count, "needs_you_s": seconds(t.Seconds), "report": s.readReport(name),
	}}, func(err error) {
		if err != nil {
			s.mu.Lock()
			end.failed = true
			s.mu.Unlock()
			return
		}
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		merr := s.Host.SetOptions(ctx, name, map[string]string{sessionhost.OptEnded: "1"})
		s.mu.Lock()
		end.written = true
		killed := end.clean
		clean := end.clean
		s.mu.Unlock()
		// A killed ship's session is usually gone by now. Without the marker the
		// next start writes `ended` a second time.
		if merr != nil && !killed && !sessionhost.IsNoSession(merr) {
			fmt.Fprintf(os.Stderr, "admiral: ship %s: its ended marker cannot be set: %v\n", name, merr)
		}
		if clean {
			s.removeShipFiles(name, owner, FromSession(sess, time.Now()).Origin())
		}
	})
}

// retryEnded does nothing while the first write is still pending.
func (s *Service) retryEnded(sess sessionhost.Session) {
	s.mu.Lock()
	tr := s.tracks[sess.Name]
	if tr == nil || tr.end == nil || !tr.end.failed {
		s.mu.Unlock()
		return
	}
	tr.end.failed = false
	e := ending{sess, tr.tally, tr.end, tr}
	s.mu.Unlock()
	s.queueEnded(e)
}

// killShip kills whether or not the log works, but removes the files only once
// `ended` is in the log. Dismiss must run observe first, so that tr.end exists to be set.
func (s *Service) killShip(ctx context.Context, name string, keepInbox bool) error {
	marked := false
	sess, found := s.find(name)
	if found {
		marked = sess.Options[sessionhost.OptEnded] != ""
	}
	kept := keepInbox && found && s.keepInbox(sess)
	s.mu.Lock()
	tr := s.tracks[name] // before the kill, so a pass can't prune it from under us
	s.mu.Unlock()
	if err := s.Host.KillSession(ctx, name); err != nil {
		if kept {
			_ = s.State.DropInbox(name)
		}
		return err
	}
	s.mu.Lock()
	clean := marked
	if tr != nil && tr.end != nil {
		if clean = tr.end.written; !clean {
			tr.end.clean = true
		}
	}
	s.mu.Unlock()
	if clean {
		s.removeShipFiles(name, tr, FromSession(sess, time.Now()).Origin())
	}
	return nil
}

// keepInbox looks at the inbox again once the record is down: an answer that
// landed in between has found no record to clear.
func (s *Service) keepInbox(sess sessionhost.Session) bool {
	home, _, _, ok := s.shipFiles(sess.Name)
	if !ok || !inbox.Waiting(home, sess.Name) {
		return false
	}
	o := sess.Options
	rec := state.KeptShip{Name: o["name"], Fleet: o["fleet"], FleetName: o["fleet-name"], FleetColor: o["fleet-color"], Harness: o["harness"],
		Dir: o["dir"], OriginSource: o["origin-source"], OriginPanel: o["origin-panel"]}
	if err := s.State.KeepInbox(sess.Name, rec); err != nil {
		fmt.Fprintf(os.Stderr, "admiral: ship %s: its inbox cannot be kept: %v\n", sess.Name, err)
		return false
	}
	if !inbox.Waiting(home, sess.Name) {
		_ = s.State.DropInbox(sess.Name)
		return false
	}
	return true
}

func signalName(sig string) string { return strings.TrimPrefix(strings.ToUpper(sig), "SIG") }

func (s *Service) shipFiles(session string) (home, report, inbox string, ok bool) {
	home, err := filepath.Abs(s.Launcher.Home)
	if err != nil {
		return "", "", "", false
	}
	report, inbox, ok = launcher.ShipFiles(home, session)
	return home, report, inbox, ok
}

// removeShipFiles skips while a launch is under way, since it may be reusing the
// name. The checks and deletes share one hold of s.mu, so a later launch waits.
func (s *Service) removeShipFiles(session string, owner *track, from inbox.Origin) {
	home, _, _, ok := s.shipFiles(session)
	if !ok {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if tr := s.tracks[session]; s.launching > 0 || (tr != nil && tr != owner) { // a pruned track is the dying ship's
		return
	}
	if sess, running := s.find(session); running && !sess.PaneDead {
		return
	}
	if _, kept := s.State.KeptInbox(session); kept { // an item waits for its answer
		return
	}
	if s.BeforeRemoveFiles != nil {
		s.BeforeRemoveFiles(session, from)
	}
	launcher.RemoveShipFiles(home, session)
}

func (s *Service) readReport(session string) json.RawMessage {
	_, path, _, ok := s.shipFiles(session)
	if !ok {
		return nil
	}
	// The ship wrote it: never follow a link, never wait on a pipe.
	f, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if err != nil {
		if !os.IsNotExist(err) {
			fmt.Fprintf(os.Stderr, "admiral: ship %s: its report file cannot be read: %v\n", session, err)
		}
		return nil
	}
	defer f.Close()
	if fi, err := f.Stat(); err != nil || !fi.Mode().IsRegular() {
		fmt.Fprintf(os.Stderr, "admiral: ship %s: its report file is not a regular file\n", session)
		return nil
	}
	b, err := io.ReadAll(io.LimitReader(f, maxReport+1))
	switch {
	case err != nil:
		fmt.Fprintf(os.Stderr, "admiral: ship %s: its report file cannot be read: %v\n", session, err)
	case len(b) > maxReport:
		fmt.Fprintf(os.Stderr, "admiral: ship %s: its report is larger than %d bytes, so it is left out of the log\n", session, maxReport)
	case !json.Valid(b):
		fmt.Fprintf(os.Stderr, "admiral: ship %s: its report is not valid JSON, so it is left out of the log\n", session)
	default:
		return json.RawMessage(b)
	}
	return nil
}
