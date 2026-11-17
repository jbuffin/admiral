//go:build e2e

package main

import (
	"fmt"
	"os"

	"github.com/jbuffin/admiral/internal/outcomes"
)

// outcomeWriteFailed prints a line per failed write, which a test waits on to know the writer has tried a record.
func outcomeWriteFailed(r outcomes.Record, _ error) {
	fmt.Fprintf(os.Stderr, "admiral: e2e: outcome write failed: %s %s\n", r.Event, r.Ship)
}
