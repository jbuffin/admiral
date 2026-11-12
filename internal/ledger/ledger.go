// Package ledger keeps each fleet's AdmiralBot memory: notes the engineer asked it to remember.
// chat/<fleet>/ledger.jsonl is an append-only event log, folded in memory on read.
package ledger

import "time"

const (
	EventAdded     = "added"
	EventCancelled = "cancelled"
	KindNote       = "note"
	MaxNotes       = 30
	MaxNoteChars   = 500
)

// Entry is a note as it stands after folding the log.
type Entry struct {
	ID   string    `json:"id"`
	Kind string    `json:"kind"`
	Text string    `json:"text"`
	TS   time.Time `json:"ts"`
	Via  string    `json:"via,omitempty"`
}

type Ledger struct{}

func New(home string, now func() time.Time) *Ledger { return &Ledger{} }

// AddNote appends an added event; via is "" for the engineer.
func (l *Ledger) AddNote(fleet, text, via string) (Entry, error) { return Entry{}, nil }

// Notes are the open notes, oldest first.
func (l *Ledger) Notes(fleet string) ([]Entry, error) { return nil, nil }
