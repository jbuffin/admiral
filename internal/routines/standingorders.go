package routines

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"
)

const (
	maxChecks        = 20
	maxCheckRunes    = 300
	maxGuidanceRunes = 2000
)

// StandingOrders are a fleet's Checks and Guidance, kept beside routines.json.
type StandingOrders struct {
	Checks   []string `json:"checks"`
	Guidance string   `json:"guidance"`
}

// StandingOrdersPatch leaves a nil field as stored.
type StandingOrdersPatch struct {
	Checks   *[]string
	Guidance *string
}

func (s *Store) ordersPath(fleet string) string {
	return filepath.Join(s.dir(fleet), "standing-orders.json")
}

func (s *Store) loadOrders(fleet string) (StandingOrders, error) {
	var o StandingOrders
	if err := readJSON(s.ordersPath(fleet), &o); err != nil {
		return o, err
	}
	if o.Checks == nil {
		o.Checks = []string{}
	}
	return o, nil
}

// checkOne is why a single Check can't be stored, empty when it can.
func checkOne(c string) string {
	switch {
	case strings.TrimSpace(c) == "":
		return "must not be empty"
	case utf8.RuneCountInString(c) > maxCheckRunes:
		return "longer than 300 characters"
	}
	return ""
}

func checkGuidance(g string) string {
	if utf8.RuneCountInString(g) > maxGuidanceRunes {
		return "longer than 2000 characters"
	}
	return ""
}

func validateOrders(o StandingOrders) error {
	if len(o.Checks) > maxChecks {
		return invalidf("checks: at most %d Checks, got %d", maxChecks, len(o.Checks))
	}
	for i, c := range o.Checks {
		if msg := checkOne(c); msg != "" {
			return invalidf("checks[%d]: %s", i, msg)
		}
		for _, earlier := range o.Checks[:i] {
			if earlier == c {
				return invalidf("checks[%d]: listed twice", i)
			}
		}
	}
	if msg := checkGuidance(o.Guidance); msg != "" {
		return invalidf("guidance: %s", msg)
	}
	return nil
}

// StandingOrders reads the fleet's orders; a missing file is empty orders.
func (s *Store) StandingOrders(fleet string) (StandingOrders, error) {
	if _, err := s.fleet(fleet); err != nil {
		return StandingOrders{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.loadOrders(fleet)
}

// SetStandingOrders saves the patch over what is stored; a refused patch changes nothing.
func (s *Store) SetStandingOrders(fleet string, p StandingOrdersPatch) (StandingOrders, error) {
	if _, err := s.fleet(fleet); err != nil {
		return StandingOrders{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	o, err := s.loadOrders(fleet)
	if err != nil {
		return o, err
	}
	if p.Checks != nil {
		o.Checks = *p.Checks
		if o.Checks == nil {
			o.Checks = []string{}
		}
	}
	if p.Guidance != nil {
		o.Guidance = *p.Guidance
	}
	if err := validateOrders(o); err != nil {
		return StandingOrders{}, err
	}
	if err := writeFile(s.ordersPath(fleet), o); err != nil {
		return StandingOrders{}, err
	}
	return o, nil
}

// snapshotOrders returns a func that puts the fleet's orders file back as it is now, absent included.
func (s *Store) snapshotOrders(fleet string) (restore func() error, err error) {
	path := s.ordersPath(fleet)
	s.mu.Lock()
	defer s.mu.Unlock()
	prev, err := os.ReadFile(path)
	existed := err == nil
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	return func() error {
		s.mu.Lock()
		defer s.mu.Unlock()
		if !existed {
			if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
				return err
			}
			return nil
		}
		return os.WriteFile(path, prev, 0o600)
	}, nil
}
