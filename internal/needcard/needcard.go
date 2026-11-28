// Package needcard is the one place that says which need card a ship's stop or an Inbox item posts and which choices one tap answers it with.
package needcard

import (
	"github.com/jbuffin/admiral/internal/inbox"
	"github.com/jbuffin/admiral/internal/ships"
)

// Choice is one button of a need card that a single tap answers whole.
type Choice struct {
	ID    string `json:"id"`
	Label string `json:"label"`
}

// Resolver reads cards off ships and items. Configured says whether a fleet key is one of the configured fleets, the only ones that post cards.
type Resolver struct {
	Configured func(fleet string) bool
}

// ForShip is the id of the card the ship's current stop posts and its choices, or "" when the stop posts no card.
func (Resolver) ForShip(ships.Ship) (string, []Choice) { return "", nil }

// ForItem is the id of the card the Inbox item posts and its choices, or "" when the item posts no card.
func (Resolver) ForItem(inbox.Item) (string, []Choice) { return "", nil }
