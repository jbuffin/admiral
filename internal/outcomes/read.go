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

// MaxLine leaves room for any `ended`: a 1 MiB report can grow sixfold when the
// writer escapes <, > and &.
const MaxLine = 7 << 20

var monthFile = regexp.MustCompile(`^\d{4}-\d{2}\.jsonl$`)

type Line struct {
	Event, Ship string
	TS          time.Time // in UTC
	// Fields holds only the keys counting runs needs; a report is never held.
	Fields map[string]any
}

// Every key but v is an any, so a forged line of the wrong type reads as a missing key.
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

// Read orders files by name and lines by append; that position is what "before"
// and "after" mean, never ts. A file that is not regular is skipped, so it cannot hang a read.
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

// ReadLine returns a line longer than MaxLine empty and discards the rest unread,
// so one huge record cannot hide the others.
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
