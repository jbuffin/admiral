package follow

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/jbuffin/admiral/internal/campaigns"
	"github.com/jbuffin/admiral/internal/github"
	"github.com/jbuffin/admiral/internal/ships"
)

// Followed is one followed campaign as stored.
type Followed struct {
	ID         string
	URL        string // the parent's issue URL
	Fleet      string
	WorkRepo   string // a repository URL, or empty
	Overrides  map[string]campaigns.Override
	FollowedAt time.Time
	EndedAt    time.Time // zero while the campaign is followed
	Tree       *github.TreeNode
	ReadAt     time.Time // of the last good read
	ReadError  string    // of the last failed read, empty after a good one
}

func (f Followed) Ended() bool { return !f.EndedAt.IsZero() }

// Store is the file of followed campaigns, campaigns.json under the home.
type Store struct {
	path string
	mu   sync.Mutex
	list []Followed
}

const storeFile = "campaigns.json"

var errUnknown = errors.New("no such campaign")

// OpenStore reads the file under home; a missing file is an empty store.
func OpenStore(home string) (*Store, error) {
	s := &Store{path: filepath.Join(home, storeFile)}
	b, err := os.ReadFile(s.path)
	if errors.Is(err, os.ErrNotExist) {
		return s, nil
	}
	if err != nil {
		return nil, err
	}
	var file struct{ Campaigns []Followed }
	if err := json.Unmarshal(b, &file); err != nil {
		return nil, fmt.Errorf("%s: %w", s.path, err)
	}
	s.list = file.Campaigns
	return s, nil
}

// save writes the file whole, through a temp file in the same folder, so a crash leaves the old one. s.mu is held.
func (s *Store) save() error {
	b, err := json.MarshalIndent(map[string]any{"Campaigns": s.list}, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(s.path), 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(s.path), storeFile+".*.tmp")
	if err != nil {
		return err
	}
	_, werr := tmp.Write(b)
	if cerr := tmp.Close(); werr == nil {
		werr = cerr
	}
	if werr != nil {
		_ = os.Remove(tmp.Name())
		return werr
	}
	if err := os.Rename(tmp.Name(), s.path); err != nil {
		_ = os.Remove(tmp.Name())
		return err
	}
	return nil
}

func clone(f Followed) Followed {
	if f.Overrides != nil {
		o := make(map[string]campaigns.Override, len(f.Overrides))
		for k, v := range f.Overrides {
			o[k] = v
		}
		f.Overrides = o
	}
	return f
}

func sameIssue(a, b string) bool {
	ka, oka := ships.ClaimKey(a)
	kb, okb := ships.ClaimKey(b)
	return oka && okb && ka == kb
}

// Lookup finds the campaign followed for the parent issue url, ended or not.
func (s *Store) Lookup(url string) (Followed, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, f := range s.list {
		if sameIssue(f.URL, url) {
			return clone(f), true
		}
	}
	return Followed{}, false
}

// Follow adds f, or returns the campaign already followed for the same issue with created false.
func (s *Store) Follow(f Followed) (got Followed, created bool, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, have := range s.list {
		if sameIssue(have.URL, f.URL) {
			return clone(have), false, nil
		}
	}
	var id [8]byte
	if _, err := rand.Read(id[:]); err != nil {
		return Followed{}, false, err
	}
	f.ID = hex.EncodeToString(id[:])
	s.list = append(s.list, f)
	if err := s.save(); err != nil {
		s.list = s.list[:len(s.list)-1]
		return Followed{}, false, err
	}
	return clone(f), true, nil
}

func (s *Store) Get(id string) (Followed, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, f := range s.list {
		if f.ID == id {
			return clone(f), true
		}
	}
	return Followed{}, false
}

// List returns every campaign, active and ended, in the order they were followed.
func (s *Store) List() []Followed {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Followed, len(s.list))
	for i, f := range s.list {
		out[i] = clone(f)
	}
	return out
}

// update changes one campaign and saves; the change is undone when the save fails.
func (s *Store) update(id string, change func(f *Followed)) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range s.list {
		if s.list[i].ID != id {
			continue
		}
		before := clone(s.list[i])
		change(&s.list[i])
		if err := s.save(); err != nil {
			s.list[i] = before
			return err
		}
		return nil
	}
	return errUnknown
}

func (s *Store) End(id string, at time.Time) error {
	return s.update(id, func(f *Followed) {
		if f.EndedAt.IsZero() {
			f.EndedAt = at
		}
	})
}

// SetOverride stores o for the child; a zero override removes it.
func (s *Store) SetOverride(id, childURL string, o campaigns.Override) error {
	return s.update(id, func(f *Followed) {
		if o.Mode == "" && o.Skill == nil {
			delete(f.Overrides, childURL)
			return
		}
		if f.Overrides == nil {
			f.Overrides = map[string]campaigns.Override{}
		}
		f.Overrides[childURL] = o
	})
}

func (s *Store) SetWorkRepo(id, repo string) error {
	return s.update(id, func(f *Followed) { f.WorkRepo = repo })
}

// SetRead records a read; a nil tree keeps the last good one and notes the error.
func (s *Store) SetRead(id string, tree *github.TreeNode, at time.Time, readErr string) error {
	return s.update(id, func(f *Followed) {
		if tree != nil {
			f.Tree, f.ReadAt = tree, at
		}
		f.ReadError = readErr
	})
}
