// Package digest keeps chat/<fleet>/digest.json: the events since AdmiralBot's last turn.
package digest

const (
	MaxLines = 20
	MaxBytes = 2000
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

// Add appends a line for each text, dropping the oldest lines past MaxLines or MaxBytes.
func Add(home, fleet string, texts ...string) error { return nil }

// Read is the fleet's digest; a fleet with none has the zero File.
func Read(home, fleet string) (File, error) { return File{}, nil }

// Ack drops the lines at or below seq.
func Ack(home, fleet string, seq uint64) error { return nil }
