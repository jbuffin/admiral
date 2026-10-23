package inbox

import (
	"encoding/json"
	"strings"
	"time"

	"github.com/jbuffin/admiral/internal/outcomes"
)

// Proposal is what a finding card proposes: a diff to apply or a reply to post.
type Proposal struct {
	Kind    string `json:"kind"`
	Content string `json:"content"`
}

// CardDecision is the engineer's decision on one card. An edited card carries
// the proposal as the engineer left it; no other card does.
type CardDecision struct {
	ID       string    `json:"id"`
	Decision string    `json:"decision"`
	Proposal *Proposal `json:"proposal,omitempty"`
}

// FindingsRequest is the body of the findings route: a decision for every card.
type FindingsRequest struct {
	Cards []CardDecision `json:"cards"`
}

// cardAnswer is one card's entry in the answer file.
type cardAnswer struct {
	Decision string    `json:"decision"`
	Proposal *Proposal `json:"proposal,omitempty"`
}

// cardIDs is the ids of the cards of a findings item.
func cardIDs(it Item) []string {
	var f struct {
		Cards []struct {
			ID string `json:"id"`
		} `json:"cards"`
	}
	_ = json.Unmarshal(it.Field(KindFindings), &f)
	var ids []string
	for _, c := range f.Cards {
		ids = append(ids, c.ID)
	}
	return ids
}

// AnswerFindings writes the answer to a findings item and records one outcome
// per card. Every card needs one decision, and nothing goes to GitHub: the skill
// that resumes posts what was approved.
func (s *Service) AnswerFindings(session, id string, req FindingsRequest) error {
	return s.Answer(session, id, KindFindings, func(it Item) (any, []outcomes.Record, error) {
		want := map[string]bool{}
		for _, c := range cardIDs(it) {
			want[c] = true
		}
		// An item with no cards takes an empty list, so it can still be answered.
		cards := map[string]cardAnswer{}
		var recs []outcomes.Record
		for _, c := range req.Cards {
			switch {
			case !want[c.ID]:
				return nil, nil, invalid("the item has no card %q", c.ID)
			case cards[c.ID].Decision != "":
				return nil, nil, invalid("card %s has two decisions", c.ID)
			}
			switch c.Decision {
			case "approved", "rejected":
				if c.Proposal != nil {
					return nil, nil, invalid("card %s is %s, so it takes no proposal", c.ID, c.Decision)
				}
			case "edited":
				if c.Proposal == nil || (c.Proposal.Kind != "diff" && c.Proposal.Kind != "reply") || strings.TrimSpace(c.Proposal.Content) == "" {
					return nil, nil, invalid("card %s is edited, so it needs a proposal with kind diff or reply and some content", c.ID)
				}
			default:
				return nil, nil, invalid("card %s: %q is not approved, edited or rejected", c.ID, c.Decision)
			}
			cards[c.ID] = cardAnswer{c.Decision, c.Proposal}
			recs = append(recs, outcomes.Record{Event: "card_" + c.Decision, Fields: map[string]any{"item": id, "kind": KindFindings, "card": c.ID}})
		}
		for c := range want {
			if _, ok := cards[c]; !ok {
				return nil, nil, invalid("card %s has no decision", c)
			}
		}
		file := struct {
			Version  int                   `json:"version"`
			ID       string                `json:"id"`
			Kind     string                `json:"kind"`
			Answered string                `json:"answered"`
			Cards    map[string]cardAnswer `json:"cards"`
		}{1, id, KindFindings, time.Now().UTC().Format(time.RFC3339), cards}
		return file, recs, nil
	})
}
