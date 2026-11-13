package moor

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Skill is what the ship ran: a feedback round relaunches the same one.
type Skill struct {
	Kind string `json:"kind"`
	Name string `json:"name"`
}

// Record is a moored ship, kept in $ADMIRAL_HOME/moored/<name>.json while no tmux session holds it.
type Record struct {
	Name     string `json:"name"`
	Session  string `json:"session"`
	Fleet    string `json:"fleet"`
	Harness  string `json:"harness"`
	Model    string `json:"model"`
	Skill    Skill  `json:"skill"`
	Checkout string `json:"checkout"`
	// The order the ship was launched from, so its order stays hidden while it is moored.
	Source  string   `json:"source"`
	Panel   string   `json:"panel"`
	OrderID string   `json:"order_id"`
	URL     string   `json:"url"`
	Issues  []string `json:"issues,omitempty"`
	Title   string   `json:"title"`
	PRs     []string `json:"prs"`
	Resume  string   `json:"resume"`
	Round   int      `json:"round"`
	// Via is who launched the ship when it was not the engineer.
	Via string `json:"via,omitempty"`
	// Chat is the ship's chat:<message id> launch key, so a moored ship still holds its message.
	Chat string `json:"chat,omitempty"`
	// Routine is the ship's routine:<id> launch key, so a moored ship still holds its routine.
	Routine string `json:"routine,omitempty"`
	// Commitment is the ship's commitment:<id> launch key, so a moored ship still holds its commitment.
	Commitment string `json:"commitment,omitempty"`
	// Resources the ship held, so a round holds the same.
	Resources []string  `json:"resources"`
	MooredAt  time.Time `json:"moored_at"`
}

func dir(home string) string { return filepath.Join(home, "moored") }

func checkName(name string) error {
	if name == "" || strings.ContainsAny(name, `/\`) || name == "." || name == ".." {
		return errors.New("moor: " + name + " is not a name a record can have")
	}
	return nil
}

// Write replaces the ship's record. The name is a ship name, never a path.
func Write(home string, r Record) error {
	if err := checkName(r.Name); err != nil {
		return err
	}
	if err := os.MkdirAll(dir(home), 0o700); err != nil {
		return err
	}
	b, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return err
	}
	path := filepath.Join(dir(home), r.Name+".json")
	tmp, err := os.CreateTemp(dir(home), ".record-*")
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
	if err := os.Rename(tmp.Name(), path); err != nil {
		_ = os.Remove(tmp.Name())
		return err
	}
	return nil
}

// List reads every record, skipping one that is not valid JSON.
func List(home string) ([]Record, error) {
	entries, err := os.ReadDir(dir(home))
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out []Record
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		b, err := os.ReadFile(filepath.Join(dir(home), e.Name()))
		if err != nil {
			continue
		}
		var r Record
		if json.Unmarshal(b, &r) == nil && r.Name != "" {
			out = append(out, r)
		}
	}
	return out, nil
}
