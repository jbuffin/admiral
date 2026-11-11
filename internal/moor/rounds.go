package moor

import "time"

// MaxRounds is how many rounds a ship's history keeps; the oldest drop off the front.
const MaxRounds = 50

// Round is one round of a ship, kept in $ADMIRAL_HOME/moored/rounds/<name>.json. It never holds transcript content.
type Round struct {
	Round      int       `json:"round"`
	SessionID  string    `json:"session_id"`
	Fleet      string    `json:"fleet"`
	Transcript string    `json:"transcript"` // "" for a round with no transcript of ours
	Started    time.Time `json:"started"`
	Ended      time.Time `json:"ended"`
}

// ReadRounds reads the ship's history, oldest round first; a ship with none has none.
func ReadRounds(home, name string) ([]Round, error) { return nil, nil }

// AppendRound adds a round unless the history already holds that round number, and keeps the latest MaxRounds.
func AppendRound(home, name string, r Round) error { return nil }

// RemoveRounds deletes the ship's history; one already gone is no error.
func RemoveRounds(home, name string) error { return nil }
