//go:build !e2e

package main

import (
	"time"

	"github.com/jbuffin/admiral/internal/routines"
)

// routineClock is the routine scheduler's clock: the wall clock, except in the e2e build.
func routineClock() routines.Clock { return realClock{} }

type realClock struct{}

func (realClock) Now() time.Time                         { return time.Now() }
func (realClock) After(d time.Duration) <-chan time.Time { return time.After(d) }
