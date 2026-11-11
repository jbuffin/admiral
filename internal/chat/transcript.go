// Package chat starts the AdmiralBot `_chat` session, follows the transcript Claude Code writes
// for it, and serves the conversation.
package chat

import "github.com/jbuffin/admiral/internal/conversation"

type Message struct {
	ID   string `json:"id"`
	Role string `json:"role"` // "user" or "assistant"
	Text string `json:"text"`
	At   string `json:"at"`
}

// A notice (a subagent's report) is shown as the user's message, as the chat always has.
func messages(items []conversation.Item) []Message {
	out := make([]Message, 0, len(items))
	for _, it := range items {
		role := "user"
		if it.Kind == conversation.KindAssistant {
			role = "assistant"
		}
		out = append(out, Message{ID: it.ID, Role: role, Text: it.Text, At: it.At})
	}
	return out
}
