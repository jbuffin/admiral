// Package uselive feeds the live usage store from each harness's live source.
package uselive

import (
	"bytes"
	"context"
	"io"
	"os"
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
		// A line's own time orders readings, but a future time would pin the login's quota; a line with no time is read now.
		seen := r.Seen
		if n := now(); seen.IsZero() || seen.After(n) {
			seen = n
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

// Forget drops a ship's thread totals once its record has taken them: the next round's threads start a new sum.
func (c *Codex) Forget(ship string) {
	c.mu.Lock()
	delete(c.threads, ship)
	c.mu.Unlock()
}

// File is a Copilot ship's events file under its identity dir.
type File struct {
	Ship, Dir, Path string
}

// Copilot sums each events file as it grows: usage_record lines are per call, and the file is append-only.
type Copilot struct {
	Store *usage.Store
	Files func() []File
	Now   func() time.Time

	seen map[string]*copilotRead
}

// copilotRead is what the feed keeps of one events file: where it has read to and what that adds up to.
type copilotRead struct {
	info     os.FileInfo
	size     int64
	mod      time.Time
	offset   int64 // end of the last whole line read
	skipping bool  // inside a line too long to keep
	acc      usage.CopilotAcc
}

// A file this large is skipped on its first read; later ticks read only what was appended.
const maxCopilotFile = 128 << 20

// maxCopilotLine bounds a partial line held between ticks.
const maxCopilotLine = 4 << 20

func (c *Copilot) Poll(ctx context.Context) {
	files := c.Files()
	next := make(map[string]*copilotRead, len(files))
	for _, f := range files {
		if ctx.Err() != nil {
			return
		}
		fh, info, err := transcriptfs.OpenFor("copilot", f.Dir, f.Path)
		if err != nil {
			continue
		}
		st := c.seen[f.Path]
		// A shorter file, or another file under the same path, was rewritten: its sums start over.
		if st == nil || info.Size() < st.offset || !os.SameFile(st.info, info) {
			st = &copilotRead{info: info}
		}
		next[f.Path] = st
		if info.Size() == st.size && info.ModTime().Equal(st.mod) || info.Size() > maxCopilotFile {
			fh.Close()
			continue
		}
		err = st.readFrom(fh, info.Size())
		fh.Close()
		if err != nil {
			delete(next, f.Path) // read again next tick
			continue
		}
		st.size, st.mod = info.Size(), info.ModTime()
		fig, err := st.acc.Figures()
		if err != nil {
			continue
		}
		c.Store.SetFigures(f.Ship, f.Path, fig, st.mod)
	}
	c.seen = next
}

// readFrom feeds the lines after offset, up to size, to the sums. A last line without its newline waits for it.
func (st *copilotRead) readFrom(fh *os.File, size int64) error {
	if _, err := fh.Seek(st.offset, io.SeekStart); err != nil {
		return err
	}
	r := io.LimitReader(fh, size-st.offset)
	buf := make([]byte, 64<<10)
	var carry []byte
	for {
		n, err := r.Read(buf)
		data := append(carry, buf[:n]...)
		for {
			i := bytes.IndexByte(data, '\n')
			if i < 0 {
				break
			}
			if !st.skipping {
				if l := bytes.TrimSpace(data[:i]); len(l) > 0 {
					st.acc.Line(l)
				}
			}
			st.skipping = false
			st.offset += int64(i) + 1
			data = data[i+1:]
		}
		carry = append(carry[:0], data...)
		if len(carry) > maxCopilotLine {
			st.offset += int64(len(carry))
			st.skipping, carry = true, carry[:0]
		}
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
	}
}
