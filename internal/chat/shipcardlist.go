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
	Voyage string
	Name   string
	Result string
	PRs    []string
}

// PostedShipCards are the ship cards the fleet's chat log holds that were logged after since, oldest first. A card has no At of its own, so At is when it was logged.
func (s *Service) PostedShipCards(fleet string, since time.Time) []PostedShipCard {
	entries, err := s.history().Since(fleet, since)
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
			Voyage string `json:"first_launched"`
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
		card := PostedShipCard{At: at, Ship: body.Ship, Voyage: body.Voyage, Name: body.Name, Result: body.Result}
		for _, p := range body.PRs {
			card.PRs = append(card.PRs, p.URL)
		}
		out = append(out, card)
	}
	return out
}
