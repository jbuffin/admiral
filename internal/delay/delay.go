// Package delay holds the timing values tests need to shorten. Each reads an
// environment variable holding a Go duration and falls back to its production
// value; the e2e harness sets them all.
package delay

import (
	"os"
	"time"
)

// From returns the duration in the environment variable env, or def when it
// is unset, unparsable or not positive.
func From(env string, def time.Duration) time.Duration {
	if d, err := time.ParseDuration(os.Getenv(env)); err == nil && d > 0 {
		return d
	}
	return def
}
