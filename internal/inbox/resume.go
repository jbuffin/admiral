package inbox

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"slices"
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
// until an add succeeds, the source refuses the text for good, or an add is
// uncertain (the plugin may have added it): never twice.

// maxReport is the most of a report that is read; a ship wrote it.
const maxReport = 1 << 20

// resumeRetry is how long a resume order that could not be added waits before
// the next try, when nothing else prompts one.
const resumeRetry = 15 * time.Second

// addTimeout bounds one add.
const addTimeout = 10 * time.Second

// AddKind is what a failed add says about trying again.
type AddKind int

const (
	// AddFinal: the source refused the order for good; it is dropped.
	AddFinal AddKind = iota
	// AddRetryable: nothing was changed and the add may work later.
	AddRetryable
	// AddUncertain: the order was sent and may have landed; it is never sent again.
	AddUncertain
)

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

// Origin is where a ship ran: its checkout, and the autopilot panel it counted
// against, if autopilot launched it (#120). A resume order of its report
// launches in that checkout, under that panel's entry.
type Origin struct {
	Dir, Source, Panel string
}

// CaptureResume copies the resume text of session's report into the state when
// every item the report lists is answered, and says nothing otherwise. It does
// no network I/O. Whatever deletes a report calls it first.
func (s *Service) CaptureResume(session string) {
	var from Origin
	if s.Origin != nil {
		from, _ = s.Origin(session)
	}
	s.CaptureResumeFrom(session, from)
}

// CaptureResumeFrom is CaptureResume for a caller that has the ship's origin
// from a session that may be gone by now; with no directory it asks s.Origin.
func (s *Service) CaptureResumeFrom(session string, from Origin) {
	if s.Kept == nil || !validSession(session) {
		return
	}
	if from.Dir == "" && s.Origin != nil {
		from, _ = s.Origin(session)
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
	// The key is the order itself, not the report: a ship that rewrites its report
	// without changing them adds nothing new.
	ids := slices.Clone(r.Inbox)
	slices.Sort(ids)
	sum := sha256.Sum256([]byte(r.Resume + "\x00" + strings.Join(ids, "\x00")))
	fresh, err := s.Kept.QueueResume(session+":"+hex.EncodeToString(sum[:8]), session, r.Resume, from.Dir, from.Source, from.Panel)
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
		if errors.Is(err, fs.ErrNotExist) {
			s.Kept.KeepResumes(nil)
		}
		return // a reports/ that cannot be opened proves nothing about the done orders
	}
	defer reports.Close()
	names, err := listDir(reports)
	if err != nil {
		return
	}
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

// addResumes tries the resume orders that wait, one at a time (only session's
// when it is not empty), and says whether any is left to try again. It makes
// plugin calls: never under answerMu. A failure that changed nothing and may pass
// is tried again later; one that is final is dropped; one that may have added the
// order is counted as done and recorded as uncertain, because a second order can
// launch a second ship.
func (s *Service) addResumes(session string) (left bool) {
	if s.Kept == nil || s.AddOrder == nil || s.ResumeTarget == nil {
		return false
	}
	s.resumeMu.Lock()
	defer s.resumeMu.Unlock()
	pending := s.Kept.PendingResumes()
	keys := make([]string, 0, len(pending))
	for k, r := range pending {
		if session == "" || r.Session == session {
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)
	for _, key := range keys {
		t := s.ResumeTarget()
		if t == nil {
			continue // nothing to try until the config names a target
		}
		r := pending[key]
		recorded := func(string) {}
		if s.ResumeAdd != nil {
			recorded = s.ResumeAdd(t.Source, t.Panel, Origin{Dir: r.Dir, Source: r.OriginSource, Panel: r.OriginPanel})
		}
		ctx, cancel := context.WithTimeout(context.Background(), addTimeout)
		id, err := s.AddOrder(ctx, t.Source, t.Panel, r.Text)
		cancel()
		who := r.Session
		var fail interface{ AddKind() AddKind }
		uncertain := false
		if err != nil {
			kind := AddFinal
			if errors.As(err, &fail) {
				kind = fail.AddKind()
			}
			switch kind {
			case AddRetryable:
				recorded("")
				left = true
				if s.lastErr[key] != err.Error() {
					fmt.Fprintf(os.Stderr, "admiral: ship %s: its resume order is not added yet: %v\n", who, err)
					if s.lastErr == nil {
						s.lastErr = map[string]string{}
					}
					s.lastErr[key] = err.Error()
				}
				continue
			case AddUncertain:
				uncertain = true
				fmt.Fprintf(os.Stderr, "admiral: ship %s: its resume order may not have landed, and is not sent again: %v\n", who, err)
				fmt.Fprintf(os.Stderr, "admiral: ship %s: if it did, it is an ordinary order: the add gave no id, so its checkout is not recorded\n", who)
			default:
				fmt.Fprintf(os.Stderr, "admiral: ship %s: its resume order is dropped: %v\n", who, err)
			}
		}
		delete(s.lastErr, key)
		recorded(id) // the order is in the state's hands now: "" when the add gave no id
		if ferr := s.Kept.FinishResume(key); ferr != nil {
			fmt.Fprintf(os.Stderr, "admiral: ship %s: its resume order cannot be marked done: %v\n", who, ferr)
		}
		if (err == nil || uncertain) && s.Outcomes != nil {
			fields := map[string]any{"source": t.Source, "panel": t.Panel}
			if uncertain {
				fields["uncertain"] = true
			} else {
				fields["order"] = id
			}
			s.Outcomes.Enqueue(outcomes.Record{Ship: who, Event: "resumed", Fields: fields}, nil)
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
		if s.addResumes("") {
			retry.Reset(resumeRetry)
		}
	}
}
