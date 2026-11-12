package config

import (
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

const FileName = "config.json"

// reloadDelay lets an editor finish its write-and-rename before we read.
const reloadDelay = 100 * time.Millisecond

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
}

// Open does not fail on an invalid or missing file: that shows up in View.
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

func (m *Manager) Close() {
	_ = m.watcher.Close()
	<-m.done
}

// Current is a copy; callers may change it freely.
func (m *Manager) Current() *Config {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.cur.clone()
}

// SetFavoriteSkills reloads before it returns, so the next View and the pushed
// config already carry the change.
func (m *Manager) SetFavoriteSkills(skills []string) error {
	if err := SetFavoriteSkills(filepath.Dir(m.path), skills); err != nil {
		return err
	}
	m.reload()
	return nil
}

// SetChat reloads before it returns.
func (m *Manager) SetChat(c Chat) error {
	if err := SetChat(filepath.Dir(m.path), c); err != nil {
		return err
	}
	m.reload()
	return nil
}

// SetRemote reloads before it returns.
func (m *Manager) SetRemote(r Remote) error {
	if err := SetRemote(filepath.Dir(m.path), r); err != nil {
		return err
	}
	m.reload()
	return nil
}

// SetSources reloads before it returns, and refuses a value the parser would
// refuse, so a bad write never leaves the file invalid.
func (m *Manager) SetSources(sources json.RawMessage) error {
	if _, err := parseSources(sources); err != nil {
		return err
	}
	if err := SetSources(filepath.Dir(m.path), sources); err != nil {
		return err
	}
	m.reload()
	return nil
}

// SetFleetFields reloads before it returns, so the next View already carries the change.
func (m *Manager) SetFleetFields(key string, f FleetFields) error {
	if err := EditFleet(filepath.Dir(m.path), key, f); err != nil {
		return err
	}
	m.reload()
	return nil
}

// Subscribe fires after every reload, whether or not anything changed.
// Notifications coalesce; call View or Current for the state.
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

// Reload reads the file now, for a caller that just wrote it behind the watcher's delay.
func (m *Manager) Reload() { m.reload() }

// reload keeps the last good config on any failure.
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
	var subs []chan struct{}
	for ch := range m.subs {
		subs = append(subs, ch)
	}
	m.mu.Unlock()

	for _, ch := range subs {
		select {
		case ch <- struct{}{}:
		default:
		}
	}
}

// syncWatches also watches a symlink target's directory, since edits there never touch ours.
func (m *Manager) syncWatches() {
	want := map[string]bool{filepath.Dir(m.path): true}
	target := ""
	if resolved := resolveLink(m.path); resolved != m.path {
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

// resolveLink follows the links by hand when the target is gone, so the
// directory it will come back in stays watched.
func resolveLink(path string) string {
	if resolved, err := filepath.EvalSymlinks(path); err == nil {
		return resolved
	}
	for range 10 {
		dest, err := os.Readlink(path)
		if err != nil {
			break
		}
		if !filepath.IsAbs(dest) {
			dest = filepath.Join(filepath.Dir(path), dest)
		}
		path = dest
	}
	return path
}
