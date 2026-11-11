package shipconv

import (
	"context"
	"os"
	"sync"
	"time"

	"github.com/jbuffin/admiral/internal/conversation"
	"github.com/jbuffin/admiral/internal/ships"
	"github.com/jbuffin/admiral/internal/transcriptfs"
)

// retryEvery spaces the look for a transcript a ship does not have yet; each look reads tmux.
const (
	retryEvery = 2 * time.Second
	maxRetry   = time.Minute
)

// follow is one ship's transcript, read only when its size or mtime changes.
type follow struct {
	launched string
	loc      where
	located  bool
	lastTry  time.Time
	retry    time.Duration // the wait before the next look for the transcript
	lastFind time.Time
	path     string
	reader   *conversation.Reader
	size     int64
	mtime    time.Time
	tail     string
}

// Tails follows the Claude Code and Copilot ships' transcripts and holds each ship's tail. Other harnesses have no
// conversation adapter, so their tail stays blank and nothing is read for them.
type Tails struct {
	list   func() []ships.Ship
	locate func(session string) (w where, ok, none bool)
	open   func(w where, path string, find bool) (*os.File, os.FileInfo, string, error)

	mu      sync.Mutex
	follows map[string]*follow
	tails   map[string]string
	reads   int // refreshes of a reader, for tests
}

// Tails returns the follower of the service's ships; run it with Run and read it with Tail.
func (s *Service) Tails() *Tails {
	return &Tails{list: s.Ships.List, locate: s.locate, open: openAt}
}

// Tail is the ship's last known tail, "" when nothing is readable.
func (t *Tails) Tail(session string) string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.tails[session]
}

// Run follows until ctx ends. The ships service sees a changed tail on its own tick.
func (t *Tails) Run(ctx context.Context) {
	tick := time.NewTicker(pollEvery)
	defer tick.Stop()
	for {
		t.Poll()
		select {
		case <-tick.C:
		case <-ctx.Done():
			return
		}
	}
}

// Poll follows each ship once, forgets the ships that are gone, and reports whether a tail changed.
func (t *Tails) Poll() bool {
	live := map[string]ships.Ship{}
	for _, sh := range t.list() {
		if ships.HasConversation(sh.Harness) {
			live[sh.Session] = sh
		}
	}
	t.mu.Lock()
	if t.follows == nil {
		t.follows, t.tails = map[string]*follow{}, map[string]string{}
	}
	for name, f := range t.follows {
		if sh, ok := live[name]; !ok || sh.Launched != f.launched {
			delete(t.follows, name)
			delete(t.tails, name)
		}
	}
	t.mu.Unlock()
	changed := false
	for name, sh := range live {
		t.mu.Lock()
		f := t.follows[name]
		if f == nil {
			f = &follow{launched: sh.Launched}
			t.follows[name] = f
		}
		t.mu.Unlock()
		tail, ok := t.step(f, name)
		if !ok {
			continue
		}
		t.mu.Lock()
		if t.follows[name] == f && t.tails[name] != tail {
			t.tails[name] = tail
			changed = true
		}
		t.mu.Unlock()
	}
	return changed
}

// step reads what the transcript gained, if anything. ok is false when the tail is unchanged.
func (t *Tails) step(f *follow, session string) (tail string, ok bool) {
	if !f.located {
		if f.retry == 0 {
			f.retry = retryEvery
		}
		if time.Since(f.lastTry) < f.retry {
			return "", false
		}
		f.lastTry = time.Now()
		w, found, none := t.locate(session)
		switch {
		case none: // the ship has no conversation of ours; asking again rarely helps
			f.retry = min(f.retry*2, maxRetry)
			return "", false
		case !found:
			return "", false
		}
		f.loc, f.located = w, true
		f.reader = conversation.NewReader(conversation.Options{Harness: w.harness, Keep: conversation.TailItem, MaxItems: 50, MaxOutput: 1, MaxBytes: 64 << 10})
	}
	if f.path != "" && !f.mtime.IsZero() {
		if fi, err := os.Lstat(f.path); err == nil && fi.Size() == f.size && fi.ModTime().Equal(f.mtime) {
			return "", false
		}
	}
	find := time.Since(f.lastFind) >= findEvery
	if find {
		f.lastFind = time.Now()
	}
	file, info, p, err := t.open(f.loc, f.path, find)
	if err != nil || file == nil {
		if f.tail == "" {
			return "", false
		}
		f.tail, f.size, f.mtime = "", 0, time.Time{}
		return "", true
	}
	defer file.Close()
	f.path = p
	t.mu.Lock()
	t.reads++
	t.mu.Unlock()
	if _, err := f.reader.Refresh(file, info); err != nil {
		return "", false
	}
	f.size, f.mtime = f.reader.Offset(), info.ModTime()
	tail = conversation.Tail(f.reader.Items())
	changed := tail != f.tail
	f.tail = tail
	return tail, changed
}

// openAt tries the recorded path, and only when find is set the other project dirs.
func openAt(w where, path string, find bool) (*os.File, os.FileInfo, string, error) {
	if path != "" {
		if f, info, err := transcriptfs.OpenFor(w.harness, w.configDir, path); err == nil {
			return f, info, path, nil
		}
	}
	return w.open(find)
}
