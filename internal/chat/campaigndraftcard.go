package chat

import (
	"encoding/json"
	"strings"

	"github.com/jbuffin/admiral/internal/chatlog"
)

// CampaignDraftCard is the outcome of an Inbox campaign draft. A dismissed draft has no URL and is not Created.
// Failed, when set, is what went wrong after the campaign's parent issue was filed.
type CampaignDraftCard struct {
	ID      string // the draft item's ID
	Title   string
	URL     string
	Created bool
	Failed  string
}

// PostCampaignDraftCard appends the card to the fleet's chat log and starts no turn; a card whose ID the log holds adds nothing.
// AdmiralBot never reads card entries and is not told the outcome.
func (s *Service) PostCampaignDraftCard(fleet string, c CampaignDraftCard) error {
	title := strings.Join(strings.Fields(c.Title), " ")
	text := "Thrown away the campaign draft \"" + title + "\"."
	if c.Created {
		text = "Created the campaign \"" + title + "\": " + c.URL
		if c.Failed != "" {
			text += "\n\n" + c.Failed
		}
	}
	card, err := json.Marshal(map[string]any{"title": c.Title, "url": c.URL, "created": c.Created, "failed": c.Failed})
	if err != nil {
		return err
	}
	fresh, err := s.history().Append(fleet, chatlog.Entry{ID: c.ID, Kind: chatlog.KindCampaignDraftCard, Text: text, Card: card})
	if err != nil || !fresh {
		return err
	}
	return nil
}
