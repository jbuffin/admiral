package harnessmodels

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// FileName is the store's file under $ADMIRAL_HOME.
const FileName = "harness_models.json"

// Entry is what the store keeps for one fleet's harness.
type Entry struct {
	Models   []string  `json:"models,omitempty"`
	ListedAt time.Time `json:"listed_at,omitzero"`
	// Error is why the last listing failed; "" after a successful one.
	Error    string    `json:"error,omitempty"`
	FailedAt time.Time `json:"failed_at,omitzero"`
}

// Store is $ADMIRAL_HOME/harness_models.json. It reads the file on every call, like the capability store.
type Store struct {
	home string
	mu   sync.Mutex
}

type file struct {
	Fleets map[string]map[string]*Entry `json:"fleets"`
}

// Open returns a store over home. It does no I/O.
func Open(home string) *Store { return &Store{home: home} }

func (s *Store) path() string { return filepath.Join(s.home, FileName) }

// errCorrupt marks a file that is not the store's JSON; the next write replaces it.
var errCorrupt = errors.New("corrupt")

// load treats a missing file as empty and a corrupt one as empty with an error that wraps errCorrupt.
func (s *Store) load() (*file, error) {
	f := &file{Fleets: map[string]map[string]*Entry{}}
	raw, err := os.ReadFile(s.path())
	if errors.Is(err, fs.ErrNotExist) {
		return f, nil
	}
	if err != nil {
		return f, err
	}
	var parsed file
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return f, fmt.Errorf("%w: %v", errCorrupt, err)
	}
	if parsed.Fleets != nil {
		f = &parsed
	}
	return f, nil
}

func (s *Store) Get(fleet, harness string) (Entry, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	f, err := s.load()
	e := f.Fleets[fleet][harness]
	if e == nil {
		return Entry{}, false, err
	}
	return *e, true, err
}

// All is every entry, by fleet then harness.
func (s *Store) All() (map[string]map[string]Entry, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	f, err := s.load()
	out := map[string]map[string]Entry{}
	for fleet, byHarness := range f.Fleets {
		out[fleet] = map[string]Entry{}
		for h, e := range byHarness {
			out[fleet][h] = *e
		}
	}
	return out, err
}

func (s *Store) update(fleet, harness string, change func(*Entry)) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	f, err := s.load()
	if err != nil && !errors.Is(err, errCorrupt) {
		return err
	}
	if f.Fleets[fleet] == nil {
		f.Fleets[fleet] = map[string]*Entry{}
	}
	e := f.Fleets[fleet][harness]
	if e == nil {
		e = &Entry{}
		f.Fleets[fleet][harness] = e
	}
	change(e)
	raw, err := json.MarshalIndent(f, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(s.home, 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(s.home, FileName+".tmp*")
	if err != nil {
		return err
	}
	_, werr := tmp.Write(append(raw, '\n'))
	if cerr := tmp.Close(); werr == nil {
		werr = cerr
	}
	if werr != nil {
		_ = os.Remove(tmp.Name())
		return werr
	}
	if err := os.Rename(tmp.Name(), s.path()); err != nil {
		_ = os.Remove(tmp.Name())
		return err
	}
	return nil
}

// Record stores a successful listing and clears the error.
func (s *Store) Record(fleet, harness string, models []string, at time.Time) error {
	return s.update(fleet, harness, func(e *Entry) { *e = Entry{Models: models, ListedAt: at} })
}

// RecordFailure keeps the last good list and its time and records why the newest listing failed.
func (s *Store) RecordFailure(fleet, harness, msg string, at time.Time) error {
	return s.update(fleet, harness, func(e *Entry) { e.Error, e.FailedAt = msg, at })
}
