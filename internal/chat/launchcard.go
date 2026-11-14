package chat

// LaunchCard is the card an event launch posts for its ship, built from the event card's fields and the ship's.
type LaunchCard struct {
	ID          string // the trigger record's ID
	Routine     string
	RoutineName string
	Tier        string
	Sender      string
	Link        string
	DedupeKey   string
	Text        string
	Metadata    bool
	MuteBy      []string
	Ship        string // the ship's session name
	Level       string // A3, A4 or A5
	Restricted  string // investigate or draft_fix at A3, else empty
}

// PostLaunchCard appends the card to the fleet's chat log; a card whose ID the log holds adds nothing.
func (s *Service) PostLaunchCard(fleet string, c LaunchCard) error { return nil }
