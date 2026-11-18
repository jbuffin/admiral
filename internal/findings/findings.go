package findings

// Voyage is the findings one voyage of a ship left, kept after it sailed off.
type Voyage struct {
	Ship          string
	FirstLaunched string
	Fleet         string
	Markdown      string
}

// Reader is how the route, the card and read_findings look findings up.
type Reader interface {
	Voyage(ship, firstLaunched string) (Voyage, bool)
	Latest(ship string) (Voyage, bool)
}

// Store keeps findings under <home>/findings.
type Store struct{ home string }

func Open(home string) *Store { return &Store{home: home} }

// Keep copies the findings file at path into the store as the voyage's findings. kept is false when the file is
// missing, empty, not a regular file or over the cap.
func (s *Store) Keep(ship, firstLaunched, fleet, path string) (kept bool, err error) {
	return false, nil
}

func (s *Store) Voyage(ship, firstLaunched string) (Voyage, bool) { return Voyage{}, false }

func (s *Store) Latest(ship string) (Voyage, bool) { return Voyage{}, false }
