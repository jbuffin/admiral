// Package uselive feeds the live usage store from each harness's live source.
package uselive

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/jbuffin/admiral/internal/transcriptfs"
	"github.com/jbuffin/admiral/internal/usage"
)

// Pane is one Claude pane's kept status line.
type Pane struct {
	Ship   string
	Option string
}

// Claude reads the status lines `admiral statusline` keeps in the panes.
type Claude struct {
	Store *usage.Store
	// Login is the harness login (identity dir) of a ship; "" when unknown.
	Login func(ship string) string
	Panes func(ctx context.Context) ([]Pane, error)
	Now   func() time.Time

	// Several panes share a login and a dead pane keeps its last line, so only a newer reading replaces the quota.
	quotaSeen map[string]time.Time
}

// Poll reads every pane once. A failed read keeps what the store has.
func (c *Claude) Poll(ctx context.Context) {
	panes, err := c.Panes(ctx)
	if err != nil {
		return
	}
	now := time.Now
	if c.Now != nil {
		now = c.Now
	}
	if c.quotaSeen == nil {
		c.quotaSeen = map[string]time.Time{}
	}
	for _, p := range panes {
		r, ok := usage.ParseStatusline([]byte(p.Option))
		if !ok {
			continue
		}
		seen := r.Seen
		if seen.IsZero() {
			seen = now()
		}
		if r.Figures != nil {
			c.Store.SetFigures(p.Ship, r.SessionID, *r.Figures, seen)
		}
		if r.Quota == nil || c.Login == nil {
			continue
		}
		if login := c.Login(p.Ship); login != "" && seen.After(c.quotaSeen[login]) {
			c.quotaSeen[login] = seen
			c.Store.SetQuota(login, *r.Quota, seen)
		}
	}
}

// Codex takes the app-server frames Admiral's observers read.
type Codex struct {
	Store *usage.Store
	Login func(ship string) string
	Now   func() time.Time

	mu      sync.Mutex
	threads map[string]map[string]usage.Tokens // by ship, then thread
}

// A thread's totals are cumulative, so the ship's figures are the sum over its threads (a /new starts a thread over).
const (
	maxCodexShips   = 256
	maxCodexThreads = 64
)

// Frame is safe to call from every observer at once.
func (c *Codex) Frame(ship string, frame []byte) {
	now := time.Now
	if c.Now != nil {
		now = c.Now
	}
	if thread, f, ok := usage.ParseCodexTokenUsage(frame); ok {
		c.mu.Lock()
		if c.threads == nil {
			c.threads = map[string]map[string]usage.Tokens{}
		}
		m := c.threads[ship]
		if m == nil {
			if len(c.threads) >= maxCodexShips {
				for k := range c.threads { // a ship that ended long ago, or any: the store keeps its last figures
					delete(c.threads, k)
					break
				}
			}
			m = map[string]usage.Tokens{}
			c.threads[ship] = m
		}
		if _, known := m[thread]; known || len(m) < maxCodexThreads {
			m[thread] = f.Tokens
		}
		var sum usage.Tokens
		for _, t := range m {
			sum = sum.Add(t)
		}
		c.mu.Unlock()
		c.Store.SetFigures(ship, "codex", usage.Figures{Tokens: sum}, now())
		return
	}
	if q, ok := usage.ParseCodexRateLimits(frame); ok && c.Login != nil {
		if login := c.Login(ship); login != "" {
			c.Store.SetQuota(login, q, now())
		}
	}
}

// File is a Copilot ship's events file under its identity dir.
type File struct {
	Ship, Dir, Path string
}

// Copilot re-sums a session's events file when it changes: usage_record lines are per call.
type Copilot struct {
	Store *usage.Store
	Files func() []File
	Now   func() time.Time

	seen map[string]fileStamp
}

type fileStamp struct {
	size int64
	mod  time.Time
}

// A file this large is skipped: each change re-reads it whole.
const maxCopilotFile = 128 << 20

func (c *Copilot) Poll(ctx context.Context) {
	files := c.Files()
	next := make(map[string]fileStamp, len(files))
	for _, f := range files {
		if ctx.Err() != nil {
			return
		}
		fh, info, err := transcriptfs.OpenFor("copilot", f.Dir, f.Path)
		if err != nil {
			continue
		}
		stamp := fileStamp{info.Size(), info.ModTime()}
		next[f.Path] = stamp
		if stamp == c.seen[f.Path] || stamp.size > maxCopilotFile {
			fh.Close()
			continue
		}
		fig, err := usage.ParseCopilot(fh)
		fh.Close()
		if err != nil {
			if !errors.Is(err, usage.ErrNoFigures) {
				delete(next, f.Path) // read again next tick
			}
			continue
		}
		c.Store.SetFigures(f.Ship, f.Path, fig, stamp.mod)
	}
	c.seen = next
}
