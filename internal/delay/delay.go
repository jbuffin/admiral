// Package delay holds the timing values tests need to shorten; the e2e harness sets them all.
package delay

import (
	"os"
	"time"
)

func From(env string, def time.Duration) time.Duration {
	if d, err := time.ParseDuration(os.Getenv(env)); err == nil && d > 0 {
		return d
	}
	return def
}
