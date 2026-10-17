package config

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/fsnotify/fsnotify"
)

// FileName is the config file inside $ADMIRAL_HOME.
const FileName = "config.json"

// reloadDelay lets an editor finish its write-and-rename before we read.
const reloadDelay = 100 * time.Millisecond

// Manager holds the last good config and the current error, and reloads when
// the file changes.
type Manager struct {
	path    string
	watcher *fsnotify.Watcher
	done    chan struct{}

	mu      sync.Mutex
	cur     *Config
	errMsg  string // empty when the file is fine
	existed bool   // the file has been seen; a later absence is an error
	watched map[string]bool
	target  string // base name of the resolved file, when config.json is a symlink
	subs    map[chan struct{}]struct{}
	last    []byte // the last published view, to skip no-op reloads
}

// Open loads dir/config.json and starts watching it. An invalid or missing file
// is not an error here: it shows up in View.
func Open(dir string) (*Manager, error) {
	w, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, fmt.Errorf("watch %s: %w", dir, err)
	}
	m := &Manager{
		path:    filepath.Join(dir, FileName),
		watcher: w,
		done:    make(chan struct{}),
		watched: map[string]bool{},
		subs:    map[chan struct{}]struct{}{},
		cur:     &Config{FavoriteSkills: []string{}},
	}
	m.reload()
	go m.watch()
	return m, nil
}

// Close stops watching.
func (m *Manager) Close() {
	_ = m.watcher.Close()
	<-m.done
}

// Current is the last good config; fleets are empty until a file loads.
func (m *Manager) Current() *Config {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.cur
}

// Subscribe returns a channel that receives after every change to what View
// reports. Notifications coalesce; call View for the state.
func (m *Manager) Subscribe() (<-chan struct{}, func()) {
	ch := make(chan struct{}, 1)
	m.mu.Lock()
	m.subs[ch] = struct{}{}
	m.mu.Unlock()
	return ch, func() {
		m.mu.Lock()
		delete(m.subs, ch)
		m.mu.Unlock()
	}
}

func (m *Manager) watch() {
	defer close(m.done)
	timer := time.NewTimer(time.Hour)
	timer.Stop()
	for {
		select {
		case ev, ok := <-m.watcher.Events:
			if !ok {
				return
			}
			if m.relevant(ev.Name) {
				timer.Reset(reloadDelay)
			}
		case <-m.watcher.Errors:
			// Nothing to do; the next event reloads.
		case <-timer.C:
			m.reload()
		}
	}
}

func (m *Manager) relevant(name string) bool {
	base := filepath.Base(name)
	m.mu.Lock()
	defer m.mu.Unlock()
	return base == FileName || (m.target != "" && base == m.target)
}

// reload reads the file, keeps the last good config on any failure, and
// tells subscribers if what the UI sees changed.
func (m *Manager) reload() {
	data, err := os.ReadFile(m.path)
	m.syncWatches()

	m.mu.Lock()
	switch {
	case errors.Is(err, fs.ErrNotExist):
		if m.existed {
			m.errMsg = "file not found"
		}
	case err != nil:
		m.errMsg = err.Error()
		m.existed = true
	default:
		m.existed = true
		if cfg, perr := Parse(data); perr != nil {
			m.errMsg = perr.Error()
		} else {
			m.cur, m.errMsg = cfg, ""
		}
	}
	view, _ := json.Marshal(m.viewLocked())
	changed := !bytes.Equal(view, m.last)
	m.last = view
	var subs []chan struct{}
	if changed {
		for ch := range m.subs {
			subs = append(subs, ch)
		}
	}
	m.mu.Unlock()

	for _, ch := range subs {
		select {
		case ch <- struct{}{}:
		default:
		}
	}
}

// syncWatches watches the directory holding config.json and, when it is a
// symlink, the directory of its target, since edits there never touch ours.
func (m *Manager) syncWatches() {
	want := map[string]bool{filepath.Dir(m.path): true}
	target := ""
	if resolved, err := filepath.EvalSymlinks(m.path); err == nil && resolved != m.path {
		want[filepath.Dir(resolved)] = true
		target = filepath.Base(resolved)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.target = target
	for dir := range m.watched {
		if !want[dir] {
			_ = m.watcher.Remove(dir)
			delete(m.watched, dir)
		}
	}
	for dir := range want {
		if !m.watched[dir] && m.watcher.Add(dir) == nil {
			m.watched[dir] = true
		}
	}
}
