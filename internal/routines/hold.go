package routines

import (
	"path/filepath"
	"time"

	"github.com/jbuffin/admiral/internal/actor"
)

// Hold is one Hold fire switch.
type Hold struct {
	On bool      `json:"on"`
	By string    `json:"by,omitempty"` // "engineer" or actor.Via
	At time.Time `json:"at,omitzero"`
}

const Engineer = "engineer"

// holdPath is the global switch directly under routines/, which no fleet key can collide with.
func (s *Store) holdPath(fleet string) string {
	if fleet == "" {
		return filepath.Join(s.Home, "routines", "hold.json")
	}
	return filepath.Join(s.dir(fleet), "hold.json")
}

func (s *Store) readHold(fleet string) (Hold, error) {
	var h Hold
	err := readJSON(s.holdPath(fleet), &h)
	return h, err
}

// SetHold turns the fleet's Hold fire on or off; an empty fleet is the global switch.
func (s *Store) SetHold(fleet string, on bool, by string) (Hold, error) {
	if fleet != "" {
		if _, err := s.fleet(fleet); err != nil {
			return Hold{}, err
		}
	}
	if !on && by == actor.Via {
		return Hold{}, ErrNotYours
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	h := Hold{On: on, By: by, At: s.now()}
	return h, writeFile(s.holdPath(fleet), h)
}

// GetHold reads one switch; an empty fleet is the global one.
func (s *Store) GetHold(fleet string) (Hold, error) {
	if fleet != "" {
		if _, err := s.fleet(fleet); err != nil {
			return Hold{}, err
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.readHold(fleet)
}

// HoldOn is true when the fleet's switch or the global one is on.
func (s *Store) HoldOn(fleet string) (bool, error) {
	f, err := s.GetHold(fleet)
	if err != nil || f.On {
		return f.On, err
	}
	g, err := s.GetHold("")
	return g.On, err
}

// Effective is the Before acting a routine runs with: min(stored, Ask first) while Hold fire is on.
func Effective(stored Mode, holdOn bool) Mode {
	if holdOn && stored == Act {
		return AskFirst
	}
	return stored
}
