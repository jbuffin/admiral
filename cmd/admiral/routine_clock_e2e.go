//go:build e2e

package main

import (
	"os"
	"strings"
	"time"

	"github.com/jbuffin/admiral/internal/routines"
)

// The test build can give the routine scheduler a clock the test sets: ADMIRAL_E2E_CLOCK names a file holding an
// RFC 3339 time, so a test of a routine due at a minute boundary moves the clock there instead of waiting for it.
// A release is built without this file.
func routineClock() routines.Clock {
	if path := os.Getenv("ADMIRAL_E2E_CLOCK"); path != "" {
		return fileClock(path)
	}
	return realClock{}
}

type realClock struct{}

func (realClock) Now() time.Time                         { return time.Now() }
func (realClock) After(d time.Duration) <-chan time.Time { return time.After(d) }

// fileClock reads the time from its file, which only the test writes; an unreadable file reads as the wall clock.
type fileClock string

func (c fileClock) Now() time.Time {
	b, err := os.ReadFile(string(c))
	if err != nil {
		return time.Now()
	}
	at, err := time.Parse(time.RFC3339Nano, strings.TrimSpace(string(b)))
	if err != nil {
		return time.Now()
	}
	return at
}

// After fires once the file's time is d past what it read now.
func (c fileClock) After(d time.Duration) <-chan time.Time {
	ch := make(chan time.Time, 1)
	end := c.Now().Add(d)
	go func() {
		for {
			if now := c.Now(); !now.Before(end) {
				ch <- now
				return
			}
			time.Sleep(20 * time.Millisecond)
		}
	}()
	return ch
}
