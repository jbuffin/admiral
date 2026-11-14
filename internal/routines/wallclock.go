package routines

import (
	"errors"
	"fmt"
	"log"
	"strconv"
	"time"

	"github.com/jbuffin/admiral/internal/delay"
)

// wallClockMinute is the unit of a fleet's wall_clock_minutes; the e2e harness shortens it.
var wallClockMinute = delay.From("ADMIRAL_WALL_CLOCK_MINUTE", time.Minute)

// wallClockRetry is how long the wall clock waits before it tries a ship again after its harness would not end.
const wallClockRetry = time.Minute

type wallShip struct {
	fleet    string
	ship     LiveShip
	minutes  int
	deadline time.Time
}

// wallShips is every live worker-launched ship that has not been ended, with the deadline its fleet's limit gives it.
// Hand, scheduled and promoted ships are not worker-launched, so no record of theirs picks them.
func (s *Scheduler) wallShips() []wallShip {
	if s.Listed == nil || s.EndHarness == nil {
		return nil
	}
	listed := s.Listed()
	var out []wallShip
	for _, fleet := range s.Store.FleetKeys() {
		f, ok := s.Store.Fleets(fleet)
		if !ok || f.Limits.WallClockMinutes <= 0 {
			continue
		}
		recs, err := s.Store.Records(fleet)
		if err != nil {
			continue
		}
		for _, sh := range EventShips(fleet, recs, listed) {
			if sh.Ended {
				continue
			}
			deadline := sh.Launched.Add(time.Duration(f.Limits.WallClockMinutes) * wallClockMinute)
			if retry := s.walled[sh.Trigger]; retry.After(deadline) {
				deadline = retry
			}
			out = append(out, wallShip{fleet, sh, f.Limits.WallClockMinutes, deadline})
		}
	}
	return out
}

// nextWallClock is the earliest deadline of a live worker ship, or zero.
func (s *Scheduler) nextWallClock() time.Time {
	s.mu.Lock()
	defer s.mu.Unlock()
	var best time.Time
	for _, w := range s.wallShips() {
		if best.IsZero() || w.deadline.Before(best) {
			best = w.deadline
		}
	}
	return best
}

// ErrAlreadyEnded is what Scheduler.EndHarness answers for a harness that had ended before it was signalled.
var ErrAlreadyEnded = errors.New("the harness had already ended")

// WallClockReason is the stopped-by value of a ship the wall clock ended; the page reads the minutes back from it.
func WallClockReason(minutes int) string { return "wall_clock:" + strconv.Itoa(minutes) }

// wallClock ends the harness of each worker-launched ship past its fleet's wall clock and fails its launch record with the reason.
// The ship's pane stays, so it shows anchored. The due ships are claimed under the lock and signalled after it, so a harness that
// is slow to end does not hold up event decisions.
func (s *Scheduler) wallClock(now time.Time) {
	s.mu.Lock()
	var due []wallShip
	for _, w := range s.wallShips() {
		if !now.Before(w.deadline) {
			s.walled[w.ship.Trigger] = now.Add(wallClockRetry)
			due = append(due, w)
		}
	}
	s.mu.Unlock()
	for _, w := range due {
		err := s.EndHarness(w.ship.Session, w.minutes)
		s.mu.Lock()
		switch {
		case errors.Is(err, ErrAlreadyEnded):
			s.walled[w.ship.Trigger] = now.Add(watchFor)
			s.mu.Unlock()
			continue
		case err != nil:
			s.mu.Unlock()
			log.Printf("routines: the wall clock could not end %s: %v", w.ship.Session, err)
			continue
		}
		s.walled[w.ship.Trigger] = now.Add(watchFor)
		s.mu.Unlock()
		reason := fmt.Sprintf("stopped at the %d-minute wall clock", w.minutes)
		if err := s.Store.MarkFailed(w.fleet, w.ship.Trigger, reason); err != nil && err != ErrNotFound {
			log.Printf("routines: the wall clock could not mark %s failed: %v", w.ship.Session, err)
		}
	}
}
