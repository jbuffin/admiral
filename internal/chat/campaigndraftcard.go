package chat

import (
	"encoding/json"
	"strings"

	"github.com/jbuffin/admiral/internal/chatlog"
	"github.com/jbuffin/admiral/internal/digest"
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
// AdmiralBot never reads card entries, so a digest line tells it the outcome.
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
	_ = digest.Add(s.Home, fleet, campaignDraftDigestLine(c))
	return nil
}

func campaignDraftDigestLine(c CampaignDraftCard) string {
	// The title is model-written and sits in quotes, so a quote of its own would let the rest pass as the server's words.
	title := strings.ReplaceAll(strings.Join(strings.Fields(c.Title), " "), `"`, "'")
	switch {
	case !c.Created:
		return "The engineer threw away your campaign draft \"" + title + "\"."
	case c.Failed != "":
		return "The engineer created your campaign draft \"" + title + "\" as " + c.URL + ", but " + c.Failed
	}
	return "The engineer created your campaign draft \"" + title + "\" as " + c.URL + "."
}
