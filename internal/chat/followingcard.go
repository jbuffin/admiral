package chat

import (
	"encoding/json"
	"strings"

	"github.com/jbuffin/admiral/internal/chatlog"
	"github.com/jbuffin/admiral/internal/ships"
)

// PostFollowingCard appends the card to the fleet's chat log and starts no turn; a ship's card for the same campaign adds nothing a second time.
// AdmiralBot never reads card entries.
func (s *Service) PostFollowingCard(fleet string, c ships.FollowingCard) error {
	title := strings.Join(strings.Fields(c.Title), " ")
	card, err := json.Marshal(map[string]any{"ship": c.Ship, "campaign_id": c.CampaignID, "title": c.Title})
	if err != nil {
		return err
	}
	_, err = s.history().Append(fleet, chatlog.Entry{
		ID: "following:" + c.Ship + ":" + c.CampaignID, Kind: chatlog.KindFollowingCard,
		Text: "Following the campaign \"" + title + "\".", Card: card,
	})
	return err
}
