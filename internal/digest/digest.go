// Package digest keeps chat/<fleet>/digest.json: the events since AdmiralBot's last turn.
package digest

import (
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"os"
	"sync"
	"unicode/utf8"

	"github.com/jbuffin/admiral/internal/brief"
	"github.com/jbuffin/admiral/internal/chatlog"
)

const (
	MaxLines = 20
	MaxBytes = 2000

	fileName = "digest.json"
	maxLine  = 400
)

// Line is one event, numbered so a hook can say how far it has printed.
type Line struct {
	Seq  uint64 `json:"seq"`
	Text string `json:"text"`
}

// File is the digest as stored. NextSeq outlives an emptied digest, so sequences never go backwards.
type File struct {
	NextSeq uint64 `json:"next_seq"`
	Lines   []Line `json:"lines"`
	Dropped int    `json:"dropped,omitempty"`
}

// mu orders the server's writes; a hook only reads, and a read sees a whole file because a write renames one in.
var mu sync.Mutex

// Add appends a line for each text, dropping the oldest lines past MaxLines or MaxBytes.
func Add(home, fleet string, texts ...string) error {
	mu.Lock()
	defer mu.Unlock()
	f, err := Read(home, fleet)
	if err != nil {
		return err
	}
	next := max(f.NextSeq, 1)
	for _, t := range texts {
		if t = brief.Clean(t); t == "" {
			continue
		}
		f.Lines = append(f.Lines, Line{Seq: next, Text: cut(t)})
		next++
	}
	f.NextSeq = next
	for size(f.Lines) > MaxBytes || len(f.Lines) > MaxLines {
		f.Lines = f.Lines[1:]
		f.Dropped++
	}
	return write(home, fleet, f)
}

func size(lines []Line) int {
	n := 0
	for _, l := range lines {
		n += len(l.Text)
	}
	return n
}

func cut(s string) string {
	if len(s) <= maxLine {
		return s
	}
	n := maxLine - len("…")
	for n > 0 && !utf8.RuneStart(s[n]) {
		n--
	}
	return s[:n] + "…"
}

// Read is the fleet's digest; a fleet with none has the zero File.
func Read(home, fleet string) (File, error) {
	var f File
	dir, err := chatlog.OpenFleetDir(home, fleet, false)
	if errors.Is(err, fs.ErrNotExist) {
		return f, nil
	} else if err != nil {
		return f, err
	}
	defer dir.Close()
	h, err := dir.Open(fileName)
	if errors.Is(err, fs.ErrNotExist) {
		return f, nil
	} else if err != nil {
		return f, err
	}
	defer h.Close()
	raw, err := io.ReadAll(io.LimitReader(h, 1<<20))
	if err != nil {
		return File{}, err
	}
	if err := json.Unmarshal(raw, &f); err != nil {
		return File{}, err
	}
	return f, nil
}

// Ack drops the lines at or below seq.
func Ack(home, fleet string, seq uint64) error {
	mu.Lock()
	defer mu.Unlock()
	f, err := Read(home, fleet)
	if err != nil {
		return err
	}
	keep := f.Lines[:0:0]
	for _, l := range f.Lines {
		if l.Seq > seq {
			keep = append(keep, l)
		}
	}
	if len(keep) == len(f.Lines) {
		return nil
	}
	f.Lines, f.Dropped = keep, 0
	return write(home, fleet, f)
}

// write replaces the file through a temp file renamed into place, so a link planted at the path is never followed.
func write(home, fleet string, f File) error {
	if f.Lines == nil {
		f.Lines = []Line{}
	}
	raw, err := json.Marshal(f)
	if err != nil {
		return err
	}
	dir, err := chatlog.OpenFleetDir(home, fleet, true)
	if err != nil {
		return err
	}
	defer dir.Close()
	const tmp = fileName + ".tmp"
	_ = dir.Remove(tmp)
	h, err := dir.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	_, err = h.Write(raw)
	if cerr := h.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		err = dir.Rename(tmp, fileName)
	}
	if err != nil {
		_ = dir.Remove(tmp)
	}
	return err
}
