package capabilities

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"syscall"
	"time"
)

// Store reads and writes $ADMIRAL_HOME/capabilities.json. It holds no state of its own:
// every call reads the current file, because the setup command, the server and a ship's
// crew gate are separate processes.
type Store struct{ home string }

// Open returns a store over home. It does no I/O.
func Open(home string) *Store { return &Store{home: home} }

type file struct {
	Fleets map[string]map[string]*harnessRecord `json:"fleets"`
}

type harnessRecord struct {
	Version    string                   `json:"version,omitempty"`
	DetectedAt time.Time                `json:"detected_at,omitzero"`
	Versions   map[string]map[ID]Result `json:"versions,omitempty"`
}

func (s *Store) path() string { return filepath.Join(s.home, FileName) }

// load reads the file; a missing file is an empty store, a corrupt or unreadable one is
// an empty store and an error.
func (s *Store) load() (*file, error) {
	f := &file{Fleets: map[string]map[string]*harnessRecord{}}
	raw, err := os.ReadFile(s.path())
	if errors.Is(err, fs.ErrNotExist) {
		return f, nil
	}
	if err != nil {
		return f, err
	}
	var parsed file
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return f, fmt.Errorf("%s is corrupt: %w", s.path(), err)
	}
	if parsed.Fleets != nil {
		f = &parsed
	}
	return f, nil
}

func (s *Store) record(f *file, fleet, harness string) *harnessRecord {
	if f.Fleets[fleet] == nil {
		f.Fleets[fleet] = map[string]*harnessRecord{}
	}
	r := f.Fleets[fleet][harness]
	if r == nil {
		r = &harnessRecord{}
		f.Fleets[fleet][harness] = r
	}
	return r
}

// update re-reads the file under the lock, applies change and writes it back.
func (s *Store) update(change func(*file)) error {
	if err := os.MkdirAll(s.home, 0o700); err != nil {
		return err
	}
	lock, err := os.OpenFile(s.path()+".lock", os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return err
	}
	defer lock.Close()
	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX); err != nil {
		return err
	}
	defer syscall.Flock(int(lock.Fd()), syscall.LOCK_UN)

	f, err := s.load()
	if err != nil {
		if _, statErr := os.Stat(s.path()); statErr == nil {
			// The bad file is kept for the engineer; a read error on a directory fails the rename.
			if err := os.Rename(s.path(), s.path()+".bad"); err != nil {
				return err
			}
		}
	}
	change(f)
	return s.write(f)
}

func (s *Store) write(f *file) error {
	raw, err := json.MarshalIndent(f, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(s.home, FileName+".tmp*")
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
	return os.Rename(tmp.Name(), s.path())
}

// RecordVersion stores the last detected version of a harness in a fleet.
func (s *Store) RecordVersion(fleet, harness, version string, at time.Time) error {
	return s.update(func(f *file) {
		r := s.record(f, fleet, harness)
		r.Version, r.DetectedAt = version, at
	})
}

// Version returns the last detected version and when; "" when none was recorded.
// A corrupt file returns "" and the error.
func (s *Store) Version(fleet, harness string) (string, time.Time, error) {
	f, err := s.load()
	if err != nil {
		return "", time.Time{}, err
	}
	r := f.Fleets[fleet][harness]
	if r == nil {
		return "", time.Time{}, nil
	}
	return r.Version, r.DetectedAt, nil
}

// Record stores a result for the harness version that was probed.
func (s *Store) Record(fleet, harness, version string, id ID, r Result) error {
	return s.update(func(f *file) {
		rec := s.record(f, fleet, harness)
		if rec.Versions == nil {
			rec.Versions = map[string]map[ID]Result{}
		}
		if rec.Versions[version] == nil {
			rec.Versions[version] = map[ID]Result{}
		}
		rec.Versions[version][id] = r
	})
}

// Result returns the stored result for the last detected version, if any.
func (s *Store) Result(fleet, harness string, id ID) (Result, bool, error) {
	f, err := s.load()
	if err != nil {
		return Result{}, false, err
	}
	rec := f.Fleets[fleet][harness]
	if rec == nil || rec.Version == "" {
		return Result{}, false, nil
	}
	r, ok := rec.Versions[rec.Version][id]
	return r, ok, nil
}

// Status computes the status against the last detected version. On a corrupt or
// unreadable file it returns NotChecked and the error.
func (s *Store) Status(fleet, harness string, id ID) (Status, error) {
	r, ok, err := s.Result(fleet, harness, id)
	switch {
	case err != nil || !ok:
		return NotChecked, err
	case r.Passed:
		return Passed, nil
	}
	return Failed, nil
}

// Proven is true only when Status is Passed.
func (s *Store) Proven(fleet, harness string, id ID) bool {
	st, err := s.Status(fleet, harness, id)
	return err == nil && st == Passed
}
