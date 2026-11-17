//go:build e2e

package main

import (
	"os"
	"time"
)

var clockSkew time.Duration

// The test build can run the routine scheduler ahead of the wall clock, so a test of a routine due at a minute boundary
// starts a few seconds before it instead of waiting for the real one. A release is built without this file.
func init() {
	if d, err := time.ParseDuration(os.Getenv("ADMIRAL_E2E_CLOCK_SKEW")); err == nil {
		clockSkew = d
	}
}
