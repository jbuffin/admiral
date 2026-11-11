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
	"github.com/jbuffin/admiral/internal/state"
)

// A resume order's text is copied into Admiral's state first, so it outlives the
// report's deletion and a restart, and is never added twice.

// maxReport caps the read because a ship wrote the report.
const maxReport = 1 << 20

const resumeRetry = 15 * time.Second

const addTimeout = 10 * time.Second

type AddKind int

const (
	AddFinal AddKind = iota
	AddRetryable
	// AddUncertain: the order may have landed; it is never sent again.
	AddUncertain
)

func (s *Service) readReport(session string) []byte { return readReportFile(s.Home, session) }

func readReportFile(home, session string) []byte {
	root, err := os.OpenRoot(home)
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

// Origin is where a resume order of the ship's report launches, under that
// panel's autopilot entry.
type Origin struct {
	Dir, Source, Panel string
}

// CaptureResume does no network I/O. Whatever deletes a report calls it first.
func (s *Service) CaptureResume(session string) {
	var from Origin
	if s.Origin != nil {
		from, _ = s.Origin(session)
	}
	s.CaptureResumeFrom(session, from)
}

// CaptureResumeFrom is for a caller whose session may be gone by now; with no
// directory it asks s.Origin.
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
		if _, err := dir.Lstat(id + ".json"); err != nil || !Answered(dir, id) || removed(dir, id) {
			return // a removed item never resumes its ship
		}
	}
	// A ship still afloat has the text typed at its prompt when it stops, and
	// needs no resume_source; one that has ended gets an order.
	afloat := s.Afloat != nil && s.Afloat(session)
	if !afloat && (s.ResumeTarget == nil || s.ResumeTarget() == nil) {
		s.noTarget.Do(func() {
			fmt.Fprintln(os.Stderr, "admiral: autopilot.resume_source is not set in the config, so resume orders are not added")
		})
		return
	}
	// The key is the order itself, not the report: a ship that rewrites its report
	// without changing the order adds nothing new.
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

// KickResume never blocks.
func (s *Service) KickResume() { s.kickResume() }

// addResumes makes plugin calls: never under answerMu. An uncertain add counts as
// done, because a second order can launch a second ship.
func (s *Service) addResumes(session string) (left bool) {
	if s.Kept == nil {
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
		r := pending[key]
		switch s.typeResume(key, r) {
		case liveTyped:
			continue
		case liveWait:
			left = true
			continue
		}
		if s.AddOrder == nil || s.ResumeTarget == nil {
			continue
		}
		t := s.ResumeTarget()
		if t == nil {
			continue // nothing to try until the config names a target
		}
		release := func(string) {}
		if s.ResumeAdd != nil {
			release = s.ResumeAdd(t.Source, t.Panel, Origin{Dir: r.Dir, Source: r.OriginSource, Panel: r.OriginPanel})
		}
		// Released once, whichever way the add ends, a panic included.
		released := false
		recorded := func(id string) {
			if !released {
				released = true
				release(id)
			}
		}
		defer recorded("")
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

const (
	liveGone  = iota // no ship afloat: the order goes to the resume panel
	liveWait         // the ship is afloat but not at its prompt: try again when it stops
	liveTyped        // the text went in at the ship's prompt: done
)

// typeResume types at the afloat ship's prompt, so no second ship is launched in
// its checkout. Under resumeMu.
func (s *Service) typeResume(key string, r state.Resume) int {
	if s.TypeResume == nil || !typable(r.Text) {
		return liveGone
	}
	afloat, typed, err := s.TypeResume(r.Session, r.Text)
	if err != nil && s.lastErr[key] != err.Error() {
		fmt.Fprintf(os.Stderr, "admiral: ship %s: its resume text could not be typed at its prompt: %v\n", r.Session, err)
		if s.lastErr == nil {
			s.lastErr = map[string]string{}
		}
		s.lastErr[key] = err.Error()
	}
	switch {
	case typed:
		delete(s.lastErr, key)
		if ferr := s.Kept.FinishResume(key); ferr != nil {
			fmt.Fprintf(os.Stderr, "admiral: ship %s: its resume cannot be marked done: %v\n", r.Session, ferr)
		}
		if s.Outcomes != nil {
			s.Outcomes.Enqueue(outcomes.Record{Ship: r.Session, Event: "resumed", Fields: map[string]any{"live": true}}, nil)
		}
		return liveTyped
	case afloat:
		return liveWait
	}
	return liveGone
}

func typable(text string) bool {
	return strings.TrimSpace(text) != "" && !strings.ContainsFunc(text, func(r rune) bool {
		return (r < 0x20 && r != '\n' && r != '\t') || r == 0x7f
	})
}

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
