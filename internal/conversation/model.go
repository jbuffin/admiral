// Package conversation is Admiral's model of a ship's conversation and the
// adapters that fill it from a harness's own record, which they only read.
package conversation

import (
	"encoding/json"
	"errors"
	"time"
)

// ErrUnreadable means a file has content and none of it parses; a file with only
// metadata lines is an empty conversation, not this.
var ErrUnreadable = errors.New("conversation: nothing in the file can be read")

// ErrUnknownID means Page was asked for items before an id it does not hold.
var ErrUnknownID = errors.New("conversation: no such item id")

// Kind is what an item is.
type Kind string

const (
	KindUser       Kind = "user"
	KindAssistant  Kind = "assistant"
	KindToolCall   Kind = "tool_call" // with its output folded in; a subagent run is one
	KindQuestion   Kind = "question"
	KindTurnEnd    Kind = "turn_end"
	KindCompaction Kind = "compaction"
	KindNotice     Kind = "notice" // a line the harness wrote in the user's turn, not the engineer
)

// Item is one thing in a conversation. ID is stable across reads and is the paging
// cursor; a tool call or question keeps its ID when its result arrives.
type Item struct {
	ID       string        `json:"id"`
	Kind     Kind          `json:"kind"`
	At       string        `json:"at,omitempty"`
	Text     string        `json:"text,omitempty"`
	Meta     bool          `json:"meta,omitempty"` // a notice the chat's text view does not show
	Tool     *ToolCall     `json:"tool,omitempty"`
	Question *Question     `json:"question,omitempty"`
	Duration time.Duration `json:"-"` // of a turn end; served as durationMs
}

func (it Item) MarshalJSON() ([]byte, error) {
	type plain Item
	return json.Marshal(struct {
		plain
		DurationMs int64 `json:"durationMs,omitempty"`
	}{plain(it), it.Duration.Milliseconds()})
}

type ToolCall struct {
	Name    string `json:"name"`
	Summary string `json:"summary,omitempty"`
	Output  string `json:"output,omitempty"`
	Error   bool   `json:"error,omitempty"`
	Done    bool   `json:"done"`
}

type Question struct {
	Text    string   `json:"text"`
	Options []Option `json:"options,omitempty"`
	Answer  string   `json:"answer,omitempty"`
	Pending bool     `json:"pending"`
}

type Option struct {
	Label       string `json:"label"`
	Description string `json:"description,omitempty"`
}

// TextOnly is the chat's view: user and assistant text and the notices it shows.
func TextOnly(it Item) bool {
	return it.Kind == KindUser || it.Kind == KindAssistant || (it.Kind == KindNotice && !it.Meta)
}
