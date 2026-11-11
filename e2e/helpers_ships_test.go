package e2e

import (
	"time"
)

// Helpers for tests that watch ships: waiting for a count, sailing a ship off
// and reading the outcome log.

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

func live(s shipJSON) bool { return s.Status == "underway" || s.Status == "needs_you" }
