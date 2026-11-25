package crewapi

import (
	"context"
	"time"
)

// Clock is the update checker's source of time; tests drive it by hand.
type Clock interface {
	Now() time.Time
	After(d time.Duration) <-chan time.Time
}

// Run is the background update check. It blocks until ctx ends.
func (s *Service) Run(ctx context.Context) {
	<-ctx.Done()
}
