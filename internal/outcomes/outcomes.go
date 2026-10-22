// Package outcomes is the append-only history of ships that have started,
// needed the engineer, and ended. It is written,
// never read back.
package outcomes

import "time"

// Record is one line of the log: the common fields plus the event's own.
type Record struct {
	Ship   string
	Event  string
	Fields map[string]any // the event's own fields, flattened into the line
}

// Writer appends records to $ADMIRAL_HOME/outcomes/<YYYY-MM>.jsonl from one
// goroutine, in the order they were enqueued.
type Writer struct {
	// Now stamps each record when it is written.
	Now func() time.Time
}

// New starts a writer for the outcomes directory dir. logf takes the one line
// a failure or an overflow is logged with.
func New(dir string, logf func(format string, args ...any)) *Writer {
	return &Writer{Now: time.Now}
}

// Enqueue queues r and never blocks. onWritten, when not nil, runs on the
// writer goroutine after r was written, and never when the write failed.
func (w *Writer) Enqueue(r Record, onWritten func()) {}

// Close writes what is queued and stops the writer.
func (w *Writer) Close() {}
