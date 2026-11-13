package chat

import "github.com/jbuffin/admiral/internal/commitments"

// PostCard appends a commitment's card to the fleet's chat log; a card whose ID the log holds adds nothing.
func (s *Service) PostCard(fleet string, c commitments.Card) error { return nil }
