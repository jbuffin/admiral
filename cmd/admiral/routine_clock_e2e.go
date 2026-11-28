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

func (realClock) Now() time.Time                  { return time.Now() }
func (realClock) At(t time.Time) <-chan time.Time { return time.After(time.Until(t)) }

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

// At fires once the file's time reaches t.
func (c fileClock) At(t time.Time) <-chan time.Time {
	ch := make(chan time.Time, 1)
	go func() {
		for {
			if now := c.Now(); !now.Before(t) {
				ch <- now
				return
			}
			time.Sleep(20 * time.Millisecond)
		}
	}()
	return ch
}
