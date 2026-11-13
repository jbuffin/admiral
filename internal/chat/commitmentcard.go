package chat

import (
	"encoding/json"
	"strings"

	"github.com/jbuffin/admiral/internal/chatlog"
	"github.com/jbuffin/admiral/internal/commitments"
	"github.com/jbuffin/admiral/internal/digest"
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
	_ = digest.Add(s.Home, fleet, digestLine(c))
	return nil
}

func digestLine(c commitments.Card) string {
	// The text sits in quotes, so a quote of its own would end them and let the rest pass as the server's words.
	text := strings.ReplaceAll(strings.Join(strings.Fields(c.Text), " "), `"`, "'")
	switch c.Outcome {
	case commitments.OutcomeLaunched:
		if c.Child != "" {
			return "Your commitment \"" + text + "\" launched ship " + c.Ship + " for " + campaignRef(c.Child) + "."
		}
		return "Your commitment \"" + text + "\" launched ship " + c.Ship + "."
	case commitments.OutcomeReminded:
		return "Your commitment \"" + text + "\" reminded the engineer."
	case commitments.OutcomeAged:
		return "Your commitment \"" + text + "\" is 14 days old: asked the engineer whether they still want it."
	case commitments.OutcomeEnded:
		return "Your commitment \"" + text + "\" ended: " + c.Why + "."
	case commitments.OutcomeTooLarge:
		return "Your commitment \"" + text + "\" holds too large a record to work on unattended: " + c.Why + "."
	case commitments.OutcomeDropped:
		return "Your commitment \"" + text + "\" was dropped: " + c.Why + "."
	}
	return "Your commitment \"" + text + "\" could not run: " + c.Why + "."
}
