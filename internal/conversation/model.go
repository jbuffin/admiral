// Package conversation is Admiral's one model of a ship's conversation and the
// adapters that fill it from a harness's own record (ADR 0030). The model is
// what the conversation API serves and what the chat's text view is cut from.
// An adapter reads the harness's file; it never writes to it.
package conversation

import (
	"encoding/json"
	"errors"
	"time"
)

// ErrUnreadable means a file has content and none of it parses: the caller shows
// "can't read this conversation". A file that is not there reports an error
// satisfying errors.Is(err, fs.ErrNotExist) instead ("no longer available"), and a
// file with nothing in it yet is an empty conversation, not an error.
var ErrUnreadable = errors.New("conversation: nothing in the file can be read")

// ErrUnknownID means a page was asked for before an id that is not in the part of
// the file Page reads: the caller answers 400.
var ErrUnknownID = errors.New("conversation: no such item id")

// Kind is what an item is.
type Kind string

const (
	KindUser       Kind = "user"       // a message the engineer sent
	KindAssistant  Kind = "assistant"  // the assistant's text
	KindToolCall   Kind = "tool_call"  // a tool call with its output folded in; a subagent run is one
	KindQuestion   Kind = "question"   // a question put to the engineer, with its options
	KindTurnEnd    Kind = "turn_end"   // a turn boundary, with how long the turn took
	KindCompaction Kind = "compaction" // the harness compacted the conversation here
	KindNotice     Kind = "notice"     // a line the harness wrote in the user's turn: a subagent's report, another session's message
)

// Item is one thing in a conversation. ID is stable across reads of the same
// file and doubles as the paging cursor (?before=<id>). A tool call or question
// can be re-emitted under the same ID once its result arrives.
type Item struct {
	ID       string        `json:"id"`
	Kind     Kind          `json:"kind"`
	At       string        `json:"at,omitempty"`   // the harness's timestamp, as written
	Text     string        `json:"text,omitempty"` // user, assistant and notice text
	Meta     bool          `json:"meta,omitempty"` // a notice the chat's text view does not show
	Tool     *ToolCall     `json:"tool,omitempty"`
	Question *Question     `json:"question,omitempty"`
	Duration time.Duration `json:"-"` // a turn end's "worked for N"
}

// MarshalJSON serves Duration as milliseconds.
func (it Item) MarshalJSON() ([]byte, error) {
	type plain Item
	return json.Marshal(struct {
		plain
		DurationMs int64 `json:"durationMs,omitempty"`
	}{plain(it), it.Duration.Milliseconds()})
}

// ToolCall is a call and what came back from it.
type ToolCall struct {
	Name    string `json:"name"`
	Summary string `json:"summary,omitempty"` // a short line on what it was asked to do
	Output  string `json:"output,omitempty"`
	Error   bool   `json:"error,omitempty"`
	Done    bool   `json:"done"` // the output has arrived
}

// Question is a question put to the engineer. Pending until the harness records
// the answer.
type Question struct {
	Text    string   `json:"text"`
	Options []Option `json:"options,omitempty"`
	Answer  string   `json:"answer,omitempty"`
	Pending bool     `json:"pending"`
}

// Option is one answer a question offers.
type Option struct {
	Label       string `json:"label"`
	Description string `json:"description,omitempty"`
}

// TextOnly keeps the user's and the assistant's text, and the harness notices the
// chat shows today (a subagent's report): the chat's view. Ship views may drop
// KindNotice.
func TextOnly(it Item) bool {
	return it.Kind == KindUser || it.Kind == KindAssistant || (it.Kind == KindNotice && !it.Meta)
}
