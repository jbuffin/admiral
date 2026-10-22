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
	"regexp"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/jbuffin/admiral/internal/launcher"
	"github.com/jbuffin/admiral/internal/outcomes"
	"github.com/jbuffin/admiral/internal/sessionhost"
)

// tallyOptions are the session options that hold a tally.
var tallyOptions = []string{sessionhost.OptNeedsCount, sessionhost.OptNeedsSecs, sessionhost.OptStopSince}

// maxReport is the most of a ship's report file that Admiral reads. The ship
// writes the file, so a larger one counts as invalid.
const maxReport = 1 << 20

// reasonPattern is what a reason must match to reach the log: any process in a
// pane can set @admiral-reason.
var reasonPattern = regexp.MustCompile(`^[A-Za-z0-9_.:-]{1,64}$`)

// tally is what the log needs to know of a ship between two passes. Count,
// Seconds and Since live in the ship's tmux session options, so a restart of
// Admiral keeps them (see options).
type tally struct {
	Status  string
	Count   int       // needs_you records written
	Seconds float64   // how long the finished stops lasted
	Since   time.Time // when the open stop began; zero when none
}

// options is the part of the tally kept in tmux; an empty value is unset.
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

// baseline is the tally of a ship that was already there when Admiral looked:
// what tmux holds, with the current status as the starting point, so an open
// stop gets no second needs_you. A stop that ended while Admiral was stopped
// still has its Since, and the first advance closes it.
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

// reasonOf is the reason to log for a ship that needs you: its pane's reason if
// it is well formed, unknown if it is missing or not, idle for a ship no hook
// has spoken for.
func reasonOf(sess sessionhost.Session) string {
	switch {
	case sess.State == "":
		return "idle"
	case reasonPattern.MatchString(sess.Reason):
		return sess.Reason
	}
	return "unknown"
}

// advance is the records, without a ship, that a move to status st at time at
// writes, and the tally after it. A stop is one stop whatever its reason does;
// a stop that the pane's death ends gets no underway.
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

// seconds rounds to a tenth.
func seconds(s float64) float64 { return math.Round(s*10) / 10 }

// track is what Run remembers of a ship it has written `launched` for, or found
// there at start.
type track struct {
	tally
	saved map[string]string // the options last written to tmux
	end   *endState         // set once `ended` is queued
	seen  bool              // the host has listed the session; a launch is tracked before it does
}

// endState says whether `ended` reached the log, whether its write failed (so
// Dismiss queues it again), and whether the ship's files wait for it to.
type endState struct{ written, failed, clean bool }

// ending is a ship whose pane died and whose `ended` is to be queued.
type ending struct {
	sess  sessionhost.Session
	tally tally
	end   *endState
	owner *track
}

// baselineAll starts tracking every ship that is not tracked yet, as found.
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

// record queues r; a service without a log drops it. onDone is Enqueue's.
func (s *Service) record(r outcomes.Record, onDone func(err error)) {
	if s.Outcomes != nil {
		s.Outcomes.Enqueue(r, onDone)
	}
}

// observe compares each tracked ship with the last pass and writes what changed:
// needs_you and underway from the status, ended for a pane that died. It forgets
// ships that are gone, unless the host has lost tmux and knows of none. One pass
// at a time, so Dismiss's `ended` is queued before its `dismissed`.
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

// queueEnded queues the `ended` record of a ship whose pane died. Once it is
// written, the ship is marked in tmux so no later start writes it again, and
// the files of a ship that was killed meanwhile go. A write that fails sets
// end.failed, which is what lets Dismiss queue the record again.
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
		// next start writes `ended` a second time (ADR 0012).
		if merr != nil && !killed && !sessionhost.IsNoSession(merr) {
			fmt.Fprintf(os.Stderr, "admiral: ship %s: its ended marker cannot be set: %v\n", name, merr)
		}
		if clean {
			s.removeShipFiles(name, owner)
		}
	})
}

// retryEnded queues `ended` again for a ship whose first write failed, and
// does nothing while that write is still pending.
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

// killShip ends a ship's session, as Admiral does whether or not the log works,
// and removes its report and inbox once its `ended` is in the log: now if it is,
// or when the writer gets there. A ship killed while its `ended` fails keeps its
// files. A ship Admiral has no `ended` for is not cleaned unless tmux held the
// ended marker. Dismiss must run observe first, so that tr.end exists to be set.
func (s *Service) killShip(ctx context.Context, name string) error {
	marked := false
	if sess, ok := s.find(name); ok {
		marked = sess.Options[sessionhost.OptEnded] != ""
	}
	s.mu.Lock()
	tr := s.tracks[name] // before the kill, so a pass can't prune it from under us
	s.mu.Unlock()
	if err := s.Host.KillSession(ctx, name); err != nil {
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
		s.removeShipFiles(name, tr)
	}
	return nil
}

// signalName is a signal as the UI and the log show it: upper case, no SIG.
func signalName(sig string) string { return strings.TrimPrefix(strings.ToUpper(sig), "SIG") }

// shipFiles is $ADMIRAL_HOME, made absolute, and the ship's report path and
// inbox directory; ok is false for a session name that has none.
func (s *Service) shipFiles(session string) (home, report, inbox string, ok bool) {
	home, err := filepath.Abs(s.Launcher.Home)
	if err != nil {
		return "", "", "", false
	}
	report, inbox, ok = launcher.ShipFiles(home, session)
	return home, report, inbox, ok
}

// removeShipFiles deletes the ship's report and inbox, unless a launch is under
// way (it may be reusing the name before Admiral can tell), a newer ship's track
// holds the name (owner is the dying ship's, nil if it had none), a ship with
// that name is running again, or reports/ or inbox/ is not a real directory. The
// deletes go through a root on $ADMIRAL_HOME, so none leaves the home; a link
// swapped in inside the home can still send one elsewhere within it. The checks
// and the deletes share one hold of s.mu, so a launch that counts itself after
// the checks waits for the deletes; one that counted before them keeps the files.
func (s *Service) removeShipFiles(session string, owner *track) {
	home, report, inbox, ok := s.shipFiles(session)
	if !ok {
		return
	}
	root, err := os.OpenRoot(home)
	if err != nil {
		return
	}
	defer root.Close()
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.launching > 0 || s.tracks[session] != owner {
		return
	}
	if sess, running := s.find(session); running && !sess.PaneDead {
		return
	}
	if rel, err := filepath.Rel(home, report); err == nil && realDir(root, filepath.Dir(rel)) {
		_ = root.Remove(rel)
	}
	if rel, err := filepath.Rel(home, inbox); err == nil && realDir(root, filepath.Dir(rel)) {
		_ = root.RemoveAll(rel)
	}
}

// realDir is whether name, relative to root, is a directory itself and not a
// link to one: a link would send a delete under it somewhere else.
func realDir(root *os.Root, name string) bool {
	fi, err := root.Lstat(name)
	return err == nil && fi.IsDir()
}

// readReport is the ship's report as the JSON it holds: nil when the file is
// missing, or too large or not JSON, which is logged.
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
