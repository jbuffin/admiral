package e2e

import (
	"testing"
	"time"
)

// Helpers for tests that watch ships: waiting for a count, holding a negative
// for a fixed time, sailing a ship off and reading the outcome log.

const hold = 2500 * time.Millisecond

func (w *world) shipsWhere(pred func(shipJSON) bool) []shipJSON {
	w.t.Helper()
	out := []shipJSON{}
	for _, s := range w.in.ships(w.t) {
		if pred(s) {
			out = append(out, s)
		}
	}
	return out
}

func (w *world) waitShipCount(d time.Duration, what string, pred func(shipJSON) bool, n int) []shipJSON {
	w.t.Helper()
	return w.in.waitShipsList(w.t, d, what, func(list []shipJSON) bool {
		c := 0
		for _, s := range list {
			if pred(s) {
				c++
			}
		}
		return c >= n
	})
}

// holdThat polls check for the length of hold and fails the test with msg the
// first time it does not hold.
func holdThat(t *testing.T, msg string, check func() bool) {
	t.Helper()
	end := time.Now().Add(hold)
	for time.Now().Before(end) {
		if !check() {
			t.Fatalf("%s", msg)
		}
		time.Sleep(100 * time.Millisecond)
	}
}

func live(s shipJSON) bool { return s.Status == "underway" || s.Status == "needs_you" }
