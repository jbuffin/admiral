package chat

import (
	"encoding/json"
	"github.com/jbuffin/admiral/internal/brief"
	"github.com/jbuffin/admiral/internal/chatlog"
	"github.com/jbuffin/admiral/internal/ships"
)

// PostFollowingCard appends the card to the fleet's chat log and starts no turn; one ship launch's card for the same campaign adds nothing a second time.
// AdmiralBot never reads card entries.
func (s *Service) PostFollowingCard(fleet string, c ships.FollowingCard) error {
	title := brief.Clean(c.Title)
	card, err := json.Marshal(map[string]any{"ship": c.Ship, "campaign_id": c.CampaignID, "title": title})
	if err != nil {
		return err
	}
	_, err = s.history().Append(fleet, chatlog.Entry{
		ID: "following:" + c.Ship + ":" + c.First + ":" + c.CampaignID, Kind: chatlog.KindFollowingCard,
		Text: "Following the campaign \"" + title + "\".", Card: card,
	})
	return err
}
