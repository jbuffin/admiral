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
	o := map[string]string{sessionhost.OptNeedsCount: "", sessionhost.OptNeedsSecs: "", sessionhost.OptStopSince: ""}
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

// endState says whether `ended` reached the log, and whether the ship's files
// wait for it to.
type endState struct{ written, clean bool }

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

// record queues r; a service without a log drops it.
func (s *Service) record(r outcomes.Record, onWritten func()) {
	if s.Outcomes != nil {
		s.Outcomes.Enqueue(r, onWritten)
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
	type ending struct {
		sess  sessionhost.Session
		tally tally
		end   *endState
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
			endings = append(endings, ending{sess, next, tr.end})
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
		s.queueEnded(e.sess, e.tally, e.end)
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
// the files of a ship that was killed meanwhile go.
func (s *Service) queueEnded(sess sessionhost.Session, t tally, end *endState) {
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
	}}, func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = s.Host.SetOptions(ctx, name, map[string]string{sessionhost.OptEnded: "1"}) // the session may be gone
		s.mu.Lock()
		end.written = true
		clean := end.clean
		s.mu.Unlock()
		if clean {
			s.removeShipFiles(name)
		}
	})
}

// killShip ends a ship's session, as Admiral does whether or not the log works,
// and removes its report and inbox once its `ended` is in the log: now if it is,
// or when the writer gets there. A ship killed while its `ended` fails keeps its
// files. A ship Admiral has no `ended` for is not cleaned unless tmux held the
// ended marker.
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
		s.removeShipFiles(name)
	}
	return nil
}

// signalName is a signal as the UI and the log show it: upper case, no SIG.
func signalName(sig string) string { return strings.TrimPrefix(strings.ToUpper(sig), "SIG") }

// removeShipFiles deletes the ship's report and inbox, unless a ship with that
// name is running again or reports/ or inbox/ is not a real directory (a link
// there would send the delete elsewhere).
func (s *Service) removeShipFiles(session string) {
	home, err := filepath.Abs(s.Launcher.Home)
	if err != nil {
		return
	}
	report, inbox, ok := launcher.ShipFiles(home, session)
	if !ok {
		return
	}
	if sess, running := s.find(session); running && !sess.PaneDead {
		return
	}
	if realDir(filepath.Dir(report)) {
		_ = os.Remove(report)
	}
	if realDir(filepath.Dir(inbox)) {
		_ = os.RemoveAll(inbox)
	}
}

func realDir(path string) bool {
	fi, err := os.Lstat(path)
	return err == nil && fi.IsDir()
}

// readReport is the ship's report as the JSON it holds: nil when the file is
// missing, or too large or not JSON, which is logged.
func (s *Service) readReport(session string) json.RawMessage {
	home, err := filepath.Abs(s.Launcher.Home)
	if err != nil {
		return nil
	}
	path, _, ok := launcher.ShipFiles(home, session)
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
