package moor

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"time"
)

// MaxRounds is how many rounds a ship's history keeps; the oldest drop off the front.
const MaxRounds = 50

// Round is one round of a ship, kept in $ADMIRAL_HOME/moored/rounds/<name>.json. It never holds transcript content.
type Round struct {
	Round      int       `json:"round"`
	SessionID  string    `json:"session_id"`
	Fleet      string    `json:"fleet"`
	Transcript string    `json:"transcript"`       // "" for a round with no transcript of ours
	Started    time.Time `json:"started,omitzero"` // the launch time the pane recorded; zero when it recorded none
	Ended      time.Time `json:"ended"`
}

func roundsDir(home string) string { return filepath.Join(dir(home), "rounds") }

// ReadRounds reads the ship's history, oldest round first; a ship with none has none.
func ReadRounds(home, name string) ([]Round, error) {
	if err := checkName(name); err != nil {
		return nil, err
	}
	path := filepath.Join(roundsDir(home), name+".json")
	if err := refuseLink(roundsDir(home)); err != nil {
		return nil, err
	}
	if err := refuseLink(path); err != nil {
		return nil, err
	}
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var rs []Round
	if err := json.Unmarshal(b, &rs); err != nil {
		return nil, err
	}
	return rs, nil
}

// AppendRound adds a round unless the history already holds that round number, and keeps the latest MaxRounds.
func AppendRound(home, name string, r Round) error {
	rs, err := ReadRounds(home, name)
	if err != nil {
		return err
	}
	for _, have := range rs {
		if have.Round == r.Round {
			return nil
		}
	}
	rs = append(rs, r)
	if len(rs) > MaxRounds {
		rs = rs[len(rs)-MaxRounds:]
	}
	d := roundsDir(home)
	if err := os.MkdirAll(dir(home), 0o700); err != nil {
		return err
	}
	if err := os.MkdirAll(d, 0o700); err != nil {
		return err
	}
	b, err := json.MarshalIndent(rs, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(d, ".rounds-*")
	if err != nil {
		return err
	}
	_, werr := tmp.Write(append(b, '\n'))
	if cerr := tmp.Close(); werr == nil {
		werr = cerr
	}
	if werr != nil {
		_ = os.Remove(tmp.Name())
		return werr
	}
	if err := os.Rename(tmp.Name(), filepath.Join(d, name+".json")); err != nil {
		_ = os.Remove(tmp.Name())
		return err
	}
	return nil
}

// RemoveRounds deletes the ship's history; one already gone is no error.
func RemoveRounds(home, name string) error {
	if err := checkName(name); err != nil {
		return err
	}
	if err := refuseLink(roundsDir(home)); err != nil {
		return err
	}
	if err := os.Remove(filepath.Join(roundsDir(home), name+".json")); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}
