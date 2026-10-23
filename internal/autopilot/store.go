package autopilot

import (
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// FileName is the autopilot file inside $ADMIRAL_HOME.
const FileName = "autopilot.json"

// Version is the file format this Admiral reads.
const Version = 1

type pair struct{ Source, OrderID string }

// failure is what the engine remembers of an order whose launch failed.
type failure struct {
	Attempts  int       `json:"attempts"`
	NextRetry time.Time `json:"next_retry"`
	GaveUp    bool      `json:"gave_up"` // the third failure: the order is hot
}

type fileOrder struct {
	Source  string `json:"source"`
	OrderID string `json:"order_id"`
}

type fileFailure struct {
	fileOrder
	failure
}

// Resume is where a resume order launches (#120): the checkout of the ship that
// wrote the report, and the autopilot panel that ship counted against ("" for a
// ship autopilot did not launch).
type Resume struct {
	Dir          string `json:"dir"`
	OriginSource string `json:"origin_source,omitempty"`
	OriginPanel  string `json:"origin_panel,omitempty"`
	// Seen says a pass has met the order in its source's listing. The add only
	// asks the source to refresh, so until then the order missing from a listing
	// proves nothing, and the record is not pruned.
	Seen bool `json:"seen,omitempty"`
}

type fileResume struct {
	fileOrder
	Resume
}

type file struct {
	Version  int           `json:"version"`
	Launched []fileOrder   `json:"launched"`
	Failures []fileFailure `json:"failures"`
	// Resumes is optional, so Version stays 1.
	Resumes []fileResume `json:"resumes,omitempty"`
}

// Store is the launched set and the failure state, in $ADMIRAL_HOME/autopilot.json.
// A change is written at once: the file is small and changes only when an order
// launches, fails or leaves its listing.
type Store struct {
	path string

	mu       sync.Mutex
	launched map[pair]bool
	failures map[pair]failure
	resumes  map[pair]Resume
}

// OpenStore reads dir/autopilot.json. A file that is corrupt or has another
// version is moved to autopilot.json.bad and the store starts empty.
func OpenStore(dir string) (*Store, error) {
	s := &Store{path: filepath.Join(dir, FileName), launched: map[pair]bool{}, failures: map[pair]failure{}, resumes: map[pair]Resume{}}
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
	for _, o := range f.Launched {
		s.launched[pair{o.Source, o.OrderID}] = true
	}
	for _, x := range f.Failures {
		s.failures[pair{x.Source, x.OrderID}] = x.failure
	}
	for _, x := range f.Resumes {
		s.resumes[pair{x.Source, x.OrderID}] = x.Resume
	}
	return s, nil
}

func (s *Store) hasLaunched(source, id string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.launched[pair{source, id}]
}

func (s *Store) resumeOf(source, id string) (Resume, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	r, ok := s.resumes[pair{source, id}]
	return r, ok
}

// addResume records where the resume order launches.
func (s *Store) addResume(source, id string, r Resume) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.resumes[pair{source, id}] = r
	return s.save()
}

func (s *Store) failureOf(source, id string) failure {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.failures[pair{source, id}]
}

// markLaunched adds the order to the launched set and forgets its failures.
func (s *Store) markLaunched(source, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, failing := s.failures[pair{source, id}]
	if s.launched[pair{source, id}] && !failing {
		return nil
	}
	s.launched[pair{source, id}] = true
	delete(s.failures, pair{source, id})
	return s.save()
}

func (s *Store) setFailure(source, id string, f failure) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.failures[pair{source, id}] = f
	return s.save()
}

// prune drops what the source no longer lists: listed holds its order ids. It
// says whether it dropped an order autopilot had given up on. A resume record
// is dropped once its order has been listed and is not any more.
func (s *Store) prune(source string, listed map[string]bool) (cooled bool, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	changed := false
	for p := range s.launched {
		if p.Source == source && !listed[p.OrderID] {
			delete(s.launched, p)
			changed = true
		}
	}
	for p := range s.failures {
		if p.Source == source && !listed[p.OrderID] {
			cooled = cooled || s.failures[p].GaveUp
			delete(s.failures, p)
			changed = true
		}
	}
	for p, r := range s.resumes {
		switch {
		case p.Source != source:
		case listed[p.OrderID] && !r.Seen:
			r.Seen = true
			s.resumes[p] = r
			changed = true
		case !listed[p.OrderID] && r.Seen:
			delete(s.resumes, p)
			changed = true
		}
	}
	if !changed {
		return false, nil
	}
	return cooled, s.save()
}

// save writes the file atomically. s.mu must be held.
func (s *Store) save() error {
	f := file{Version: Version, Launched: []fileOrder{}, Failures: []fileFailure{}}
	for p, r := range s.resumes {
		f.Resumes = append(f.Resumes, fileResume{fileOrder{p.Source, p.OrderID}, r})
	}
	for p := range s.launched {
		f.Launched = append(f.Launched, fileOrder{p.Source, p.OrderID})
	}
	for p, x := range s.failures {
		f.Failures = append(f.Failures, fileFailure{fileOrder{p.Source, p.OrderID}, x})
	}
	raw, err := json.MarshalIndent(f, "", "  ")
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
