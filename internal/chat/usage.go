package chat

import (
	"sync"
	"time"

	"github.com/jbuffin/admiral/internal/outcomes"
	"github.com/jbuffin/admiral/internal/transcriptfs"
	"github.com/jbuffin/admiral/internal/usage"
)

// resetUsage turns the chat transcript's cumulative reading into what was used since the last reset.
// Its zero value is ready to use.
type resetUsage struct {
	mu   sync.Mutex
	path string
	prev *usage.Figures
}

// figures answers the use since the last reset of the same file, or the whole file for a new path or when
// a figure went down (the file started over). With no reading it falls back to last, and keeps the stored
// reading, so the next delta still counts from it.
func (r *resetUsage) figures(path string, cur *usage.Figures, last func() (usage.Figures, bool)) *usage.Figures {
	r.mu.Lock()
	defer r.mu.Unlock()
	if cur == nil {
		if last != nil {
			if f, ok := last(); ok {
				return &f
			}
		}
		return nil
	}
	got := cur
	if r.prev != nil && r.path == path {
		if d, ok := cur.Sub(*r.prev); ok {
			got = &d
		}
	}
	keep := *cur
	r.path, r.prev = path, &keep
	return got
}

// clearWait bounds how long a /clear is given to land its SessionStart before the old transcript is read.
const clearWait = 3 * time.Second

// recordReset writes a `usage` record for the chat session that is about to end or clear, read off the
// caller's goroutine. path is the transcript recorded before the clear or kill, since a clear rewrites it.
// A /clear passes the transcript option as it stood before, and Claude may write its cost-state while it
// handles the command: the file is read once the clear's SessionStart changes the option, or after clearWait.
func (s *fleetChat) recordReset(path, configDir, fleet, clearedFrom string) {
	if s.Outcomes == nil {
		return
	}
	go func() {
		if clearedFrom != "" {
			for end := time.Now().Add(clearWait); time.Now().Before(end); time.Sleep(50 * time.Millisecond) {
				s.mu.Lock()
				landed := s.st.optRaw != clearedFrom
				s.mu.Unlock()
				if landed {
					break
				}
			}
		}
		var cur *usage.Figures
		if path != "" {
			if f, _, err := transcriptfs.Open(configDir, path); err == nil {
				if fig, err := usage.ParseClaude(f); err == nil {
					cur = &fig
				}
				f.Close()
			}
		}
		var last func() (usage.Figures, bool)
		if s.LastLive != nil {
			last = func() (usage.Figures, bool) { return s.LastLive(s.name()) }
		}
		got := s.resets.figures(path, cur, last)
		if s.ForgetLive != nil {
			s.ForgetLive(s.name())
		}
		s.Outcomes.Enqueue(outcomes.Record{Ship: s.name(), Event: "usage",
			Fields: usage.ExitFields(fleet, "claude", 0, got, nil)}, nil)
	}()
}
