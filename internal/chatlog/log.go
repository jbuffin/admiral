// Package chatlog keeps each fleet's chat history: the engineer's and AdmiralBot's messages, chat
// questions as cards and New chat markers. The history is Admiral's. It is never fed back to
// the model, and a transcript read again adds nothing.
package chatlog

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"sync"
	"time"
)

const (
	KindMessage  = "message"
	KindQuestion = "question"
	KindReset    = "reset"
	// KindCommitment is a card the commitment runner posted: a promise kept, or why it was not.
	KindCommitment = "commitment"
	// KindEventCard is an A0 card for an outside event, shown to the engineer and never read by AdmiralBot.
	KindEventCard = "event_card"
	// KindAnswer keeps what the engineer answered a question that was then removed; its id is "a-" and the question's.
	KindAnswer = "answer"
)

const (
	current = "conversation.jsonl"
	// tailCap is how many of the newest entries a fleet keeps in memory.
	tailCap = 500
)

var (
	fleetKey = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)
	rotated  = regexp.MustCompile(`^conversation-(\d{4}-\d{2})\.jsonl$`)
)

type Entry struct {
	ID   string `json:"id"`
	Kind string `json:"kind"`
	Role string `json:"role,omitempty"` // "user" or "assistant" for a message
	Text string `json:"text"`
	// At is when the message was written; a zero At is left out.
	At time.Time `json:"at,omitzero"`
	// Card is a question's item, as the inbox spells it.
	Card json.RawMessage `json:"card,omitempty"`
	// Logged is when Admiral appended the entry, by the Log's clock; it names the file's month.
	Logged time.Time `json:"logged"`
}

type Log struct {
	home string
	now  func() time.Time

	mu     sync.Mutex
	fleets map[string]*fleetLog
}

type fleetLog struct {
	seen  map[string]bool
	tail  []Entry
	month string // the month of the current file's first entry, "" while it holds none
	// terminated is set once this process has made sure the current file ends in a newline.
	terminated bool
}

func New(home string, now func() time.Time) *Log {
	return &Log{home: home, now: now, fleets: map[string]*fleetLog{}}
}

// Append reports whether the entry was new. Admiral's clock stamps it, never its own At.
func (l *Log) Append(fleet string, e Entry) (bool, error) {
	if !fleetKey.MatchString(fleet) {
		return false, fmt.Errorf("chat log: %q is not a fleet key", fleet)
	}
	if e.ID == "" {
		return false, errors.New("chat log: an entry needs an id")
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	fl, err := l.load(fleet)
	if err != nil {
		return false, err
	}
	if fl.seen[e.ID] {
		return false, nil
	}
	now := l.now().UTC()
	e.Logged = now
	line, err := json.Marshal(e)
	if err != nil {
		return false, err
	}
	dir, err := l.fleetDir(fleet, true)
	if err != nil {
		return false, err
	}
	defer dir.Close()
	month := now.Format("2006-01")
	if fl.month != "" && fl.month != month {
		if err := rotate(dir, fl.month); err != nil {
			return false, err
		}
		fl.month = ""
		fl.terminated = false
	}
	f, err := dir.OpenFile(current, os.O_RDWR|os.O_APPEND|os.O_CREATE, 0o600)
	if err != nil {
		return false, err
	}
	// A line is one Write, so a crash leaves at most a torn last line, which a read skips. The
	// first append after it starts a new line, or the torn one would swallow the entry.
	if !fl.terminated {
		if err = endLine(f); err != nil {
			f.Close()
			return false, err
		}
		fl.terminated = true
	}
	_, err = f.Write(append(line, '\n'))
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return false, err
	}
	if fl.month == "" {
		fl.month = month
	}
	fl.seen[e.ID] = true
	fl.tail = append(fl.tail, e)
	if len(fl.tail) > tailCap {
		fl.tail = append([]Entry(nil), fl.tail[len(fl.tail)-tailCap:]...)
	}
	return true, nil
}

func endLine(f *os.File) error {
	fi, err := f.Stat()
	if err != nil || fi.Size() == 0 {
		return err
	}
	last := make([]byte, 1)
	if _, err := f.ReadAt(last, fi.Size()-1); err != nil {
		return err
	}
	if last[0] == '\n' {
		return nil
	}
	_, err = f.Write([]byte{'\n'})
	return err
}

// Recent is the last n entries, oldest first, across months. A fleet with no log has none.
func (l *Log) Recent(fleet string, n int) ([]Entry, error) {
	if !fleetKey.MatchString(fleet) {
		return nil, fmt.Errorf("chat log: %q is not a fleet key", fleet)
	}
	if n <= 0 {
		return []Entry{}, nil
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	fl, err := l.load(fleet)
	if err != nil {
		return nil, err
	}
	if n >= len(fl.tail) {
		n = len(fl.tail)
	}
	return append([]Entry{}, fl.tail[len(fl.tail)-n:]...), nil
}

func (l *Log) load(fleet string) (*fleetLog, error) {
	if fl, ok := l.fleets[fleet]; ok {
		return fl, nil
	}
	fl := &fleetLog{seen: map[string]bool{}}
	dir, err := l.fleetDir(fleet, false)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		l.fleets[fleet] = fl
		return fl, nil
	case err != nil:
		return nil, err
	}
	defer dir.Close()
	names, err := listFiles(dir)
	if err != nil {
		return nil, err
	}
	for _, name := range names {
		es, err := readFile(dir, name)
		if err != nil {
			return nil, err
		}
		for _, e := range es {
			fl.seen[e.ID] = true
		}
		fl.tail = append(fl.tail, es...)
		if len(fl.tail) > tailCap {
			fl.tail = append([]Entry(nil), fl.tail[len(fl.tail)-tailCap:]...)
		}
		if name == current && len(es) > 0 {
			fl.month = es[0].Logged.UTC().Format("2006-01")
		}
	}
	l.fleets[fleet] = fl
	return fl, nil
}

func (l *Log) fleetDir(fleet string, create bool) (*os.Root, error) {
	return OpenFleetDir(l.home, fleet, create)
}

// OpenFleetDir opens chat/<fleet> under home; neither directory may be a link. create makes what is missing.
func OpenFleetDir(home, fleet string, create bool) (*os.Root, error) {
	if !fleetKey.MatchString(fleet) {
		return nil, fmt.Errorf("chat log: %q is not a fleet key", fleet)
	}
	root, err := os.OpenRoot(home)
	if err != nil {
		return nil, err
	}
	defer root.Close()
	chat, err := subDir(root, "chat", create)
	if err != nil {
		return nil, err
	}
	defer chat.Close()
	return subDir(chat, fleet, create)
}

func subDir(root *os.Root, name string, create bool) (*os.Root, error) {
	fi, err := root.Lstat(name)
	switch {
	case errors.Is(err, fs.ErrNotExist) && create:
		if err := root.Mkdir(name, 0o700); err != nil && !errors.Is(err, fs.ErrExist) {
			return nil, err
		}
	case err != nil:
		return nil, err
	case !fi.IsDir():
		return nil, fmt.Errorf("%s is not a directory", name)
	}
	return root.OpenRoot(name)
}

// listFiles is the fleet's log files, oldest month first and the current file last.
func listFiles(dir *os.Root) ([]string, error) {
	f, err := dir.Open(".")
	if err != nil {
		return nil, err
	}
	names, err := f.Readdirnames(-1)
	f.Close()
	if err != nil {
		return nil, err
	}
	var months []string
	hasCurrent := false
	for _, n := range names {
		if n == current {
			hasCurrent = true
		} else if rotated.MatchString(n) {
			months = append(months, n)
		}
	}
	sort.Strings(months)
	if hasCurrent {
		months = append(months, current)
	}
	return months, nil
}

func rotate(dir *os.Root, month string) error {
	to := "conversation-" + month + ".jsonl"
	if _, err := dir.Lstat(to); err == nil {
		// A month's file is never overwritten: the entries go after its own.
		return appendFile(dir, current, to)
	}
	return dir.Rename(current, to)
}

func appendFile(dir *os.Root, from, to string) error {
	src, err := dir.Open(from)
	if err != nil {
		return err
	}
	defer src.Close()
	dst, err := dir.OpenFile(to, os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return err
	}
	_, err = io.Copy(dst, src)
	if cerr := dst.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return err
	}
	return dir.Remove(from)
}

// readFile skips a line that is not an entry, such as a torn last one.
func readFile(dir *os.Root, name string) ([]Entry, error) {
	fi, err := dir.Lstat(name)
	if err != nil {
		return nil, err
	}
	if !fi.Mode().IsRegular() {
		return nil, fmt.Errorf("%s is not a regular file", name)
	}
	f, err := dir.Open(name)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var out []Entry
	r := bufio.NewReader(f)
	for {
		line, err := r.ReadBytes('\n')
		if line = bytes.TrimSpace(line); len(line) > 0 {
			var e Entry
			if json.Unmarshal(line, &e) == nil && e.ID != "" {
				out = append(out, e)
			}
		}
		if err != nil {
			if err == io.EOF {
				return out, nil
			}
			return nil, err
		}
	}
}

// Dir is where a fleet's chat runs and keeps its files.
func Dir(home, fleet string) string { return filepath.Join(home, "chat", fleet) }

// Valid reports whether the key can name a directory under chat/.
func Valid(fleet string) bool { return fleetKey.MatchString(fleet) }
