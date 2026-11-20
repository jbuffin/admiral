package harnessmodels

import (
	"context"
	"fmt"
	"log"
	"slices"
	"sync"
	"time"

	"github.com/jbuffin/admiral/internal/config"
)

// Service lists docks, stores the results and says when a served list changed.
type Service struct {
	store    *Store
	timeout  time.Duration
	onChange func()

	mu     sync.Mutex
	served map[string][]string // the listed models per dock; read by the config view, so never behind a listing
	// locks serialises a dock's listings, so a Check again waits for the one running instead of racing its store write
	locks map[string]*sync.Mutex
}

// New returns a service over home that serves the stored lists at once. onChange runs (synchronously)
// after a listing whose models differ from the ones served.
func New(home string, timeout time.Duration, onChange func()) *Service {
	s := &Service{store: Open(home), timeout: timeout, onChange: onChange, served: map[string][]string{}, locks: map[string]*sync.Mutex{}}
	all, err := s.store.All()
	if err != nil {
		log.Printf("harness models: reading %s: %v", s.store.path(), err)
	}
	for fleet, byHarness := range all {
		for harness, e := range byHarness {
			if models := clean(e.Models); len(models) > 0 {
				s.served[key(fleet, harness)] = models
			}
		}
	}
	return s
}

func key(fleet, harness string) string { return fleet + "\x00" + harness }

// List lists one dock now and stores the outcome. A failure keeps the last good list.
func (s *Service) List(ctx context.Context, fleet config.Fleet, harness string) error {
	h, ok := fleet.Harness(harness)
	if !ok {
		return fmt.Errorf("fleet %s has no %s dock", fleet.Key, harness)
	}
	k := key(fleet.Key, harness)
	s.mu.Lock()
	lock := s.locks[k]
	if lock == nil {
		lock = &sync.Mutex{}
		s.locks[k] = lock
	}
	s.mu.Unlock()
	lock.Lock()
	defer lock.Unlock()

	models, err := List(ctx, h, s.timeout)
	if err != nil {
		if ctx.Err() == nil {
			if serr := s.store.RecordFailure(fleet.Key, harness, err.Error(), time.Now()); serr != nil {
				log.Printf("harness models: storing the failure for %s %s: %v", fleet.Key, harness, serr)
			}
		}
		return err
	}
	if err := s.store.Record(fleet.Key, harness, models, time.Now()); err != nil {
		log.Printf("harness models: storing the list for %s %s: %v", fleet.Key, harness, err)
	}
	s.mu.Lock()
	changed := !slices.Equal(s.served[k], models)
	s.served[k] = models
	s.mu.Unlock()
	if changed && s.onChange != nil {
		s.onChange()
	}
	return nil
}

// Models is the stored list; listed is false when the dock has none.
func (s *Service) Models(fleet, harness string) (models []string, listed bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	models = s.served[key(fleet, harness)]
	return slices.Clone(models), len(models) > 0
}
