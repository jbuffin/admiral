package conversation

import (
	"encoding/json"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

// maxText cuts one message's text, a tool's output or a question's answer.
const maxText = 64 << 10

// maxPending is how many tool calls the decoder holds while it waits for their
// results; past it, it forgets them and a late result is dropped.
const maxPending = 1000

// claudeLine is the part of a Claude Code transcript line Admiral reads. The
// format is undocumented (ADR 0019, ADR 0030): anything else in a line is ignored.
type claudeLine struct {
	Type        string `json:"type"`
	Subtype     string `json:"subtype"`
	UUID        string `json:"uuid"`
	Timestamp   string `json:"timestamp"`
	IsMeta      bool   `json:"isMeta"`
	IsSidechain bool   `json:"isSidechain"`
	// Claude Code's compaction summary is a user line that only the transcript shows.
	IsCompactSummary          bool  `json:"isCompactSummary"`
	IsVisibleInTranscriptOnly bool  `json:"isVisibleInTranscriptOnly"`
	DurationMs                int64 `json:"durationMs"`
	Message                   struct {
		Content json.RawMessage `json:"content"`
	} `json:"message"`
	ToolUseResult json.RawMessage `json:"toolUseResult"`
}

// emission is an item the decoder hands on. An update is a call or question
// whose result has arrived: the same item again, filled in.
type emission struct {
	Item   Item
	Update bool
}

// claudeDecoder turns transcript lines into items. It remembers the calls that
// have no result yet so a result on a later line folds into its call.
type claudeDecoder struct {
	calls map[string][]Item // tool_use id → the items it made, until its result
}

func newClaudeDecoder() *claudeDecoder { return &claudeDecoder{calls: map[string][]Item{}} }

func (d *claudeDecoder) reset() { d.calls = map[string][]Item{} }

// lineItems collects the items of one line, giving each its id: the line's uuid
// for the first, uuid/1, uuid/2 and so on for the rest.
type lineItems struct {
	uuid, at string
	n        int // items made by add; updates folded in don't count
	out      []emission
}

func (b *lineItems) add(it Item) Item {
	it.At = b.at
	it.ID = b.uuid
	if b.n > 0 {
		it.ID = b.uuid + "/" + strconv.Itoa(b.n)
	}
	b.n++
	b.out = append(b.out, emission{Item: it})
	return it
}

// line decodes one line. ok is false when the line is not a JSON object, which
// is what tells a file with nothing parseable from one with nothing to show.
func (d *claudeDecoder) line(raw []byte) (out []emission, ok bool) {
	var probe map[string]json.RawMessage
	if json.Unmarshal(raw, &probe) != nil {
		return nil, false
	}
	var l claudeLine
	if json.Unmarshal(raw, &l) != nil {
		return nil, true // JSON, but a field is not the shape we know
	}
	if l.UUID == "" || l.IsMeta || l.IsSidechain || l.IsCompactSummary || l.IsVisibleInTranscriptOnly {
		return nil, true
	}
	b := &lineItems{uuid: l.UUID, at: l.Timestamp}
	switch l.Type {
	case "system":
		switch l.Subtype {
		case "turn_duration":
			b.add(Item{Kind: KindTurnEnd, Duration: time.Duration(l.DurationMs) * time.Millisecond})
		case "compact_boundary":
			b.add(Item{Kind: KindCompaction})
		}
	case "assistant":
		d.assistant(l, b)
	case "user":
		d.user(l, b)
	}
	return b.out, true
}

type claudeBlock struct {
	Type      string          `json:"type"`
	Text      string          `json:"text"`
	ID        string          `json:"id"`
	Name      string          `json:"name"`
	Input     json.RawMessage `json:"input"`
	ToolUseID string          `json:"tool_use_id"`
	Content   json.RawMessage `json:"content"`
	IsError   bool            `json:"is_error"`
}

// blocks is a message's content as blocks; a plain string is one text block.
func blocks(raw json.RawMessage) []claudeBlock {
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return []claudeBlock{{Type: "text", Text: s}}
	}
	var bs []claudeBlock
	if json.Unmarshal(raw, &bs) != nil {
		return nil
	}
	return bs
}

// textOf joins the text blocks by a blank line.
func textOf(bs []claudeBlock) string {
	var parts []string
	for _, b := range bs {
		if b.Type == "text" && b.Text != "" {
			parts = append(parts, b.Text)
		}
	}
	return strings.Join(parts, "\n\n")
}

// assistant makes an item of the text blocks and one of each tool call. Thinking
// blocks are dropped.
func (d *claudeDecoder) assistant(l claudeLine, b *lineItems) {
	bs := blocks(l.Message.Content)
	if text := strings.TrimSpace(textOf(bs)); text != "" {
		b.add(Item{Kind: KindAssistant, Text: cut(text)})
	}
	for _, blk := range bs {
		if blk.Type != "tool_use" || blk.Name == "" {
			continue
		}
		var made []Item
		if blk.Name == "AskUserQuestion" {
			for _, asked := range askedQuestions(blk.Input) {
				q := asked
				q.Pending = true
				made = append(made, b.add(Item{Kind: KindQuestion, Question: &q}))
			}
		}
		if len(made) == 0 {
			made = append(made, b.add(Item{Kind: KindToolCall, Tool: &ToolCall{Name: blk.Name, Summary: summary(blk.Input)}}))
		}
		if blk.ID != "" {
			if len(d.calls) >= maxPending {
				d.calls = map[string][]Item{}
			}
			d.calls[blk.ID] = made
		}
	}
}

// askedQuestions is the questions an AskUserQuestion call put.
func askedQuestions(input json.RawMessage) []Question {
	var in struct {
		Questions []struct {
			Question string `json:"question"`
			Options  []struct {
				Label       string `json:"label"`
				Description string `json:"description"`
			} `json:"options"`
		} `json:"questions"`
	}
	if json.Unmarshal(input, &in) != nil {
		return nil
	}
	var qs []Question
	for _, q := range in.Questions {
		out := Question{Text: q.Question}
		for _, o := range q.Options {
			out.Options = append(out.Options, Option{Label: o.Label, Description: o.Description})
		}
		qs = append(qs, out)
	}
	return qs
}

// summary is a short line on what a tool was asked to do, from the input fields
// tools commonly carry.
func summary(input json.RawMessage) string {
	var in map[string]json.RawMessage
	if json.Unmarshal(input, &in) != nil {
		return ""
	}
	for _, key := range []string{"description", "command", "file_path", "path", "pattern", "url", "query", "prompt"} {
		var s string
		if json.Unmarshal(in[key], &s) == nil && s != "" {
			if r := []rune(s); len(r) > 200 {
				s = string(r[:200]) + "…"
			}
			return s
		}
	}
	return ""
}

// user makes an item of a user line's text, unless it is one Claude Code or its
// harness wrote and the engineer did not, and folds its tool results into their calls.
func (d *claudeDecoder) user(l claudeLine, b *lineItems) {
	bs := blocks(l.Message.Content)
	for _, blk := range bs {
		if blk.Type == "tool_result" {
			d.fold(blk, l.ToolUseResult, b)
		}
	}
	text := textOf(bs)
	head := strings.TrimLeft(text, " \t\r\n")
	for _, p := range []string{"<command-name>", "<local-command-", "<task-notification>"} {
		if strings.HasPrefix(head, p) {
			return
		}
	}
	if text = strings.TrimSpace(text); text != "" {
		b.add(Item{Kind: KindUser, Text: cut(text)})
	}
}

// fold puts a tool result into the call it answers and emits that call again. A
// result whose call was not seen (a read that began mid-file) is dropped.
func (d *claudeDecoder) fold(res claudeBlock, extra json.RawMessage, b *lineItems) {
	made, ok := d.calls[res.ToolUseID]
	if !ok {
		return
	}
	delete(d.calls, res.ToolUseID)
	output := cut(strings.TrimSpace(resultText(res.Content)))
	var answers struct {
		Answers map[string]string `json:"answers"`
	}
	_ = json.Unmarshal(extra, &answers)
	for _, it := range made {
		if it.Tool != nil {
			t := *it.Tool
			t.Output, t.Error, t.Done = output, res.IsError, true
			it.Tool = &t
		}
		if it.Question != nil {
			q := *it.Question
			q.Pending = false
			if a, ok := answers.Answers[q.Text]; ok {
				q.Answer = cut(a)
			} else {
				q.Answer = output
			}
			it.Question = &q
		}
		b.out = append(b.out, emission{Item: it, Update: true})
	}
}

// resultText is a tool result's content: a string, or text blocks.
func resultText(raw json.RawMessage) string {
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}
	var bs []claudeBlock
	if json.Unmarshal(raw, &bs) != nil {
		return ""
	}
	return textOf(bs)
}

// cut ends a text at maxText, on a character boundary.
func cut(text string) string {
	if len(text) <= maxText {
		return text
	}
	n := maxText
	for n > 0 && !utf8.RuneStart(text[n]) {
		n--
	}
	return text[:n] + "…"
}
