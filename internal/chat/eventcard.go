package chat

import (
	"encoding/json"

	"github.com/jbuffin/admiral/internal/chatlog"
)

// EventCard is an A0 card for an outside event, built from Lookout fields or the metadata line only.
type EventCard struct {
	ID          string // the trigger record's ID
	Routine     string
	RoutineName string
	Tier        string
	Sender      string
	Link        string
	DedupeKey   string   // the Lookout's validated key, the value a Mute rule by dedupe_key names
	Text        string   // the Lookout's summary, or the metadata line
	Metadata    bool     // Text is the metadata line, not a summary
	MuteBy      []string // the Mute rule choices the event allows: sender, dedupe_key
}

// PostEventCard appends the card to the fleet's chat log; a card whose ID the log holds adds nothing. It writes nothing to
// the digest, which AdmiralBot reads.
func (s *Service) PostEventCard(fleet string, c EventCard) error {
	card, err := json.Marshal(map[string]any{
		"routine": c.Routine, "routine_name": c.RoutineName, "tier": c.Tier, "sender": c.Sender,
		"link": c.Link, "dedupe_key": c.DedupeKey, "metadata": c.Metadata, "mute_by": c.MuteBy,
	})
	if err != nil {
		return err
	}
	_, err = s.history().Append(fleet, chatlog.Entry{ID: c.ID, Kind: chatlog.KindEventCard, Text: c.Text, Card: card})
	return err
}
