// Package brief renders a fleet's AdmiralBot brief, the Markdown a session loads at start.
package brief

import "time"

// MaxBytes keeps the brief under the 10,000 UTF-16 units Claude Code passes a hook's output whole.
const MaxBytes = 9500

// Input is everything a brief is rendered from, as plain values.
type Input struct {
	Instructions string
	// Questions are AdmiralBot's waiting chat questions, newest first.
	Questions []Question
	// Ships are every ship of the fleet; Render picks the ones the brief names.
	Ships     []Ship
	Campaigns []Campaign
	Notes     []Note // oldest first
	// Entries are the fleet's recent chat log entries of every kind, oldest first.
	Entries []Entry
}

type Question struct {
	Text  string
	Asked time.Time
}

type Ship struct {
	Name   string
	Status string // underway, moored, needs_you, anchored, sailing_off
	Via    string // "admiralbot" for a ship AdmiralBot launched
	Title  string
}

type Campaign struct{ Name string }

type Note struct {
	Text string
	Via  string
	TS   time.Time
}

type Entry struct {
	Kind string // "message", "question", "reset"
	Role string // "user" or "assistant" for a message
	Text string
}

// Render is the brief as Markdown of at most MaxBytes, "" when there is nothing to say.
func Render(in Input) string { return "" }
