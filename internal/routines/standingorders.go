package routines

import (
	"encoding/json"
	"errors"
	"fmt"
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

// ordersError is an ErrInvalid whose message says standing orders, not routine.
type ordersError struct{ msg string }

func (e ordersError) Error() string        { return "invalid standing orders: " + e.msg }
func (e ordersError) Is(target error) bool { return target == ErrInvalid }

func invalidOrdersf(format string, a ...any) error {
	return ordersError{fmt.Sprintf(format, a...)}
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

// HasChecks reports whether the fleet's orders hold Checks. A file that can't be parsed (or that a read has already set
// aside as .bad) counts as holding some, so a damaged file never reads as having none.
func (s *Store) HasChecks(fleet string) bool {
	if _, err := s.fleet(fleet); err != nil {
		return true
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, err := os.Stat(s.ordersPath(fleet) + ".bad"); err == nil {
		return true
	}
	b, err := os.ReadFile(s.ordersPath(fleet))
	if errors.Is(err, os.ErrNotExist) {
		return false
	}
	var o StandingOrders
	if err != nil || json.Unmarshal(b, &o) != nil {
		return true
	}
	return len(o.Checks) > 0
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
		return invalidOrdersf("checks: at most %d Checks, got %d", maxChecks, len(o.Checks))
	}
	for i, c := range o.Checks {
		if msg := checkOne(c); msg != "" {
			return invalidOrdersf("checks[%d]: %s", i, msg)
		}
		for _, earlier := range o.Checks[:i] {
			if earlier == c {
				return invalidOrdersf("checks[%d]: listed twice", i)
			}
		}
	}
	if msg := checkGuidance(o.Guidance); msg != "" {
		return invalidOrdersf("guidance: %s", msg)
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
		o.Checks = make([]string, 0, len(*p.Checks))
		for _, c := range *p.Checks {
			o.Checks = append(o.Checks, strings.TrimSpace(c))
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
	_, statErr := os.Stat(path)
	existed := statErr == nil
	if statErr != nil && !errors.Is(statErr, os.ErrNotExist) {
		return nil, statErr
	}
	prev, err := s.loadOrders(fleet)
	if err != nil {
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
		return writeFile(path, prev)
	}, nil
}
