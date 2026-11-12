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

// Resume text (ADR 0016, ADR 0034): once every item a ship's report lists in `inbox`
// is answered, the report's `resume` text is typed at the prompt of the ship while it
// is afloat. The text is copied into Admiral's state first, so it outlives the report's
// deletion and a restart, and is never typed twice. A ship that is gone, or text that
// can't be typed, finishes the entry with nothing typed.

// maxReport caps the read because a ship wrote the report.
const maxReport = 1 << 20

const resumeRetry = 15 * time.Second

// readReport is the report of session as the ship wrote it, nil when it is
// missing or unusable.
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

// CaptureResume does no network I/O. Whatever deletes a report calls it first.
func (s *Service) CaptureResume(session string) {
	if s.Kept == nil || !validSession(session) {
		return
	}
	if s.Afloat == nil || !s.Afloat(session) {
		return // a ship that is gone has nothing to read its answers
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
	// The key is the text itself, not the report: a ship that rewrites its report
	// without changing them adds nothing new.
	ids := slices.Clone(r.Inbox)
	slices.Sort(ids)
	sum := sha256.Sum256([]byte(r.Resume + "\x00" + strings.Join(ids, "\x00")))
	fresh, err := s.Kept.QueueResume(session+":"+hex.EncodeToString(sum[:8]), session, r.Resume)
	switch {
	case err != nil:
		fmt.Fprintf(os.Stderr, "admiral: ship %s: its resume text cannot be kept: %v\n", session, err)
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

// addResumes makes no plugin calls but never runs under answerMu. A text whose ship is
// gone, or that can't be typed, is finished with one stderr line and kept, so it is
// never tried twice.
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
		fmt.Fprintf(os.Stderr, "admiral: ship %s: its resume text is not typed: the ship is not afloat or the text cannot be typed at a prompt\n", r.Session)
		delete(s.lastErr, key)
		if ferr := s.Kept.FinishResume(key); ferr != nil {
			fmt.Fprintf(os.Stderr, "admiral: ship %s: its resume text cannot be marked done: %v\n", r.Session, ferr)
		}
	}
	return left
}

const (
	liveGone  = iota // no ship afloat, or text that can't be typed: finished
	liveWait         // the ship is afloat but not at its prompt: try again when it stops
	liveTyped        // the text went in at the ship's prompt: done
)

// typeResume types at the afloat ship's prompt, so the ship that asked reads its
// answers. Under resumeMu.
func (s *Service) typeResume(key string, r state.Resume) int {
	if s.TypeResume == nil || !Typable(r.Text) {
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

// Typable is whether text can be typed at a prompt: not blank, no control character but a newline or tab.
func Typable(text string) bool {
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
