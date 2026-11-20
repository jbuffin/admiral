// Package harnessmodels lists the models each dock's harness offers and keeps the answers.
package harnessmodels

import (
	"context"
	"errors"
	"time"

	"github.com/jbuffin/admiral/internal/config"
)

// Red-phase stubs: the model-list work replaces these.

var errUnimplemented = errors.New("not implemented")

// List asks one dock's harness for its models, killing it when it answers or the timeout passes.
func List(ctx context.Context, h config.Harness, timeout time.Duration) ([]string, error) {
	return nil, errUnimplemented
}

// Entry is what the store keeps for one fleet's harness.
type Entry struct {
	Models   []string
	ListedAt time.Time
	// Error is why the last listing failed; "" after a successful one.
	Error string
}

// Store is $ADMIRAL_HOME/harness_models.json.
type Store struct{}

func Open(home string) *Store { return &Store{} }

func (s *Store) Get(fleet, harness string) (Entry, bool, error) {
	return Entry{}, false, errUnimplemented
}

func (s *Store) Record(fleet, harness string, models []string, at time.Time) error {
	return errUnimplemented
}

func (s *Store) RecordFailure(fleet, harness, msg string, at time.Time) error {
	return errUnimplemented
}

// Service lists docks, stores the results and says when a served list changed.
type Service struct{}

// New returns a service over home; onChange runs (synchronously) after a listing whose models differ from the ones served.
func New(home string, timeout time.Duration, onChange func()) *Service { return &Service{} }

// List lists one dock now and stores the outcome.
func (s *Service) List(ctx context.Context, fleet config.Fleet, harness string) error {
	return errUnimplemented
}

// Models is the stored list; listed is false when the dock has none.
func (s *Service) Models(fleet, harness string) (models []string, listed bool) { return nil, false }
