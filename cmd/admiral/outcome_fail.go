//go:build !e2e

package main

import "github.com/jbuffin/admiral/internal/outcomes"

// outcomeWriteFailed is nil in a release: only the test build reports each failed outcome write.
var outcomeWriteFailed func(outcomes.Record, error)
