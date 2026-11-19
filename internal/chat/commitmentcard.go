package chat

import (
	"encoding/json"

	"github.com/jbuffin/admiral/internal/chatlog"
	"github.com/jbuffin/admiral/internal/commitments"
	"github.com/jbuffin/admiral/internal/ledger"
)

// Notes is the fleet ledgers' one instance: its mutex is per instance, so the commitment runner shares it.
func (s *Service) Notes() *ledger.Ledger { return s.notes() }

// PostCard appends a commitment's card to the fleet's chat log; a card whose ID the log holds adds nothing.
func (s *Service) PostCard(fleet string, c commitments.Card) error {
	card, err := json.Marshal(map[string]string{"commitment": c.Commitment, "outcome": c.Outcome, "ship": c.Ship, "reason": c.Why, "child": c.Child})
	if err != nil {
		return err
	}
	fresh, err := s.history().Append(fleet, chatlog.Entry{ID: c.ID, Kind: chatlog.KindCommitment, Text: c.Text, Card: card})
	if err != nil || !fresh {
		return err
	}
	return nil
}
