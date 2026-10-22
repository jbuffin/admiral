// Package outcomes is the append-only history of ships that have started,
// needed the engineer, and ended. It is written,
// never read back.
package outcomes

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// queueSize is how many records wait for the writer before new ones are dropped.
const queueSize = 1024

// Record is one line of the log: the common fields plus the event's own.
type Record struct {
	Ship   string
	Event  string
	Fields map[string]any // the event's own fields, flattened into the line
}

type item struct {
	rec    Record
	onDone func(err error)
}

// Writer appends records to $ADMIRAL_HOME/outcomes/<YYYY-MM>.jsonl from one
// goroutine, in the order they were enqueued.
type Writer struct {
	// Now stamps each record when it is written.
	Now func() time.Time

	dir  string
	logf func(format string, args ...any)

	mu     sync.Mutex // guards closed and the send on queue
	closed bool
	queue  chan item
	done   chan struct{}

	// Both are touched by the writer goroutine, and overflowing also by Enqueue.
	failing     bool // the last write failed, and said so
	overflowing bool // a drop has been logged since the queue last drained
	lateLogged  bool // a record that came after Close has been logged
}

// New starts a writer for the outcomes directory dir. logf takes the one line
// a failure or an overflow is logged with.
func New(dir string, logf func(format string, args ...any)) *Writer {
	w := &Writer{Now: time.Now, dir: dir, logf: logf, queue: make(chan item, queueSize), done: make(chan struct{})}
	go w.run()
	return w
}

// Enqueue queues r and never blocks. onDone, when not nil, runs on the writer
// goroutine after r was tried, with the error of a failed write, and never for
// a record that was dropped. A record that finds the queue full is dropped, and
// the first drop is logged; so is the first one that comes after Close.
func (w *Writer) Enqueue(r Record, onDone func(err error)) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.closed {
		if !w.lateLogged {
			w.lateLogged = true
			w.logf("outcome log: a %s record for %s came after the log was closed and is dropped", r.Event, r.Ship)
		}
		return
	}
	select {
	case w.queue <- item{r, onDone}:
	default:
		if !w.overflowing {
			w.overflowing = true
			w.logf("outcome log: the queue of %d records is full, dropping records until it drains", queueSize)
		}
	}
}

// Close writes what is queued and stops the writer.
func (w *Writer) Close() {
	w.mu.Lock()
	if !w.closed {
		w.closed = true
		close(w.queue)
	}
	w.mu.Unlock()
	<-w.done
}

func (w *Writer) run() {
	defer close(w.done)
	for it := range w.queue {
		if err := w.write(it.rec); it.onDone != nil {
			it.onDone(err)
		}
		w.mu.Lock()
		if len(w.queue) == 0 {
			w.overflowing = false
		}
		w.mu.Unlock()
	}
}

// write appends one line, opening the file each time so a directory that was
// missing or unwritable is tried again by the next record. The first failure
// after a success is logged.
func (w *Writer) write(r Record) error {
	err := w.append(r)
	switch {
	case err != nil && !w.failing:
		w.logf("outcome log: %v", err)
		w.failing = true
	case err == nil:
		w.failing = false
	}
	return err
}

func (w *Writer) append(r Record) error {
	now := w.Now().UTC()
	line := map[string]any{}
	for k, v := range r.Fields {
		line[k] = v
	}
	line["v"], line["ts"], line["ship"], line["event"] = 1, now.Format(time.RFC3339), r.Ship, r.Event
	b, err := json.Marshal(line)
	if err != nil {
		return fmt.Errorf("a %s record for %s: %w", r.Event, r.Ship, err)
	}
	if err := os.MkdirAll(w.dir, 0o700); err != nil {
		return err
	}
	f, err := os.OpenFile(filepath.Join(w.dir, now.Format("2006-01")+".jsonl"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	_, err = f.Write(append(b, '\n'))
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	return err
}
