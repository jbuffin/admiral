// Package state holds what Admiral remembers besides config and tmux
// (ADR 0012): where each ship sits on the water, the last directory
// launched into at each dock, and when each directory was last launched into.
// It lives in $ADMIRAL_HOME/state.json.
package state

import (
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// FileName is the state file inside $ADMIRAL_HOME.
const FileName = "state.json"

// Version is the file format this Admiral reads.
const Version = 1

// settle is how long after the last change the file is written.
const settle = time.Second

// Position is a ship's place as fractions 0–1 of the open water.
type Position struct {
	X float64 `json:"x"`
	Y float64 `json:"y"`
}

type file struct {
	Version   int                  `json:"version"`
	Positions map[string]Position  `json:"positions"`              // by tmux session
	LastDirs  map[string]string    `json:"last_dirs"`              // by DockKey
	DirsUsed  map[string]time.Time `json:"dirs_used"`              // by directory
	Kept      map[string]KeptShip  `json:"kept_inboxes,omitempty"` // by tmux session
	// Paused holds the autopilot panels the engineer paused, by source key and
	// panel id. Optional, so Version stays 1.
	Paused map[string]map[string]bool `json:"paused,omitempty"`
}

// KeptShip is what Admiral remembers of a ship that sailed off with an item
// still unanswered in its inbox (ADR 0016): enough to name it in the inbox and
// to find its dock for gh once the tmux session is gone.
type KeptShip struct {
	Name       string `json:"name"`
	Fleet      string `json:"fleet"`
	FleetName  string `json:"fleet_name,omitempty"`
	FleetColor string `json:"fleet_color,omitempty"`
	Harness    string `json:"harness"`
}

// DockKey keys a dock's last directory.
func DockKey(fleet, harness string) string { return fleet + "/" + harness }

// Store is the in-memory state, written to disk shortly after it settles.
type Store struct {
	path string

	mu    sync.Mutex
	data  file
	timer *time.Timer

	flushMu sync.Mutex // one write at a time, so an older snapshot never lands last
}

// Open reads dir/state.json. A file that is corrupt or has another version is
// moved to state.json.bad and the store starts empty.
func Open(dir string) (*Store, error) {
	s := &Store{path: filepath.Join(dir, FileName), data: file{Version: Version,
		Positions: map[string]Position{}, LastDirs: map[string]string{}, DirsUsed: map[string]time.Time{}}}
	raw, err := os.ReadFile(s.path)
	if errors.Is(err, fs.ErrNotExist) {
		return s, nil
	}
	if err != nil {
		return nil, err
	}
	var f file
	if json.Unmarshal(raw, &f) != nil || f.Version != Version {
		if err := os.Rename(s.path, s.path+".bad"); err != nil {
			return nil, err
		}
		return s, nil
	}
	if f.Positions != nil {
		s.data.Positions = f.Positions
	}
	if f.LastDirs != nil {
		s.data.LastDirs = f.LastDirs
	}
	if f.DirsUsed != nil {
		s.data.DirsUsed = f.DirsUsed
	}
	if f.Paused != nil {
		s.data.Paused = f.Paused
	}
	if f.Kept != nil {
		s.data.Kept = f.Kept
	}
	return s, nil
}

// KeepInbox records a sailed-off ship whose inbox is kept, and writes the file
// now: a lost record would let a new launch reuse the name and clear the inbox.
func (s *Store) KeepInbox(session string, k KeptShip) error {
	s.mu.Lock()
	if s.data.Kept == nil {
		s.data.Kept = map[string]KeptShip{}
	}
	s.data.Kept[session] = k
	s.mu.Unlock()
	return s.Flush()
}

// KeptInbox is the record of a ship whose inbox is kept.
func (s *Store) KeptInbox(session string) (KeptShip, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	k, ok := s.data.Kept[session]
	return k, ok
}

// KeptInboxes is the sessions that have a record.
func (s *Store) KeptInboxes() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]string, 0, len(s.data.Kept))
	for session := range s.data.Kept {
		out = append(out, session)
	}
	return out
}

// DropInbox forgets the record, once the ship's files are gone.
func (s *Store) DropInbox(session string) error {
	s.mu.Lock()
	_, ok := s.data.Kept[session]
	delete(s.data.Kept, session)
	s.mu.Unlock()
	if !ok {
		return nil
	}
	return s.Flush()
}

func (s *Store) Position(session string) (Position, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	p, ok := s.data.Positions[session]
	return p, ok
}

func (s *Store) SetPosition(session string, p Position) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.data.Positions[session] = p
	s.changed()
}

// Keep drops the positions of sessions that no longer exist.
func (s *Store) Keep(sessions map[string]bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for name := range s.data.Positions {
		if !sessions[name] {
			delete(s.data.Positions, name)
			s.changed()
		}
	}
}

func (s *Store) LastDir(fleet, harness string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.data.LastDirs[DockKey(fleet, harness)]
}

// SetLastDir also records that dir was used now, on any dock.
func (s *Store) SetLastDir(fleet, harness, dir string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.data.LastDirs[DockKey(fleet, harness)] = dir
	s.data.DirsUsed[dir] = time.Now()
	s.changed()
}

// DirUsed is when dir was last launched into, or zero if never.
func (s *Store) DirUsed(dir string) time.Time {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.data.DirsUsed[dir]
}

// Paused says the engineer paused autopilot on the panel.
func (s *Store) Paused(source, panel string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.data.Paused[source][panel]
}

// SetPaused pauses or resumes autopilot on the panel. A pause outlives a
// removed source, and applies to one added again under the same key.
func (s *Store) SetPaused(source, panel string, paused bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if paused {
		if s.data.Paused == nil {
			s.data.Paused = map[string]map[string]bool{}
		}
		if s.data.Paused[source] == nil {
			s.data.Paused[source] = map[string]bool{}
		}
		s.data.Paused[source][panel] = true
	} else {
		delete(s.data.Paused[source], panel)
		if len(s.data.Paused[source]) == 0 {
			delete(s.data.Paused, source)
		}
	}
	s.changed()
}

// changed schedules a write; every change pushes it back, so a burst makes one.
func (s *Store) changed() {
	if s.timer == nil {
		s.timer = time.AfterFunc(settle, func() { _ = s.Flush() })
		return
	}
	s.timer.Reset(settle)
}

// Flush writes the file now, atomically.
func (s *Store) Flush() error {
	s.flushMu.Lock()
	defer s.flushMu.Unlock()
	s.mu.Lock()
	raw, err := json.MarshalIndent(s.data, "", "  ")
	s.mu.Unlock()
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(s.path), FileName+".tmp*")
	if err != nil {
		return err
	}
	_, werr := tmp.Write(append(raw, '\n'))
	if werr == nil {
		werr = tmp.Sync() // on disk before the rename makes it the file
	}
	cerr := tmp.Close()
	if werr != nil || cerr != nil {
		_ = os.Remove(tmp.Name())
		return errors.Join(werr, cerr)
	}
	if err := os.Chmod(tmp.Name(), 0o600); err != nil {
		_ = os.Remove(tmp.Name())
		return err
	}
	return os.Rename(tmp.Name(), s.path)
}
