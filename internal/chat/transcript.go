// Package chat runs one AdmiralBot chat session per fleet, follows the transcript Claude Code
// writes for it, keeps the fleet's history in its log and serves it.
package chat

import (
	"encoding/json"

	"github.com/jbuffin/admiral/internal/chatlog"
	"github.com/jbuffin/admiral/internal/conversation"
)

// Message is one entry of the fleet's history: text, a question card or a New chat marker.
type Message struct {
	ID   string `json:"id"`
	Kind string `json:"kind"`           // "message", "question", "reset" or "commitment"
	Role string `json:"role,omitempty"` // "user" or "assistant", for a message
	Text string `json:"text"`           // a card's title; a marker's "New chat"
	At   string `json:"at"`
	// Card is a question's item as it was asked, without its answer, or a commitment card's {commitment, outcome, ship, reason}; outcome is launched, reminded, cant_run, dropped or aged (a no-trigger commitment 14 days old).
	Card json.RawMessage `json:"card,omitempty"`
}

// A notice (a subagent's report, or another Claude session's message) shows as the user's message.
func messages(items []conversation.Item) []Message {
	out := make([]Message, 0, len(items))
	for _, it := range items {
		role := "user"
		if it.Kind == conversation.KindAssistant {
			role = "assistant"
		}
		out = append(out, Message{ID: it.ID, Kind: chatlog.KindMessage, Role: role, Text: it.Text, At: it.At})
	}
	return out
}
