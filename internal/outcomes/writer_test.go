package outcomes

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// logSink collects what a writer logs.
type logSink struct {
	mu    sync.Mutex
	lines []string
}

func (l *logSink) logf(format string, args ...any) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.lines = append(l.lines, fmt.Sprintf(format, args...))
}

func (l *logSink) count() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return len(l.lines)
}

func readLines(t *testing.T, path string) []map[string]any {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("the log file is not there: %v", err)
	}
	defer f.Close()
	var out []map[string]any
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		var m map[string]any
		if err := json.Unmarshal(sc.Bytes(), &m); err != nil {
			t.Fatalf("log line %q is not a JSON object: %v", sc.Text(), err)
		}
		out = append(out, m)
	}
	return out
}

func fixedClock(ts ...string) func() time.Time {
	var mu sync.Mutex
	i := 0
	return func() time.Time {
		mu.Lock()
		defer mu.Unlock()
		t, err := time.Parse(time.RFC3339, ts[min(i, len(ts)-1)])
		if err != nil {
			panic(err)
		}
		i++
		return t
	}
}

// Records come out one JSON object per line, in the order they were queued,
// with v, ts, ship and event first-class and the event's fields flattened in.
// The directory is created, and what a file already holds is kept, also when a
// second writer appends to it.
func TestWriterAppendsInOrderToTheMonthsFile(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "outcomes")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "2026-02.jsonl")
	if err := os.WriteFile(path, []byte(`{"old":true}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	var sink logSink
	a, b := New(dir, sink.logf), New(dir, sink.logf)
	a.Now = fixedClock("2026-02-10T08:00:00Z")
	b.Now = fixedClock("2026-02-10T08:00:01Z")

	for i := 0; i < 200; i++ {
		a.Enqueue(Record{Ship: "s1", Event: "needs_you", Fields: map[string]any{"n": i, "reason": "stop"}}, nil)
	}
	b.Enqueue(Record{Ship: "s2", Event: "dismissed"}, nil)
	a.Close()
	b.Close()

	got := readLines(t, path)
	if len(got) != 202 {
		t.Fatalf("file has %d lines, want the old one, 200 from a and 1 from b", len(got))
	}
	if got[0]["old"] != true {
		t.Errorf("the line the file already held is %v, want it kept first", got[0])
	}
	n := 0
	var seenB bool
	for _, m := range got[1:] {
		if m["ship"] == "s2" {
			seenB = true
			if m["event"] != "dismissed" {
				t.Errorf("b's record = %v, want event dismissed", m)
			}
			continue
		}
		if int(m["n"].(float64)) != n {
			t.Fatalf("a's records out of order: got n=%v where %d was next", m["n"], n)
		}
		n++
		if m["v"] != float64(1) || m["ts"] != "2026-02-10T08:00:00Z" || m["ship"] != "s1" ||
			m["event"] != "needs_you" || m["reason"] != "stop" {
			t.Fatalf("record %v, want v 1, ts 2026-02-10T08:00:00Z, ship s1, event needs_you, reason stop", m)
		}
	}
	if !seenB || n != 200 {
		t.Errorf("a wrote %d of 200 records, b's record present: %v", n, seenB)
	}
	if sink.count() != 0 {
		t.Errorf("a clean run logged %q", sink.lines)
	}
}

// The file is the month of the record's ts in UTC, and ts is written in UTC.
func TestWriterNamesTheFileByTheUTCMonth(t *testing.T) {
	dir := t.TempDir()
	w := New(dir, (&logSink{}).logf)
	w.Now = fixedClock("2026-02-01T05:00:00+09:00", "2026-02-01T00:00:00Z", "2026-12-31T23:59:59-05:00")
	for _, ship := range []string{"a", "b", "c"} {
		w.Enqueue(Record{Ship: ship, Event: "dismissed"}, nil)
	}
	w.Close()

	want := map[string]struct{ ship, ts string }{
		"2026-01.jsonl": {"a", "2026-01-31T20:00:00Z"},
		"2026-02.jsonl": {"b", "2026-02-01T00:00:00Z"},
		"2027-01.jsonl": {"c", "2027-01-01T04:59:59Z"},
	}
	for name, w := range want {
		got := readLines(t, filepath.Join(dir, name))
		if len(got) != 1 || got[0]["ship"] != w.ship || got[0]["ts"] != w.ts {
			t.Errorf("%s = %v, want one line for ship %s with ts %s", name, got, w.ship, w.ts)
		}
	}
}

// A failed write is logged once, calls nothing and drops the record; every
// later record tries again, and a failure after a success is logged again.
func TestWriterLogsAFailureOnceAndRetriesLater(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "outcomes")
	if err := os.WriteFile(dir, nil, 0o644); err != nil { // a file where the directory belongs
		t.Fatal(err)
	}
	var sink logSink
	w := New(dir, sink.logf)
	w.Now = fixedClock("2026-01-15T10:00:00Z")
	written := make(chan string, 8)
	enq := func(ship string) {
		w.Enqueue(Record{Ship: ship, Event: "dismissed"}, func(err error) {
			if err == nil {
				written <- ship
			}
		})
	}
	waitLogged := func(n int) {
		t.Helper()
		for deadline := time.Now().Add(5 * time.Second); sink.count() < n; time.Sleep(10 * time.Millisecond) {
			if time.Now().After(deadline) {
				t.Fatalf("after 5s the writer had logged %d lines, want %d", sink.count(), n)
			}
		}
	}

	enq("lost1")
	enq("lost2")
	waitLogged(1)
	time.Sleep(200 * time.Millisecond) // lost2 has been tried by now
	if n := sink.count(); n != 1 {
		t.Errorf("two failed writes logged %d lines (%q), want one", n, sink.lines)
	}
	select {
	case s := <-written:
		t.Errorf("onWritten ran for %s although its write failed", s)
	default:
	}

	// The directory becomes writable: the next record is written, nothing before it.
	if err := os.Remove(dir); err != nil {
		t.Fatal(err)
	}
	enq("kept1")
	if got := <-written; got != "kept1" {
		t.Fatalf("onWritten ran for %s, want kept1", got)
	}
	jan := filepath.Join(dir, "2026-01.jsonl")
	if got := readLines(t, jan); len(got) != 1 || got[0]["ship"] != "kept1" {
		t.Errorf("after the retry the file holds %v, want only kept1", got)
	}

	// A new month whose file can't be opened: a failure after a success logs again.
	w.Now = fixedClock("2026-03-01T00:00:00Z")
	if err := os.Mkdir(filepath.Join(dir, "2026-03.jsonl"), 0o755); err != nil {
		t.Fatal(err)
	}
	enq("lost3")
	enq("lost4")
	waitLogged(2)
	time.Sleep(200 * time.Millisecond)
	if n := sink.count(); n != 2 {
		t.Errorf("a failure after a success logged %d lines in all (%q), want 2", n, sink.lines)
	}
	if err := os.Remove(filepath.Join(dir, "2026-03.jsonl")); err != nil {
		t.Fatal(err)
	}
	enq("kept2")
	if got := <-written; got != "kept2" {
		t.Errorf("onWritten ran for %s, want kept2", got)
	}
	w.Close()
	if got := readLines(t, filepath.Join(dir, "2026-03.jsonl")); len(got) != 1 || got[0]["ship"] != "kept2" {
		t.Errorf("March file holds %v, want only kept2", got)
	}
}

// A stuck writer never blocks Enqueue. A full queue (1024) drops the record and
// logs once until it drains, and logs again at the next overflow.
func TestWriterDropsOnOverflowAndLogsOncePerFill(t *testing.T) {
	dir := t.TempDir()
	var sink logSink
	w := New(dir, sink.logf)
	w.Now = fixedClock("2026-01-15T10:00:00Z")

	// stall queues a record whose onWritten holds the writer, and returns once
	// the writer is inside it: the queue is then empty with 1024 free slots.
	stall := func(release chan struct{}) {
		t.Helper()
		inside := make(chan struct{})
		w.Enqueue(Record{Ship: "stall", Event: "dismissed"}, func(error) { close(inside); <-release })
		select {
		case <-inside:
		case <-time.After(5 * time.Second):
			t.Fatal("the writer never wrote the first record")
		}
	}
	fill := func(n int) {
		t.Helper()
		done := make(chan struct{})
		go func() {
			defer close(done)
			for i := 0; i < n; i++ {
				w.Enqueue(Record{Ship: fmt.Sprintf("q%d", i), Event: "dismissed"}, nil)
			}
		}()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Fatal("Enqueue blocked while the writer was stuck")
		}
	}

	release := make(chan struct{})
	stall(release)
	fill(1024 + 5)
	if n := sink.count(); n != 1 {
		t.Errorf("5 dropped records logged %d lines (%q), want one", n, sink.lines)
	}
	close(release)
	for deadline := time.Now().Add(5 * time.Second); ; time.Sleep(10 * time.Millisecond) {
		if len(readLines(t, filepath.Join(dir, "2026-01.jsonl"))) == 1+1024 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("the file never reached 1025 lines (the stalled record and the 1024 that fit)")
		}
	}

	release = make(chan struct{})
	stall(release)
	fill(1024 + 1)
	if n := sink.count(); n != 2 {
		t.Errorf("a second overflow after the queue drained: %d lines logged in all, want 2", n)
	}
	close(release)
	w.Close()
	if lines := strings.Join(sink.lines, "\n"); !strings.Contains(strings.ToLower(lines), "outcome") {
		t.Errorf("the overflow line %q doesn't say it is about the outcome log", lines)
	}
}

// A record that comes after Close is dropped, and the first one is logged.
func TestWriterLogsARecordAfterCloseOnce(t *testing.T) {
	var sink logSink
	w := New(t.TempDir(), sink.logf)
	w.Close()
	w.Enqueue(Record{Ship: "late1", Event: "dismissed"}, nil)
	w.Enqueue(Record{Ship: "late2", Event: "dismissed"}, nil)
	if n := sink.count(); n != 1 {
		t.Errorf("two records after Close logged %d lines (%q), want one", n, sink.lines)
	}
}
