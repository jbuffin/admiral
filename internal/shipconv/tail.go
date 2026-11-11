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
const retryEvery = 2 * time.Second

// follow is one Claude ship's transcript, read only when its size or mtime changes.
type follow struct {
	launched string
	loc      where
	located  bool
	lastTry  time.Time
	path     string
	reader   *conversation.Reader
	size     int64
	mtime    time.Time
	tail     string
}

// Tails follows the Claude ships' transcripts and holds each ship's tail. Other harnesses have no
// conversation adapter, so their tail stays blank and nothing is read for them.
type Tails struct {
	list   func() []ships.Ship
	locate func(session string) (w where, ok, none bool)

	mu      sync.Mutex
	follows map[string]*follow
	tails   map[string]string
	reads   int // refreshes of a reader, for tests
}

// Tails returns the follower of the service's ships; run it with Run and read it with Tail.
func (s *Service) Tails() *Tails {
	return &Tails{list: s.Ships.List, locate: s.locate}
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

// Poll follows each Claude ship once, forgets the ships that are gone, and reports whether a tail changed.
func (t *Tails) Poll() bool {
	live := map[string]ships.Ship{}
	for _, sh := range t.list() {
		if sh.Harness == "claude" {
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
		if time.Since(f.lastTry) < retryEvery {
			return "", false
		}
		f.lastTry = time.Now()
		w, found, none := t.locate(session)
		if !found || none {
			return "", false
		}
		f.loc, f.located = w, true
		f.reader = conversation.NewReader(conversation.Options{Keep: conversation.TailItem, MaxItems: 50})
	}
	var (
		file *os.File
		info os.FileInfo
		err  error
	)
	if f.path != "" {
		file, info, err = transcriptfs.Open(f.loc.configDir, f.path)
	}
	if file == nil {
		var p string
		if file, info, p, err = f.loc.open(true); err == nil {
			f.path = p
		}
	}
	if err != nil || file == nil {
		return "", false
	}
	defer file.Close()
	if info.Size() == f.size && info.ModTime().Equal(f.mtime) && !f.mtime.IsZero() {
		return "", false
	}
	f.size, f.mtime = info.Size(), info.ModTime()
	t.mu.Lock()
	t.reads++
	t.mu.Unlock()
	_, _ = f.reader.Refresh(file, info)
	f.tail = conversation.Tail(f.reader.Items())
	return f.tail, true
}
