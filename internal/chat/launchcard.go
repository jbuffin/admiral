package chat

import (
	"encoding/json"

	"github.com/jbuffin/admiral/internal/chatlog"
)

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

// PostLaunchCard appends the card to the fleet's chat log; a card whose ID the log holds adds nothing. It writes nothing to
// the digest, which AdmiralBot reads.
func (s *Service) PostLaunchCard(fleet string, c LaunchCard) error {
	card, err := json.Marshal(map[string]any{
		"routine": c.Routine, "routine_name": c.RoutineName, "tier": c.Tier, "sender": c.Sender,
		"link": c.Link, "dedupe_key": c.DedupeKey, "metadata": c.Metadata, "mute_by": c.MuteBy,
		"ship": c.Ship, "level": c.Level, "restricted": c.Restricted,
	})
	if err != nil {
		return err
	}
	_, err = s.history().Append(fleet, chatlog.Entry{ID: c.ID, Kind: chatlog.KindLaunchCard, Text: c.Text, Card: card})
	return err
}
