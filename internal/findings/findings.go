// Package findings keeps what a ship wrote to its findings file after the ship sails off.
package findings

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"sync"
	"syscall"
	"time"
)

// MaxBytes is the largest findings file Admiral keeps.
const MaxBytes = 1 << 20

// maxName bounds the ship and first-launch strings, so an encoded file name stays within a file system's limit.
const maxName = 100

// Voyage is the findings one voyage of a ship left, kept after it sailed off.
type Voyage struct {
	Ship          string
	FirstLaunched string
	Fleet         string
	Markdown      string
}

// Reader is how the route, the card and read_findings look findings up.
type Reader interface {
	Voyage(ship, firstLaunched string) (Voyage, bool)
	Latest(ship string) (Voyage, bool)
	Has(ship, firstLaunched string) bool
}

// Store keeps findings under <home>/findings/<ship>/<first launch>.json. Names are hex-encoded, so no ship name or
// timestamp can reach outside the store or fold into another's file.
type Store struct {
	home   string
	mu     sync.Mutex
	warned sync.Map
}

func Open(home string) *Store { return &Store{home: home} }

type record struct {
	Ship          string `json:"ship"`
	FirstLaunched string `json:"first_launched"`
	Fleet         string `json:"fleet"`
	Markdown      string `json:"markdown"`
}

func encode(s string) (string, bool) {
	if s == "" || len(s) > maxName {
		return "", false
	}
	return hex.EncodeToString([]byte(s)), true
}

// root opens the store's directory; only a write makes it.
func (s *Store) root(create bool) (*os.Root, error) {
	home, err := os.OpenRoot(s.home)
	if err != nil {
		return nil, err
	}
	defer home.Close()
	if create {
		if err := home.MkdirAll("findings", 0o700); err != nil {
			return nil, err
		}
	}
	return home.OpenRoot("findings")
}

// refused logs once per name a ship or first launch that encode will not take.
func (s *Store) refused(ship, firstLaunched string) {
	if _, seen := s.warned.LoadOrStore(ship+"\x00"+firstLaunched, true); !seen {
		fmt.Fprintf(os.Stderr, "admiral: ship %q: its findings are not kept: the ship name or first launch is empty or over %d bytes\n", ship, maxName)
	}
}

// Keep copies the findings file at p into the store as the voyage's findings. kept is false when the file is
// missing, empty or blank, not a regular file or over the cap; a voyage's earlier copy then stays.
func (s *Store) Keep(ship, firstLaunched, fleet, p string) (kept bool, err error) {
	dir, ok1 := encode(ship)
	name, ok2 := encode(firstLaunched)
	if !ok1 || !ok2 {
		s.refused(ship, firstLaunched)
		return false, nil
	}
	// The ship wrote the file: never follow a link, never wait on a pipe.
	f, err := os.OpenFile(p, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if err != nil {
		return false, nil
	}
	defer f.Close()
	if fi, err := f.Stat(); err != nil || !fi.Mode().IsRegular() {
		return false, nil
	}
	b, err := io.ReadAll(io.LimitReader(f, MaxBytes+1))
	switch {
	case err != nil:
		return false, nil
	case len(b) > MaxBytes:
		fmt.Fprintf(os.Stderr, "admiral: ship %q: its findings are larger than %d bytes, so they are not kept\n", ship, MaxBytes)
		return false, nil
	case len(bytes.TrimSpace(b)) == 0:
		return false, nil
	}
	data, err := json.Marshal(record{Ship: ship, FirstLaunched: firstLaunched, Fleet: fleet, Markdown: string(b)})
	if err != nil {
		return false, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	root, err := s.root(true)
	if err != nil {
		return false, err
	}
	defer root.Close()
	if err := root.MkdirAll(dir, 0o700); err != nil {
		return false, err
	}
	tmp := path.Join(dir, "."+name+".tmp")
	if err := writeFile(root, tmp, data); err != nil {
		_ = root.Remove(tmp)
		return false, err
	}
	if err := root.Rename(tmp, path.Join(dir, name+".json")); err != nil {
		_ = root.Remove(tmp)
		return false, err
	}
	return true, nil
}

func writeFile(root *os.Root, name string, data []byte) error {
	f, err := root.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	if _, err := f.Write(data); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}

func (s *Store) read(root *os.Root, name string) (Voyage, bool) {
	b, err := root.ReadFile(name)
	if err != nil {
		return Voyage{}, false
	}
	var r record
	if json.Unmarshal(b, &r) != nil {
		return Voyage{}, false
	}
	return Voyage{Ship: r.Ship, FirstLaunched: r.FirstLaunched, Fleet: r.Fleet, Markdown: r.Markdown}, true
}

func (s *Store) Voyage(ship, firstLaunched string) (Voyage, bool) {
	dir, ok1 := encode(ship)
	name, ok2 := encode(firstLaunched)
	if !ok1 || !ok2 {
		return Voyage{}, false
	}
	root, err := s.root(false)
	if err != nil {
		return Voyage{}, false
	}
	defer root.Close()
	v, ok := s.read(root, path.Join(dir, name+".json"))
	if !ok || v.Ship != ship || v.FirstLaunched != firstLaunched {
		return Voyage{}, false
	}
	return v, true
}

// Latest is the ship's voyage with the newest first launch.
func (s *Store) Latest(ship string) (Voyage, bool) {
	dir, ok := encode(ship)
	if !ok {
		return Voyage{}, false
	}
	root, err := s.root(false)
	if err != nil {
		return Voyage{}, false
	}
	defer root.Close()
	d, err := root.Open(dir)
	if err != nil {
		return Voyage{}, false
	}
	defer d.Close()
	entries, err := d.ReadDir(-1)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return Voyage{}, false
	}
	var best Voyage
	var bestAt time.Time
	found := false
	for _, e := range entries {
		if e.IsDir() || path.Ext(e.Name()) != ".json" {
			continue
		}
		v, ok := s.read(root, path.Join(dir, e.Name()))
		if !ok || v.Ship != ship {
			continue
		}
		at, _ := time.Parse(time.RFC3339, v.FirstLaunched)
		if !found || at.After(bestAt) || (at.Equal(bestAt) && v.FirstLaunched > best.FirstLaunched) {
			best, bestAt, found = v, at, true
		}
	}
	return best, found
}

// Has says whether the voyage has kept findings. It is a stat, so the card can ask for every ship on every chat tick.
func (s *Store) Has(ship, firstLaunched string) bool {
	dir, ok1 := encode(ship)
	name, ok2 := encode(firstLaunched)
	if !ok1 || !ok2 {
		return false
	}
	root, err := s.root(false)
	if err != nil {
		return false
	}
	defer root.Close()
	fi, err := root.Stat(path.Join(dir, name+".json"))
	return err == nil && fi.Mode().IsRegular()
}
