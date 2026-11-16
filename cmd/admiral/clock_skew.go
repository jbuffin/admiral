//go:build !e2e

package main

import "time"

// clockSkew is how far ahead of the wall clock the routine scheduler runs; only the e2e build moves it.
const clockSkew time.Duration = 0
