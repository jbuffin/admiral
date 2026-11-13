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
	"slices"
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

	Trigger *Trigger        `json:"trigger,omitempty"`
	Action  string          `json:"action,omitempty"`
	Launch  json.RawMessage `json:"launch,omitempty"`
	Reason  string          `json:"reason,omitempty"`
}

// RefusedError is a write the ledger won't take; its message is for AdmiralBot to act on.
type RefusedError struct{ Msg string }

func (e *RefusedError) Error() string { return e.Msg }

type Ledger struct {
	home string
	now  func() time.Time

	// OnChange runs after every record is appended; it must not block or call the ledger.
	OnChange func()

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
	text, err := checkText(text, "note")
	if err != nil {
		return Entry{}, err
	}
	id, err := newID()
	if err != nil {
		return Entry{}, err
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	open, _, err := l.fold(fleet)
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
	open, _, err := l.fold(fleet)
	if err != nil {
		return nil, err
	}
	if open == nil {
		open = []Entry{}
	}
	return open, nil
}

// fold is the open notes and the open commitments (waiting included), each oldest first. A fleet
// with no ledger has none. A closed entry never changes again, so an event for one is ignored.
func (l *Ledger) fold(fleet string) ([]Entry, []Commitment, error) {
	dir, err := l.fleetDir(fleet, false)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil, nil
	} else if err != nil {
		return nil, nil, err
	}
	defer dir.Close()
	evs, err := readEvents(dir)
	if err != nil {
		return nil, nil, err
	}
	var notes []Entry
	var commits []Commitment
	for _, ev := range evs {
		if ev.Event == EventAdded {
			if ev.Kind == KindCommitment {
				c := Commitment{ID: ev.ID, Text: ev.Text, Action: ev.Action, Launch: ev.Launch, TS: ev.TS, Via: ev.Via}
				if ev.Trigger != nil {
					c.Trigger = *ev.Trigger
				}
				commits = append(commits, c)
			} else {
				notes = append(notes, Entry{ID: ev.ID, Kind: ev.Kind, Text: ev.Text, TS: ev.TS, Via: ev.Via})
			}
			continue
		}
		if ev.Event == EventCancelled {
			if i := indexOf(notes, ev.ID); i >= 0 {
				notes = append(notes[:i], notes[i+1:]...)
				continue
			}
		}
		i := slices.IndexFunc(commits, func(c Commitment) bool { return c.ID == ev.ID })
		if i < 0 {
			continue
		}
		switch ev.Event {
		case EventFired:
			commits[i].Waiting = true
		case EventShown:
			commits[i].Shown = true
		case EventDone, EventDropped, EventCancelled:
			commits = append(commits[:i], commits[i+1:]...)
		}
	}
	return notes, commits, nil
}

func indexOf(notes []Entry, id string) int {
	return slices.IndexFunc(notes, func(e Entry) bool { return e.ID == id })
}

// checkText trims text and refuses it when empty, too long or hiding a character; what names the record in the reason.
func checkText(text, what string) (string, error) {
	text = trim(text)
	if r, bad := HiddenRune(text); bad {
		return "", hiddenRefusal(r, what)
	}
	switch n := utf8.RuneCountInString(text); {
	case n == 0:
		return "", &RefusedError{fmt.Sprintf("the %s is empty", what)}
	case n > MaxNoteChars:
		return "", &RefusedError{fmt.Sprintf("the %s is %d characters; a %s is at most %d, so shorten it", what, n, what, MaxNoteChars)}
	}
	return text, nil
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
	if err == nil && l.OnChange != nil {
		l.OnChange()
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

func hiddenRefusal(r rune, what string) *RefusedError {
	return &RefusedError{fmt.Sprintf("the %s holds a character that shows nothing (U+%04X); write it again in plain visible text", what, r)}
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
		return "", hiddenRefusal(r, "note")
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
	open, _, err := l.fold(fleet)
	if err != nil {
		return Entry{}, err
	}
	for _, e := range open {
		if e.ID == id {
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
