package chat

import "errors"

// EventCard is an A0 card for an outside event, built from Lookout fields or the metadata line only.
type EventCard struct {
	ID          string // the trigger record's ID
	Routine     string
	RoutineName string
	Tier        string
	Sender      string
	Link        string
	Text        string   // the Lookout's summary, or the metadata line
	Metadata    bool     // Text is the metadata line, not a summary
	MuteBy      []string // the Mute rule choices the event allows: sender, dedupe_key
}

// PostEventCard appends the card to the fleet's chat log. It never writes the card's text to the digest, which AdmiralBot reads.
func (s *Service) PostEventCard(fleet string, c EventCard) error {
	return errors.New("not implemented")
}
