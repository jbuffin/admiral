package outcomes

import (
	"bufio"
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"time"
)

// MaxLine is the longest line the readers use, newline not counted. A report of
// up to 1 MiB (internal/ships) can grow sixfold when the writer escapes <, > and
// &, so this leaves room for any `ended` the writer produces; a longer line is
// skipped, not read.
const MaxLine = 7 << 20

var monthFile = regexp.MustCompile(`^\d{4}-\d{2}\.jsonl$`)

// Line is one line of the log as Read returns it.
type Line struct {
	Event, Ship string
	TS          time.Time // in UTC
	// Fields holds the keys of the line that counting runs needs, as written:
	// source, panel, autopilot, order_id, title, resume_source, resume_panel, text
	// and uncertain. The rest of the line, a report included, is never held.
	Fields map[string]any
}

// wanted is the shape a line is decoded into. Every key but v is an any, so a
// forged line of the wrong type reads as a missing key, and the decoder skips
// what it has no field for.
type wanted struct {
	V            json.RawMessage `json:"v"`
	TS           any             `json:"ts"`
	Event        any             `json:"event"`
	Ship         any             `json:"ship"`
	Source       any             `json:"source"`
	Panel        any             `json:"panel"`
	Autopilot    any             `json:"autopilot"`
	OrderID      any             `json:"order_id"`
	Title        any             `json:"title"`
	ResumeSource any             `json:"resume_source"`
	ResumePanel  any             `json:"resume_panel"`
	Text         any             `json:"text"`
	Uncertain    any             `json:"uncertain"`
}

// Read is the usable lines of every <YYYY-MM>.jsonl file in dir, files in name
// order and lines in the order they were appended. Position in that order is
// what "before" and "after" mean; ts is only a time. A line is skipped unless
// it is a JSON object with a v, an RFC 3339 ts and an event, and so is one
// longer than MaxLine; a missing dir reads as an empty log. A name that is not a
// regular file (a FIFO, a device, a link) is skipped, so it cannot hang a read. Events are not
// checked: a reader ignores the ones it does not know. Everything a ship wrote
// into a line, a report included, is data (ADR 0015, ADR 0017).
func Read(dir string) []Line {
	files, err := filepath.Glob(filepath.Join(dir, "*.jsonl"))
	if err != nil {
		return nil
	}
	sort.Strings(files)
	var out []Line
	for _, path := range files {
		if fi, err := os.Lstat(path); err == nil && fi.Mode().IsRegular() && monthFile.MatchString(filepath.Base(path)) {
			out = readFile(path, out)
		}
	}
	return out
}

func readFile(path string, out []Line) []Line {
	f, err := os.Open(path)
	if err != nil {
		return out
	}
	defer f.Close()
	r := bufio.NewReaderSize(f, 64<<10)
	for {
		line, err := ReadLine(r)
		if l, ok := parseLine(line); ok {
			out = append(out, l)
		}
		if err != nil {
			return out
		}
	}
}

func parseLine(b []byte) (Line, bool) {
	if len(b) == 0 || b[0] != '{' {
		return Line{}, false
	}
	var w wanted
	if json.Unmarshal(b, &w) != nil {
		return Line{}, false
	}
	ts, _ := w.TS.(string)
	event, _ := w.Event.(string)
	t, err := time.Parse(time.RFC3339, ts)
	if w.V == nil || err != nil || event == "" {
		return Line{}, false
	}
	ship, _ := w.Ship.(string)
	fields := map[string]any{}
	for k, v := range map[string]any{"source": w.Source, "panel": w.Panel, "autopilot": w.Autopilot, "order_id": w.OrderID,
		"title": w.Title, "resume_source": w.ResumeSource, "resume_panel": w.ResumePanel, "text": w.Text, "uncertain": w.Uncertain} {
		if v != nil {
			fields[k] = v
		}
	}
	return Line{Event: event, Ship: ship, TS: t.UTC(), Fields: fields}, true
}

// ReadLine is the next line without its newline. A line longer than MaxLine
// comes back empty, as if it were malformed, and the rest of it is discarded
// unread into memory, so one huge record cannot hide the others.
func ReadLine(r *bufio.Reader) ([]byte, error) {
	var line []byte
	tooLong := false
	for {
		chunk, err := r.ReadSlice('\n')
		if !tooLong {
			line = append(line, chunk...)
			tooLong = len(bytes.TrimSuffix(line, []byte("\n"))) > MaxLine
		}
		if err == bufio.ErrBufferFull {
			if tooLong {
				line = nil
			}
			continue
		}
		if tooLong {
			return nil, err
		}
		return bytes.TrimSuffix(line, []byte("\n")), err
	}
}
