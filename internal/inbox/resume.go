package inbox

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
	"syscall"
	"time"

	"github.com/jbuffin/admiral/internal/outcomes"
)

// Resume orders (ADR 0016): once every item a ship's report lists in `inbox` has
// an answer, the report's `resume` text becomes an order in the panel that
// autopilot.resume_source names. The text is copied into Admiral's own state
// first, so it outlives the report's deletion and a restart, and it stays there
// until an add succeeds or the source refuses the text for good.

// maxReport is the most of a report that is read; a ship wrote it.
const maxReport = 1 << 20

// resumeRetry is how long a resume order that could not be added waits before
// the next try, when nothing else prompts one.
const resumeRetry = 15 * time.Second

// addTimeout bounds one add.
const addTimeout = 10 * time.Second

// readReport is the report of session as the ship wrote it, nil when it is
// missing or unusable.
func (s *Service) readReport(session string) []byte {
	root, err := os.OpenRoot(s.Home)
	if err != nil {
		return nil
	}
	defer root.Close()
	reports, err := realDir(root, "reports")
	if err != nil {
		return nil
	}
	defer reports.Close()
	f, err := reports.OpenFile(session+".json", os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if err != nil {
		return nil
	}
	defer f.Close()
	if fi, err := f.Stat(); err != nil || !fi.Mode().IsRegular() {
		return nil
	}
	b, err := io.ReadAll(io.LimitReader(f, maxReport+1))
	if err != nil || len(b) > maxReport {
		return nil
	}
	return b
}

// CaptureResume copies the resume text of session's report into the state when
// every item the report lists is answered, and says nothing otherwise. It does
// no network I/O. Whatever deletes a report calls it first.
func (s *Service) CaptureResume(session string) {
	if s.Kept == nil || !validSession(session) {
		return
	}
	b := s.readReport(session)
	if b == nil {
		return
	}
	var r struct {
		Resume string   `json:"resume"`
		Inbox  []string `json:"inbox"`
	}
	if json.Unmarshal(b, &r) != nil || strings.TrimSpace(r.Resume) == "" || len(r.Inbox) == 0 {
		return
	}
	dir, err := openShipDir(s.Home, session)
	if err != nil {
		return
	}
	defer dir.Close()
	for _, id := range r.Inbox {
		if !idPattern.MatchString(id) {
			return
		}
		if _, err := dir.Lstat(id + ".json"); err != nil || !Answered(dir, id) {
			return
		}
	}
	if s.ResumeTarget == nil || s.ResumeTarget() == nil {
		s.noTarget.Do(func() {
			fmt.Fprintln(os.Stderr, "admiral: autopilot.resume_source is not set in the config, so resume orders are not added")
		})
		return
	}
	sum := sha256.Sum256(b)
	fresh, err := s.Kept.QueueResume(session+":"+hex.EncodeToString(sum[:8]), session, r.Resume)
	switch {
	case err != nil:
		fmt.Fprintf(os.Stderr, "admiral: ship %s: its resume order cannot be kept: %v\n", session, err)
	case fresh:
		s.kickResume()
	}
}

// captureAll runs CaptureResume for every report, and forgets the finished
// resume orders of sessions whose report is gone.
func (s *Service) captureAll() {
	if s.Kept == nil {
		return
	}
	root, err := os.OpenRoot(s.Home)
	if err != nil {
		return
	}
	defer root.Close()
	reports, err := realDir(root, "reports")
	if err != nil {
		s.Kept.KeepResumes(nil)
		return
	}
	defer reports.Close()
	names, _ := listDir(reports)
	have := map[string]bool{}
	for _, n := range names {
		if session, ok := strings.CutSuffix(n, ".json"); ok && validSession(session) {
			have[session] = true
			s.CaptureResume(session)
		}
	}
	s.Kept.KeepResumes(have)
}

func (s *Service) kickResume() {
	s.mu.Lock()
	if s.resume == nil {
		s.resume = make(chan struct{}, 1)
	}
	c := s.resume
	s.mu.Unlock()
	select {
	case c <- struct{}{}:
	default:
	}
}

// KickResume asks for the resume orders that wait to be tried again, for a
// change in a source or the config. It never blocks.
func (s *Service) KickResume() { s.kickResume() }

// addResumes tries every resume order that waits, one at a time, and says
// whether any is left to try again. It makes plugin calls: never under answerMu.
func (s *Service) addResumes() (left bool) {
	if s.Kept == nil || s.AddOrder == nil || s.ResumeTarget == nil {
		return false
	}
	s.resumeMu.Lock()
	defer s.resumeMu.Unlock()
	pending := s.Kept.PendingResumes()
	keys := make([]string, 0, len(pending))
	for k := range pending {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, key := range keys {
		t := s.ResumeTarget()
		if t == nil {
			continue // nothing to try until the config names a target
		}
		ctx, cancel := context.WithTimeout(context.Background(), addTimeout)
		id, retry, err := s.AddOrder(ctx, t.Source, t.Panel, pending[key].Text)
		cancel()
		session := pending[key].Session
		if err != nil {
			if retry {
				left = true
				if s.lastErr[key] != err.Error() {
					fmt.Fprintf(os.Stderr, "admiral: ship %s: its resume order is not added yet: %v\n", session, err)
					if s.lastErr == nil {
						s.lastErr = map[string]string{}
					}
					s.lastErr[key] = err.Error()
				}
				continue
			}
			fmt.Fprintf(os.Stderr, "admiral: ship %s: its resume order is dropped: %v\n", session, err)
		}
		delete(s.lastErr, key)
		if ferr := s.Kept.FinishResume(key); ferr != nil {
			fmt.Fprintf(os.Stderr, "admiral: ship %s: its resume order cannot be marked done: %v\n", session, ferr)
		}
		if err == nil && s.Outcomes != nil {
			s.Outcomes.Enqueue(outcomes.Record{Ship: session, Event: "resumed",
				Fields: map[string]any{"source": t.Source, "panel": t.Panel, "order": id}}, nil)
		}
	}
	return left
}

// resumeLoop adds the resume orders that wait, when prompted and while one is
// left to try again.
func (s *Service) resumeLoop(ctx context.Context) {
	s.kickResume()
	s.mu.Lock()
	kick := s.resume
	s.mu.Unlock()
	retry := time.NewTimer(time.Hour)
	retry.Stop()
	for {
		select {
		case <-kick:
		case <-retry.C:
		case <-ctx.Done():
			return
		}
		if s.addResumes() {
			retry.Reset(resumeRetry)
		}
	}
}
