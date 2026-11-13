// Package ledger keeps each fleet's AdmiralBot memory: notes the engineer asked it to remember.
// chat/<fleet>/ledger.jsonl is an append-only event log, folded in memory on read.
package ledger

import (
	"bufio"
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/jbuffin/admiral/internal/chatlog"
)

const (
	EventAdded     = "added"
	EventCancelled = "cancelled"
	KindNote       = "note"
	MaxNotes       = 30
	MaxNoteChars   = 500

	fileName = "ledger.jsonl"
	version  = 1
)

// Entry is a note as it stands after folding the log.
type Entry struct {
	ID   string    `json:"id"`
	Kind string    `json:"kind"`
	Text string    `json:"text"`
	TS   time.Time `json:"ts"`
	Via  string    `json:"via,omitempty"`
}

// event is one line of the log, named like the outcome record.
type event struct {
	V     int       `json:"v"`
	TS    time.Time `json:"ts"`
	Event string    `json:"event"`
	ID    string    `json:"id"`
	Kind  string    `json:"kind,omitempty"`
	Text  string    `json:"text,omitempty"`
	Via   string    `json:"via,omitempty"`
}

// RefusedError is a write the ledger won't take; its message is for AdmiralBot to act on.
type RefusedError struct{ Msg string }

func (e *RefusedError) Error() string { return e.Msg }

type Ledger struct {
	home string
	now  func() time.Time

	mu sync.Mutex // one lock for every fleet: writes are rare and the files small
	// terminated holds the fleets whose file this process has made sure ends in a newline.
	terminated map[string]bool
}

func New(home string, now func() time.Time) *Ledger {
	return &Ledger{home: home, now: now, terminated: map[string]bool{}}
}

// AddNote appends an added event; via is "" for the engineer.
func (l *Ledger) AddNote(fleet, text, via string) (Entry, error) {
	if !chatlog.Valid(fleet) {
		return Entry{}, fmt.Errorf("ledger: %q is not a fleet key", fleet)
	}
	text = trim(text)
	if r, bad := HiddenRune(text); bad {
		return Entry{}, hiddenRefusal(r)
	}
	switch n := utf8.RuneCountInString(text); {
	case n == 0:
		return Entry{}, &RefusedError{"the note is empty"}
	case n > MaxNoteChars:
		return Entry{}, &RefusedError{fmt.Sprintf("the note is %d characters; a note is at most %d, so shorten it", n, MaxNoteChars)}
	}
	id, err := newID()
	if err != nil {
		return Entry{}, err
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	open, err := l.fold(fleet)
	if err != nil {
		return Entry{}, err
	}
	if len(open) >= MaxNotes {
		return Entry{}, &RefusedError{fmt.Sprintf("the ledger is full: %d notes are saved. Tell the engineer they can remove notes on Preferences, then save this one again", MaxNotes)}
	}
	ev := event{V: version, TS: l.now().UTC().Truncate(time.Second), Event: EventAdded, ID: id, Kind: KindNote, Text: text, Via: via}
	if err := l.append(fleet, ev); err != nil {
		return Entry{}, err
	}
	return Entry{ID: id, Kind: KindNote, Text: text, TS: ev.TS, Via: via}, nil
}

// Notes are the open notes, oldest first.
func (l *Ledger) Notes(fleet string) ([]Entry, error) {
	if !chatlog.Valid(fleet) {
		return nil, fmt.Errorf("ledger: %q is not a fleet key", fleet)
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	open, err := l.fold(fleet)
	if err != nil {
		return nil, err
	}
	out := []Entry{}
	for _, e := range open {
		if e.Kind == KindNote {
			out = append(out, e)
		}
	}
	return out, nil
}

// fold is the open entries of every kind, oldest first. A fleet with no ledger has none.
func (l *Ledger) fold(fleet string) ([]Entry, error) {
	dir, err := l.fleetDir(fleet, false)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	} else if err != nil {
		return nil, err
	}
	defer dir.Close()
	evs, err := readEvents(dir)
	if err != nil {
		return nil, err
	}
	var open []Entry
	for _, ev := range evs {
		switch ev.Event {
		case EventAdded:
			open = append(open, Entry{ID: ev.ID, Kind: ev.Kind, Text: ev.Text, TS: ev.TS, Via: ev.Via})
		case EventCancelled:
			for i, e := range open {
				if e.ID == ev.ID {
					open = append(open[:i], open[i+1:]...)
					break
				}
			}
		}
	}
	return open, nil
}

func (l *Ledger) fleetDir(fleet string, create bool) (*os.Root, error) {
	return chatlog.OpenFleetDir(l.home, fleet, create)
}

// append writes one event as one Write. A crash leaves at most a torn last line, which a read
// skips; the first append after it starts a new line, or the torn one would swallow the event.
func (l *Ledger) append(fleet string, ev event) error {
	line, err := json.Marshal(ev)
	if err != nil {
		return err
	}
	dir, err := l.fleetDir(fleet, true)
	if err != nil {
		return err
	}
	defer dir.Close()
	f, err := dir.OpenFile(fileName, os.O_RDWR|os.O_APPEND|os.O_CREATE, 0o600)
	if err != nil {
		return err
	}
	if !l.terminated[fleet] {
		if err = endLine(f); err != nil {
			f.Close()
			return err
		}
		l.terminated[fleet] = true
	}
	_, err = f.Write(append(line, '\n'))
	if err != nil {
		l.terminated[fleet] = false
	}
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	return err
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

// readEvents skips a line that is not an event, such as a torn last one.
func readEvents(dir *os.Root) ([]event, error) {
	fi, err := dir.Lstat(fileName)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	} else if err != nil {
		return nil, err
	}
	if !fi.Mode().IsRegular() {
		return nil, fmt.Errorf("%s is not a regular file", fileName)
	}
	f, err := dir.Open(fileName)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var out []event
	r := bufio.NewReader(f)
	for {
		line, err := r.ReadBytes('\n')
		if line = bytes.TrimSpace(line); len(line) > 0 {
			var ev event
			if json.Unmarshal(line, &ev) == nil && ev.ID != "" {
				out = append(out, ev)
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

func newID() (string, error) {
	b := make([]byte, 6)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

// trim drops surrounding whitespace and a byte order mark, as the page's count does.
func trim(s string) string {
	return strings.TrimFunc(s, func(r rune) bool { return r == '\uFEFF' || unicode.IsSpace(r) })
}

// HiddenRune is the first character of text that shows nothing or can change how the text reads:
// a control other than newline and tab, a format character (zero-width, bidi override, tag), a
// private-use or unassigned code point, or bytes that are not UTF-8. Note text is AdmiralBot's,
// so it must not carry any into what the engineer promotes or the brief renders.
func HiddenRune(text string) (rune, bool) {
	for _, r := range text {
		if r == '\n' || r == '\t' {
			continue
		}
		if r == utf8.RuneError || !unicode.IsGraphic(r) {
			return r, true
		}
	}
	return 0, false
}

func hiddenRefusal(r rune) *RefusedError {
	return &RefusedError{fmt.Sprintf("the note holds a character that shows nothing (U+%04X); write it again in plain visible text", r)}
}

// MaxInstructionChars caps a fleet's instructions, in code points after trimming.
const MaxInstructionChars = 2000

const instructionsFile = "instructions.md"

// ErrNotFound is a note id that is unknown or already cancelled.
var ErrNotFound = errors.New("ledger: no such open note")

// Instructions are the fleet's instructions, "" when it has none.
func (l *Ledger) Instructions(fleet string) (string, error) {
	if !chatlog.Valid(fleet) {
		return "", fmt.Errorf("ledger: %q is not a fleet key", fleet)
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.readInstructions(fleet)
}

// SetInstructions stores the engineer's instructions trimmed and returns them; empty text removes them.
func (l *Ledger) SetInstructions(fleet, text string) (string, error) {
	if !chatlog.Valid(fleet) {
		return "", fmt.Errorf("ledger: %q is not a fleet key", fleet)
	}
	text = trim(text)
	if n := utf8.RuneCountInString(text); n > MaxInstructionChars {
		return "", &RefusedError{fmt.Sprintf("the instructions are %d characters; at most %d fit, so cut %d characters", n, MaxInstructionChars, n-MaxInstructionChars)}
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if err := l.writeInstructions(fleet, text); err != nil {
		return "", err
	}
	return text, nil
}

// Cancel closes an open note as the engineer, with no via; ErrNotFound when none is open.
func (l *Ledger) Cancel(fleet, id string) error {
	if !chatlog.Valid(fleet) {
		return fmt.Errorf("ledger: %q is not a fleet key", fleet)
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if _, err := l.openNote(fleet, id); err != nil {
		return err
	}
	return l.cancel(fleet, id)
}

// Promote appends the note to the instructions and cancels it under one lock, returning the new instructions.
func (l *Ledger) Promote(fleet, id string) (string, error) {
	if !chatlog.Valid(fleet) {
		return "", fmt.Errorf("ledger: %q is not a fleet key", fleet)
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	note, err := l.openNote(fleet, id)
	if err != nil {
		return "", err
	}
	old, err := l.readInstructions(fleet)
	if err != nil {
		return "", err
	}
	if r, bad := HiddenRune(note.Text); bad {
		return "", hiddenRefusal(r)
	}
	next := note.Text
	if old != "" {
		next = old + "\n\n" + note.Text
	}
	if n := utf8.RuneCountInString(next); n > MaxInstructionChars {
		return "", &RefusedError{fmt.Sprintf("the note would put the instructions %d characters over the %d limit; shorten the instructions first", n-MaxInstructionChars, MaxInstructionChars)}
	}
	if err := l.writeInstructions(fleet, next); err != nil {
		return "", err
	}
	if err := l.cancel(fleet, id); err != nil {
		if rerr := l.writeInstructions(fleet, old); rerr != nil {
			err = errors.Join(err, rerr)
		}
		return "", err
	}
	return next, nil
}

func (l *Ledger) openNote(fleet, id string) (Entry, error) {
	open, err := l.fold(fleet)
	if err != nil {
		return Entry{}, err
	}
	for _, e := range open {
		if e.ID == id && e.Kind == KindNote {
			return e, nil
		}
	}
	return Entry{}, ErrNotFound
}

func (l *Ledger) cancel(fleet, id string) error {
	return l.append(fleet, event{V: version, TS: l.now().UTC().Truncate(time.Second), Event: EventCancelled, ID: id})
}

func (l *Ledger) readInstructions(fleet string) (string, error) {
	dir, err := l.fleetDir(fleet, false)
	if errors.Is(err, fs.ErrNotExist) {
		return "", nil
	} else if err != nil {
		return "", err
	}
	defer dir.Close()
	fi, err := dir.Lstat(instructionsFile)
	if errors.Is(err, fs.ErrNotExist) {
		return "", nil
	} else if err != nil {
		return "", err
	}
	if !fi.Mode().IsRegular() {
		return "", fmt.Errorf("%s is not a regular file", instructionsFile)
	}
	f, err := dir.Open(instructionsFile)
	if err != nil {
		return "", err
	}
	defer f.Close()
	b, err := io.ReadAll(f)
	return trim(string(b)), err
}

// writeInstructions replaces the file through a temp file renamed into place, so a link planted
// at the path is replaced and never followed; empty text removes it.
func (l *Ledger) writeInstructions(fleet, text string) error {
	if text == "" {
		dir, err := l.fleetDir(fleet, false)
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		} else if err != nil {
			return err
		}
		defer dir.Close()
		if err := dir.Remove(instructionsFile); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return err
		}
		return nil
	}
	dir, err := l.fleetDir(fleet, true)
	if err != nil {
		return err
	}
	defer dir.Close()
	tmp := instructionsFile + ".tmp"
	_ = dir.Remove(tmp)
	f, err := dir.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	_, err = f.WriteString(text)
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		err = dir.Rename(tmp, instructionsFile)
	}
	if err != nil {
		_ = dir.Remove(tmp)
	}
	return err
}
