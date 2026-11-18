package chat

import (
	"encoding/json"
	"time"

	"github.com/jbuffin/admiral/internal/chatlog"
)

// PostedShipCard is a ship card as the fleet's chat log holds it, with when it was posted.
type PostedShipCard struct {
	At     time.Time
	Ship   string
	Name   string
	Result string
	PRs    []string
}

// PostedShipCards are the ship cards among the fleet's newest log entries, oldest first. A card has no At of its own, so At is when it was logged.
func (s *Service) PostedShipCards(fleet string) []PostedShipCard {
	entries, err := s.history().Recent(fleet, chatlog.TailCap)
	if err != nil {
		return nil
	}
	var out []PostedShipCard
	for _, e := range entries {
		if e.Kind != chatlog.KindShipCard {
			continue
		}
		var body struct {
			Ship   string `json:"ship"`
			Name   string `json:"name"`
			Result string `json:"result"`
			PRs    []struct {
				URL string `json:"url"`
			} `json:"prs"`
		}
		if json.Unmarshal(e.Card, &body) != nil {
			continue
		}
		at := e.At
		if at.IsZero() {
			at = e.Logged
		}
		card := PostedShipCard{At: at, Ship: body.Ship, Name: body.Name, Result: body.Result}
		for _, p := range body.PRs {
			card.PRs = append(card.PRs, p.URL)
		}
		out = append(out, card)
	}
	return out
}
